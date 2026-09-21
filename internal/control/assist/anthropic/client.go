package anthropic

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"strings"
	"time"

	"github.com/alexei-led/archfit-app/internal/control/assist"
)

const (
	endpoint         = "https://api.anthropic.com/v1/messages"
	apiVersion       = "2023-06-01"
	maxResponseBytes = 1 << 20
	stopEndTurn      = "end_turn"
	blockText        = "text"
	roleUser         = "user"
	thinkingDisabled = "disabled"
)

// Errors never carry vendor or model text: the vendor may echo the prompt.
var (
	errInvalidPrompt    = errors.New("anthropic: invalid prompt")
	errResponseTooLarge = errors.New("anthropic: response larger than 1 MB")
	errInvalidResponse  = errors.New("anthropic: response is not a valid message")
	errUnusableAnswer   = errors.New("anthropic: unusable answer")
)

type statusError struct {
	status  int
	errType string
}

func (e *statusError) Error() string {
	return fmt.Sprintf("anthropic: status %d (%s)", e.status, e.errType)
}

type provider struct {
	apiKey string
	model  string
	price  price
	hc     *http.Client

	// Seams for tests; New sets the real ones.
	attemptContext func(context.Context, time.Duration) (context.Context, context.CancelFunc)
	sleep          func(context.Context, time.Duration) error
	now            func() time.Time
}

type message struct {
	Role    string `json:"role"`
	Content string `json:"content"`
}

type thinking struct {
	Type string `json:"type"`
}

type request struct {
	Model     string    `json:"model"`
	MaxTokens int       `json:"max_tokens"`
	System    string    `json:"system"`
	Messages  []message `json:"messages"`
	Thinking  thinking  `json:"thinking"`
}

type response struct {
	Content []struct {
		Type string `json:"type"`
		Text string `json:"text"`
	} `json:"content"`
	StopReason string `json:"stop_reason"`
	Usage      *usage `json:"usage"`
}

type errorResponse struct {
	Error struct {
		Type string `json:"type"`
	} `json:"error"`
}

// outcome is one attempt: what the vendor said and what it costs under the Provider cost rule.
type outcome struct {
	status     int
	retryAfter string
	usage      usage
	text       string
	cost       int64
	err        error
}

func (p *provider) MaxCostMicroUSD(pr assist.Prompt) int64 {
	return p.price.maxCost(len(pr.System)+len(pr.User), pr.MaxTokens)
}

func (p *provider) Complete(ctx context.Context, pr assist.Prompt) (assist.Completion, error) {
	if pr.MaxTokens <= 0 {
		return assist.Completion{}, fmt.Errorf("%w: MaxTokens must be positive", errInvalidPrompt)
	}
	if pr.User == "" {
		return assist.Completion{}, fmt.Errorf("%w: User is empty", errInvalidPrompt)
	}
	body, err := json.Marshal(request{
		Model:     p.model,
		MaxTokens: pr.MaxTokens,
		System:    pr.System,
		Messages:  []message{{Role: roleUser, Content: pr.User}},
		Thinking:  thinking{Type: thinkingDisabled},
	})
	if err != nil {
		return assist.Completion{}, fmt.Errorf("%w: not encodable", errInvalidPrompt)
	}

	start := p.now()
	maxCost := p.MaxCostMicroUSD(pr)
	var out outcome
	attempts := 0
	for {
		// A request that was never sent costs nothing: keep the previous outcome's cost.
		if err := ctx.Err(); err != nil {
			out.err = fmt.Errorf("anthropic: %w", err)
			break
		}
		attempts++
		out = p.attempt(ctx, body, maxCost)
		if attempts > maxRetries || !retryable(out.status) {
			break
		}
		if err := p.sleep(ctx, retryDelay(out.retryAfter)); err != nil {
			out.err = fmt.Errorf("anthropic: waiting to retry: %w", err)
			break
		}
	}
	p.log(ctx, out, attempts, p.now().Sub(start))
	if out.err != nil {
		return assist.Completion{CostMicroUSD: out.cost}, out.err
	}
	return assist.Completion{Text: out.text, CostMicroUSD: out.cost}, nil
}

func (p *provider) attempt(ctx context.Context, body []byte, maxCost int64) outcome {
	ctx, cancel := p.attemptContext(ctx, attemptTimeout)
	defer cancel()

	req, err := http.NewRequestWithContext(ctx, http.MethodPost, endpoint, bytes.NewReader(body))
	if err != nil {
		return outcome{err: fmt.Errorf("anthropic: building request: %w", err)}
	}
	req.Header.Set("x-api-key", p.apiKey)
	req.Header.Set("anthropic-version", apiVersion)
	req.Header.Set("content-type", "application/json")

	resp, err := p.hc.Do(req)
	if err != nil {
		// Sent or not, the outcome is unknown.
		return outcome{cost: maxCost, err: fmt.Errorf("anthropic: request failed: %w", err)}
	}
	defer func() { _ = resp.Body.Close() }()
	raw, readErr := io.ReadAll(io.LimitReader(resp.Body, maxResponseBytes+1))

	if resp.StatusCode != http.StatusOK {
		// The vendor rejected the call: no cost, whatever the body holds.
		var e errorResponse
		_ = json.Unmarshal(raw, &e)
		return outcome{
			status:     resp.StatusCode,
			retryAfter: resp.Header.Get("retry-after"),
			err:        &statusError{status: resp.StatusCode, errType: safeToken(e.Error.Type)},
		}
	}
	out := outcome{status: resp.StatusCode, cost: maxCost}
	if readErr != nil {
		out.err = fmt.Errorf("anthropic: reading response: %w", readErr)
		return out
	}
	if len(raw) > maxResponseBytes {
		out.err = errResponseTooLarge
		return out
	}
	var msg response
	// The decode error is dropped: it can quote bytes of the completion.
	if json.Unmarshal(raw, &msg) != nil || msg.Usage == nil || !msg.Usage.valid() {
		out.err = errInvalidResponse
		return out
	}

	// From here the vendor's usage is known, usable answer or not.
	out.usage = *msg.Usage
	out.cost = p.price.cost(out.usage)
	if msg.StopReason != stopEndTurn {
		out.err = fmt.Errorf("%w: stop_reason %s", errUnusableAnswer, safeToken(msg.StopReason))
		return out
	}
	var text strings.Builder
	for _, block := range msg.Content {
		if block.Type == blockText {
			text.WriteString(block.Text)
		}
	}
	if text.Len() == 0 {
		out.err = fmt.Errorf("%w: no text", errUnusableAnswer)
		return out
	}
	out.text = text.String()
	return out
}

func (p *provider) log(ctx context.Context, out outcome, attempts int, d time.Duration) {
	level, attrs := slog.LevelInfo, []slog.Attr{
		slog.String("model", p.model),
		slog.Int("status", out.status),
		slog.Int("attempts", attempts),
		slog.Int64("input_tokens", out.usage.InputTokens+out.usage.CacheCreationInputTokens+out.usage.CacheReadInputTokens),
		slog.Int64("output_tokens", out.usage.OutputTokens),
		slog.Int64("cost_micro_usd", out.cost),
		slog.Int64("duration_ms", d.Milliseconds()),
	}
	if out.err != nil {
		level = slog.LevelWarn
		attrs = append(attrs, slog.String("error", out.err.Error()))
	}
	slog.Default().LogAttrs(ctx, level, "anthropic completion", attrs...)
}

// safeToken passes a vendor identifier such as "rate_limit_error" and nothing that could be prose.
func safeToken(s string) string {
	if s == "" || len(s) > 64 {
		return "unknown"
	}
	for _, r := range s {
		if (r < 'a' || r > 'z') && (r < '0' || r > '9') && r != '_' {
			return "unknown"
		}
	}
	return s
}

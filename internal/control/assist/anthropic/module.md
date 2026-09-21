# Assist Anthropic Provider

**Path**: internal/control/assist/anthropic/
**Parent**: internal/control/assist/ (Assist)
**Submodules**: none (leaf)

## Purpose

This module is the one managed-mode LLM integration of the first version (decisions.md §7): an
implementation of the `Provider` seam over the Anthropic Messages API. It exists so that endpoint,
headers, model, prices, timeouts and retry behavior of one vendor stay out of `assist`. Without it
`assist` would know a vendor's wire format, and replacing the vendor would touch request logic.

## Functional Responsibilities

- Send one prompt to `POST https://api.anthropic.com/v1/messages` and return the text answer.
- State the worst-case cost of a prompt before the call, so that `assist` can reserve budget.
- Report the actual cost of a call in micro-USD from the API's token usage and the embedded price table.
- Bound every call: 60 second timeout per attempt **[default]**, at most one retry **[default]**, response capped at 1 MB **[default]**.
- Refuse unusable answers: truncated (`max_tokens`), refused, empty, or non-text.

## Subdomain Classification

Generic (a vendor HTTP client). Volatility: moderate. Model IDs, prices and request options change with
vendor releases; the `Provider` contract does not.

## Encapsulated Knowledge

- Endpoint, headers (`x-api-key`, `anthropic-version: 2023-06-01`, `content-type: application/json`), request and response JSON.
- The price table, in micro-USD per token **[default, from the vendor price list of 2026-06]**:
  `claude-sonnet-5` input 2, output 10; `claude-opus-5` input 5, output 25. No other model is accepted.
- The request options: no streaming, `thinking: {"type": "disabled"}`, no sampling parameters (current models
  reject `temperature`), no tools, no assistant prefill (rejected by current models) **[default]**.
- The token upper bound used before a call: one token per byte of `System` plus `User`, plus 256 **[default]**.
- Retry and timeout numbers, the response size cap (1 MB **[default]**), the mapping of `stop_reason` values.

## Public Contract

```go
type Config struct {
    APIKey string // from a secret reference
    Model  string // default "claude-sonnet-5"
}

// New returns a Provider backed by the Anthropic Messages API over net/http.
// New fails on an empty key or a model without an embedded price.
func New(c Config, hc *http.Client) (Provider, error)
```

### Rules

- `Model` empty means `claude-sonnet-5`. `hc` nil means a new `http.Client` with no global timeout; the per-attempt
  timeout comes from a context either way.
- `New` returns an error when `APIKey` is empty or `Model` is not in the price table: an unpriced model would make the
  budget meaningless. The error names the field, never its value. `internal/app` calls `New` only when a key is
  configured, and treats the error as a start-up failure.
- `MaxCostMicroUSD(p) = inPrice × (len(p.System) + len(p.User) + 256) + outPrice × p.MaxTokens`. Pure, no network.
  It is an upper bound because a token is at least one byte.
- `Complete` request body: `model`, `max_tokens = p.MaxTokens`, `system = p.System`,
  `messages = [{"role": "user", "content": p.User}]`, `thinking = {"type": "disabled"}`.
- `Complete` success: HTTP `200`, `stop_reason == "end_turn"`, at least one `text` content block. `Text` is the
  concatenation of all `text` blocks in order. `CostMicroUSD = inPrice × (usage.input_tokens +
  usage.cache_creation_input_tokens + usage.cache_read_input_tokens) + outPrice × usage.output_tokens`.
- `Complete` failure always returns a non-nil error and an empty `Text`. `CostMicroUSD` on failure follows the
  `Provider` cost rule restated below.
- `p.MaxTokens <= 0` or an empty `p.User` is an error without a network call.

## Integrations

### internal/control/assist (parent)

- **Counterpart**: `internal/control/assist/module.md`
- **Direction**: anthropic depends on assist: it implements `Provider` and imports the package for `Prompt` and
  `Completion`. Assist never imports anthropic. Root import rule: an implementation package of a seam may import the
  package that declares the interface (root decision 6).
- **Strength**: contract
- **LCA / Rank / Distance**: control/assist / 1 / 1
- **Volatility**: moderate
- **Balanced?**: yes
- **Shared knowledge**: the seam below, plus its cost rule, which assist owns: on success `CostMicroUSD` is the actual
  cost; on failure it is `0` when the vendor rejected the call (any non-`200` status), the usage-based cost when a `200`
  answer was unusable, and `MaxCostMicroUSD(p)` when the outcome is unknown (timeout, cancellation or transport error
  after the request was sent). Assist settles the budget with that value, so the monthly cap holds.

<!-- contract: Provider, Prompt, Completion — restated from internal/control/assist/module.md -->
```go
// Provider is the LLM seam. Implementations own endpoint, model choice, and pricing.
type Provider interface {
    // MaxCostMicroUSD is the worst-case cost of one Complete call for p; used to reserve budget.
    MaxCostMicroUSD(p Prompt) int64
    Complete(ctx context.Context, p Prompt) (Completion, error)
}

type Prompt struct {
    System    string
    User      string
    MaxTokens int
}

type Completion struct {
    Text         string
    CostMicroUSD int64
}
```

### internal/app

- **Counterpart**: `internal/app/module.md`
- **Direction**: app depends on anthropic (calls `New`, hands the result to `control.New` as `Deps.Provider`)
- **Strength**: contract (constructor only)
- **LCA / Rank / Distance**: root / 3 / 3
- **Volatility**: low
- **Balanced?**: yes — contract coupling tolerates the distance
- **Shared knowledge**: `Config` and `New` above. App reads the key from `ARCHFIT_ANTHROPIC_KEY_FILE`; an empty setting
  means app never calls `New` and managed mode reports "not available".

### Anthropic Messages API (external)

- **Counterpart**: `https://api.anthropic.com/v1/messages`, outside the tree
- **Direction**: anthropic depends on it
- **Strength**: contract (vendor-versioned by the `anthropic-version` header)
- **LCA / Rank / Distance**: none / — / highest (another company's service)
- **Volatility**: moderate
- **Balanced?**: yes — pinned version header, strict reading of the few fields used
- **Shared knowledge**: response fields read: `content[].type`, `content[].text`, `stop_reason`, `usage.input_tokens`,
  `usage.output_tokens`, `usage.cache_creation_input_tokens`, `usage.cache_read_input_tokens`; on errors
  `error.type` only. Status `429` (rate limit) and `529` (overloaded) are retryable; the `retry-after` header gives seconds.

## Change Vectors

- Default model, accepted models and prices (price table only).
- Turning thinking or streaming on when answers need it; timeout and retry numbers.
- A newer `anthropic-version`.
- Prompt caching of the system prompt (the schema text is stable); the cost formula already counts cache tokens at the
  full input price, which stays an upper bound.
- A second vendor is a sibling package implementing `Provider`; nothing here changes.

## Constraints and Invariants

- Explicit request only. This package is imported by `internal/app` alone; nothing under `internal/gate` may import it
  (root import test). A failure here never touches editing, checks, or onboarding.
- One attempt plus at most one retry, only on `429` or `529` **[default]**. Wait = `retry-after` seconds capped at 10
  **[default]**, else 2 seconds **[default]**. Every attempt has its own 60 second timeout **[default]**. Context cancellation stops waiting and sending at once.
- The response body is read through a 1 MB limit **[default]**; a longer body is an error with unknown outcome.
- `stop_reason` other than `end_turn` (`max_tokens`, `refusal`, anything else) is an error: a truncated or refused
  answer never becomes a proposal.
- Never logged and never placed in an error value: the API key, the prompt, the completion text, the vendor's error
  message (it may echo prompt text). Errors carry the HTTP status and the vendor `error.type` only. Logs carry model,
  status, attempt count, token counts, cost and duration.
- The API key is sent only in the `x-api-key` header and only to the fixed host. Redirects are not followed **[default]**.
- Customer-mode credentials never reach this module: it holds exactly one key, Archfit's own.
- Standard library only (`net/http`, `encoding/json`). No vendor SDK: root rule "stdlib first"; the surface used is
  one request and six response fields.
- Safe for concurrent use; no state besides the immutable config.

## Test Specification

The vendor is faked with an `http.RoundTripper` inside the injected `http.Client`. Nothing else is mocked. No test
calls the real API.

### Unit Tests

- **Test name**: `TestMaxCostIsAnUpperBound`
  **Scenario**: table of prompts (ASCII, multi-byte UTF-8, empty system, 16 KB user text) and models.
  **Expected behavior**: the formula's exact value per row; for every row `MaxCostMicroUSD` is at least the cost computed
  from any usage with `input_tokens <= bytes + 256` and `output_tokens <= MaxTokens`.
- **Test name**: `TestCostFromUsage`
  **Scenario**: table of usage objects, with and without cache token fields, for both priced models.
  **Expected behavior**: `CostMicroUSD` equals input-side tokens × input price + output tokens × output price.
- **Test name**: `TestRetryDelay`
  **Scenario**: table of `retry-after` values: absent, `1`, `7`, `600`, `abc`, negative.
  **Expected behavior**: 2 s, 1 s, 7 s, 10 s, 2 s, 2 s.

### Integration Contract Tests

- **Test name**: `TestCompleteSendsPinnedRequest`
  **Scenario**: one `Complete` call against the fake.
  **Expected behavior**: `POST` to `/v1/messages` on `api.anthropic.com`; headers `x-api-key`, `anthropic-version: 2023-06-01`,
  `content-type: application/json`; body has exactly `model`, `max_tokens`, `system`, `messages`, `thinking`; one user message.
- **Test name**: `TestCompleteReturnsTextAndCost`
  **Scenario**: a `200` answer with two text blocks, `end_turn`, usage 1 000 in and 500 out on `claude-sonnet-5`.
  **Expected behavior**: `Text` is both blocks joined; `CostMicroUSD == 7000`; nil error.
- **Test name**: `TestCompleteFailureCost`
  **Scenario**: table: `400`, `401`, `500`; `200` with `stop_reason` `max_tokens`; `200` with `refusal`; `200` with no text
  block; `200` with invalid JSON; transport error after send; attempt timeout; caller cancels mid-flight.
  **Expected behavior**: error in every row and empty `Text`; cost `0` for the status rows, usage-based for the three
  unusable `200` rows, `MaxCostMicroUSD(p)` for invalid JSON, transport error, timeout and cancellation.
- **Test name**: `TestDefaultModel`
  **Scenario**: `Config{Model: ""}`.
  **Expected behavior**: the request names `claude-sonnet-5`.

### Boundary Tests

- **Test name**: `TestRetryIsBounded` (T20)
  **Scenario**: table: `429` then `200`; `529` then `200`; `429` twice; `500` once; `429` with `retry-after: 600`.
  **Expected behavior**: two requests at most; success for the first two rows; error after the second `429`; no retry
  on `500`; the wait never exceeds 10 s (fake clock or injected sleep).
- **Test name**: `TestNewRejectsBadConfig`
  **Scenario**: table: empty key; model `gpt-x`; model `claude-sonnet-5-20260101`; then a valid key with each priced model.
  **Expected behavior**: error and nil `Provider` for the first three, naming the field; the key value is not in the
  error text; no panic; the valid rows return a `Provider` and nil error.
- **Test name**: `TestOversizedResponseIsAnError`
  **Scenario**: the fake streams a 2 MB body.
  **Expected behavior**: error; at most 1 MB + 1 byte is read; cost is `MaxCostMicroUSD(p)`.
- **Test name**: `TestNothingSensitiveInLogsOrErrors` (T20)
  **Scenario**: success and every failure row, with a capturing logger and recognizable key, prompt, completion and
  vendor error message strings.
  **Expected behavior**: none of the four strings appears in any log line or `err.Error()`.
- **Test name**: `TestInvalidPromptMakesNoCall`
  **Scenario**: table: `MaxTokens` 0; negative; empty `User`.
  **Expected behavior**: error, zero cost, zero requests.
- **Test name**: `TestRedirectIsNotFollowed`
  **Scenario**: the fake answers `307` to another host.
  **Expected behavior**: error; the key is never sent to the second host.

### Behavior Tests

- **Test name**: `TestProposalRoundTrip`
  **Scenario**: a prompt carrying a small policy YAML and an instruction; the fake returns an edited YAML.
  **Expected behavior**: the exact text comes back with the usage-based cost, ready for assist to validate and propose.
- **Test name**: `TestVendorOutageIsContained` (T20)
  **Scenario**: the fake hangs for every attempt.
  **Expected behavior**: `Complete` returns within 60 s of fake time with an error and the worst-case cost; no goroutine
  is left running; a following call works once the fake recovers.
- **Test name**: `TestTruncatedAnswerNeverBecomesText` (T20)
  **Scenario**: a `200` answer cut at `max_tokens` with half a YAML document.
  **Expected behavior**: error and empty `Text`, so a partial file can never be committed.

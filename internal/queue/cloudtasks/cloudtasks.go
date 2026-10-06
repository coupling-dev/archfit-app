// Package cloudtasks is the Google Cloud Tasks driver of queue.Queue, over the REST API and net/http.
package cloudtasks

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"regexp"
	"strings"

	"github.com/alexei-led/archfit-app/internal/queue"
)

const (
	defaultEndpoint = "https://cloudtasks.googleapis.com"

	// maxBody keeps a task under the Cloud Tasks limit of 1 MiB per task, with room for the headers and the URL.
	maxBody = 900 << 10

	maxResponseBody = 64 << 10
)

// Config names the queue and the dispatch target. Every value is a resource name, not a secret.
type Config struct {
	Queue    string // "projects/<project>/locations/<location>/queues/<queue>"
	URL      string // the worker route of every task; the queue-level uriOverride replaces it when set
	Audience string // OIDC audience of the dispatch token: the worker service URL
	Invoker  string // service account e-mail that Cloud Tasks signs the dispatch token as
	Endpoint string // API base URL; empty means https://cloudtasks.googleapis.com
}

// TokenSource returns an OAuth 2.0 access token for the Cloud Tasks API.
type TokenSource interface {
	Token(ctx context.Context) (string, error)
}

var queuePattern = regexp.MustCompile(`^projects/[a-z][a-z0-9-]{4,28}[a-z0-9]/locations/[a-z0-9-]+/queues/[A-Za-z0-9-]{1,100}$`)

type driver struct {
	c      Config
	tokens TokenSource
	hc     *http.Client
}

// New returns a Queue that creates one Cloud Tasks HTTP task per Enqueue. It makes no network call.
func New(c Config, ts TokenSource, hc *http.Client) (queue.Queue, error) {
	if c.Endpoint == "" {
		c.Endpoint = defaultEndpoint
	}
	switch {
	case !queuePattern.MatchString(c.Queue):
		return nil, errors.New("cloudtasks: queue must be projects/<project>/locations/<location>/queues/<queue>")
	case !absoluteHTTPS(c.URL):
		return nil, errors.New("cloudtasks: URL must be an absolute https URL")
	case !absoluteHTTPS(c.Audience):
		return nil, errors.New("cloudtasks: audience must be an absolute https URL")
	case !strings.Contains(c.Invoker, "@"):
		return nil, errors.New("cloudtasks: invoker must be a service account e-mail")
	case ts == nil:
		return nil, errors.New("cloudtasks: nil token source")
	}
	if hc == nil {
		hc = http.DefaultClient
	}
	return &driver{c: c, tokens: ts, hc: hc}, nil
}

func absoluteHTTPS(raw string) bool {
	u, err := url.Parse(raw)
	return err == nil && u.Scheme == "https" && u.Host != ""
}

type createRequest struct {
	Task task `json:"task"`
}

type task struct {
	Name        string      `json:"name"`
	HTTPRequest httpRequest `json:"httpRequest"`
}

type httpRequest struct {
	URL        string            `json:"url"`
	HTTPMethod string            `json:"httpMethod"`
	Headers    map[string]string `json:"headers,omitempty"`
	Body       []byte            `json:"body"` // encoding/json writes []byte as base64, the API's bytes encoding
	OIDCToken  oidcToken         `json:"oidcToken"`
}

type oidcToken struct {
	ServiceAccountEmail string `json:"serviceAccountEmail"`
	Audience            string `json:"audience"`
}

// Enqueue creates the task. Cloud Tasks refuses a name it saw recently with 409; that is the deduplication,
// so it is success.
func (d *driver) Enqueue(ctx context.Context, t queue.Task) error {
	if !queue.ValidName(t.Name) {
		return fmt.Errorf("cloudtasks: task name: %w", queue.ErrInvalidTask)
	}
	if len(t.Body) > maxBody {
		return fmt.Errorf("cloudtasks: body of %d bytes is over %d: %w", len(t.Body), maxBody, queue.ErrInvalidTask)
	}
	headers := make(map[string]string, len(t.Header))
	for k := range t.Header {
		headers[http.CanonicalHeaderKey(k)] = t.Header.Get(k)
	}
	payload, err := json.Marshal(createRequest{Task: task{
		Name: d.c.Queue + "/tasks/" + t.Name,
		HTTPRequest: httpRequest{
			URL: d.c.URL, HTTPMethod: http.MethodPost, Headers: headers, Body: t.Body,
			OIDCToken: oidcToken{ServiceAccountEmail: d.c.Invoker, Audience: d.c.Audience},
		},
	}})
	if err != nil {
		return fmt.Errorf("cloudtasks: encode task: %w", err)
	}
	token, err := d.tokens.Token(ctx)
	if err != nil {
		return fmt.Errorf("cloudtasks: access token: %w", err)
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, d.c.Endpoint+"/v2/"+d.c.Queue+"/tasks", bytes.NewReader(payload))
	if err != nil {
		return errors.New("cloudtasks: build request")
	}
	req.Header.Set("Authorization", "Bearer "+token)
	req.Header.Set("Content-Type", "application/json")
	resp, err := d.hc.Do(req)
	if err != nil {
		// A *url.Error repeats the URL; keep only its cause.
		if ue, ok := errors.AsType[*url.Error](err); ok {
			err = ue.Err
		}
		return fmt.Errorf("cloudtasks: create task: %w", err)
	}
	defer func() { _ = resp.Body.Close() }()
	body, _ := io.ReadAll(io.LimitReader(resp.Body, maxResponseBody))
	switch {
	case resp.StatusCode >= 200 && resp.StatusCode < 300:
		return nil
	case resp.StatusCode == http.StatusConflict && apiStatus(body) == statusAlreadyExists:
		return nil
	default:
		return fmt.Errorf("cloudtasks: create task: status %d %s", resp.StatusCode, apiStatus(body))
	}
}

// statusAlreadyExists is the Google API error status of a task name in use. HTTP 409 also carries ABORTED,
// which is not a duplicate.
const statusAlreadyExists = "ALREADY_EXISTS"

// apiStatus reads the status code word of a Google API error body; the message is never read, so no
// caller-supplied text reaches an error.
func apiStatus(body []byte) string {
	var e struct {
		Error struct {
			Status string `json:"status"`
		} `json:"error"`
	}
	if json.Unmarshal(body, &e) != nil || !statusWord.MatchString(e.Error.Status) {
		return ""
	}
	return e.Error.Status
}

var statusWord = regexp.MustCompile(`^[A-Z_]{1,40}$`)

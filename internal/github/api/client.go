package api

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
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/alexei-led/archfit-app/internal/github/auth"
)

const (
	apiHost    = "api.github.com"
	baseURL    = "https://" + apiHost
	apiVersion = "2022-11-28"
	userAgent  = "archfit-app"

	defaultTimeout  = 10 * time.Second
	maxResponseBody = 10 << 20
	waitBudget      = 30 * time.Second
	firstBackoff    = time.Second
	maxTransient    = 3 // retries of a GET after 502, 503, 504 or a transport error: 1 s, 2 s, 4 s
)

var fullNameRE = regexp.MustCompile(`^[A-Za-z0-9_.-]+/[A-Za-z0-9_.-]+$`)

// Client implements every role interface. Consumers depend on the role interfaces they need.
type Client struct {
	ts    auth.TokenSource
	hc    *http.Client
	now   func() time.Time
	sleep func(context.Context, time.Duration) error
	cache *etagCache

	appMu sync.Mutex
	app   *appInfo
}

// appInfo is the App's own identity, read once from GET /app.
type appInfo struct {
	id       int64
	botLogin string
}

// New returns a client for https://api.github.com. A nil hc means a client with a 10 s timeout.
func New(ts auth.TokenSource, hc *http.Client) *Client {
	if hc == nil {
		hc = &http.Client{Timeout: defaultTimeout}
	}
	own := *hc
	// Redirects are never followed blindly: Artifact follows its one redirect itself, without credentials.
	own.CheckRedirect = func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }
	return &Client{ts: ts, hc: &own, now: time.Now, sleep: sleepCtx, cache: newETagCache()}
}

func sleepCtx(ctx context.Context, d time.Duration) error {
	t := time.NewTimer(d)
	defer t.Stop()
	select {
	case <-ctx.Done():
		return ctx.Err()
	case <-t.C:
		return nil
	}
}

type credKind int

const (
	credApp credKind = iota
	credInst
	credUser
	credNone
)

type cred struct {
	kind      credKind
	instID    int64
	userToken string
}

var appCred = cred{kind: credApp}

func instCred(id int64) (cred, error) {
	if id <= 0 {
		return cred{}, errors.New("github: invalid installation ID")
	}
	return cred{kind: credInst, instID: id}, nil
}

func userCred(token string) (cred, error) {
	if token == "" {
		return cred{}, errors.New("github: empty user token")
	}
	return cred{kind: credUser, userToken: token}, nil
}

// principal is the cache namespace of a credential; "" means its responses are never cached.
func (k cred) principal() string {
	switch k.kind {
	case credApp:
		return "app"
	case credInst:
		return "inst:" + strconv.FormatInt(k.instID, 10)
	default:
		return ""
	}
}

func (c *Client) token(ctx context.Context, k cred) (string, error) {
	switch k.kind {
	case credApp:
		return c.ts.AppJWT()
	case credInst:
		return c.ts.InstallationToken(ctx, k.instID)
	default:
		return k.userToken, nil
	}
}

// call is one logical request. route is the template used in errors; url is never shown.
type call struct {
	cred     cred
	method   string
	route    string
	url      string
	body     any
	maxBody  int64 // 0 means maxResponseBody
	redirect bool  // a 302 is the result, not an error
}

type result struct {
	body     []byte
	link     string
	location string
}

type response struct {
	status int
	header http.Header
	body   []byte
}

// statusError is a GitHub answer that maps to no sentinel. It never holds a URL, a body or a token.
type statusError struct {
	method, route string
	status        int
	requestID     string
}

func (e *statusError) Error() string {
	return fmt.Sprintf("github: %s %s: status %d (request id %q)", e.method, e.route, e.status, e.requestID)
}

var errResponseTooLarge = errors.New("response too large")

func isStatus(err error, status int) bool {
	var se *statusError
	return errors.As(err, &se) && se.status == status
}

func (k call) errorf(format string, args ...any) error {
	return fmt.Errorf("github: %s %s: "+format, append([]any{k.method, k.route}, args...)...)
}

// do sends k with retries: once after a 401 on an installation token, within waitBudget on rate limits, and
// for GET on 502, 503, 504 and transport errors.
func (c *Client) do(ctx context.Context, k call) (result, error) {
	var payload []byte
	if k.body != nil {
		var err error
		if payload, err = json.Marshal(k.body); err != nil {
			return result{}, k.errorf("encode request")
		}
	}
	cacheKey := ""
	if p := k.cred.principal(); p != "" && k.method == http.MethodGet {
		cacheKey = p + " " + k.url
	}

	var waited time.Duration
	backoff, transient, reauthed := firstBackoff, 0, false
	for {
		token, err := c.token(ctx, k.cred)
		if err != nil {
			return result{}, k.errorf("token: %w", err)
		}
		var cached *cacheEntry
		if cacheKey != "" {
			cached = c.cache.get(cacheKey)
		}
		resp, err := c.roundTrip(ctx, k, payload, token, cached)
		if err == nil && cacheKey != "" && resp.status != http.StatusNotModified {
			c.cache.remove(cacheKey)
		}

		var wait time.Duration
		var failure error
		switch {
		case err != nil:
			if ctx.Err() != nil {
				return result{}, ctx.Err()
			}
			if errors.Is(err, errResponseTooLarge) {
				return result{}, err
			}
			failure = err
		case resp.status == http.StatusNotModified && cached != nil:
			return result{body: cached.body, link: cached.link}, nil
		case resp.status >= 200 && resp.status < 300:
			res := result{body: resp.body, link: resp.header.Get("Link")}
			if etag := resp.header.Get("ETag"); cacheKey != "" && etag != "" {
				c.cache.put(cacheKey, &cacheEntry{etag: etag, body: res.body, link: res.link})
			}
			return res, nil
		case resp.status == http.StatusFound && k.redirect:
			return result{location: resp.header.Get("Location")}, nil
		case resp.status == http.StatusNotFound || resp.status == http.StatusGone:
			return result{}, k.errorf("%w", ErrNotFound)
		case resp.status == http.StatusUnauthorized && k.cred.kind == credInst && !reauthed:
			c.ts.Invalidate(k.cred.instID)
			reauthed = true
			continue
		case rateLimited(resp):
			wait = c.rateLimitWait(resp.header, &backoff)
			if waited+wait > waitBudget {
				return result{}, k.errorf("%w", ErrRateLimited)
			}
		default:
			failure = &statusError{method: k.method, route: k.route, status: resp.status,
				requestID: resp.header.Get("X-GitHub-Request-Id")}
			if !transientStatus(resp.status) {
				return result{}, failure
			}
		}

		if failure != nil {
			wait = firstBackoff << transient
			if k.method != http.MethodGet || transient == maxTransient || waited+wait > waitBudget {
				return result{}, failure
			}
			transient++
		}
		if err := c.sleep(ctx, wait); err != nil {
			return result{}, err
		}
		waited += wait
	}
}

func (c *Client) roundTrip(ctx context.Context, k call, payload []byte, token string, cached *cacheEntry) (response, error) {
	var body io.Reader
	if payload != nil {
		body = bytes.NewReader(payload)
	}
	req, err := http.NewRequestWithContext(ctx, k.method, k.url, body)
	if err != nil {
		return response{}, k.errorf("build request")
	}
	req.Header.Set("User-Agent", userAgent)
	if k.cred.kind != credNone {
		req.Header.Set("Accept", "application/vnd.github+json")
		req.Header.Set("X-GitHub-Api-Version", apiVersion)
		req.Header.Set("Authorization", "Bearer "+token)
	}
	if payload != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	if cached != nil {
		req.Header.Set("If-None-Match", cached.etag)
	}

	resp, err := c.hc.Do(req)
	if err != nil {
		// A *url.Error carries the full URL; keep only its cause.
		var ue *url.Error
		if errors.As(err, &ue) {
			err = ue.Err
		}
		return response{}, k.errorf("transport: %w", err)
	}
	defer func() { _ = resp.Body.Close() }()

	limit := k.maxBody
	if limit == 0 {
		limit = maxResponseBody
	}
	data, err := io.ReadAll(io.LimitReader(resp.Body, limit+1))
	if err != nil {
		return response{}, k.errorf("transport: read response")
	}
	if int64(len(data)) > limit {
		return response{}, k.errorf("%w", errResponseTooLarge)
	}
	return response{status: resp.StatusCode, header: resp.Header, body: data}, nil
}

func transientStatus(status int) bool {
	return status == http.StatusBadGateway || status == http.StatusServiceUnavailable || status == http.StatusGatewayTimeout
}

func rateLimited(r response) bool {
	if r.status == http.StatusTooManyRequests {
		return true
	}
	return r.status == http.StatusForbidden &&
		(r.header.Get("X-RateLimit-Remaining") == "0" || r.header.Get("Retry-After") != "")
}

// rateLimitWait is Retry-After, else the time to x-ratelimit-reset of an exhausted primary limit, else a
// doubling backoff.
func (c *Client) rateLimitWait(h http.Header, backoff *time.Duration) time.Duration {
	if n, err := strconv.Atoi(h.Get("Retry-After")); err == nil && n > 0 {
		return time.Duration(n) * time.Second
	}
	if h.Get("X-RateLimit-Remaining") == "0" {
		if n, err := strconv.ParseInt(h.Get("X-RateLimit-Reset"), 10, 64); err == nil {
			if d := time.Unix(n, 0).Sub(c.now()); d > 0 {
				return d
			}
		}
	}
	d := *backoff
	*backoff *= 2
	return d
}

// send runs k and decodes the JSON answer into out; a nil out discards it.
func (c *Client) send(ctx context.Context, k call, out any) error {
	res, err := c.do(ctx, k)
	if err != nil {
		return err
	}
	if out == nil {
		return nil
	}
	// The decode error is dropped: it can quote response content.
	if json.Unmarshal(res.body, out) != nil {
		return k.errorf("invalid response")
	}
	return nil
}

func (c *Client) appIdentity(ctx context.Context) (appInfo, error) {
	c.appMu.Lock()
	defer c.appMu.Unlock()
	if c.app != nil {
		return *c.app, nil
	}
	var got struct {
		ID   int64  `json:"id"`
		Slug string `json:"slug"`
	}
	k := call{cred: appCred, method: http.MethodGet, route: "/app", url: apiURL("/app", nil)}
	if err := c.send(ctx, k, &got); err != nil {
		return appInfo{}, err
	}
	if got.ID <= 0 || got.Slug == "" {
		return appInfo{}, k.errorf("invalid response")
	}
	c.app = &appInfo{id: got.ID, botLogin: got.Slug + "[bot]"}
	return *c.app, nil
}

func apiURL(path string, q url.Values) string {
	if len(q) == 0 {
		return baseURL + path
	}
	return baseURL + path + "?" + q.Encode()
}

// repoPath returns "/repos/{owner}/{name}" for a guarded full name.
func repoPath(fullName string) (string, error) {
	if !fullNameRE.MatchString(fullName) {
		return "", errors.New("github: invalid repository name")
	}
	for seg := range strings.SplitSeq(fullName, "/") {
		if seg == "." || seg == ".." {
			return "", errors.New("github: invalid repository name")
		}
	}
	return "/repos/" + fullName, nil
}

// target guards r and returns its credential and "/repos/{owner}/{name}".
func (r RepoRef) target() (cred, string, error) {
	k, err := instCred(r.InstallationID)
	if err != nil {
		return cred{}, "", err
	}
	p, err := repoPath(r.FullName)
	return k, p, err
}

func (r RepoRef) owner() string {
	owner, _, _ := strings.Cut(r.FullName, "/")
	return owner
}

// escapeSegments escapes a ref, branch or file path per segment. Empty and dot segments are refused: they
// would let a caller string change the route.
func escapeSegments(s string) (string, error) {
	parts := strings.Split(s, "/")
	for i, p := range parts {
		if p == "" || p == "." || p == ".." {
			return "", errors.New("github: invalid ref or path")
		}
		parts[i] = url.PathEscape(p)
	}
	return strings.Join(parts, "/"), nil
}

// escapeSegment escapes a value that must stay one path segment.
func escapeSegment(s string) (string, error) {
	if strings.Contains(s, "/") {
		return "", errors.New("github: invalid path segment")
	}
	return escapeSegments(s)
}

func itoa(n int64) string { return strconv.FormatInt(n, 10) }

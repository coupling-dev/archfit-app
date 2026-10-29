package control

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/url"
	"sync"
	"testing"

	"github.com/alexei-led/archfit-app/internal/control/account"
	"github.com/alexei-led/archfit-app/internal/gate"
	"github.com/alexei-led/archfit-app/internal/github/api"
	"github.com/alexei-led/archfit-app/internal/github/auth"
	"github.com/alexei-led/archfit-app/internal/store"
	"github.com/alexei-led/archfit-app/internal/wire/envelope"
)

const testBaseURL = "https://archfit.test"

var testCookieKey = []byte("0123456789abcdef0123456789abcdef")

// harness is a real Control over a memory Store and a fake GitHub HTTP server behind a real *api.Client.
type harness struct {
	t        *testing.T
	c        *Control
	gh       *fakeGitHub
	store    *flakyStore
	clock    *clock
	receiver *fakeReceiver
	sandbox  *fakeSandbox
	provider *fakeProvider
	// status reads the account records of the same Store without going through the façade.
	status account.Account
}

type harnessOption func(*Deps)

func withoutSandbox(d *Deps)  { d.Sandbox = nil }
func withoutProvider(d *Deps) { d.Provider = nil }

func newHarness(t *testing.T, opts ...harnessOption) *harness {
	t.Helper()
	h := &harness{
		t: t, gh: newFakeGitHub(t), store: &flakyStore{Store: store.NewMemory()}, clock: newClock(),
		receiver: &fakeReceiver{}, sandbox: &fakeSandbox{}, provider: &fakeProvider{},
	}
	admissions := gate.NewAdmissions(h.store, testAccounts)
	g := gate.New(gate.Deps{GitHub: h.gh.client(), Store: h.store, Admissions: admissions, Audience: testBaseURL, Now: h.clock.Now})
	d := Deps{
		GitHub: h.gh.client(), Tokens: fakeTokens{}, OAuth: fakeOAuth{}, Store: h.store, Receiver: h.receiver,
		Admissions: admissions,
		Approvals:  g, Decisions: g,
		Sandbox: h.sandbox, Provider: h.provider, CookieKey: testCookieKey, BaseURL: testBaseURL, Now: h.clock.Now,
	}
	for _, o := range opts {
		o(&d)
	}
	c, err := New(d)
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	h.c = c
	h.status, _, _ = account.New(h.store)
	return h
}

func (h *harness) entitlement(id int64) (gate.Entitlement, bool) {
	h.t.Helper()
	raw, err := h.store.Store.Get(context.Background(), store.Tenant(id), gate.EntitlementKey)
	if err != nil {
		return gate.Entitlement{}, false
	}
	var e gate.Entitlement
	if err := json.Unmarshal(raw, &e); err != nil {
		h.t.Fatalf("entitlement of %d: %v", id, err)
	}
	return e, true
}

func (h *harness) tenants() map[store.Tenant]bool {
	h.t.Helper()
	ts, err := h.store.Store.Tenants(context.Background())
	if err != nil {
		h.t.Fatalf("Tenants: %v", err)
	}
	out := map[store.Tenant]bool{}
	for _, t := range ts {
		out[t] = true
	}
	return out
}

// budgetBooked reports whether the SystemTenant counters sum to want micro-USD.
func (h *harness) budgetBooked(want int64) bool {
	h.t.Helper()
	h.store.mu.Lock()
	defer h.store.mu.Unlock()
	var sum int64
	for key := range h.store.addKeys {
		v, err := h.store.Store.Add(context.Background(), store.SystemTenant, key, 0)
		if err != nil {
			h.t.Fatalf("read budget counter: %v", err)
		}
		sum += v
	}
	return sum == want
}

// flakyStore fails on demand; the embedded memory Store stays readable for assertions.
type flakyStore struct {
	store.Store
	mu         sync.Mutex
	failGet    bool
	failAll    bool
	failPut    map[store.Tenant]bool
	failDelete map[store.Tenant]bool
	failList   bool
	writes     int
	adds       int
	addKeys    map[string]bool // SystemTenant counters touched through Add
}

func (s *flakyStore) set(f func(*flakyStore)) {
	s.mu.Lock()
	defer s.mu.Unlock()
	f(s)
}

func (s *flakyStore) Get(ctx context.Context, t store.Tenant, key string) ([]byte, error) {
	s.mu.Lock()
	fail := s.failGet || s.failAll
	s.mu.Unlock()
	if fail {
		return nil, errBoom
	}
	return s.Store.Get(ctx, t, key)
}

func (s *flakyStore) Put(ctx context.Context, t store.Tenant, key string, value []byte) error {
	s.mu.Lock()
	fail := s.failPut[t] || s.failAll
	if !fail {
		s.writes++
	}
	s.mu.Unlock()
	if fail {
		return errBoom
	}
	return s.Store.Put(ctx, t, key, value)
}

// CompareAndSwapAll fails like Put: account writes its record and entitlement through it. Each key counts as a write.
func (s *flakyStore) CompareAndSwapAll(ctx context.Context, t store.Tenant, swaps []store.Swap) (bool, error) {
	s.mu.Lock()
	fail := s.failPut[t] || s.failAll
	if !fail {
		s.writes += len(swaps)
	}
	s.mu.Unlock()
	if fail {
		return false, errBoom
	}
	return s.Store.CompareAndSwapAll(ctx, t, swaps)
}

func (s *flakyStore) Add(ctx context.Context, t store.Tenant, key string, delta int64) (int64, error) {
	s.mu.Lock()
	fail := s.failAll
	if !fail {
		s.adds++
		if t == store.SystemTenant {
			if s.addKeys == nil {
				s.addKeys = map[string]bool{}
			}
			s.addKeys[key] = true
		}
	}
	s.mu.Unlock()
	if fail {
		return 0, errBoom
	}
	return s.Store.Add(ctx, t, key, delta)
}

func (s *flakyStore) DeleteTenant(ctx context.Context, t store.Tenant) error {
	s.mu.Lock()
	fail := s.failDelete[t] || s.failAll
	s.mu.Unlock()
	if fail {
		return errBoom
	}
	return s.Store.DeleteTenant(ctx, t)
}

func (s *flakyStore) Tenants(ctx context.Context) ([]store.Tenant, error) {
	s.mu.Lock()
	fail := s.failList || s.failAll
	s.mu.Unlock()
	if fail {
		return nil, errBoom
	}
	return s.Store.Tenants(ctx)
}

type fakeTokens struct{}

func (fakeTokens) AppJWT() (string, error) { return "app-jwt", nil }
func (fakeTokens) InstallationToken(_ context.Context, id int64) (string, error) {
	return fmt.Sprintf("tok-%d", id), nil
}
func (fakeTokens) Invalidate(int64) {}
func (fakeTokens) RepoReadToken(_ context.Context, id int64, _ string) (string, error) {
	return fmt.Sprintf("read-%d", id), nil
}

// fakeOAuth signs in the login named by the code: code "alice" yields the user token "user-alice".
type fakeOAuth struct{}

func (fakeOAuth) AuthorizeURL(state, redirectURL string) string {
	return "https://github.test/login/oauth/authorize?" + url.Values{"state": {state}, "redirect_uri": {redirectURL}}.Encode()
}

func (fakeOAuth) Exchange(_ context.Context, code string) (auth.UserToken, error) {
	return auth.UserToken{AccessToken: "user-" + code, Login: code, UserID: 1}, nil
}

type fakeReceiver struct {
	mu     sync.Mutex
	upload gate.Upload
	err    error
	calls  int
}

func (f *fakeReceiver) Receive(_ context.Context, _ *http.Request, _ envelope.Kind) (gate.Upload, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.calls++
	return f.upload, f.err
}

func (f *fakeReceiver) answer(u gate.Upload, err error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.upload, f.err = u, err
}

type fakeSandbox struct {
	mu     sync.Mutex
	jobs   []DiscoveryJob
	result DiscoveryResult
	err    error
}

func (f *fakeSandbox) Discover(_ context.Context, job DiscoveryJob) (DiscoveryResult, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.jobs = append(f.jobs, job)
	return f.result, f.err
}

func (f *fakeSandbox) count() int {
	f.mu.Lock()
	defer f.mu.Unlock()
	return len(f.jobs)
}

type fakeProvider struct {
	mu      sync.Mutex
	maxCost int64
	answer  Completion
	err     error
	calls   int
}

func (f *fakeProvider) MaxCostMicroUSD(Prompt) int64 {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.maxCost
}

func (f *fakeProvider) Complete(context.Context, Prompt) (Completion, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.calls++
	return f.answer, f.err
}

func (f *fakeProvider) count() int {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.calls
}

// rewriteTransport sends every api.github.com request to the fake server.
type rewriteTransport struct{ target *url.URL }

func (rt rewriteTransport) RoundTrip(r *http.Request) (*http.Response, error) {
	r = r.Clone(r.Context())
	r.URL.Scheme, r.URL.Host = rt.target.Scheme, rt.target.Host
	return http.DefaultTransport.RoundTrip(r)
}

func (g *fakeGitHub) client() *api.Client {
	target, err := url.Parse(g.srv.URL)
	if err != nil {
		g.t.Fatalf("fake GitHub URL: %v", err)
	}
	return api.New(fakeTokens{}, &http.Client{Transport: rewriteTransport{target: target}})
}

// testAccounts are the owners of the test repositories, approved by configuration.
var testAccounts = []string{"org", "org7", "org8", "owner", "secret-org", "acme"}

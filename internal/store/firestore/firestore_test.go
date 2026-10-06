package firestore

import (
	"context"
	"encoding/hex"
	"errors"
	"fmt"
	"net/http"
	"os"
	"slices"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/alexei-led/archfit-app/internal/store"
	"github.com/alexei-led/archfit-app/internal/store/storetest"
)

const marker = "SECRET-MARKER"

func TestConformance(t *testing.T) {
	storetest.Run(t, func() store.Store { return newFake(t).client(t) })
}

// emulatorHost is set by CI, which starts the official Firestore emulator. Locally the test skips.
func emulatorHost(t *testing.T) string {
	t.Helper()
	host := os.Getenv("FIRESTORE_EMULATOR_HOST")
	if host == "" {
		t.Skip("FIRESTORE_EMULATOR_HOST is not set")
	}
	return host
}

var emulatorProjects atomic.Int64

// newEmulatorStores returns n clients of one fresh database: the emulator keeps each project apart.
func newEmulatorStores(t *testing.T, host string, n int) []store.Store {
	t.Helper()
	project := "test-" + strconv.FormatInt(time.Now().UnixNano(), 36) + "-" + strconv.FormatInt(emulatorProjects.Add(1), 10)
	stores := make([]store.Store, n)
	for i := range stores {
		s, err := New(Config{ProjectID: project, EmulatorHost: host}, nil, &http.Client{Timeout: 30 * time.Second})
		if err != nil {
			t.Fatalf("New: %v", err)
		}
		stores[i] = s
	}
	return stores
}

func TestConformanceEmulator(t *testing.T) {
	host := emulatorHost(t)
	storetest.Run(t, func() store.Store { return newEmulatorStores(t, host, 1)[0] })
}

// Every client stands for one App instance: each has its own local lock, so only the commit preconditions
// keep the swap atomic.
func testCompareAndSwapAcrossClients(t *testing.T, clients []store.Store) {
	const perClient, rounds = 3, 20
	ctx := context.Background()
	for round := range rounds {
		var old []byte
		if round > 0 {
			old = []byte(strconv.Itoa(round - 1))
		}
		var wins atomic.Int64
		errs := make([]error, len(clients)*perClient)
		var wg sync.WaitGroup
		for i := range errs {
			wg.Go(func() {
				ok, err := clients[i%len(clients)].CompareAndSwap(ctx, 1, "feedback/lease/abc", old, []byte(strconv.Itoa(round)))
				errs[i] = err
				if ok {
					wins.Add(1)
				}
			})
		}
		wg.Wait()
		if err := errors.Join(errs...); err != nil {
			t.Fatalf("round %d: %v", round, err)
		}
		if n := wins.Load(); n != 1 {
			t.Fatalf("round %d: %d winners, want 1", round, n)
		}
	}
	got, err := clients[0].Get(ctx, 1, "feedback/lease/abc")
	if err != nil || string(got) != strconv.Itoa(rounds-1) {
		t.Errorf("final value = %q, %v, want %d", got, err, rounds-1)
	}
}

func testAddAcrossClients(t *testing.T, clients []store.Store) {
	const perClient = 25
	ctx := context.Background()
	errs := make([]error, len(clients))
	var wg sync.WaitGroup
	for i, c := range clients {
		wg.Go(func() {
			for range perClient {
				if _, err := c.Add(ctx, store.SystemTenant, "llm-budget/2026-10", 1); err != nil {
					errs[i] = err
					return
				}
			}
		})
	}
	wg.Wait()
	if err := errors.Join(errs...); err != nil {
		t.Fatal(err)
	}
	if got, err := clients[0].Add(ctx, store.SystemTenant, "llm-budget/2026-10", 0); err != nil || got != int64(len(clients)*perClient) {
		t.Errorf("counter = %d, %v, want %d", got, err, len(clients)*perClient)
	}
}

func fakeClients(t *testing.T, n int) []store.Store {
	t.Helper()
	f := newFake(t)
	clients := make([]store.Store, n)
	for i := range clients {
		clients[i] = f.client(t)
	}
	return clients
}

func TestCompareAndSwapIsAtomicAcrossClients(t *testing.T) {
	t.Run("fake", func(t *testing.T) { testCompareAndSwapAcrossClients(t, fakeClients(t, 4)) })
	t.Run("emulator", func(t *testing.T) { testCompareAndSwapAcrossClients(t, newEmulatorStores(t, emulatorHost(t), 4)) })
}

func TestAddIsAtomicAcrossClients(t *testing.T) {
	t.Run("fake", func(t *testing.T) { testAddAcrossClients(t, fakeClients(t, 3)) })
	t.Run("emulator", func(t *testing.T) { testAddAcrossClients(t, newEmulatorStores(t, emulatorHost(t), 3)) })
}

func TestCommitFailures(t *testing.T) {
	aborted := fault{httpStatus: http.StatusConflict, status: statusAborted}
	stalePre := fault{httpStatus: http.StatusBadRequest, status: statusFailedPrecondition}
	// The commit landed but the answer was lost: the outcome is unknown to the driver.
	lost := fault{httpStatus: http.StatusServiceUnavailable, status: "UNAVAILABLE", apply: true}
	tests := []struct {
		name        string
		faults      []fault
		wantSwapped bool
		wantErr     bool
		wantValue   string
		wantCommits int
		wantPauses  int
	}{
		{"an aborted commit is retried", []fault{aborted}, true, false, "new", 2, 1},
		{"a failed precondition is read again and retried", []fault{stalePre, stalePre}, true, false, "new", 3, 2},
		{"a conflict that never ends is an error", slices.Repeat([]fault{aborted}, maxAttempts), false, true, "old", maxAttempts, maxAttempts - 1},
		{"an unknown outcome is an error, not a retry", []fault{lost}, false, true, "new", 1, 0},
		{"a server error is not retried", []fault{{httpStatus: http.StatusInternalServerError, status: "INTERNAL"}}, false, true, "old", 1, 0},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			f := newFake(t)
			d := f.client(t)
			var waits []time.Duration
			d.wait = func(_ context.Context, d time.Duration) error { waits = append(waits, d); return nil }
			ctx := context.Background()
			if err := d.Put(ctx, 1, "k", []byte("old")); err != nil {
				t.Fatalf("Put: %v", err)
			}
			before := f.commitCount()
			f.inject(tt.faults...)

			swapped, err := d.CompareAndSwap(ctx, 1, "k", []byte("old"), []byte("new"))
			if swapped != tt.wantSwapped || (err != nil) != tt.wantErr {
				t.Errorf("CompareAndSwap = %v, %v, want %v, error %v", swapped, err, tt.wantSwapped, tt.wantErr)
			}
			if got := f.commitCount() - before; got != tt.wantCommits {
				t.Errorf("commits = %d, want %d", got, tt.wantCommits)
			}
			if len(waits) != tt.wantPauses {
				t.Errorf("pauses = %v, want %d", waits, tt.wantPauses)
			}
			for _, w := range waits {
				if w <= 0 || w > retryCap {
					t.Errorf("pause %v outside (0, %v]", w, retryCap)
				}
			}
			if got, err := d.Get(ctx, 1, "k"); err != nil || string(got) != tt.wantValue {
				t.Errorf("Get = %q, %v, want %q", got, err, tt.wantValue)
			}
		})
	}
}

func TestAddRetriesAConflictAndReportsAnUnknownOutcome(t *testing.T) {
	f := newFake(t)
	d := f.client(t)
	ctx := context.Background()

	f.inject(fault{httpStatus: http.StatusConflict, status: statusAborted})
	if n, err := d.Add(ctx, 1, "n", 5); err != nil || n != 5 {
		t.Errorf("Add after an aborted commit = %d, %v, want 5, nil", n, err)
	}
	f.inject(fault{httpStatus: http.StatusGatewayTimeout, status: "DEADLINE_EXCEEDED", apply: true})
	if n, err := d.Add(ctx, 1, "n", 1); err == nil {
		t.Errorf("Add with a lost answer = %d, nil, want an error", n)
	}
}

type tokenFunc func(context.Context) (string, error)

func (f tokenFunc) Token(ctx context.Context) (string, error) { return f(ctx) }

func TestAccessToken(t *testing.T) {
	f := newFake(t)
	var asked atomic.Int64
	tokens := tokenFunc(func(context.Context) (string, error) {
		asked.Add(1)
		return fakeToken, nil
	})
	s, err := New(Config{ProjectID: "archfit-test"}, tokens, f.srv.Client())
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	if asked.Load() != 0 {
		t.Error("New asked for a token; startup must contact no server")
	}
	s.(*driver).base = f.srv.URL + "/v1/"
	if err := s.Put(context.Background(), 1, "k", []byte("v")); err != nil {
		t.Fatalf("Put with a valid token: %v", err)
	}
	if asked.Load() == 0 {
		t.Error("Put sent no token")
	}

	t.Run("a token error sends nothing", func(t *testing.T) {
		before := f.commitCount()
		failing, err := New(Config{ProjectID: "archfit-test"}, tokenFunc(func(context.Context) (string, error) {
			return "", errors.New("metadata server down")
		}), f.srv.Client())
		if err != nil {
			t.Fatalf("New: %v", err)
		}
		failing.(*driver).base = f.srv.URL + "/v1/"
		if err := failing.Put(context.Background(), 1, "k", []byte("x")); err == nil {
			t.Error("Put without a token = nil, want an error")
		}
		if f.commitCount() != before {
			t.Error("a request was sent without a token")
		}
	})
}

func TestEmulatorModeUsesPlainHTTPAndNoCredentials(t *testing.T) {
	var gotScheme, gotAuth string
	hc := &http.Client{Transport: roundTripper(func(r *http.Request) (*http.Response, error) {
		gotScheme, gotAuth = r.URL.Scheme, r.Header.Get("Authorization")
		return nil, errors.New("no network")
	})}
	s, err := New(Config{ProjectID: "archfit-test", EmulatorHost: "localhost:8086"}, nil, hc)
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	_, _ = s.Get(context.Background(), 1, "k")
	if gotScheme != "http" || gotAuth != "Bearer owner" {
		t.Errorf("scheme %q, Authorization %q; want http and the emulator's owner bypass", gotScheme, gotAuth)
	}
}

type roundTripper func(*http.Request) (*http.Response, error)

func (f roundTripper) RoundTrip(r *http.Request) (*http.Response, error) { return f(r) }

func TestNewValidatesConfig(t *testing.T) {
	tokens := staticToken("t")
	tests := []struct {
		name   string
		c      Config
		tokens TokenSource
		ok     bool
	}{
		{"project and default database", Config{ProjectID: "coupling-dev-510710"}, tokens, true},
		{"named database", Config{ProjectID: "coupling-dev-510710", DatabaseID: "archfit"}, tokens, true},
		{"explicit default database", Config{ProjectID: "coupling-dev-510710", DatabaseID: "(default)"}, tokens, true},
		{"emulator without tokens", Config{ProjectID: "archfit-test", EmulatorHost: "127.0.0.1:8086"}, nil, true},
		{"missing project", Config{}, tokens, false},
		{"project with a slash", Config{ProjectID: "coupling/dev"}, tokens, false},
		{"upper-case project", Config{ProjectID: "Coupling-dev"}, tokens, false},
		{"database with a slash", Config{ProjectID: "coupling-dev-510710", DatabaseID: "a/b"}, tokens, false},
		{"emulator host with a scheme", Config{ProjectID: "archfit-test", EmulatorHost: "http://localhost:8086"}, nil, false},
		{"no tokens outside the emulator", Config{ProjectID: "coupling-dev-510710"}, nil, false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			_, err := New(tt.c, tt.tokens, http.DefaultClient)
			if (err == nil) != tt.ok {
				t.Errorf("New(%+v) error = %v, want ok %v", tt.c, err, tt.ok)
			}
		})
	}
}

func TestDocumentIDIsValidForEveryKey(t *testing.T) {
	d := newFake(t).client(t)
	keys := []string{"entitlement", "feedback/lease/abc", "..", "__x__", "a b\xff", strings.Repeat("\xff", maxKeyLen)}
	for _, key := range keys {
		name := d.docName(9223372036854775807, key)
		id := strings.TrimPrefix(name, d.documents+"/"+collection+"/")
		if strings.Contains(id, "/") || len(id) > 1500 || id == "." || id == ".." || strings.HasPrefix(id, "__") {
			t.Errorf("document ID %q for key %q is not a valid Firestore ID", id, key)
		}
	}
	if a, b := d.docName(1, "2-a"), d.docName(12, "a"); a == b {
		t.Errorf("tenants 1 and 12 share the document %s", a)
	}
}

func TestCorruptDocumentIsAnErrorNotAMissingKey(t *testing.T) {
	f := newFake(t)
	d := f.client(t)
	f.put(d.docName(1, "k"), map[string]fsValue{fieldTenant: integerValue(1)})
	if _, err := d.Get(context.Background(), 1, "k"); err == nil || errors.Is(err, store.ErrNotFound) {
		t.Errorf("Get of a document without a value = %v, want an error other than ErrNotFound", err)
	}
	if _, err := d.CompareAndSwap(context.Background(), 1, "k", nil, []byte("v")); err == nil {
		t.Error("CompareAndSwap over a corrupt document = nil error, want an error")
	}
}

func TestErrorsCarryNoKeysOrValues(t *testing.T) {
	f := newFake(t)
	d := f.client(t)
	ctx := context.Background()
	key, value := "consent/"+marker, []byte(marker)
	f.inject(fault{httpStatus: http.StatusInternalServerError, status: "INTERNAL"})

	unreachable := newFake(t).client(t)
	unreachable.base = "http://127.0.0.1:1/v1/" // nothing listens on port 1

	_, missingErr := d.Get(ctx, 1, key)
	_, notFoundServer := d.CompareAndSwap(ctx, 1, key+"/x", []byte("old"), value)
	_, transportErr := unreachable.Get(ctx, 1, key)
	errs := map[string]error{
		"server error":    d.Put(ctx, 1, key, value),
		"transport error": transportErr,
		"too long value":  d.Put(ctx, 1, key, make([]byte, maxValueLen+1)),
		"corrupt counter": func() error {
			_ = d.Put(ctx, 1, key, value)
			_, err := d.Add(ctx, 1, key, 1)
			return err
		}(),
	}
	if !errors.Is(missingErr, store.ErrNotFound) || notFoundServer != nil {
		t.Fatalf("setup: Get = %v, CompareAndSwap = %v", missingErr, notFoundServer)
	}
	for name, err := range errs {
		switch {
		case err == nil:
			t.Errorf("%s: want an error", name)
		case strings.Contains(err.Error(), marker), strings.Contains(err.Error(), hex.EncodeToString([]byte(marker))):
			t.Errorf("%s: error leaks the key or the value: %v", name, err)
		case !strings.HasPrefix(err.Error(), "store:"):
			t.Errorf("%s: error %q lacks the store: prefix", name, err)
		}
	}
}

func TestDeleteTenantDeletesMoreThanOneBatch(t *testing.T) {
	d := newFake(t).client(t)
	ctx := context.Background()
	const n = deleteBatch + 7
	for i := range n {
		if err := d.Put(ctx, 4, fmt.Sprintf("k/%d", i), []byte("v")); err != nil {
			t.Fatalf("Put: %v", err)
		}
	}
	if err := d.Put(ctx, 5, "k", []byte("v")); err != nil {
		t.Fatalf("Put: %v", err)
	}
	if err := d.DeleteTenant(ctx, 4); err != nil {
		t.Fatalf("DeleteTenant: %v", err)
	}
	if got, err := d.Tenants(ctx); err != nil || !slices.Equal(got, []store.Tenant{5}) {
		t.Errorf("Tenants = %v, %v, want [5]", got, err)
	}
}

func TestStreamErrorIsAnError(t *testing.T) {
	ops := map[string]func(*driver) error{
		"Get":          func(d *driver) error { _, err := d.Get(context.Background(), 4, "k"); return err },
		"DeleteTenant": func(d *driver) error { return d.DeleteTenant(context.Background(), 4) },
		"Tenants":      func(d *driver) error { _, err := d.Tenants(context.Background()); return err },
	}
	for name, op := range ops {
		t.Run(name, func(t *testing.T) {
			f := newFake(t)
			d := f.client(t)
			if err := d.Put(context.Background(), 4, "k", []byte("v")); err != nil {
				t.Fatalf("Put: %v", err)
			}
			f.mu.Lock()
			f.streamErr = "INTERNAL"
			f.mu.Unlock()
			if err := op(d); err == nil || errors.Is(err, store.ErrNotFound) {
				t.Errorf("%s with a failed stream = %v, want an error", name, err)
			}
			if got, err := d.Get(context.Background(), 4, "k"); err != nil || string(got) != "v" {
				t.Errorf("Get after the failed stream = %q, %v, want the value kept", got, err)
			}
		})
	}
}

func TestMissingDatabaseIsAnErrorNotAMissingKey(t *testing.T) {
	f := newFake(t)
	s, err := New(Config{ProjectID: "no-such-project"}, staticToken(fakeToken), f.srv.Client())
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	d := s.(*driver)
	d.base = f.srv.URL + "/v1/"
	ctx := context.Background()
	if _, err := d.Get(ctx, 1, "entitlement"); err == nil || errors.Is(err, store.ErrNotFound) {
		t.Errorf("Get = %v, want an error other than ErrNotFound", err)
	}
	if ok, err := d.CompareAndSwap(ctx, 1, "lease", []byte("v"), nil); err == nil {
		t.Errorf("CompareAndSwap = %v, nil, want an error", ok)
	}
	if _, err := d.Add(ctx, 1, "n", 1); err == nil {
		t.Error("Add = nil error, want an error")
	}
	if got := f.commitCount(); got != 0 {
		t.Errorf("commits = %d, want 0: a failed read writes nothing", got)
	}
}

func TestWaitingForTheLocalLockHonorsTheContext(t *testing.T) {
	d := newFake(t).client(t)
	unlock, err := d.lock(context.Background(), d.docName(1, "lease"))
	if err != nil {
		t.Fatalf("lock: %v", err)
	}
	defer unlock()

	// The stripe is held, so only the context can end the wait.
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	done := make(chan error, 1)
	go func() {
		_, err := d.lock(ctx, d.docName(1, "lease"))
		done <- err
	}()
	select {
	case err := <-done:
		if !errors.Is(err, context.Canceled) {
			t.Errorf("lock = %v, want context.Canceled", err)
		}
	case <-time.After(10 * time.Second):
		t.Fatal("the wait for the local lock ignored the ended context")
	}
}

// The driver retries exactly the statuses Firestore uses for a failed precondition. The emulator run checks
// the classification against Firestore itself, not only against the fake.
func testPreconditionFailuresAreConflicts(t *testing.T, d *driver) {
	ctx := context.Background()
	name := d.docName(1, "k")
	if err := d.Put(ctx, 1, "k", []byte("v1")); err != nil {
		t.Fatalf("Put: %v", err)
	}
	first, found, err := d.read(ctx, name)
	if err != nil || !found {
		t.Fatalf("read = %v, %v", found, err)
	}
	if err := d.Put(ctx, 1, "k", []byte("v2")); err != nil {
		t.Fatalf("Put: %v", err)
	}
	stale := precondition(first, true)
	cases := []struct {
		name  string
		setup func() error
		w     write
	}{
		{"create over an existing document", nil, d.update(1, "k", []byte("x"), precondition(stored{}, false))},
		{"update after another write", nil, d.update(1, "k", []byte("x"), stale)},
		{"delete after another write", nil, write{Delete: name, CurrentDocument: stale}},
		{"update of a deleted document", func() error { return d.Delete(ctx, 1, "k") }, d.update(1, "k", []byte("x"), stale)},
	}
	for _, tc := range cases {
		if tc.setup != nil {
			if err := tc.setup(); err != nil {
				t.Fatalf("%s: setup: %v", tc.name, err)
			}
		}
		err := d.commit(ctx, tc.w)
		var apiErr *apiError
		if !errors.As(err, &apiErr) || !apiErr.conflict() {
			t.Errorf("%s: commit = %v, want a conflict status", tc.name, err)
		}
	}
}

func TestPreconditionFailuresAreConflicts(t *testing.T) {
	t.Run("fake", func(t *testing.T) { testPreconditionFailuresAreConflicts(t, newFake(t).client(t)) })
	t.Run("emulator", func(t *testing.T) {
		testPreconditionFailuresAreConflicts(t, newEmulatorStores(t, emulatorHost(t), 1)[0].(*driver))
	})
}

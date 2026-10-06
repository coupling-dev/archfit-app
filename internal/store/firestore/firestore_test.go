package firestore

import (
	"bytes"
	"context"
	"encoding/hex"
	"errors"
	"fmt"
	"hash/maphash"
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

// The two keys stand for the account record and its entitlement. Half of the callers list them in the other
// order, so a lock order that depends on the caller would deadlock.
func testCompareAndSwapAllAcrossClients(t *testing.T, clients []store.Store) {
	const perClient, rounds = 3, 20
	ctx := context.Background()
	for round := range rounds {
		var old []byte
		if round > 0 {
			old = []byte(strconv.Itoa(round - 1))
		}
		next := []byte(strconv.Itoa(round))
		var wins atomic.Int64
		errs := make([]error, len(clients)*perClient)
		var wg sync.WaitGroup
		for i := range errs {
			swaps := []store.Swap{{Key: "account", Old: old, Value: next}, {Key: "entitlement", Old: old, Value: next}}
			if i%2 == 1 {
				slices.Reverse(swaps)
			}
			wg.Go(func() {
				ok, err := clients[i%len(clients)].CompareAndSwapAll(ctx, 1, swaps)
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
		for _, k := range []string{"account", "entitlement"} {
			if got, err := clients[0].Get(ctx, 1, k); err != nil || !bytes.Equal(got, next) {
				t.Fatalf("round %d: %s = %q, %v, want %q", round, k, got, err, next)
			}
		}
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

// A key that a swap only checks, as missing or as unchanged, stays guarded until the commit: another instance's
// write between the read and the commit fails the whole call.
func TestCompareAndSwapAllGuardsCheckedKeys(t *testing.T) {
	tests := []struct {
		name    string
		stored  map[string]string
		checked store.Swap
		other   string // what another instance writes at the checked key between the read and the commit
	}{
		{"a key that must stay missing is created", nil, store.Swap{Key: "c"}, "other"},
		{"a key checked as unchanged changes", map[string]string{"c": "c1"}, store.Swap{Key: "c", Old: []byte("c1"), Value: []byte("c1")}, "c2"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			f := newFake(t)
			d := f.client(t)
			ctx := context.Background()
			for k, v := range tt.stored {
				if err := d.Put(ctx, 1, k, []byte(v)); err != nil {
					t.Fatalf("Put: %v", err)
				}
			}
			f.mu.Lock()
			f.beforeCommit = func(f *fakeFirestore) {
				name := d.docName(1, tt.checked.Key)
				f.docs[name] = fakeDoc{
					fields:     map[string]fsValue{fieldTenant: integerValue(1), fieldValue: bytesValue([]byte(tt.other))},
					updateTime: f.tick(),
				}
			}
			f.mu.Unlock()

			ok, err := d.CompareAndSwapAll(ctx, 1, []store.Swap{{Key: "a", Value: []byte("new")}, tt.checked})
			if err != nil || ok {
				t.Errorf("CompareAndSwapAll = %v, %v, want false, nil", ok, err)
			}
			if _, err := d.Get(ctx, 1, "a"); !errors.Is(err, store.ErrNotFound) {
				t.Errorf("Get a = %v, want ErrNotFound: the write next to the checked key landed", err)
			}
			if got, err := d.Get(ctx, 1, tt.checked.Key); err != nil || string(got) != tt.other {
				t.Errorf("Get %s = %q, %v, want the other instance's %q", tt.checked.Key, got, err, tt.other)
			}
		})
	}
}

func TestCompareAndSwapAllIsAtomicAcrossClients(t *testing.T) {
	t.Run("fake", func(t *testing.T) { testCompareAndSwapAllAcrossClients(t, fakeClients(t, 4)) })
	t.Run("emulator", func(t *testing.T) {
		testCompareAndSwapAllAcrossClients(t, newEmulatorStores(t, emulatorHost(t), 4))
	})
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
	old, next := []byte("old"), []byte("new")
	ops := []struct {
		name string
		keys []string
		swap func(context.Context, *driver) (bool, error)
	}{
		{"CompareAndSwap", []string{"k"}, func(ctx context.Context, d *driver) (bool, error) {
			return d.CompareAndSwap(ctx, 1, "k", old, next)
		}},
		{"CompareAndSwapAll", []string{"k", "j"}, func(ctx context.Context, d *driver) (bool, error) {
			return d.CompareAndSwapAll(ctx, 1, []store.Swap{{Key: "k", Old: old, Value: next}, {Key: "j", Old: old, Value: next}})
		}},
	}
	for _, op := range ops {
		for _, tt := range tests {
			t.Run(op.name+"/"+tt.name, func(t *testing.T) {
				f := newFake(t)
				d := f.client(t)
				var waits []time.Duration
				d.wait = func(_ context.Context, d time.Duration) error { waits = append(waits, d); return nil }
				ctx := context.Background()
				for _, k := range op.keys {
					if err := d.Put(ctx, 1, k, old); err != nil {
						t.Fatalf("Put: %v", err)
					}
				}
				before := f.commitCount()
				f.inject(tt.faults...)

				swapped, err := op.swap(ctx, d)
				if swapped != tt.wantSwapped || (err != nil) != tt.wantErr {
					t.Errorf("%s = %v, %v, want %v, error %v", op.name, swapped, err, tt.wantSwapped, tt.wantErr)
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
				for _, k := range op.keys {
					if got, err := d.Get(ctx, 1, k); err != nil || string(got) != tt.wantValue {
						t.Errorf("Get %s = %q, %v, want %q", k, got, err, tt.wantValue)
					}
				}
			})
		}
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
	if _, err := d.CompareAndSwapAll(context.Background(), 1, []store.Swap{{Key: "j", Value: []byte("v")}, {Key: "k", Value: []byte("v")}}); err == nil {
		t.Error("CompareAndSwapAll over a corrupt document = nil error, want an error")
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
		"server error": d.Put(ctx, 1, key, value),
		"server error of a multi-key swap": func() error {
			f.inject(fault{httpStatus: http.StatusInternalServerError, status: "INTERNAL"})
			_, err := d.CompareAndSwapAll(ctx, 1, []store.Swap{{Key: key, Value: value}, {Key: key + "/2", Value: value}})
			return err
		}(),
		"a key twice": func() error {
			_, err := d.CompareAndSwapAll(ctx, 1, []store.Swap{{Key: key, Value: value}, {Key: key, Value: value}})
			return err
		}(),
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
		"CompareAndSwapAll": func(d *driver) error {
			_, err := d.CompareAndSwapAll(context.Background(), 4, []store.Swap{{Key: "k", Old: []byte("v"), Value: []byte("x")}})
			return err
		},
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
	if ok, err := d.CompareAndSwapAll(ctx, 1, []store.Swap{{Key: "account"}, {Key: "entitlement", Value: []byte("v")}}); err == nil {
		t.Errorf("CompareAndSwapAll = %v, nil, want an error", ok)
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

// stripeMates returns two names of documents on one lock stripe.
func stripeMates(d *driver) (string, string) {
	first := d.docName(1, "k0")
	stripe := maphash.String(d.seed, first) % lockStripes
	for i := 1; ; i++ {
		name := d.docName(1, "k"+strconv.Itoa(i))
		if maphash.String(d.seed, name)%lockStripes == stripe {
			return first, name
		}
	}
}

func TestLockTakesEachStripeOnce(t *testing.T) {
	d := newFake(t).client(t)
	a, b := stripeMates(d)
	done := make(chan error, 1)
	go func() {
		unlock, err := d.lock(context.Background(), a, b)
		if err == nil {
			unlock()
		}
		done <- err
	}()
	select {
	case err := <-done:
		if err != nil {
			t.Fatalf("lock = %v", err)
		}
	case <-time.After(10 * time.Second):
		t.Fatal("lock of two documents on one stripe waits for itself")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	unlock, err := d.lock(ctx, a)
	if err != nil {
		t.Fatalf("the stripe stayed held after unlock: %v", err)
	}
	unlock()
}

func TestLockReleasesTheStripesItTookWhenTheContextEnds(t *testing.T) {
	d := newFake(t).client(t)
	var low, high string
	for i := 0; low == "" || high == ""; i++ {
		name := d.docName(1, "k"+strconv.Itoa(i))
		switch maphash.String(d.seed, name) % lockStripes {
		case 0:
			low = name
		case lockStripes - 1:
			high = name
		}
	}
	unlockHigh, err := d.lock(context.Background(), high)
	if err != nil {
		t.Fatalf("lock: %v", err)
	}
	defer unlockHigh()

	// The caller takes the low stripe, then waits for the held high stripe until its context ends.
	ctx, cancel := context.WithTimeout(context.Background(), 50*time.Millisecond)
	defer cancel()
	if _, err := d.lock(ctx, high, low); !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("lock = %v, want context.DeadlineExceeded", err)
	}
	free, cancelFree := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancelFree()
	unlock, err := d.lock(free, low)
	if err != nil {
		t.Fatalf("the low stripe stayed held: %v", err)
	}
	unlock()
}

// The driver retries exactly the statuses Firestore uses for a failed precondition. The emulator run checks
// the classification against Firestore itself, not only against the fake.
func testPreconditionFailuresAreConflicts(t *testing.T, d *driver) {
	ctx := context.Background()
	name := d.docName(1, "k")
	if err := d.Put(ctx, 1, "k", []byte("v1")); err != nil {
		t.Fatalf("Put: %v", err)
	}
	first, err := d.read(ctx, name)
	if err != nil || !first.found {
		t.Fatalf("read = %v, %v", first.found, err)
	}
	if err := d.Put(ctx, 1, "k", []byte("v2")); err != nil {
		t.Fatalf("Put: %v", err)
	}
	stale := precondition(first)
	missing := precondition(stored{})
	// other is missing; a write that creates it rides along with the failing write and must not land.
	createOther := d.update(1, "other", []byte("x"), missing)
	cases := []struct {
		name   string
		setup  func() error
		writes []write
	}{
		{"create over an existing document", nil, []write{d.update(1, "k", []byte("x"), missing)}},
		{"guard of a missing document that exists", nil, []write{{Delete: name, CurrentDocument: missing}}},
		{"update after another write", nil, []write{d.update(1, "k", []byte("x"), stale)}},
		{"delete after another write", nil, []write{{Delete: name, CurrentDocument: stale}}},
		{"a stale write after a valid write", nil, []write{createOther, d.update(1, "k", []byte("x"), stale)}},
		{"a stale write before a valid write", nil, []write{d.update(1, "k", []byte("x"), stale), createOther}},
		{"update of a deleted document", func() error { return d.Delete(ctx, 1, "k") }, []write{d.update(1, "k", []byte("x"), stale)}},
	}
	for _, tc := range cases {
		if tc.setup != nil {
			if err := tc.setup(); err != nil {
				t.Fatalf("%s: setup: %v", tc.name, err)
			}
		}
		err := d.commit(ctx, tc.writes...)
		var apiErr *apiError
		if !errors.As(err, &apiErr) || !apiErr.conflict() {
			t.Errorf("%s: commit = %v, want a conflict status", tc.name, err)
		}
	}
	if _, err := d.Get(ctx, 1, "other"); !errors.Is(err, store.ErrNotFound) {
		t.Errorf("Get other = %v, want ErrNotFound: a commit with a failed precondition wrote", err)
	}
	// CompareAndSwapAll keeps a key that must stay missing guarded with a delete under exists=false.
	if err := d.commit(ctx, write{Delete: d.docName(1, "never"), CurrentDocument: missing}, createOther); err != nil {
		t.Errorf("a guard of a missing document = %v, want success", err)
	}
	if got, err := d.Get(ctx, 1, "other"); err != nil || string(got) != "x" {
		t.Errorf("Get other = %q, %v, want the write next to the guard", got, err)
	}
}

func TestPreconditionFailuresAreConflicts(t *testing.T) {
	t.Run("fake", func(t *testing.T) { testPreconditionFailuresAreConflicts(t, newFake(t).client(t)) })
	t.Run("emulator", func(t *testing.T) {
		testPreconditionFailuresAreConflicts(t, newEmulatorStores(t, emulatorHost(t), 1)[0].(*driver))
	})
}

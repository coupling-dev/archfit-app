package gate

import (
	"context"
	"encoding/json"
	"errors"
	"sync"
	"testing"

	"github.com/coupling-dev/archfit-app/internal/gate/inputs"
	"github.com/coupling-dev/archfit-app/internal/gate/records"
	"github.com/coupling-dev/archfit-app/internal/store"
)

// readCounts counts the Store reads of one key kind: the installation's admission and the repository's record.
type readCounts struct {
	store.Store
	mu    sync.Mutex
	reads map[string]int
	fail  map[string]error // key -> error for every read of it
}

func (s *readCounts) Get(ctx context.Context, t store.Tenant, key string) ([]byte, error) {
	s.mu.Lock()
	s.reads[key]++
	err := s.fail[key]
	s.mu.Unlock()
	if err != nil {
		return nil, err
	}
	return s.Store.Get(ctx, t, key)
}

func (s *readCounts) count(key string) int {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.reads[key]
}

// memoWorld is the shop with an operator's admission record in the Store, read through a counting Store by a Gate and
// by Home.
type memoWorld struct {
	*world4
	counts *readCounts
	gate   *Gate
	home   HomeViews
}

func newMemoWorld(t *testing.T) *memoWorld {
	t.Helper()
	w := ownerViewWorld(t, OwnerWorkflow, 7)
	raw, err := json.Marshal(inputs.Admission{Account: "acme", By: "operator", ByID: 1})
	if err != nil {
		t.Fatal(err)
	}
	if err := w.mem.Put(t.Context(), store.Tenant(w.shop.InstallationID), inputs.AdmissionKey, raw); err != nil {
		t.Fatal(err)
	}
	counts := &readCounts{Store: w.store, reads: map[string]int{}, fail: map[string]error{}}
	admissions := NewAdmissions(counts, nil)
	g := compose(w.gh, w.oidc, counts, admissions, testAudience, w.clock)
	g.v4.engineApproved, g.v4.actionApproved = w.gate.v4.engineApproved, w.gate.v4.actionApproved
	return &memoWorld{world4: w, counts: counts, gate: g, home: NewHome(w.gh, counts, admissions)}
}

// request is what Home does for one authorized request: the Home reads, the Setup state and the owner review probes.
func (w *memoWorld) request(ctx context.Context) {
	_, _ = w.home.Home(ctx, w.shop)
	_, _ = w.gate.SetupState(ctx, w.shop)
	for _, a := range []OwnerAction{{Kind: OwnerFirst, Pull: 7}, {Kind: OwnerFirst}, {Kind: OwnerWorkflow, Pull: 7}} {
		_ = w.gate.OwnerReview(ctx, Target{Repo: w.shop}, a, oliviaAdmin)
	}
}

// Home, the Setup state and three owner review probes ask for the admission five times, and Home and the Setup state
// read the repository record twice. Under the request memo each is read once.
func TestRequestMemoReadsAdmissionAndRepositoryRecordOncePerRequest(t *testing.T) {
	repoKey := records.RepoKey(shopName)
	t.Run("without the memo", func(t *testing.T) {
		w := newMemoWorld(t)
		w.request(t.Context())
		t.Logf("admission reads = %d", w.counts.count(inputs.AdmissionKey))
		if got := w.counts.count(inputs.AdmissionKey); got != 5 {
			t.Fatalf("admission reads = %d, want 5", got)
		}
	})
	t.Run("with the memo", func(t *testing.T) {
		w := newMemoWorld(t)
		ctx := WithRequestMemo(t.Context())
		_, _ = w.home.Home(ctx, w.shop)
		_, _ = w.gate.SetupState(ctx, w.shop)
		homeAndSetup := w.counts.count(repoKey)
		for _, a := range []OwnerAction{{Kind: OwnerFirst, Pull: 7}, {Kind: OwnerFirst}, {Kind: OwnerWorkflow, Pull: 7}} {
			_ = w.gate.OwnerReview(ctx, Target{Repo: w.shop}, a, oliviaAdmin)
		}
		t.Logf("admission reads = %d, repository record reads by Home and Setup state = %d", w.counts.count(inputs.AdmissionKey), homeAndSetup)
		if got := w.counts.count(inputs.AdmissionKey); got != 1 {
			t.Fatalf("admission reads = %d, want 1", got)
		}
		if homeAndSetup != 1 {
			t.Fatalf("repository record reads by Home and Setup state = %d, want 1", homeAndSetup)
		}
	})
	t.Run("without the memo, Home and Setup state read the record twice", func(t *testing.T) {
		w := newMemoWorld(t)
		_, _ = w.home.Home(t.Context(), w.shop)
		_, _ = w.gate.SetupState(t.Context(), w.shop)
		if got := w.counts.count(repoKey); got != 2 {
			t.Fatalf("repository record reads = %d, want 2", got)
		}
	})
}

// A read that fails under the memo fails every step of the request the same way, and reads nothing twice.
func TestRequestMemoFailsClosedAtEveryStepOnAnUnreadableAdmissionOrRecord(t *testing.T) {
	for _, key := range []string{inputs.AdmissionKey, records.RepoKey(shopName)} {
		t.Run(key, func(t *testing.T) {
			w := newMemoWorld(t)
			down := errors.New("store down")
			w.counts.fail[key] = down
			ctx := WithRequestMemo(t.Context())
			if _, err := w.home.Home(ctx, w.shop); !errors.Is(err, down) {
				t.Fatalf("Home error = %v, want the store error", err)
			}
			if _, err := w.gate.SetupState(ctx, w.shop); !errors.Is(err, down) {
				t.Fatalf("SetupState error = %v, want the store error", err)
			}
			if got := w.counts.count(key); got != 1 {
				t.Fatalf("reads of %s = %d, want 1", key, got)
			}
		})
	}
}

// The memo is the request's: a request without it reads the record again, and does not see an earlier request's.
func TestRequestMemoDoesNotOutliveTheRequest(t *testing.T) {
	w := newMemoWorld(t)
	first, err := w.gate.SetupState(WithRequestMemo(t.Context()), w.shop)
	if err != nil {
		t.Fatal(err)
	}
	w.approved(w.shop, policy4+"# changed\n")
	second, err := w.gate.SetupState(WithRequestMemo(t.Context()), w.shop)
	if err != nil || second.ApprovedDigest == "" || second.ApprovedDigest == first.ApprovedDigest {
		t.Fatalf("a new request saw %+v, %v after %+v", second, err, first)
	}
	if got := w.counts.count(records.RepoKey(shopName)); got != 2 {
		t.Fatalf("record reads over two requests = %d, want 2", got)
	}
}

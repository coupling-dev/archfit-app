package records_test

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"os"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/alexei-led/archfit-app/internal/gate/records"
	"github.com/alexei-led/archfit-app/internal/store"
	"github.com/alexei-led/archfit-app/internal/store/firestore"
)

const (
	tenant   store.Tenant = 4711
	repoName              = "Acme/Shop"
)

var at = time.Date(2026, 11, 11, 9, 30, 0, 0, time.UTC)

// backend returns n clients of one fresh store. Every emulator client stands for one App instance with its own
// local lock, so only the commit preconditions keep the swaps atomic.
type backend struct {
	name    string
	clients func(t *testing.T, n int) []store.Store
}

var emulatorProjects atomic.Int64

func backends() []backend {
	return []backend{
		{"memory", func(_ *testing.T, n int) []store.Store {
			s := store.NewMemory()
			out := make([]store.Store, n)
			for i := range out {
				out[i] = s
			}
			return out
		}},
		{"emulator", func(t *testing.T, n int) []store.Store {
			t.Helper()
			// CI starts the official Firestore emulator and sets the host. Locally the case skips without it.
			host := os.Getenv("FIRESTORE_EMULATOR_HOST")
			if host == "" {
				t.Skip("FIRESTORE_EMULATOR_HOST is not set")
			}
			project := "records-" + strconv.FormatInt(time.Now().UnixNano(), 36) + "-" +
				strconv.FormatInt(emulatorProjects.Add(1), 10)
			out := make([]store.Store, n)
			for i := range out {
				s, err := firestore.New(firestore.Config{ProjectID: project, EmulatorHost: host}, nil,
					&http.Client{Timeout: 30 * time.Second})
				if err != nil {
					t.Fatalf("firestore.New: %v", err)
				}
				out[i] = s
			}
			return out
		}},
	}
}

// forEachBackend runs f on n clients of every backend.
func forEachBackend(t *testing.T, n int, f func(t *testing.T, clients []store.Store)) {
	for _, b := range backends() {
		t.Run(b.name, func(t *testing.T) { f(t, b.clients(t, n)) })
	}
}

func put(t *testing.T, s store.Store, key string, v records.Value) {
	t.Helper()
	raw, err := records.Encode(v)
	if err != nil {
		t.Fatalf("Encode: %v", err)
	}
	if err := s.Put(context.Background(), tenant, key, raw); err != nil {
		t.Fatalf("Put(%q): %v", key, err)
	}
}

func load[T any, P interface {
	*T
	records.Value
}](t *testing.T, s store.Store, key string) T {
	t.Helper()
	var v T
	if err := records.Load(context.Background(), s, tenant, key, P(&v)); err != nil {
		t.Fatalf("Load(%q): %v", key, err)
	}
	return v
}

func loadIndex(t *testing.T, s store.Store, key, schema string) records.Index {
	t.Helper()
	x := records.Index{Schema: schema}
	err := records.Load(context.Background(), s, tenant, key, &x)
	if err != nil && !errors.Is(err, records.ErrNoRecord) {
		t.Fatalf("Load(%q): %v", key, err)
	}
	return x
}

func approval(state string) *records.Approval {
	return &records.Approval{Kind: records.KindApprove, Request: 431, Change: json.RawMessage(`{"allow":"a -> b"}`),
		Owner: 99, Credential: "cred-1", At: at, State: state}
}

// openProposal stores an open proposal on pull request pr with its proposal index entry.
func openProposal(t *testing.T, s store.Store, id string, pr int, digest string) {
	t.Helper()
	p := &records.Proposal{PullRequest: pr, HeadSHA: "h" + id, ExpectedDigest: digest, State: records.StateOpen}
	err := records.Indexed(context.Background(), s, tenant, records.ProposalKey(repoName, id),
		records.ProposalIndexKey(repoName), records.SchemaProposalIndex,
		func(_ []byte, x *records.Index) ([]byte, error) {
			if err := x.Add(records.PullEntry(pr), id); err != nil {
				return nil, err
			}
			return records.Encode(p)
		})
	if err != nil {
		t.Fatalf("open proposal %s: %v", id, err)
	}
}

// recording is a Store double that remembers every key it was asked about.
type recording struct {
	store.Store
	mu   sync.Mutex
	keys []string
}

func (r *recording) note(keys ...string) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.keys = append(r.keys, keys...)
}

func (r *recording) Get(ctx context.Context, t store.Tenant, key string) ([]byte, error) {
	r.note(key)
	return r.Store.Get(ctx, t, key)
}

func (r *recording) Put(ctx context.Context, t store.Tenant, key string, v []byte) error {
	r.note(key)
	return r.Store.Put(ctx, t, key, v)
}

func (r *recording) Delete(ctx context.Context, t store.Tenant, key string) error {
	r.note(key)
	return r.Store.Delete(ctx, t, key)
}

func (r *recording) Add(ctx context.Context, t store.Tenant, key string, d int64) (int64, error) {
	r.note(key)
	return r.Store.Add(ctx, t, key, d)
}

func (r *recording) CompareAndSwap(ctx context.Context, t store.Tenant, key string, old, v []byte) (bool, error) {
	r.note(key)
	return r.Store.CompareAndSwap(ctx, t, key, old, v)
}

func (r *recording) CompareAndSwapAll(ctx context.Context, t store.Tenant, swaps []store.Swap) (bool, error) {
	for _, s := range swaps {
		r.note(s.Key)
	}
	return r.Store.CompareAndSwapAll(ctx, t, swaps)
}

func (r *recording) notV4() []string {
	r.mu.Lock()
	defer r.mu.Unlock()
	var out []string
	for _, k := range r.keys {
		if !strings.HasPrefix(k, records.Prefix) {
			out = append(out, k)
		}
	}
	return out
}

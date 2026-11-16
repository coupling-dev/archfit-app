package gate

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/alexei-led/archfit-app/internal/gate/records"
	"github.com/alexei-led/archfit-app/internal/github/api"
	"github.com/alexei-led/archfit-app/internal/store"
)

type homeReadStore struct {
	store.Store
	started      chan string
	release      chan struct{}
	active, peak atomic.Int64
	failures     map[string]error
}

func (s *homeReadStore) Get(ctx context.Context, tenant store.Tenant, key string) ([]byte, error) {
	if !strings.HasPrefix(key, "v4/request/") {
		return s.Store.Get(ctx, tenant, key)
	}
	active := s.active.Add(1)
	defer s.active.Add(-1)
	for old := s.peak.Load(); active > old; old = s.peak.Load() {
		if s.peak.CompareAndSwap(old, active) {
			break
		}
	}
	s.started <- key
	select {
	case <-s.release:
	case <-ctx.Done():
		return nil, ctx.Err()
	}
	if err := s.failures[key]; err != nil {
		return nil, err
	}
	return s.Store.Get(ctx, tenant, key)
}

func TestHomeRecordReadsOverlapWithBoundedConcurrencyAndStableErrors(t *testing.T) {
	failure := errors.New("record unavailable")
	for _, tc := range []struct {
		name          string
		first, second error
	}{
		{"success", nil, nil},
		{"rate limit wins in index order", api.ErrRateLimited, failure},
		{"timeout", nil, context.DeadlineExceeded},
	} {
		t.Run(tc.name, func(t *testing.T) {
			ctx, cancel := context.WithTimeout(t.Context(), 2*time.Second)
			defer cancel()
			mem := store.NewMemory()
			put := func(key string, value records.Value) {
				t.Helper()
				raw, err := records.Encode(value)
				if err != nil {
					t.Fatal(err)
				}
				if err := mem.Put(ctx, 1, key, raw); err != nil {
					t.Fatal(err)
				}
			}
			entries := make([]records.Entry, 0, 8)
			for i := 1; i <= 8; i++ {
				entries = append(entries, records.Entry{Key: fmt.Sprint(i), ID: fmt.Sprint(i)})
				put(records.RequestKey("acme/app", i), &records.Request{State: records.StateOpen, Rule: "graph", From: "app", To: "other"})
			}
			put(records.RequestIndexKey("acme/app"), &records.Index{Schema: records.SchemaRequestIndex, Entries: entries})
			memReads := &homeReadStore{Store: mem, started: make(chan string, 8), release: make(chan struct{}), failures: map[string]error{
				records.RequestKey("acme/app", 1): tc.first, records.RequestKey("acme/app", 2): tc.second,
			}}
			h := NewHome(&homeFiles{}, memReads, NewAdmissions(mem, []string{"acme"}))
			var out HomeState
			var err error
			var wg sync.WaitGroup
			wg.Go(func() {
				out, err = h.Home(ctx, api.Repo{RepoRef: api.RepoRef{InstallationID: 1, FullName: "acme/app"}, DefaultBranch: "main"})
			})
			for range 4 {
				select {
				case <-memReads.started:
				case <-ctx.Done():
					close(memReads.release)
					wg.Wait()
					t.Fatal("record reads did not overlap")
				}
			}
			close(memReads.release)
			wg.Wait()
			want := tc.first
			if want == nil {
				want = tc.second
			}
			if !errors.Is(err, want) {
				t.Fatalf("error = %v, want %v", err, want)
			}
			if peak := memReads.peak.Load(); peak != 4 {
				t.Fatalf("concurrency = %d, want 4", peak)
			}
			if want == nil {
				if len(out.Requests) != 8 {
					t.Fatalf("requests = %+v", out.Requests)
				}
				for i, r := range out.Requests {
					if r.Number != i+1 || r.Rule != "graph" {
						t.Fatalf("request = %+v", r)
					}
				}
			}
		})
	}
}

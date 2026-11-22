package gate

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/coupling-dev/archfit-app/internal/gate/weekly"

	"github.com/coupling-dev/archfit-app/internal/gate/records"
	"github.com/coupling-dev/archfit-app/internal/store"
)

type failingWeeklyStore struct{ store.Store }

func (s failingWeeklyStore) CompareAndSwapAll(ctx context.Context, tenant store.Tenant, swaps []store.Swap) (bool, error) {
	for _, sw := range swaps {
		if strings.HasPrefix(sw.Key, "v4/weekly/") {
			return false, errors.New("weekly unavailable")
		}
	}
	return s.Store.CompareAndSwapAll(ctx, tenant, swaps)
}

func TestWeeklyFailureDoesNotAffectGate(t *testing.T) {
	w, p, spec := advisoryWorld(t, policy4Advisory)
	w.gate.v4.store = failingWeeklyStore{w.gate.v4.store}
	w.wantStatus(w.send4(spec), "pass", "")
	w.wantGate(p, "completed", "success")
	var pull records.Pull
	if err := records.Load(context.Background(), w.mem, store.Tenant(p.repo.InstallationID), records.PullKey(p.repo.FullName, 7), &pull); err != nil {
		t.Fatal(err)
	}
	if pull.Status != "pass" {
		t.Fatalf("stored status = %q", pull.Status)
	}
}

func TestWeeklyRecordsAcceptedUpload(t *testing.T) {
	w, p, spec := advisoryWorld(t, policy4Advisory)
	w.wantStatus(w.send4(spec), "pass", "")
	got, err := weekly.Read(context.Background(), w.mem, store.Tenant(p.repo.InstallationID), p.repo.FullName, w.gate.now().Add(time.Second))
	if err != nil || !got.Known || got.Checked != 1 || got.Passed != 1 {
		t.Fatalf("summary = %+v, %v", got, err)
	}
}

func TestWeeklyClassifiesAcceptedFailures(t *testing.T) {
	for _, tc := range []struct {
		golden string
		owner  int
	}{{"graph", 1}, {"deny", 0}} {
		t.Run(tc.golden, func(t *testing.T) {
			w, p, spec := advisoryWorld(t, policy4Advisory)
			spec.result = w.result(tc.golden, p.head)
			w.usePolicy(spec, policy4Advisory)
			w.unrecorded()
			w.wantStatus(w.send4(spec), "fail", "")
			got, err := weekly.Read(context.Background(), w.mem, store.Tenant(p.repo.InstallationID), p.repo.FullName, w.gate.now().Add(time.Second))
			if err != nil || !got.Known || got.Checked != 1 || got.Passed != 0 || got.Owner != tc.owner {
				t.Fatalf("summary = %+v, %v", got, err)
			}
		})
	}
}

type slowWeeklyStore struct{ store.Store }

func (s slowWeeklyStore) Get(ctx context.Context, tenant store.Tenant, key string) ([]byte, error) {
	if strings.HasPrefix(key, "v4/weekly/") {
		<-ctx.Done()
		return nil, ctx.Err()
	}
	return s.Store.Get(ctx, tenant, key)
}
func TestSlowWeeklyDoesNotHoldUpload(t *testing.T) {
	w, p, spec := advisoryWorld(t, policy4Advisory)
	w.gate.v4.store = slowWeeklyStore{w.gate.v4.store}
	start := time.Now()
	w.wantStatus(w.send4(spec), "pass", "")
	if elapsed := time.Since(start); elapsed > 2*time.Second {
		t.Fatalf("upload held for %s", elapsed)
	}
	w.wantGate(p, "completed", "success")
}

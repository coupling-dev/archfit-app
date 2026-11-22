package gate

import (
	"context"
	"errors"
	"strings"
	"testing"

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

package inputs

import (
	"context"
	"encoding/json"
	"errors"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/coupling-dev/archfit-app/internal/store"
)

// countingStore answers every admission read with get and counts the reads. Safe for concurrent use.
type countingStore struct {
	store.Store
	get   func(installationID int64) ([]byte, error)
	reads atomic.Int64
}

func (s *countingStore) Get(_ context.Context, t store.Tenant, _ string) ([]byte, error) {
	s.reads.Add(1)
	return s.get(int64(t))
}

func admissionRecord(t *testing.T) []byte {
	t.Helper()
	raw, err := json.Marshal(Admission{Account: "octo-labs", By: "alexei-led", ByID: 1898375, At: time.Date(2026, 10, 22, 0, 0, 0, 0, time.UTC)})
	if err != nil {
		t.Fatal(err)
	}
	return raw
}

func TestAdmissionMemoReadsEachInstallationOncePerRequest(t *testing.T) {
	record := admissionRecord(t)
	s := &countingStore{get: func(id int64) ([]byte, error) {
		if id == 77 {
			return record, nil
		}
		return nil, store.ErrNotFound
	}}
	a := NewAdmissions(s, nil)
	ctx := WithAdmissionMemo(context.Background())
	for range 3 {
		if ok, err := a.Admitted(ctx, 77, "octo-labs"); !ok || err != nil {
			t.Fatalf("admitted = %t, %v", ok, err)
		}
	}
	if ok, err := a.Admitted(ctx, 78, "other"); ok || err != nil {
		t.Fatalf("not admitted = %t, %v", ok, err)
	}
	if got := s.reads.Load(); got != 2 {
		t.Fatalf("reads = %d, want one per installation", got)
	}
}

func TestAdmissionMemoSharesOneReadBetweenConcurrentCallers(t *testing.T) {
	record := admissionRecord(t)
	release := make(chan struct{})
	s := &countingStore{get: func(int64) ([]byte, error) { <-release; return record, nil }}
	a := NewAdmissions(s, nil)
	ctx := WithAdmissionMemo(context.Background())
	var callers sync.WaitGroup
	results := make([]bool, 8)
	for i := range results {
		callers.Go(func() { results[i], _ = a.Admitted(ctx, 77, "octo-labs") })
	}
	// The first caller holds the read; the others wait for its answer instead of reading again.
	close(release)
	callers.Wait()
	for i, ok := range results {
		if !ok {
			t.Fatalf("caller %d: not admitted", i)
		}
	}
	if got := s.reads.Load(); got != 1 {
		t.Fatalf("reads = %d, want 1", got)
	}
}

// A failed read stays failed for the rest of the request: no step sees the installation as admitted, and none retries.
func TestAdmissionMemoRemembersAFailedReadAndNeverAdmitsOnIt(t *testing.T) {
	down := errors.New("store down")
	for name, get := range map[string]func(int64) ([]byte, error){
		"store down":     func(int64) ([]byte, error) { return nil, down },
		"bad record":     func(int64) ([]byte, error) { return []byte("{"), nil },
		"no operator ID": func(int64) ([]byte, error) { return []byte("{}"), nil },
	} {
		t.Run(name, func(t *testing.T) {
			s := &countingStore{get: get}
			a := NewAdmissions(s, nil)
			ctx := WithAdmissionMemo(context.Background())
			for range 2 {
				if ok, err := a.Admitted(ctx, 77, "octo-labs"); ok || err == nil {
					t.Fatalf("admitted = %t, %v; want false and an error", ok, err)
				}
			}
			if got := s.reads.Load(); got != 1 {
				t.Fatalf("reads = %d, want 1", got)
			}
		})
	}
}

// Without the memo every call reads fresh, so an admission an operator just changed is seen at once.
func TestAdmissionWithoutMemoReadsEveryTime(t *testing.T) {
	admitted := atomic.Bool{}
	record := admissionRecord(t)
	s := &countingStore{get: func(int64) ([]byte, error) {
		if admitted.Load() {
			return record, nil
		}
		return nil, store.ErrNotFound
	}}
	a := NewAdmissions(s, nil)
	if ok, _ := a.Admitted(context.Background(), 77, "octo-labs"); ok {
		t.Fatal("admitted before the operator approved")
	}
	admitted.Store(true)
	if ok, err := a.Admitted(context.Background(), 77, "octo-labs"); !ok || err != nil {
		t.Fatalf("admitted = %t, %v after the operator approved", ok, err)
	}
	if got := s.reads.Load(); got != 2 {
		t.Fatalf("reads = %d, want 2", got)
	}
}

// One request's memo never answers for another Admissions, nor for a context without it.
func TestAdmissionMemoIsPerAdmissionsAndPerContext(t *testing.T) {
	record := admissionRecord(t)
	one := &countingStore{get: func(int64) ([]byte, error) { return record, nil }}
	other := &countingStore{get: func(int64) ([]byte, error) { return nil, store.ErrNotFound }}
	ctx := WithAdmissionMemo(context.Background())
	if ok, _ := NewAdmissions(one, nil).Admitted(ctx, 77, "octo-labs"); !ok {
		t.Fatal("first Admissions did not admit")
	}
	if ok, _ := NewAdmissions(other, nil).Admitted(ctx, 77, "octo-labs"); ok {
		t.Fatal("a memo for one Admissions answered for another")
	}
	if ok, _ := NewAdmissions(other, nil).Admitted(WithAdmissionMemo(context.Background()), 77, "octo-labs"); ok {
		t.Fatal("a memo carried over to another request")
	}
	if WithAdmissionMemo(ctx) != ctx {
		t.Fatal("installing the memo twice replaced it")
	}
}

package store_test

import (
	"bytes"
	"context"
	"errors"
	"os/exec"
	"slices"
	"strconv"
	"strings"
	"sync"
	"testing"

	"github.com/alexei-led/archfit-app/internal/store"
	"github.com/alexei-led/archfit-app/internal/store/storetest"
)

const (
	storePkg  = "github.com/alexei-led/archfit-app/internal/store"
	budgetKey = "llm-budget/2026-09"
)

func TestConformance(t *testing.T) {
	storetest.Run(t, store.NewMemory)
}

func TestStoreImportsOnlyStdlib(t *testing.T) {
	// The test runs in the package folder, so ./... is ./internal/store/... from the root.
	out, err := exec.Command("go", "list", "-deps", "-f", "{{if not .Standard}}{{.ImportPath}}{{end}}", "./...").Output()
	if err != nil {
		t.Fatalf("go list: %v", err)
	}
	if !strings.Contains(string(out), storePkg) {
		t.Fatalf("go list did not list %s: %q", storePkg, out)
	}
	// store, storetest and the drivers under store/ import the standard library and store only: a driver
	// reaches its platform over net/http and takes credentials through an interface.
	for pkg := range strings.FieldsSeq(string(out)) {
		if pkg != storePkg && !strings.HasPrefix(pkg, storePkg+"/") {
			t.Errorf("store depends on %s; only the standard library is allowed", pkg)
		}
	}
}

func TestErrorsCarryNoValues(t *testing.T) {
	const marker = "SECRET-MARKER"
	ctx := context.Background()
	s := store.NewMemory()
	value := []byte(marker)
	if err := s.Put(ctx, 1, marker, value); err != nil {
		t.Fatalf("Put: %v", err)
	}
	if err := s.Put(ctx, 1, marker+"/max", []byte(strconv.FormatInt(1<<62, 10))); err != nil {
		t.Fatalf("Put: %v", err)
	}
	longKey := strings.Repeat(marker, 40)
	nulKey := marker + "\x00"
	bigValue := bytes.Repeat(value, 64<<10/len(marker)+1)

	errs := map[string]error{
		"Put empty key":         s.Put(ctx, 1, "", value),
		"Put long key":          s.Put(ctx, 1, longKey, value),
		"Put NUL key":           s.Put(ctx, 1, nulKey, value),
		"Put negative tenant":   s.Put(ctx, -1, marker, value),
		"Put big value":         s.Put(ctx, 1, marker, bigValue),
		"Delete long key":       s.Delete(ctx, 1, longKey),
		"Delete NUL key":        s.Delete(ctx, 1, nulKey),
		"Delete negative":       s.Delete(ctx, -1, marker),
		"DeleteTenant negative": s.DeleteTenant(ctx, -1),
		"DeleteTenant system":   s.DeleteTenant(ctx, store.SystemTenant),
	}
	_, errs["Get long key"] = s.Get(ctx, 1, longKey)
	_, errs["Get NUL key"] = s.Get(ctx, 1, nulKey)
	_, errs["Get negative tenant"] = s.Get(ctx, -1, marker)
	_, errs["Get missing"] = s.Get(ctx, 1, marker+"/missing")
	_, errs["Add long key"] = s.Add(ctx, 1, longKey, 1)
	_, errs["Add NUL key"] = s.Add(ctx, 1, nulKey, 1)
	_, errs["Add negative tenant"] = s.Add(ctx, -1, marker, 1)
	_, errs["Add non-counter"] = s.Add(ctx, 1, marker, 1)
	_, errs["Add overflow"] = s.Add(ctx, 1, marker+"/max", 1<<62)

	for name, err := range errs {
		switch {
		case err == nil:
			t.Errorf("%s: want an error", name)
		case strings.Contains(err.Error(), marker):
			t.Errorf("%s: error text leaks the key or the value: %v", name, err)
		case !strings.HasPrefix(err.Error(), "store:"):
			t.Errorf("%s: error %q lacks the store: prefix", name, err)
		}
	}
}

func installationKeys() []string {
	return []string{"entitlement", "account", "consent/source_processing"}
}

func seedInstallations(t *testing.T, s store.Store, tenants ...store.Tenant) {
	t.Helper()
	ctx := context.Background()
	for _, tn := range tenants {
		for _, k := range installationKeys() {
			if err := s.Put(ctx, tn, k, []byte(k)); err != nil {
				t.Fatalf("Put(%d, %q): %v", tn, k, err)
			}
		}
	}
	if _, err := s.Add(ctx, store.SystemTenant, budgetKey, 250); err != nil {
		t.Fatalf("Add budget: %v", err)
	}
}

func wantInstallation(t *testing.T, s store.Store, tn store.Tenant, present bool) {
	t.Helper()
	for _, k := range installationKeys() {
		got, err := s.Get(context.Background(), tn, k)
		switch {
		case present && (err != nil || string(got) != k):
			t.Errorf("Get(%d, %q) = %q, %v, want it intact", tn, k, got, err)
		case !present && !errors.Is(err, store.ErrNotFound):
			t.Errorf("Get(%d, %q) error = %v, want ErrNotFound", tn, k, err)
		}
	}
}

func wantBudget(t *testing.T, s store.Store, want int64) {
	t.Helper()
	got, err := s.Add(context.Background(), store.SystemTenant, budgetKey, 0)
	if err != nil || got != want {
		t.Errorf("budget counter = %d, %v, want %d, nil", got, err, want)
	}
}

func TestUninstallRemovesOneTenantOnly(t *testing.T) {
	s := store.NewMemory()
	seedInstallations(t, s, 1001, 1002)

	if err := s.DeleteTenant(context.Background(), 1001); err != nil {
		t.Fatalf("DeleteTenant: %v", err)
	}

	wantInstallation(t, s, 1001, false)
	wantInstallation(t, s, 1002, true)
	wantBudget(t, s, 250)
}

func TestMissedUninstallIsFoundBySweep(t *testing.T) {
	ctx := context.Background()
	s := store.NewMemory()
	seedInstallations(t, s, 1, 2, 3)
	installed := []store.Tenant{1, 3}

	sweep := func() []store.Tenant {
		tenants, err := s.Tenants(ctx)
		if err != nil {
			t.Fatalf("Tenants: %v", err)
		}
		var deleted []store.Tenant
		for _, tn := range tenants {
			if slices.Contains(installed, tn) {
				continue
			}
			if err := s.DeleteTenant(ctx, tn); err != nil {
				t.Fatalf("DeleteTenant(%d): %v", tn, err)
			}
			deleted = append(deleted, tn)
		}
		return deleted
	}

	if got, err := s.Tenants(ctx); err != nil || !slices.Equal(got, []store.Tenant{1, 2, 3}) {
		t.Fatalf("Tenants = %v, %v, want [1 2 3], nil", got, err)
	}
	if got := sweep(); !slices.Equal(got, []store.Tenant{2}) {
		t.Errorf("first pass deleted %v, want [2]", got)
	}
	wantInstallation(t, s, 2, false)
	wantInstallation(t, s, 1, true)
	wantInstallation(t, s, 3, true)
	wantBudget(t, s, 250)
	if got := sweep(); len(got) != 0 {
		t.Errorf("second pass deleted %v, want nothing", got)
	}
}

// spend is the caller pattern of control/account: reserve the maximum, roll back over the cap,
// settle to the actual cost under it.
func spend(s store.Store, limit, reserved, actual int64) (bool, error) {
	ctx := context.Background()
	n, err := s.Add(ctx, store.SystemTenant, budgetKey, reserved)
	if err != nil {
		return false, err
	}
	if n > limit {
		_, err = s.Add(ctx, store.SystemTenant, budgetKey, -reserved)
		return false, err
	}
	_, err = s.Add(ctx, store.SystemTenant, budgetKey, actual-reserved)
	return true, err
}

func TestBudgetReserveRollbackSettle(t *testing.T) {
	const limit, reserved = 100, 30
	s := store.NewMemory()

	var settled int64
	for _, actual := range []int64{12, 30, 18} {
		ok, err := spend(s, limit, reserved, actual)
		if err != nil || !ok {
			t.Fatalf("spend(%d) = %v, %v, want true, nil", actual, ok, err)
		}
		settled += actual
	}
	wantBudget(t, s, settled)

	if ok, err := spend(s, limit, 50, 1); err != nil || ok {
		t.Errorf("spend over the cap = %v, %v, want false, nil", ok, err)
	}
	wantBudget(t, s, settled)

	// 60 spent: only one more reservation of 30 fits under 100.
	const racers, actual = 2, 20
	oks := make([]bool, racers)
	errs := make([]error, racers)
	var wg sync.WaitGroup
	for i := range racers {
		wg.Go(func() { oks[i], errs[i] = spend(s, limit, reserved, actual) })
	}
	wg.Wait()
	if err := errors.Join(errs...); err != nil {
		t.Fatalf("spend: %v", err)
	}
	for _, ok := range oks {
		if ok {
			settled += actual
		}
	}
	if oks[0] && oks[1] {
		t.Error("both racers stayed under the cap, want at most one")
	}
	wantBudget(t, s, settled)
}

var errOutage = errors.New("i/o error")

type failingStore struct{}

func (failingStore) Get(context.Context, store.Tenant, string) ([]byte, error) {
	return nil, errOutage
}
func (failingStore) Put(context.Context, store.Tenant, string, []byte) error { return errOutage }
func (failingStore) Delete(context.Context, store.Tenant, string) error      { return errOutage }
func (failingStore) Add(context.Context, store.Tenant, string, int64) (int64, error) {
	return 0, errOutage
}
func (failingStore) CompareAndSwap(context.Context, store.Tenant, string, []byte, []byte) (bool, error) {
	return false, errOutage
}
func (failingStore) DeleteTenant(context.Context, store.Tenant) error { return errOutage }
func (failingStore) Tenants(context.Context) ([]store.Tenant, error)  { return nil, errOutage }

func TestOutageIsAnErrorNotADefault(t *testing.T) {
	ctx := context.Background()
	var s store.Store = failingStore{}

	_, getErr := s.Get(ctx, 1, "entitlement")
	_, addErr := s.Add(ctx, store.SystemTenant, budgetKey, 1)
	_, swapErr := s.CompareAndSwap(ctx, 1, "lease", nil, []byte("v"))
	_, tenantsErr := s.Tenants(ctx)
	errs := map[string]error{
		"Get":            getErr,
		"Put":            s.Put(ctx, 1, "entitlement", []byte("v")),
		"Delete":         s.Delete(ctx, 1, "entitlement"),
		"Add":            addErr,
		"CompareAndSwap": swapErr,
		"DeleteTenant":   s.DeleteTenant(ctx, 1),
		"Tenants":        tenantsErr,
	}
	for op, err := range errs {
		if !errors.Is(err, errOutage) {
			t.Errorf("%s error = %v, want the outage error", op, err)
		}
		if errors.Is(err, store.ErrNotFound) {
			t.Errorf("%s: an outage must not read as a missing key", op)
		}
	}
}

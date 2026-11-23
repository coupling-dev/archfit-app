package proposals

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/coupling-dev/archfit-app/internal/gate/records"
	"github.com/coupling-dev/archfit-app/internal/gate/requests"
	"github.com/coupling-dev/archfit-app/internal/store"
)

// newWorldWithPolicy is newWorld with another policy on the default branch.
func newWorldWithPolicy(t *testing.T, policy string) *world {
	t.Helper()
	w := &world{t: t, hub: newFakeHub(policy), store: store.NewMemory(), tokens: map[string]string{}}
	w.svc = New(w.hub, w.store, func() time.Time { return testNow })
	w.svc.sleep = func(ctx context.Context, _ time.Duration) error { return ctx.Err() }
	w.approveTip()
	return w
}

func TestMakeRequiredMergesThePolicyWithGateRequired(t *testing.T) {
	w := newWorld(t)
	v, err := w.svc.MakeRequiredPreview(context.Background(), testRepo, olivia)
	if err != nil {
		t.Fatal(err)
	}
	if v.Digest == "" || !strings.Contains(v.Diff, "+gate: required") || v.Against != AgainstDefaultBranch {
		t.Fatalf("preview %+v", v)
	}
	token := "required-token"
	w.tokens[token] = v.Digest
	out, err := w.svc.MakeRequired(context.Background(), testRepo, olivia, w.redeem(token))
	if err != nil || out.State != requests.PullMerged {
		t.Fatalf("MakeRequired = %+v, %v", out, err)
	}
	if got, want := w.hub.file(fakeBase, policyPath), testPolicy+"gate: required\n"; got != want {
		t.Fatalf("merged file = %q, want %q", got, want)
	}
	a := load[records.Approval](t, w.store, records.ApprovalKey(testRepo.FullName, out.Approval))
	if a.Kind != records.KindEdit || a.Login != "olivia" || a.Credential != "cred-required-token" {
		t.Fatalf("approval %+v", a)
	}
	if len(w.redeemed) != 1 || w.redeemed[0] != v.Digest {
		t.Fatalf("redeemed %v, want %q", w.redeemed, v.Digest)
	}
}

func TestMakeRequiredRefusesANonOwner(t *testing.T) {
	w := newWorld(t)
	before := len(w.hub.writes())
	mallory := Owner{ID: 900, Login: "mallory"}
	if _, err := w.svc.MakeRequiredPreview(context.Background(), testRepo, mallory); !errors.Is(err, ErrNotOwner) {
		t.Fatalf("preview err = %v, want ErrNotOwner", err)
	}
	_, err := w.svc.MakeRequired(context.Background(), testRepo, mallory, w.redeem("unused"))
	if !errors.Is(err, ErrNotOwner) {
		t.Fatalf("err = %v, want ErrNotOwner", err)
	}
	if len(w.hub.writes()) != before || len(w.redeemed) != 0 {
		t.Errorf("a refusal wrote or redeemed: %v", w.hub.writes()[before:])
	}
}

func TestMakeRequiredIsANoOpWhenTheGateIsRequired(t *testing.T) {
	w := newWorldWithPolicy(t, testPolicy+"gate: required\n")
	before := len(w.hub.writes())
	v, err := w.svc.MakeRequiredPreview(context.Background(), testRepo, olivia)
	if err != nil || v.Diff != "" {
		t.Fatalf("preview %+v, %v", v, err)
	}
	out, err := w.svc.MakeRequired(context.Background(), testRepo, olivia, func(context.Context, string) (string, error) {
		t.Error("the passkey was redeemed for a no-op")
		return "", errToken
	})
	if err != nil || out != (Outcome{}) {
		t.Fatalf("MakeRequired = %+v, %v", out, err)
	}
	if len(w.hub.writes()) != before {
		t.Errorf("a no-op wrote: %v", w.hub.writes()[before:])
	}
}

func TestMakeRequiredRefusesANonGateDifference(t *testing.T) {
	w := newWorld(t)
	w.svc.requireGate = func([]byte) ([]byte, error) {
		altered := strings.Replace(testPolicy, "owners: [olivia, oscar]", "owners: [olivia]", 1)
		return []byte(altered + "gate: required\n"), nil
	}
	before := len(w.hub.writes())
	_, err := w.svc.MakeRequired(context.Background(), testRepo, olivia, w.redeem("unused"))
	if err == nil || !strings.Contains(err.Error(), "another policy field") {
		t.Fatalf("err = %v, want a non-gate change refusal", err)
	}
	if len(w.hub.writes()) != before || len(w.redeemed) != 0 {
		t.Errorf("a refusal wrote or redeemed: %v", w.hub.writes()[before:])
	}
}

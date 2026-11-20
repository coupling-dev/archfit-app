package approvals

import (
	"context"
	"testing"

	"github.com/coupling-dev/archfit-app/internal/gate/records"
)

func TestOnboardingCheckRequiresTheExactOpenApproval(t *testing.T) {
	for _, tc := range []struct {
		name   string
		change func(*world, Done)
		want   bool
	}{
		{"matches", func(*world, Done) {}, true},
		{"head moved", func(w *world, _ Done) { w.openPull(12, "h2", policyV(1), "wf-1") }, false},
		{"policy changed", func(w *world, _ Done) { w.gh.files["h1"] = policyV(2) }, false},
		{"workflow changed", func(w *world, _ Done) { w.gh.workflows["h1"] = "wf-2" }, false},
		{"policy missing", func(w *world, _ Done) { delete(w.gh.files, "h1") }, false},
		{"workflow missing", func(w *world, _ Done) { delete(w.gh.workflows, "h1") }, false},
		{"activated", func(w *world, _ Done) { w.approvedAt(digestV(1), "c0") }, false},
		{"default policy installed", func(w *world, _ Done) { w.gh.commit("c1", policyV(1)) }, false},
		{"closed", func(w *world, _ Done) { p := w.gh.pulls[12]; p.State = "closed"; w.gh.pulls[12] = p }, false},
		{"fork", func(w *world, _ Done) { p := w.gh.pulls[12]; p.HeadRepo = "fork/shop"; w.gh.pulls[12] = p }, false},
		{"other base", func(w *world, _ Done) { p := w.gh.pulls[12]; p.BaseRef = "other"; w.gh.pulls[12] = p }, false},
		{"void approval", func(w *world, d Done) {
			a := w.approval(d.Approval)
			a.State = records.StateVoid
			raw, err := records.Encode(&a)
			if err != nil {
				w.t.Fatal(err)
			}
			if err := w.mem.Put(context.Background(), w.tenant(), records.ApprovalKey(testRepo.FullName, d.Approval), raw); err != nil {
				w.t.Fatal(err)
			}
		}, false},
		{"no proposal", func(w *world, _ Done) {
			if err := w.mem.Delete(context.Background(), w.tenant(), records.ProposalIndexKey(testRepo.FullName)); err != nil {
				w.t.Fatal(err)
			}
		}, false},
		{"outside flow", func(w *world, _ Done) {
			raw, err := records.Encode(&records.Repo{OutsideFlow: &records.OutsideFlow{Digest: digestV(1)}})
			if err != nil {
				w.t.Fatal(err)
			}
			if err := w.mem.Put(context.Background(), w.tenant(), records.RepoKey(testRepo.FullName), raw); err != nil {
				w.t.Fatal(err)
			}
		}, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			w := newWorld(t)
			w.gh.bare("c0")
			w.openPull(12, "h1", policyV(1), "wf-1")
			d, err := w.act(admin, Action{Kind: records.KindFirst, Pull: 12})
			if err != nil {
				t.Fatal(err)
			}
			tc.change(w, d)
			check, err := w.svc.OnboardingCheck(context.Background(), testRepo, 12, "h1")
			if err != nil {
				t.Fatal(err)
			}
			if (check != nil) != tc.want {
				t.Fatalf("check = %+v, want present %v", check, tc.want)
			}
			if check != nil && check.ExternalID != "approval:"+d.Approval {
				t.Errorf("check = %+v", check)
			}
		})
	}
}

func TestOnboardingCheckDoesNotHideReadErrors(t *testing.T) {
	w := newWorld(t)
	w.gh.bare("c0")
	w.openPull(12, "h1", policyV(1), "wf-1")
	if _, err := w.act(admin, Action{Kind: records.KindFirst, Pull: 12}); err != nil {
		t.Fatal(err)
	}
	if err := w.mem.Put(context.Background(), w.tenant(), records.ProposalIndexKey(testRepo.FullName), []byte("broken")); err != nil {
		t.Fatal(err)
	}
	if check, err := w.svc.OnboardingCheck(context.Background(), testRepo, 12, "h1"); err == nil || check != nil {
		t.Errorf("check = %+v, err = %v, want a read error", check, err)
	}
}

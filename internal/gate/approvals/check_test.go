package approvals

import (
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/alexei-led/archfit-app/internal/gate/proposals"
	"github.com/alexei-led/archfit-app/internal/gate/records"
)

func TestPullApprovalSetsAnOwnerVerdictOnce(t *testing.T) {
	for _, kind := range []string{records.KindFirst, records.KindWorkflow} {
		t.Run(kind, func(t *testing.T) {
			w := newWorld(t)
			if kind == records.KindFirst {
				w.gh.bare("c0")
			} else {
				w.gh.commit("c0", policyV(0))
				w.approvedAt(digestV(0), "c0")
			}
			w.openPull(12, "h1", policyV(1), "wf-1")
			done, err := w.act(admin, Action{Kind: kind, Pull: 12})
			if err != nil {
				t.Fatal(err)
			}
			if len(w.gh.checks) != 1 {
				t.Fatalf("checks = %+v, want one owner verdict", w.gh.checks)
			}
			check := w.gh.checks[0]
			if check.Name != "archfit/gate" || check.HeadSHA != "h1" || check.Status != "completed" ||
				check.Conclusion != "success" || check.ExternalID != "approval:"+done.Approval {
				t.Errorf("check = %+v", check)
			}
			for _, want := range []string{"olivia", "#12", "h1", digestV(1), records.FileDigest([]byte("wf-1")), "not an engine pass"} {
				if !strings.Contains(check.Summary, want) {
					t.Errorf("summary %q does not contain %q", check.Summary, want)
				}
			}
			if _, ok := proposals.ProposalOf(check); ok {
				t.Error("owner verdict was mistaken for a proposal verdict")
			}
			if _, err := w.act(admin, Action{Kind: kind, Pull: 12}); !errors.Is(err, ErrNotDue) {
				t.Errorf("second Act = %v, want ErrNotDue", err)
			}
			if len(w.gh.checks) != 1 || len(w.proposal(w.approval(done.Approval).Proposal).Approvals) != 1 {
				t.Error("second Act wrote another check or approval")
			}
		})
	}
}

func TestPullApprovalCheckFailureDoesNotUndoApproval(t *testing.T) {
	w := newWorld(t)
	w.gh.bare("c0")
	w.openPull(12, "h1", policyV(1), "wf-1")
	w.gh.failChecks(errors.New("github: 502"))
	done, err := w.act(admin, Action{Kind: records.KindFirst, Pull: 12})
	if err != nil {
		t.Fatal(err)
	}
	if a := w.approval(done.Approval); a.Login != admin.Login || a.State != records.StateOpen {
		t.Errorf("approval = %+v", a)
	}
	if len(w.gh.checks) != 0 {
		t.Errorf("checks = %+v, want no successful write", w.gh.checks)
	}
}

func TestPullApprovalNeverWritesSuccessOnAMovedHead(t *testing.T) {
	for _, duringRedeem := range []bool{false, true} {
		t.Run(map[bool]string{false: "before Act", true: "during Act"}[duringRedeem], func(t *testing.T) {
			w := newWorld(t)
			w.gh.bare("c0")
			w.openPull(12, "h1", policyV(1), "wf-1")
			ctx := context.Background()
			a := Action{Kind: records.KindFirst, Pull: 12}
			c, err := w.svc.Bind(ctx, testRepo, a)
			if err != nil {
				t.Fatal(err)
			}
			move := func() { w.openPull(12, "h2", policyV(1), "wf-1") }
			if !duringRedeem {
				move()
			}
			_, err = w.svc.Act(ctx, testRepo, Input{Action: a, Owner: admin, Digest: c.Digest(),
				Redeem: func(context.Context, string) (string, error) { move(); return passkeyID, nil }})
			if duringRedeem && err != nil || !duringRedeem && !errors.Is(err, ErrMoved) {
				t.Fatalf("Act = %v", err)
			}
			if duringRedeem && (len(w.gh.checks) != 1 || w.gh.checks[0].HeadSHA != "h1") {
				t.Errorf("checks = %+v, want only the approved h1", w.gh.checks)
			}
			for _, check := range w.gh.checks {
				if check.HeadSHA == "h2" {
					t.Error("new head got the old approval's success")
				}
			}
		})
	}
}

func TestTipApprovalWritesNoCheck(t *testing.T) {
	for _, kind := range []string{records.KindFirst, records.KindConfirm} {
		t.Run(kind, func(t *testing.T) {
			w := newWorld(t)
			w.gh.commit("c0", policyV(0))
			if kind == records.KindConfirm {
				w.approvedAt(digestV(0), "c0")
				w.gh.commit("c1", policyV(1))
				w.push("c0", "c1")
			}
			if _, err := w.act(admin, Action{Kind: kind}); err != nil {
				t.Fatal(err)
			}
			if len(w.gh.checks) != 0 {
				t.Errorf("checks = %+v, want none", w.gh.checks)
			}
		})
	}
}

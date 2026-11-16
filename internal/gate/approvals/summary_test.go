package approvals

import (
	"context"
	"errors"
	"testing"

	"github.com/alexei-led/archfit-app/internal/gate/records"
)

func TestApprovalSummaryMatchesReview(t *testing.T) {
	for _, kind := range []string{records.KindFirst, records.KindWorkflow} {
		for _, change := range []string{"exact head", "changed head", "changed policy", "changed workflow", "unreadable", "missing file", "merged unprocessed", "closed"} {
			t.Run(kind+"/"+change, func(t *testing.T) {
				w := newWorld(t)
				if kind == records.KindFirst {
					w.gh.bare("c0")
				} else {
					w.gh.commit("c0", policyV(0))
					w.approvedAt(digestV(0), "c0")
				}
				w.openPull(12, "h1", policyV(1), "wf-1")
				a := Action{Kind: kind, Pull: 12}
				if _, err := w.act(admin, a); err != nil {
					t.Fatal(err)
				}
				want := Pending
				switch change {
				case "changed head":
					w.openPull(12, "h2", policyV(1), "wf-1")
					want = Stale
				case "changed policy":
					w.gh.files["h1"] = policyV(2)
					want = Stale
				case "changed workflow":
					w.gh.workflows["h1"] = "wf-2"
					want = Stale
				case "unreadable":
					w.gh.fail["File"] = errors.New("unavailable")
					want = Unknown
				case "missing file":
					delete(w.gh.files, "h1")
					want = Unknown
				case "merged unprocessed":
					p := w.gh.pulls[12]
					w.mergeWith(12, "m1", policyV(1), "wf-1")
					p.State, p.Merged, p.MergeCommitSHA = "closed", true, "m1"
					w.gh.pulls[12] = p
					a.Pull = 0 // OpenPulls no longer returns this PR.
				case "closed":
					p := w.gh.pulls[12]
					p.State = "closed"
					w.gh.pulls[12] = p
					want = Unknown
				}
				got, err := w.svc.Summary(t.Context(), testRepo, a)
				if change != "unreadable" && err != nil {
					t.Fatal(err)
				}
				if got.Status != want || got.Kind != kind || got.Pull != 12 || got.ApprovedHead != "h1" ||
					got.Merged != (change == "merged unprocessed") {
					t.Fatalf("summary = %+v, err %v; want %s", got, err, want)
				}
				if change == "exact head" || change == "changed head" || change == "changed policy" || change == "changed workflow" {
					bound, err := w.svc.Bind(t.Context(), testRepo, a)
					if err != nil {
						t.Fatal(err)
					}
					err = w.svc.Review(t.Context(), testRepo, a, admin, bound.Digest())
					if (got.Status == Pending) != errors.Is(err, ErrNotDue) {
						t.Fatalf("summary %+v disagrees with Review: %v", got, err)
					}
				}
			})
		}
	}
}

func TestSummaryDoesNotInferApprovalFromAbsenceOfAction(t *testing.T) {
	w := newWorld(t)
	w.gh.bare("c0")
	got, err := w.svc.Summary(context.Background(), testRepo, Action{Kind: records.KindFirst})
	if err != nil || got.Status != Needed {
		t.Fatalf("summary = %+v, %v", got, err)
	}
}

func TestSummaryFailsOnUnreadableApprovalRecord(t *testing.T) {
	w := newWorld(t)
	w.gh.bare("c0")
	w.openPull(12, "h1", policyV(1), "wf-1")
	a := Action{Kind: records.KindFirst, Pull: 12}
	if _, err := w.act(admin, a); err != nil {
		t.Fatal(err)
	}
	if err := w.mem.Put(t.Context(), w.tenant(), records.ProposalIndexKey(testRepo.FullName), []byte("invalid")); err != nil {
		t.Fatal(err)
	}
	if _, err := w.svc.Summary(t.Context(), testRepo, a); err == nil {
		t.Fatal("unreadable approval accepted")
	}
}

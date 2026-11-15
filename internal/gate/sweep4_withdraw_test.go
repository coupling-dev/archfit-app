package gate

import (
	"errors"
	"testing"

	"github.com/alexei-led/archfit-app/internal/gate/records"
	"github.com/alexei-led/archfit-app/internal/github/api"
)

func TestOutsideIssueFailureNeverKeepsAnUnapprovedSuccess(t *testing.T) {
	w := newWorld4(t)
	w.guarded4(policy4)
	p := w.openPull(w.shop, 7)
	w.wantStatus(w.send4(w.spec4(p, "pass")), "pass", "")
	before, tip := w.tip, w.sha()
	w.tip4(policy4+"# direct\n", api.Commit{SHA: tip, Parents: []string{before}})
	w.gh.fail = map[string]error{"CreateIssue": errors.New("issues disabled")}
	for range 2 {
		sum, err := w.gate.sweep4(t.Context(), Summary{})
		if err != nil || sum.Errors != 1 {
			t.Fatalf("sweep = %+v,%v", sum, err)
		}
		if w.repo4().OutsideFlow == nil {
			t.Fatal("outside authority was not recorded")
		}
		w.wantSweepGate(p, "completed", "action_required")
	}
}

func TestForcePushedBackHeadLosesItsOldSuccessWithoutAMatchingRecord(t *testing.T) {
	for _, mode := range []string{"first approval", "outside", "ambiguous"} {
		t.Run(mode, func(t *testing.T) {
			w := newWorld4(t)
			w.guarded4(policy4)
			p := w.openPull(w.shop, 7)
			w.wantStatus(w.send4(w.spec4(p, "pass")), "pass", "")
			first := p.head
			w.push(p)
			w.wantStatus(w.send4(w.spec4(p, "pass")), "pass", "")
			w.editPull(p, func(pl *api.Pull) { pl.HeadSHA = first })
			p.head = first
			switch mode {
			case "first approval":
				w.unrecorded()
			case "outside":
				before, tip := w.tip, w.sha()
				w.tip4(policy4+"# direct\n", api.Commit{SHA: tip, Parents: []string{before}})
			case "ambiguous":
				before, mid, tip := w.tip, w.sha(), w.sha()
				w.setFile(w.shop, mid, policyPath, []byte(policy4+"# first change\n"))
				w.tip4(policy4+"# second change\n", api.Commit{SHA: mid, Parents: []string{before}}, api.Commit{SHA: tip, Parents: []string{mid}})
			}
			w.sweep4()
			w.wantSweepGate(p, "completed", "action_required")
		})
	}
}

func TestWithdrawalSupersedesNewerAbandonedMarkerAfterForcePushBack(t *testing.T) {
	w := newWorld4(t)
	w.guarded4(policy4)
	p := w.openPull(w.shop, 7)
	w.wantStatus(w.send4(w.spec4(p, "pass")), "pass", "")
	head, run := p.head, p.runID
	w.put4(records.PublicationKey(shopName, p.number, head), &records.Publication{HeadSHA: head, RunID: run + 1, RunAttempt: 1, Pending: true, Staged: true, Source: "upload", Token: "abandoned"})
	w.push(p)
	w.wantStatus(w.send4(w.spec4(p, "pass")), "pass", "")
	w.editPull(p, func(pl *api.Pull) { pl.HeadSHA = head })
	p.head = head
	repo := w.repo4()
	repo.OutsideFlow = &records.OutsideFlow{Digest: "sha256:outside", Issue: 433}
	w.put4(records.RepoKey(shopName), &repo)
	w.sweep4()
	w.wantSweepGate(p, "completed", "action_required")
}

package gate

import (
	"encoding/json"
	"errors"
	"slices"
	"testing"

	"github.com/coupling-dev/archfit-app/internal/gate/records"
	"github.com/coupling-dev/archfit-app/internal/gate/sweep"
	"github.com/coupling-dev/archfit-app/internal/github/api"
	"github.com/coupling-dev/archfit-app/internal/store"
	"github.com/coupling-dev/archfit-app/internal/wire/policy3"
)

func TestRequestViewSaysWhenTheFirstPolicyWaitsForApproval(t *testing.T) {
	for _, tc := range []struct {
		name       string
		unapproved bool
		want       bool
	}{
		{name: "approved policy", want: false},
		{name: "no first approval", unapproved: true, want: true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			w := newAskWorld(t)
			p := w.pullBy(7)
			w.uploadAsk(p, "graph", "fail")
			w.ask(p, workerLogin, "/archfit request need the helper")
			if tc.unapproved {
				w.unrecorded()
			}
			got, err := w.gate.RequestView(t.Context(), w.target(p), firstRequest)
			if err != nil {
				t.Fatal(err)
			}
			if got.State != records.StateOpen || got.FirstApproval != tc.want {
				t.Errorf("state %q, first approval %v, want open and %v", got.State, got.FirstApproval, tc.want)
			}
		})
	}
}

func TestRequestViewFollowsTheRecheckAfterTheAnswer(t *testing.T) {
	// The merge of the policy pull request makes the policy with the answer the approved one.
	answered := policyAsk + "# with request #1000\n"
	approved := policy3.Digest([]byte(answered))
	for _, tc := range []struct {
		name       string
		merged     bool
		pull       *records.Pull // the waiting pull request's record after the answer; nil keeps the upload's
		closed     bool
		unreadable bool // the pull request read fails: the view still answers
		wantMerged bool
		want       []RequestRecheck
	}{
		{name: "policy pull request open"},
		{name: "merged, re-check not recorded yet", merged: true, wantMerged: true,
			want: []RequestRecheck{{Number: 7, Status: RecheckRunning}}},
		{name: "merged, check runs again", merged: true, wantMerged: true,
			pull: &records.Pull{Status: "waiting", PolicyDigest: "old", Rechecks: []records.Recheck{{Proposal: "p1", Path: sweep.PathRerun}}},
			want: []RequestRecheck{{Number: 7, Status: RecheckRunning}}},
		{name: "merged, branch needs a push", merged: true, wantMerged: true,
			pull: &records.Pull{Status: "waiting", PolicyDigest: "old", Rechecks: []records.Recheck{{Proposal: "p1", Path: sweep.PathNotUpdated}}},
			want: []RequestRecheck{{Number: 7, Status: RecheckPush}}},
		{name: "merged, an older answer's push does not count", merged: true, wantMerged: true,
			pull: &records.Pull{Status: "waiting", PolicyDigest: "old", Rechecks: []records.Recheck{{Proposal: "p0", Path: sweep.PathFork}}},
			want: []RequestRecheck{{Number: 7, Status: RecheckRunning}}},
		{name: "merged, result under the new policy", merged: true, wantMerged: true,
			pull: &records.Pull{Status: "pass", PolicyDigest: approved, Rechecks: []records.Recheck{{Proposal: "p1", Path: sweep.PathRerun}}},
			want: []RequestRecheck{{Number: 7, Status: "pass"}}},
		{name: "merged, failing result under the new policy", merged: true, wantMerged: true,
			pull: &records.Pull{Status: "fail", PolicyDigest: approved},
			want: []RequestRecheck{{Number: 7, Status: "fail"}}},
		{name: "merged, pull request closed", merged: true, wantMerged: true, closed: true},
		{name: "merged, pull request closed after its push was asked", merged: true, wantMerged: true, closed: true,
			pull: &records.Pull{Status: "waiting", PolicyDigest: "old", Rechecks: []records.Recheck{{Proposal: "p1", Path: sweep.PathFork}}}},
		{name: "merged, GitHub cannot say whether it is open", merged: true, wantMerged: true, unreadable: true,
			want: []RequestRecheck{{Number: 7, Status: RecheckRunning}}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			w := newAskWorld(t)
			p := w.pullBy(7)
			w.uploadAsk(p, "graph", "fail")
			w.ask(p, workerLogin, "/archfit request need the helper")
			change := AnswerChange{Request: firstRequest, Kind: policy3.Approve, Rule: "graph", From: "pkg_tc", To: "pkg_util"}
			raw, err := json.Marshal(change)
			if err != nil {
				t.Fatal(err)
			}
			a := records.Approval{Kind: string(policy3.Approve), Request: firstRequest, Change: raw}
			pin := &records.Pin{Proposal: "p1", PullRequest: 77, HeadSHA: "head", Digest: "digest"}
			tenant := store.Tenant(shopInstallation)
			if err := records.Answer(t.Context(), w.mem, tenant, shopName, "a1", a, pin); err != nil {
				t.Fatal(err)
			}
			if tc.merged {
				var prop records.Proposal
				if err := records.Load(t.Context(), w.mem, tenant, records.ProposalKey(shopName, "p1"), &prop); err != nil {
					t.Fatal(err)
				}
				prop.State = records.StateMerged
				w.put4(records.ProposalKey(shopName, "p1"), &prop)
				w.approved(w.shop, answered)
			}
			if tc.pull != nil {
				w.put4(records.PullKey(shopName, 7), tc.pull)
			}
			if tc.closed {
				w.editPull(p, func(pl *api.Pull) { pl.State = "closed" })
			}
			if tc.unreadable {
				w.gh.fail["Pull"] = errors.New("rate limited")
			}
			got, err := w.gate.RequestView(t.Context(), w.target(p), firstRequest)
			if err != nil {
				t.Fatal(err)
			}
			if got.PolicyPull != 77 || got.PolicyMerged != tc.wantMerged || !slices.Equal(got.Rechecks, tc.want) {
				t.Errorf("policy pull #%d merged %v, rechecks %+v; want #77 merged %v, rechecks %+v",
					got.PolicyPull, got.PolicyMerged, got.Rechecks, tc.wantMerged, tc.want)
			}
		})
	}
}

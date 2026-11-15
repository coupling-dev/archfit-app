package gate

import (
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/alexei-led/archfit-app/internal/gate/records"
	"github.com/alexei-led/archfit-app/internal/store"
	"github.com/alexei-led/archfit-app/internal/wire/policy3"
)

func TestAnAnswerIsTakenForAnAdmittedInstallationOnly(t *testing.T) {
	ctx := context.Background()
	w := newAskWorld(t)
	p := w.pullBy(7)
	w.admit.refused[shopInstallation] = true
	calls := len(w.gh.calls)
	if _, err := w.gate.AnswerDigest(ctx, w.target(p), firstRequest, RequestAnswer{Kind: policy3.Approve}); !errors.Is(err, errNotAdmitted) {
		t.Errorf("AnswerDigest = %v, want errNotAdmitted", err)
	}
	in := AnswerInput{Request: firstRequest, Answer: RequestAnswer{Kind: policy3.Reject, Instead: "No."}, Owner: AnswerOwner{ID: 1, Login: "olivia"}}
	if _, err := w.gate.Answer(ctx, w.target(p), in); !errors.Is(err, errNotAdmitted) {
		t.Errorf("Answer = %v, want errNotAdmitted", err)
	}
	if len(w.gh.calls) != calls {
		t.Errorf("GitHub calls for an installation no operator approved: %v", w.gh.calls[calls:])
	}
}

// TestAnAnswerThroughTheGate answers a real request: a login the policy does not name is refused, and an answer
// whose branch write GitHub refuses records nothing, so the request stays open. gate/proposals tests the rest.
func TestAnAnswerThroughTheGate(t *testing.T) {
	ctx := context.Background()
	w := newAskWorld(t)
	asked := w.pullBy(7)
	w.uploadAsk(asked, "graph", "fail")
	w.ask(asked, workerLogin, "/archfit request tc needs the retry helper")
	target := w.target(asked)

	digest, err := w.gate.AnswerDigest(ctx, target, firstRequest, RequestAnswer{Kind: policy3.Approve})
	if err != nil || !strings.HasPrefix(digest, "sha256:") {
		t.Errorf("AnswerDigest = %q, %v", digest, err)
	}
	reject := AnswerInput{Request: firstRequest, Answer: RequestAnswer{Kind: policy3.Reject, Instead: "Keep tc apart."},
		Owner: AnswerOwner{ID: 2, Login: workerLogin}}
	if _, err := w.gate.Answer(ctx, target, reject); !errors.Is(err, ErrNotPolicyOwner) {
		t.Errorf("an answer by the worker = %v, want ErrNotPolicyOwner", err)
	}
	reject.Owner = AnswerOwner{ID: 3, Login: "olivia"}
	if _, err := w.gate.Answer(ctx, target, reject); !errors.Is(err, errNoBranches) {
		t.Errorf("an answer whose branch write fails = %v, want the write's error", err)
	}
	var req records.Request
	if err := records.Load(ctx, w.mem, store.Tenant(shopInstallation), records.RequestKey(shopName, firstRequest), &req); err != nil ||
		req.State != records.StateOpen {
		t.Errorf("request = %+v, %v; want still open", req, err)
	}
	if issue := w.issue(firstRequest); !issue.Open {
		t.Error("the request issue was closed by an answer that was not recorded")
	}
}

// TestAnUploadOfThePolicyPullRequestSetsNoVerdict is M2 D13: the App sets archfit/gate on its own policy pull request,
// so the Action's upload there, which says unknown because the pull request changes the policy, is stored and shown
// nowhere.
func TestAnUploadOfThePolicyPullRequestSetsNoVerdict(t *testing.T) {
	ctx := context.Background()
	w := newWorld4(t)
	p := w.openPull(w.shop, 7)
	err := records.Indexed(ctx, w.mem, store.Tenant(shopInstallation), records.ProposalKey(shopName, "p1"),
		records.ProposalIndexKey(shopName), records.SchemaProposalIndex, func(_ []byte, x *records.Index) ([]byte, error) {
			if err := x.Add(records.PullEntry(p.number), "p1"); err != nil {
				return nil, err
			}
			return records.Encode(&records.Proposal{PullRequest: p.number, HeadSHA: p.head, ExpectedDigest: "d", State: records.StateOpen})
		})
	if err != nil {
		t.Fatal(err)
	}
	w.wantStatus(w.send4(w.spec4(p, "pass")), "pass", "")
	if c, ok := w.latestGate(p.head); ok {
		t.Errorf("the upload set archfit/gate on the policy pull request: %+v", c)
	}
	if len(w.comments(p)) != 0 {
		t.Errorf("the upload commented on the policy pull request: %+v", w.comments(p))
	}
	if rec, head := w.pullRecord(p); rec.HeadSHA != p.head || head != p.head {
		t.Errorf("the upload was not stored: %+v, index head %q", rec, head)
	}

	other := w.openPull(w.shop, 8)
	w.wantStatus(w.send4(w.spec4(other, "pass")), "pass", "")
	if _, ok := w.latestGate(other.head); !ok {
		t.Error("an upload of another pull request set no check")
	}
}

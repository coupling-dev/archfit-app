package proposals

import (
	"context"
	"errors"
	"slices"
	"strings"
	"testing"

	"github.com/coupling-dev/archfit-app/internal/gate/records"
	"github.com/coupling-dev/archfit-app/internal/gate/requests"
	"github.com/coupling-dev/archfit-app/internal/store"
	"github.com/coupling-dev/archfit-app/internal/wire/policy3"
)

func TestBatchAnswersShareOneRedeemAndProposal(t *testing.T) {
	w := newWorld(t)
	w.request(431, "dependency", "pkg_util", "pkg_net")
	w.request(432, "dependency", "pkg_util", "pkg_db")
	items := []BatchItem{{Request: 431, Answer: requests.Answer{Kind: policy3.Approve}}, {Request: 432, Answer: requests.Answer{Kind: policy3.Approve}}}
	digest, err := w.svc.BatchDigest(context.Background(), testRepo, items)
	if err != nil {
		t.Fatal(err)
	}
	w.tokens["batch"] = digest
	out, err := w.svc.AnswerBatch(context.Background(), testRepo, BatchInput{Answers: items, Owner: olivia, Redeem: w.redeem("batch")})
	if err != nil {
		t.Fatal(err)
	}
	if len(w.redeemed) != 1 || len(out.Approvals) != 2 || out.PullRequest == 0 {
		t.Fatalf("out=%+v redeems=%v", out, w.redeemed)
	}
	if out.State != requests.PullMerged {
		t.Fatalf("state = %s", out.State)
	}
	policy := w.hub.file(fakeBase, policyPath)
	if !strings.Contains(policy, "rule: dependency  # request #431") || !strings.Contains(policy, "rule: dependency  # request #432") {
		t.Fatalf("policy: %s", policy)
	}
	a := load[records.Approval](t, w.store, records.ApprovalKey(testRepo.FullName, out.Approvals[0]))
	proposal := load[records.Proposal](t, w.store, records.ProposalKey(testRepo.FullName, a.Proposal))
	if !slices.Equal(proposal.Approvals, out.Approvals) {
		t.Fatalf("approval order: %v", proposal.Approvals)
	}
}

func TestBatchDigestBindsOrder(t *testing.T) {
	w := newWorld(t)
	w.request(431, "dependency", "pkg_util", "pkg_net")
	w.request(432, "dependency", "pkg_util", "pkg_db")
	a := []BatchItem{{Request: 431, Answer: requests.Answer{Kind: policy3.Approve}}, {Request: 432, Answer: requests.Answer{Kind: policy3.Approve}}}
	first, err := w.svc.BatchDigest(context.Background(), testRepo, a)
	if err != nil {
		t.Fatal(err)
	}
	a[0], a[1] = a[1], a[0]
	second, err := w.svc.BatchDigest(context.Background(), testRepo, a)
	if err != nil {
		t.Fatal(err)
	}
	if first == second {
		t.Fatal("order did not change digest")
	}
}

func TestBatchDigestRefusesAnsweredRequest(t *testing.T) {
	w := newWorld(t)
	w.request(431, "dependency", "pkg_util", "pkg_net")
	w.mustAnswer(olivia, 431, requests.Answer{Kind: policy3.Approve}, true)
	_, err := w.svc.BatchDigest(context.Background(), testRepo, []BatchItem{{Request: 431, Answer: requests.Answer{Kind: policy3.Approve}}})
	if !errors.Is(err, requests.ErrClosedRequest) {
		t.Fatalf("got %v", err)
	}
}

func TestBatchRejectsTokenForDifferentOrder(t *testing.T) {
	w := newWorld(t)
	w.request(431, "dependency", "pkg_util", "pkg_net")
	w.request(432, "dependency", "pkg_util", "pkg_db")
	items := []BatchItem{{Request: 431, Answer: requests.Answer{Kind: policy3.Approve}}, {Request: 432, Answer: requests.Answer{Kind: policy3.Approve}}}
	digest, err := w.svc.BatchDigest(context.Background(), testRepo, items)
	if err != nil {
		t.Fatal(err)
	}
	w.tokens["batch"] = digest
	items[0], items[1] = items[1], items[0]
	_, err = w.svc.AnswerBatch(context.Background(), testRepo, BatchInput{Answers: items, Owner: olivia, Redeem: w.redeem("batch")})
	if !errors.Is(err, errToken) {
		t.Fatalf("got %v", err)
	}
	if len(w.hub.writes()) != 0 || len(w.redeemed) != 0 {
		t.Fatalf("unexpected writes or redeem: %v %v", w.hub.writes(), w.redeemed)
	}
}

func TestBatchUnjoinedAnswerDoesNotClaimPolicyPR(t *testing.T) {
	w := newWorld(t)
	w.request(431, "dependency", "pkg_util", "pkg_net")
	w.request(432, "duplication", "pkg_tc", "pkg_util")
	items := []BatchItem{{Request: 431, Answer: requests.Answer{Kind: policy3.Approve}}, {Request: 432, Answer: requests.Answer{Kind: policy3.Reject, Instead: "Call pkg_util."}}}
	digest, err := w.svc.BatchDigest(context.Background(), testRepo, items)
	if err != nil {
		t.Fatal(err)
	}
	w.tokens["batch"] = digest
	out, err := w.svc.AnswerBatch(context.Background(), testRepo, BatchInput{Answers: items, Owner: olivia, Redeem: w.redeem("batch")})
	if err != nil {
		t.Fatal(err)
	}
	if out.PullRequest == 0 {
		t.Fatal("missing policy PR")
	}
	if note := w.hub.lastComment(432); !strings.Contains(note, "this issue keeps it") {
		t.Fatalf("unjoined note: %s", note)
	}
}

func TestBatchRequiresOwnerIdentityBeforeRedeem(t *testing.T) {
	w := newWorld(t)
	w.request(431, "dependency", "pkg_util", "pkg_net")
	items := []BatchItem{{Request: 431, Answer: requests.Answer{Kind: policy3.Approve}}}
	digest, err := w.svc.BatchDigest(context.Background(), testRepo, items)
	if err != nil {
		t.Fatal(err)
	}
	w.tokens["batch"] = digest
	_, err = w.svc.AnswerBatch(context.Background(), testRepo, BatchInput{Answers: items, Owner: Owner{Login: "olivia"}, Redeem: w.redeem("batch")})
	if !errors.Is(err, ErrNotOwner) || len(w.redeemed) != 0 || len(w.hub.writes()) != 0 {
		t.Fatalf("err=%v redeems=%v writes=%v", err, w.redeemed, w.hub.writes())
	}
}

func TestBatchTokenRejectsChangedApprovedPolicy(t *testing.T) {
	w := newWorld(t)
	w.request(431, "graph", "pkg_tc", "pkg_util")
	items := []BatchItem{{Request: 431, Answer: requests.Answer{Kind: policy3.Approve}}}
	digest, err := w.svc.BatchDigest(context.Background(), testRepo, items)
	if err != nil {
		t.Fatal(err)
	}
	w.tokens["batch"] = digest
	w.hub.push(fakeBase, policyPath, testPolicy+"# changed\n")
	w.approveTip()
	_, err = w.svc.AnswerBatch(context.Background(), testRepo, BatchInput{Answers: items, Owner: olivia, Redeem: w.redeem("batch")})
	if !errors.Is(err, errToken) || len(w.redeemed) != 0 || len(w.hub.writes()) != 0 {
		t.Fatalf("err=%v redeem=%v writes=%v", err, w.redeemed, w.hub.writes())
	}
}

func TestBatchRedeemsExactlyReviewedDigestOnce(t *testing.T) {
	w := newWorld(t)
	w.request(431, "graph", "pkg_tc", "pkg_util")
	items := []BatchItem{{Request: 431, Answer: requests.Answer{Kind: policy3.Approve}}}
	digest, err := w.svc.BatchDigest(context.Background(), testRepo, items)
	if err != nil {
		t.Fatal(err)
	}
	w.tokens["batch"] = digest
	redeem := w.redeem("batch")
	_, err = w.svc.AnswerBatch(context.Background(), testRepo, BatchInput{Answers: items, Owner: olivia, Redeem: redeem})
	if err != nil {
		t.Fatal(err)
	}
	if len(w.redeemed) != 1 || w.redeemed[0] != digest {
		t.Fatalf("redeemed %v, want %s", w.redeemed, digest)
	}
	if _, err = redeem(context.Background(), digest); !errors.Is(err, errToken) {
		t.Fatalf("replay: %v", err)
	}
}

type batchFailStore struct{ store.Store }

func (s batchFailStore) CompareAndSwapAll(ctx context.Context, tenant store.Tenant, swaps []store.Swap) (bool, error) {
	if len(swaps) > 1 {
		return false, errors.New("pin unavailable")
	}
	return s.Store.CompareAndSwapAll(ctx, tenant, swaps)
}

func TestBatchFailedPinNeverPublishesGate(t *testing.T) {
	w := newWorld(t)
	w.request(431, "graph", "pkg_tc", "pkg_util")
	items := []BatchItem{{Request: 431, Answer: requests.Answer{Kind: policy3.Approve}}}
	digest, err := w.svc.BatchDigest(context.Background(), testRepo, items)
	if err != nil {
		t.Fatal(err)
	}
	w.tokens["batch"] = digest
	w.svc.store = batchFailStore{w.store}
	_, err = w.svc.AnswerBatch(context.Background(), testRepo, BatchInput{Answers: items, Owner: olivia, Redeem: w.redeem("batch")})
	if err == nil || !strings.Contains(err.Error(), "pin unavailable") {
		t.Fatalf("error = %v", err)
	}
	if w.hub.count("CommitOn") != 1 || w.hub.count("CreateCheckRun") != 0 || w.requestState(431) != records.StateOpen {
		t.Fatalf("writes=%v state=%s", w.hub.writes(), w.requestState(431))
	}
}

func TestBatchRefusesInvalidInputsBeforeWrites(t *testing.T) {
	for _, name := range []string{"empty", "duplicate", "oversize", "owner", "closed"} {
		t.Run(name, func(t *testing.T) {
			w := newWorld(t)
			w.request(431, "graph", "pkg_tc", "pkg_util")
			item := BatchItem{Request: 431, Answer: requests.Answer{Kind: policy3.Approve}}
			items := []BatchItem{item}
			owner := olivia
			switch name {
			case "empty":
				items = nil
			case "duplicate":
				items = append(items, item)
			case "oversize":
				items = make([]BatchItem, 21)
			case "owner":
				owner = Owner{ID: 999, Login: "stranger"}
			case "closed":
				w.mustAnswer(olivia, 431, item.Answer, true)
				w.hub.calls = nil
				w.redeemed = nil
			}
			_, err := w.svc.AnswerBatch(context.Background(), testRepo, BatchInput{Answers: items, Owner: owner, Redeem: w.redeem("none")})
			if err == nil || len(w.hub.writes()) != 0 || len(w.redeemed) != 0 {
				t.Fatalf("error=%v writes=%v redeems=%v", err, w.hub.writes(), w.redeemed)
			}
		})
	}
}

func TestBatchCleansClosedProposal(t *testing.T) {
	w := newWorld(t)
	w.request(431, "graph", "pkg_tc", "pkg_util")
	w.request(432, "graph", "pkg_tc", "pkg_db")
	first := w.mustAnswer(olivia, 431, requests.Answer{Kind: policy3.Approve}, true)
	w.hub.pulls[first.PullRequest].State = "closed"
	items := []BatchItem{{Request: 432, Answer: requests.Answer{Kind: policy3.Approve}}}
	digest, err := w.svc.BatchDigest(context.Background(), testRepo, items)
	if err != nil {
		t.Fatal(err)
	}
	w.tokens["batch"] = digest
	_, err = w.svc.AnswerBatch(context.Background(), testRepo, BatchInput{Answers: items, Owner: olivia, Redeem: w.redeem("batch")})
	if err != nil {
		t.Fatal(err)
	}
	if w.requestState(431) != records.StateOpen {
		t.Fatalf("old request: %s", w.requestState(431))
	}
}

func TestBatchRefusesNonOwnerWithValidToken(t *testing.T) {
	w := newWorld(t)
	w.request(431, "graph", "pkg_tc", "pkg_util")
	items := []BatchItem{{Request: 431, Answer: requests.Answer{Kind: policy3.Approve}}}
	digest, err := w.svc.BatchDigest(context.Background(), testRepo, items)
	if err != nil {
		t.Fatal(err)
	}
	w.tokens["batch"] = digest
	_, err = w.svc.AnswerBatch(context.Background(), testRepo, BatchInput{Answers: items, Owner: Owner{ID: 999, Login: "stranger"}, Redeem: w.redeem("batch")})
	if !errors.Is(err, ErrNotOwner) || len(w.redeemed) != 0 || len(w.hub.writes()) != 0 {
		t.Fatalf("err=%v", err)
	}
}

package proposals

import (
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/coupling-dev/archfit-app/internal/gate/requests"
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

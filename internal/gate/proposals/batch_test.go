package proposals

import (
	"context"
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

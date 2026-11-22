package records_test

import (
	"context"
	"slices"
	"testing"

	"github.com/coupling-dev/archfit-app/internal/gate/records"
	"github.com/coupling-dev/archfit-app/internal/store"
)

func TestAnswerBatchPinsBothApprovalsTogether(t *testing.T) {
	ctx := context.Background()
	s := store.NewMemory()
	for _, r := range []struct {
		id  int
		key string
	}{{431, "first"}, {432, "second"}} {
		if err := openRequest(ctx, s, r.id, r.key); err != nil {
			t.Fatal(err)
		}
	}
	a1, a2 := *approval(""), *approval("")
	a1.Request, a2.Request = 431, 432
	pin := &records.Pin{Proposal: "p1", PullRequest: 12, HeadSHA: "h1", Digest: "d1"}
	items := []records.BatchAnswer{{ApprovalID: "a1", Approval: a1, Join: true}, {ApprovalID: "a2", Approval: a2, Join: true}}
	if err := records.AnswerBatch(ctx, s, tenant, repoName, items, pin); err != nil {
		t.Fatal(err)
	}
	for _, id := range []int{431, 432} {
		if req := load[records.Request](t, s, records.RequestKey(repoName, id)); req.State != records.StateAnswered {
			t.Errorf("request %d state = %q", id, req.State)
		}
	}
	p := load[records.Proposal](t, s, records.ProposalKey(repoName, "p1"))
	if !slices.Equal(p.Approvals, []string{"a1", "a2"}) {
		t.Errorf("approvals = %v", p.Approvals)
	}
}

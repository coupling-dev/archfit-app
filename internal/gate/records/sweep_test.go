package records_test

import (
	"testing"

	"github.com/alexei-led/archfit-app/internal/gate/records"
	"github.com/alexei-led/archfit-app/internal/store"
)

func TestRecoveredConsumeKeepsItsHandoffUntilFinished(t *testing.T) {
	forEachBackend(t, 1, func(t *testing.T, clients []store.Store) {
		s := clients[0]
		put(t, s, records.RepoKey(repoName), &records.Repo{ApprovedDigest: "before", ApprovedCommit: "c0"})
		openProposal(t, s, "p1", 30, "after")
		m := merge("p1", "c1", "after")
		m.Over = "c0"
		m.Recheck = true
		if err := records.Consume(t.Context(), s, tenant, repoName, m); err != nil {
			t.Fatal(err)
		}
		proof := load[records.PolicyPush](t, s, records.RecoveredPushKey(repoName, "c1"))
		if proof.Proposal != "p1" || proof.Digest != "after" {
			t.Fatalf("recovered proof = %+v", proof)
		}
		actual := records.Push{Before: "code-only", After: "c1"}
		if err := records.RecoveredDelivery(t.Context(), s, tenant, repoName, "c1", actual, proof); err != nil {
			t.Fatal(err)
		}
		if delivered := load[records.PolicyPush](t, s, records.PolicyPushKey(repoName, actual)); delivered != proof {
			t.Errorf("actual delivery = %+v, want %+v", delivered, proof)
		}
		r := load[records.Repo](t, s, records.RepoKey(repoName))
		if r.SweepRecheck != "p1" || r.ApprovedDigest != "after" {
			t.Fatalf("repo = %+v", r)
		}
		if err := records.SweepRechecked(t.Context(), s, tenant, repoName, "another"); err != nil {
			t.Fatal(err)
		}
		if r := load[records.Repo](t, s, records.RepoKey(repoName)); r.SweepRecheck != "p1" {
			t.Fatalf("another handoff cleared p1: %+v", r)
		}
		if err := records.SweepRechecked(t.Context(), s, tenant, repoName, "p1"); err != nil {
			t.Fatal(err)
		}
		if r := load[records.Repo](t, s, records.RepoKey(repoName)); r.SweepRecheck != "" || r.ApprovedDigest != "after" {
			t.Fatalf("finished repo = %+v", r)
		}
	})
}

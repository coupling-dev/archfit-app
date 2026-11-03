package ui

import (
	"net/http"
	"slices"
	"testing"
	"time"

	"github.com/alexei-led/archfit-app/internal/gate"
)

func TestPullAuthorityIsResolvedWithoutAReportAndUsesProtectedSettingsSnapshot(t *testing.T) {
	e := newEnv(t)
	e.sameRepoHead()
	e.approvals.approval = gate.PullApproval{
		Candidate: shownBaseline, SettingsFound: true, Owners: []string{"alice", "acme/architects"},
		SettingsRef: sha(10), GateMode: "advisory", Granted: true, ApprovedBy: "alice", AuthorIsAgent: true,
		Objections: []gate.Objection{{ID: "github:77", Login: "bob", Source: "github", Reason: "No reason was supplied.", HeadSHA: sha(4), At: time.Date(2026, 11, 1, 12, 0, 0, 0, time.UTC), Overridden: true}},
		History:    []gate.DecisionHistory{{ID: "app:1", Login: "alice", Action: gate.DecisionOverride, Reason: "I accept the risk.", HeadSHA: sha(4), BaseSHA: sha(10), At: time.Date(2026, 11, 1, 13, 0, 0, 0, time.UTC), Overrode: []string{"github:77"}, OverrodeObjections: []gate.Objection{{ID: "github:77", Login: "bob", Source: "github", Reason: "No reason was supplied.", HeadSHA: sha(4)}}}},
	}
	res := e.as("alice", permWrite).get(pullAPI)
	wantStatus(t, res, http.StatusOK)
	got := decodeAs[pullJSON](t, res).Approval
	if got == nil {
		t.Fatal("approval context missing without a report")
	}
	if got.BaseSHA != sha(10) || got.SettingsRef != sha(10) || got.SettingsURL != "https://github.com/"+repoName+"/blob/"+sha(10)+"/.archfit-app.yaml" ||
		got.DecisionURL != baseURL+"/r/acme/app/pull/4" || got.GateMode != "advisory" || got.Required ||
		!slices.Equal(got.Owners, []string{"alice", "acme/architects"}) || len(got.Objections) != 1 || !got.Objections[0].Overridden ||
		len(got.History) != 1 || got.History[0].BaseSHA != sha(10) || len(got.History[0].OverrodeObjections) != 1 {
		t.Errorf("protected authority context = %+v", got)
	}
}

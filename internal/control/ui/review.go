package ui

import (
	"errors"
	"net/http"
	"strconv"

	"github.com/alexei-led/archfit-app/internal/control/answers"
	"github.com/alexei-led/archfit-app/internal/gate"
	"github.com/alexei-led/archfit-app/internal/github/api"
)

// reviewJSON is navigation, not approval authority. The destination rechecks on submission.
type reviewJSON struct {
	Label string `json:"label"`
	Href  string `json:"href,omitempty"`
}

func (s *server) ownerReview(r *http.Request, sc scope, pull int) *reviewJSON {
	done := api.StartStage(r.Context(), "owner_review")
	probes := 0
	defer func() { done(probes, 0) }()
	actions := []answers.OwnerAction{}
	if pull > 0 {
		actions = append(actions, answers.OwnerAction{Kind: gate.OwnerFirst, Pull: pull})
	}
	actions = append(actions, answers.OwnerAction{Kind: gate.OwnerFirst})
	if pull > 0 {
		actions = append(actions, answers.OwnerAction{Kind: gate.OwnerWorkflow, Pull: pull})
	}
	u := gate.OwnerUser{ID: sc.user.UserID, Login: sc.user.Login}
	permission, permissionErr := s.gh.users.Permission(r.Context(), sc.user.Token, sc.repo.FullName)
	u.Admin = permissionErr == nil && permission == permAdmin
	for _, a := range actions {
		probes++
		err := s.owners.OwnerReview(r.Context(), sc.repo, a, u)
		switch {
		case errors.Is(err, gate.ErrOwnerNotDue), errors.Is(err, gate.ErrOwnerNotPossible):
			continue
		case errors.Is(err, gate.ErrOwnerNotAdmin):
			return &reviewJSON{Label: "A repository administrator named as an architecture owner in the proposed policy can review this."}
		case errors.Is(err, gate.ErrOwnerNotOwner):
			return &reviewJSON{Label: "An architecture owner named in the approved policy can review this."}
		case err != nil:
			return &reviewJSON{Label: "The App could not determine who can review this. Try again."}
		}
		label := "Review first policy"
		if a.Kind == gate.OwnerWorkflow {
			label = "Review workflow change"
		}
		href := "/r/" + sc.repo.FullName + "/owner/" + a.Kind
		if a.Pull > 0 {
			href += "?pull=" + strconv.Itoa(a.Pull)
		}
		return &reviewJSON{Label: label, Href: href}
	}
	return nil
}

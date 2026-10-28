package ui

import (
	"errors"
	"net/http"
	"strings"

	"github.com/alexei-led/archfit-app/internal/control/account"
	"github.com/alexei-led/archfit-app/internal/control/assist"
	"github.com/alexei-led/archfit-app/internal/control/onboarding"
	"github.com/alexei-led/archfit-app/internal/control/proposal"
	"github.com/alexei-led/archfit-app/internal/control/ui/session"
	"github.com/alexei-led/archfit-app/internal/gate"
	"github.com/alexei-led/archfit-app/internal/github/api"
	"github.com/alexei-led/archfit-app/internal/wire/report"
)

const (
	retryAfterSeconds = "60"
	dateLayout        = "2 January 2006"

	stateNone       = "no state yet"
	stateUnknown    = "unknown"
	stateViolations = "violations found"
	stateWaiting    = "waiting for approval"
)

// userError is a refusal ui decides itself: its status, its log outcome and the sentence shown.
type userError struct {
	status  int
	outcome string
	msg     string
}

func (e *userError) Error() string { return "ui: " + e.outcome }

func invalid(msg string) error  { return &userError{http.StatusUnprocessableEntity, "invalid", msg} }
func conflict(msg string) error { return &userError{http.StatusConflict, "conflict", msg} }

var errCSRF = &userError{http.StatusForbidden, "csrf", "The form has expired. Reload the page and try again."}

// sentinels is the HTTP status, log outcome and wording of every error a counterpart can hand to ui.
var sentinels = []struct {
	err      error
	status   int
	outcome  string
	sentence string
}{
	{session.ErrForbidden, http.StatusNotFound, "not_found", "Not found."},
	{api.ErrNotFound, http.StatusNotFound, "not_found", "Not found."},
	{proposal.ErrStale, http.StatusConflict, "stale",
		"The draft changed on GitHub after this page was loaded. Nothing was saved. Reload the draft and apply your edit again."},
	{assist.ErrInvalidRequest, http.StatusUnprocessableEntity, "invalid", "The request is not valid: write a short instruction and try again."},
	{assist.ErrInvalidProposal, http.StatusUnprocessableEntity, "invalid_proposal",
		"The assistant did not produce a usable policy. Nothing was changed."},
	{assist.ErrNoDraft, http.StatusConflict, "no_draft", "There is no open draft yet. Save the policy first, then ask again."},
	{account.ErrNoConsent, http.StatusConflict, "no_consent",
		"Managed AI needs the recorded consent of a repository administrator."},
	{assist.ErrNotManaged, http.StatusConflict, "not_managed",
		"The settings of this repository do not allow managed mode. Use customer mode."},
	{assist.ErrPolicyTooLarge, http.StatusConflict, "policy_too_large", "The draft policy is too large for managed mode."},
	{assist.ErrNoAssistWorkflow, http.StatusConflict, "no_assist_workflow",
		"The assist workflow is not on the draft branch. Add " + assist.AssistWorkflowPath + " first."},
	{onboarding.ErrNoSandbox, http.StatusConflict, "no_sandbox", "Managed discovery is not available."},
	{onboarding.ErrAlreadyOnboarded, http.StatusConflict, "already_onboarded",
		"This repository is already onboarded. Reload the page to see its state."},
	{onboarding.ErrBusy, http.StatusConflict, "onboarding_busy",
		"Managed discovery is busy with other repositories. Try again in a few minutes."},
	{assist.ErrBusy, http.StatusTooManyRequests, "busy", "A request for this draft is still running. Wait for it to finish."},
	{assist.ErrManagedUnavailable, http.StatusServiceUnavailable, "managed_unavailable",
		"Managed AI is not configured on this service. Editing and saving still work."},
	{account.ErrBudgetExhausted, http.StatusServiceUnavailable, "budget_exhausted",
		"The managed AI budget is used up for now. Editing and saving still work."},
	{assist.ErrProvider, http.StatusServiceUnavailable, "provider", "The AI provider failed. Editing and saving still work."},
	{api.ErrRateLimited, http.StatusServiceUnavailable, "rate_limited", "GitHub is rate limiting Archfit. Try again in a minute."},
	{gate.ErrNothingToApprove, http.StatusConflict, "nothing_to_approve",
		"This pull request changes no protected file, so there is nothing to approve."},
	{gate.ErrForkPull, http.StatusConflict, "fork",
		"A pull request from a fork cannot be approved. Push the branch to this repository."},
	{gate.ErrAgentIdentity, http.StatusForbidden, "agent_identity",
		"An agent identity cannot approve. Sign in as a person who is an architecture owner."},
	{gate.ErrNotOwner, http.StatusForbidden, "not_owner",
		"Only an architecture owner named in .archfit-app.yaml on the base branch can approve."},
	{gate.ErrDigestsChanged, http.StatusConflict, "digests_changed",
		"The protected files changed since this page was loaded. Nothing was approved. Reload the page and review them again."},
}

// explain maps any error to its status, log outcome and sentence. Unknown errors are upstream failures.
func explain(err error) (status int, outcome, sentence string) {
	var ue *userError
	if errors.As(err, &ue) {
		return ue.status, ue.outcome, ue.msg
	}
	if _, waiting := errorsAsWaiting(err); waiting {
		return http.StatusForbidden, "not_admitted", waitingSentence
	}
	var tooLarge *http.MaxBytesError
	if errors.As(err, &tooLarge) {
		return http.StatusRequestEntityTooLarge, "too_large", "The request is too large."
	}
	for _, s := range sentinels {
		if errors.Is(err, s.err) {
			return s.status, s.outcome, s.sentence
		}
	}
	return http.StatusBadGateway, "upstream", "GitHub or an Archfit service failed. Nothing was changed. Try again."
}

var draftWords = map[proposal.DraftState]string{
	proposal.DraftOpen:     "draft",
	proposal.DraftInReview: "awaiting review",
	proposal.DraftMerged:   "published",
	proposal.DraftClosed:   "closed",
}

var conclusionWords = map[string]string{
	"":                "in progress",
	"success":         "healthy",
	"neutral":         "attention",
	"failure":         stateViolations, // never "blocked": in advisory mode nothing blocks
	"action_required": "action required",
	"cancelled":       "cancelled",
	"timed_out":       "timed out",
	"skipped":         "skipped",
	"stale":           "outdated",
}

// stateTones colour a state word in the web app; a word not listed is "neutral". The word is always shown next to
// the colour, so the colour is never the only signal.
var stateTones = map[string]string{
	"healthy":         "success",
	"attention":       "attention",
	stateViolations:   "danger",
	"action required": "danger",
	"in progress":     "accent",
}

func toneOf(word string) string {
	if t, ok := stateTones[word]; ok {
		return t
	}
	return "neutral"
}

// stateWord is a check run as one word, the same wherever it is shown. An advisory check that found violations is
// neutral, so its headline decides: "violations found", never "attention" over the engine's blocked verdict.
func stateWord(conclusion, title string) string {
	if conclusion == "neutral" &&
		(strings.HasPrefix(title, report.HeadlineViolations) || strings.HasPrefix(title, report.HeadlineGateFailing)) {
		return stateViolations
	}
	return conclusionWord(conclusion)
}

func conclusionWord(c string) string {
	if w, ok := conclusionWords[c]; ok {
		return w
	}
	return stateUnknown
}

var stageWords = map[onboarding.Stage]string{
	onboarding.StageInstalled:      "App installed",
	onboarding.StageWorkflowMerged: "Workflow merged",
	onboarding.StageReportReceived: "First report received",
	onboarding.StagePolicyMerged:   "Policy merged",
	onboarding.StageAdvisory:       "Gate is advisory",
	onboarding.StageRequired:       "Gate is required",
}

// banner is the entitlement sentence; public and granted installations get none.
func banner(st account.Status) string {
	switch st.Plan {
	case account.PlanTrial:
		return "Trial: private repositories are checked until " + st.TrialEnds.UTC().Format(dateLayout) + "."
	case account.PlanGrace:
		return "The trial has ended. Checks on private repositories stop on " + st.SuspendAfter.UTC().Format(dateLayout) + "."
	case account.PlanSuspended:
		return "Archfit is suspended for this installation and publishes no checks on private repositories. " +
			"Ask the Archfit team for a grant to restore them."
	}
	return ""
}

var notices = map[string]string{
	"saved":      "Saved as a commit on the draft pull request.",
	"unchanged":  "Nothing to save: the text is already on the branch.",
	"ready":      "The draft pull request is marked ready for review.",
	"proposed":   "The proposal is a new commit on the draft pull request. Review the diff on GitHub.",
	"requested":  "The assist workflow is running in your CI. Its proposal arrives as a new commit on the draft pull request.",
	"onboarding": "The onboarding pull request is open. Next: add the workflow file to it.",
	"discover":   "Discovery was dispatched. The policy proposal arrives as a pull request.",
	"approved":   "Approved for this commit. The Archfit check follows when the workflow reports on it; a new commit needs a new approval.",
}

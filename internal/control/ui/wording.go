package ui

import (
	"errors"
	"net/http"
	"strconv"
	"strings"

	"github.com/alexei-led/archfit-app/internal/control/proposal"

	"github.com/alexei-led/archfit-app/internal/control/account"
	"github.com/alexei-led/archfit-app/internal/control/onboarding"
	"github.com/alexei-led/archfit-app/internal/control/passkey"
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
	{gate.ErrNoRequest, http.StatusNotFound, "not_found", "Not found."},
	{gate.ErrClosedRequest, http.StatusConflict, "request_closed", "This request is answered or closed. Reload the page."},
	{gate.ErrAnswer, http.StatusUnprocessableEntity, "answer",
		"Choose an answer: approve, approve until a date (YYYY-MM-DD), or reject with what to do instead."},
	{gate.ErrNoPolicy, http.StatusConflict, "no_policy",
		"The default branch has no policy of version 3, so no answer can be written. Add .archfit.yaml first."},
	{gate.ErrByHand, http.StatusConflict, "by_hand", "Archfit cannot write this answer into .archfit.yaml. Edit the policy by hand."},
	{gate.ErrNotPolicyOwner, http.StatusForbidden, "not_policy_owner",
		"Only an owner named in .archfit.yaml on the default branch can answer a request."},
	{gate.ErrNeedsPasskey, http.StatusForbidden, "needs_step_up",
		"This answer loosens the policy. Confirm it with your passkey, then answer again."},
	{gate.ErrAnswerBusy, http.StatusConflict, "answer_busy",
		"Another answer for this repository is being written. Nothing was changed. Try again in a minute."},
	{gate.ErrProposalFull, http.StatusConflict, "proposal_full",
		"The policy pull request holds the most answers it can. Merge it first, then answer. Your answer was not recorded."},
	{account.ErrNoConsent, http.StatusConflict, "no_consent",
		"Managed discovery needs the recorded consent of a repository administrator."},
	{onboarding.ErrNoSandbox, http.StatusConflict, "no_sandbox", "Managed discovery is not available."},
	{onboarding.ErrAlreadyOnboarded, http.StatusConflict, "already_onboarded",
		"This repository is already onboarded. Reload the page to see its state."},
	{onboarding.ErrBusy, http.StatusConflict, "onboarding_busy",
		"Managed discovery is busy with other repositories. Try again in a few minutes."},
	{api.ErrRateLimited, http.StatusServiceUnavailable, "rate_limited", "GitHub is rate limiting Archfit. Try again in a minute."},
	{passkey.ErrNotOwner, http.StatusForbidden, "not_owner",
		"Only an owner named in the policy, or a repository administrator before the first policy, can register a passkey here."},
	{passkey.ErrNeedsPasskey, http.StatusConflict, "needs_passkey",
		"You already have a passkey. Confirm with it first: a new passkey needs an existing one."},
	{passkey.ErrNoPasskey, http.StatusConflict, "no_passkey", "Register a passkey first."},
	{passkey.ErrCeremony, http.StatusBadRequest, "ceremony",
		"The passkey check failed or took more than 5 minutes. Nothing was changed. Try again."},
	{passkey.ErrCounter, http.StatusForbidden, "counter",
		"This passkey's counter went back, so the passkey may have been copied. Nothing was changed. Use another passkey."},
	{passkey.ErrToken, http.StatusForbidden, "token",
		"The passkey confirmation was used, is older than 5 minutes, or is for another action. Confirm again."},
	{passkey.ErrTooMany, http.StatusConflict, "too_many",
		"You have " + strconv.Itoa(passkey.MaxPasskeys) + " passkeys, the most allowed. Remove one first."},
	{passkey.ErrUnknown, http.StatusNotFound, "not_found", "Not found."},
	{passkey.ErrInvalid, http.StatusUnprocessableEntity, "invalid", "The request is not valid."},
	{passkey.ErrPolicy, http.StatusConflict, "policy",
		"The policy on the default branch is not valid, so Archfit cannot tell who the owners are. Fix .archfit.yaml first."},
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
	"onboarding": "The onboarding pull request is open. Next: add the workflow file to it.",
	"discover":   "Discovery was dispatched. The policy proposal arrives as a pull request.",
}

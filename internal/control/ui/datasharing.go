package ui

import (
	"context"
	"errors"
	"log/slog"
	"net/http"
	"strings"
	"time"

	"github.com/alexei-led/archfit-app/internal/control/account"
	"github.com/alexei-led/archfit-app/internal/wire/settings"
)

const (
	actionAllow  = "allow"
	actionRevoke = "revoke"

	checkMet     = "met"
	checkNotMet  = "not_met"
	checkUnknown = "unknown"
)

// sharedFeature is one managed feature that sends data out of the customer's environment, in the page's words.
type sharedFeature struct {
	kind    account.ConsentKind
	title   string
	leaves  string // what leaves the customer's environment, and when
	note    string // shown under the feature; "" for none
	managed bool   // the feature also needs data_mode: managed in the repository settings
}

// sharedFeatures is the server's list of consent kinds: a POST naming any other kind is refused.
var sharedFeatures = []sharedFeature{
	{
		kind:  account.ConsentManagedLLM,
		title: "Managed AI",
		leaves: "When someone asks for a policy proposal in managed mode, the draft policy text and the instruction are " +
			"sent to the AI provider. No source code and no report is sent.",
		managed: true,
	},
	{
		kind:  account.ConsentSourceProcessing,
		title: "Managed discovery",
		leaves: "Archfit clones the repository into a short-lived sandbox to propose a first policy. The source code " +
			"leaves your environment for that job and is deleted with the sandbox.",
		note: "The App does not start managed discovery yet. Onboarding runs discovery in your own workflow.",
	},
}

func featureOf(kind string) (sharedFeature, bool) {
	for _, f := range sharedFeatures {
		if string(f.kind) == kind {
			return f, true
		}
	}
	return sharedFeature{}, false
}

type precondition struct {
	Label string `json:"label"`
	State string `json:"state"` // met, not_met or unknown
}

type featureJSON struct {
	Kind    string `json:"kind"`
	Title   string `json:"title"`
	Leaves  string `json:"leaves"`
	Note    string `json:"note,omitempty"`
	Allowed bool   `json:"allowed"`
	// By is the login as it was when the consent was recorded; every recorded consent holds one. At is RFC 3339, UTC.
	By     string         `json:"by,omitempty"`
	At     string         `json:"at,omitempty"`
	Checks []precondition `json:"checks"`
}

type dataSharingBody struct {
	Owner    string        `json:"owner"` // the account whose repositories a consent covers
	Features []featureJSON `json:"features"`
	Notice   string        `json:"notice,omitempty"`
}

// apiDataSharing is each managed feature with its consent and the preconditions it still needs. A consent that
// cannot be read fails the answer (502): it is never shown as "not allowed".
func (s *server) apiDataSharing(r *http.Request, sc scope) (any, error) {
	return s.dataSharing(r.Context(), sc)
}

func (s *server) dataSharing(ctx context.Context, sc scope) (dataSharingBody, error) {
	owner, _, _ := strings.Cut(sc.repo.FullName, "/")
	body := dataSharingBody{Owner: owner}
	mode := s.dataMode(ctx, sc)
	for _, f := range sharedFeatures {
		c, ok, err := s.consents.Get(ctx, sc.repo.InstallationID, f.kind)
		if err != nil {
			return dataSharingBody{}, err
		}
		out := featureJSON{Kind: string(f.kind), Title: f.title, Leaves: f.leaves, Note: f.note, Allowed: ok}
		allowed := precondition{Label: "Allowed for " + owner, State: checkNotMet}
		if ok {
			out.By, out.At, allowed.State = c.By, c.At.UTC().Format(time.RFC3339), checkMet
		}
		out.Checks = []precondition{allowed}
		if f.managed {
			out.Checks = append(out.Checks, mode)
		}
		body.Features = append(body.Features, out)
	}
	return body, nil
}

// dataMode is the repository-settings precondition of a managed feature. Settings that cannot be read leave it
// unknown: the page still answers who allowed what.
func (s *server) dataMode(ctx context.Context, sc scope) precondition {
	p := precondition{Label: "This repository's settings say data_mode: managed", State: checkUnknown}
	set, err := s.settings(ctx, sc.repo)
	switch {
	case errors.Is(err, errNoSettings):
		p.Label += " (the default branch has no valid settings file)"
	case err != nil:
	case set.DataMode == settings.DataManaged:
		p.State = checkMet
	default:
		p.State = checkNotMet
	}
	return p
}

type dataSharingJSON struct {
	Kind   string `json:"kind"`
	Action string `json:"action"` // allow or revoke
}

var errBadDataSharing = &userError{http.StatusBadRequest, "invalid", "Choose a feature and allow or revoke it."}

// apiShareData allows or revokes one feature for the installation of the authorized repository. Both are
// idempotent: allowing an allowed feature keeps who allowed it and when. Each change writes one audit line.
func (s *server) apiShareData(r *http.Request, sc scope) (any, error) {
	var in dataSharingJSON
	if err := decodeJSON(r, &in); err != nil {
		return nil, err
	}
	f, ok := featureOf(in.Kind)
	if !ok || (in.Action != actionAllow && in.Action != actionRevoke) {
		return nil, errBadDataSharing
	}
	outcome, notice, err := s.shareData(r.Context(), sc, f, in.Action)
	if err != nil {
		return nil, err
	}
	logOf(r).outcome = outcome
	// The kind is the server's constant, never the request's text.
	slog.Info("ui consent", "outcome", outcome, "kind", string(f.kind),
		"installation_id", sc.repo.InstallationID, "user_id", sc.user.UserID)
	body, err := s.dataSharing(r.Context(), sc)
	if err != nil {
		return nil, errSavedUnread
	}
	body.Notice = notice
	return body, nil
}

// errSavedUnread answers a change that was written but could not be read back: the generic upstream sentence would
// say nothing was changed.
var errSavedUnread = &userError{http.StatusBadGateway, "upstream", "The change was saved, but it could not be read back. Reload the page."}

func (s *server) shareData(ctx context.Context, sc scope, f sharedFeature, action string) (outcome, notice string, err error) {
	id := sc.repo.InstallationID
	if action == actionRevoke {
		return "consent_revoked", f.title + " is no longer allowed.", s.consents.Revoke(ctx, id, f.kind)
	}
	err = s.consents.Record(ctx, id, account.Consent{Kind: f.kind, By: sc.user.Login, ByID: sc.user.UserID, At: s.now()})
	switch {
	case errors.Is(err, account.ErrConsentExists):
		return "consent_kept", f.title + " is already allowed.", nil
	case err != nil:
		return "", "", err
	}
	return "consent_recorded", f.title + " is allowed.", nil
}

// managedAIWord is the overview's one store read about data sharing; a failed read is unknown, never not_allowed.
func (s *server) managedAIWord(ctx context.Context, sc scope) string {
	_, ok, err := s.consents.Get(ctx, sc.repo.InstallationID, account.ConsentManagedLLM)
	switch {
	case err != nil:
		return checkUnknown
	case ok:
		return "allowed"
	}
	return "not_allowed"
}

// managedMode is whether the editor offers managed AI and, when it does not, the reason in the page's words.
// Settings that cannot be read for another reason than their absence give no reason: the page claims nothing.
func (s *server) managedMode(ctx context.Context, sc scope) (offer bool, off, dataSharing string) {
	set, err := s.settings(ctx, sc.repo)
	switch {
	case errors.Is(err, errNoSettings):
		return false, "Managed mode is off: the default branch has no valid " + settings.Path + ".", ""
	case err != nil:
		return false, "", ""
	case set.DataMode != settings.DataManaged:
		return false, "Managed mode is off: this repository's settings say data_mode: " + string(set.DataMode) + ".", ""
	}
	_, ok, err := s.consents.Get(ctx, sc.repo.InstallationID, account.ConsentManagedLLM)
	switch {
	case err != nil:
		return false, "Managed mode is off: whether managed AI is allowed could not be read. Try again.", ""
	case ok:
		return true, "", ""
	}
	owner, _, _ := strings.Cut(sc.repo.FullName, "/")
	off = "Managed mode is off: managed AI is not allowed for " + owner + "."
	if _, err := s.sessions.Authorize(ctx, sc.user, sc.repo.FullName, permAdmin); err == nil {
		dataSharing = repoPath(sc, "/data-sharing")
	}
	return false, off, dataSharing
}

// consentMoved sends the old consent page to Data sharing. It stays behind the admin check, so the redirect never
// tells a non-administrator that the repository exists.
func consentMoved(w http.ResponseWriter, r *http.Request, sc scope) {
	http.Redirect(w, r, repoPath(sc, "/data-sharing"), http.StatusPermanentRedirect)
}

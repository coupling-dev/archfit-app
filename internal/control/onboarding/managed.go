package onboarding

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"strings"
	"time"

	"github.com/alexei-led/archfit-app/internal/control/account"
	"github.com/alexei-led/archfit-app/internal/control/proposal"
	"github.com/alexei-led/archfit-app/internal/github/api"
	"github.com/alexei-led/archfit-app/internal/wire/manifest"
	"github.com/alexei-led/archfit-app/internal/wire/policy"
	"github.com/alexei-led/archfit-app/internal/wire/settings"
)

const (
	maxNotes            = 20
	managedPolicyCommit = "Add the Archfit policy proposed by managed discovery"
)

func (s *service) StartManaged(ctx context.Context, r api.Repo, owners []string) (proposal.Draft, error) {
	// Consent is read on every call and before anything else: no consent, no token, no clone, no job.
	if !s.consents.Has(ctx, r.InstallationID, account.ConsentSourceProcessing) {
		return proposal.Draft{}, account.ErrNoConsent
	}
	if s.sandbox == nil || s.tokens == nil {
		return proposal.Draft{}, ErrNoSandbox
	}
	st, err := s.derive(ctx, r)
	if err != nil {
		return proposal.Draft{}, err
	}
	if st.stage != StageInstalled {
		return proposal.Draft{}, ErrAlreadyOnboarded
	}
	now := s.now()
	if m, found := parseMarker(st.pull.Body); st.draft != nil && found && m.running(now) {
		return *st.draft, nil // a job of this or another instance is alive
	}

	select {
	case s.slots <- struct{}{}:
	default:
		return proposal.Draft{}, ErrBusy
	}
	draft, err := s.openManaged(ctx, r, owners, st.draft, now)
	if err != nil {
		<-s.slots
		return proposal.Draft{}, err
	}
	s.jobs.Go(func() {
		defer func() { <-s.slots }()
		s.runJob(r, draft, now)
	})
	return draft, nil
}

// openManaged reuses the open onboarding PR or opens PR 1, then marks it running.
func (s *service) openManaged(ctx context.Context, r api.Repo, owners []string, open *proposal.Draft, now time.Time) (proposal.Draft, error) {
	var draft proposal.Draft
	if open != nil {
		draft = *open
	} else {
		set, err := newSettings(owners, settings.DataManaged)
		if err != nil {
			return proposal.Draft{}, err
		}
		if draft, err = s.proposeOnboarding(ctx, r, set); err != nil {
			return proposal.Draft{}, err
		}
	}
	if err := s.proposals.SetBody(ctx, r.RepoRef, draft.Number, runningBody(now)); err != nil {
		return proposal.Draft{}, fmt.Errorf("onboarding: mark managed discovery running: %w", err)
	}
	return draft, nil
}

// runJob is the managed job. It outlives the request, so it never uses the request context.
// Its outcome is written to the PR body only; Progress reads it from there.
func (s *service) runJob(r api.Repo, draft proposal.Draft, consentChecked time.Time) {
	started := s.now()
	result, code := s.discover(r)

	ctx, cancel := context.WithTimeout(context.Background(), writeTimeout)
	defer cancel()
	if code == "" {
		files := []api.FileChange{{Path: policy.PolicyPath, Content: result.DraftPolicy}}
		change := changeFor(r, onboardingBranch, onboardingTitle, runningBody(consentChecked), managedPolicyCommit, files)
		if _, err := s.proposals.Propose(ctx, change, ""); err != nil {
			code = codeGitHub
		}
	}
	body := failedBody(code)
	if code == "" {
		body = completeBody(consentChecked, result.Notes)
	}
	log := slog.Default().With("installation_id", r.InstallationID, "repository_id", r.ID, "pull_request", draft.Number,
		"stage", string(StageInstalled), "duration_ms", s.now().Sub(started).Milliseconds())
	if code == "" {
		log.Info("managed discovery complete", "outcome", markerComplete)
	} else {
		log.Warn("managed discovery failed", "outcome", code)
	}
	if err := s.proposals.SetBody(ctx, r.RepoRef, draft.Number, body); err != nil {
		// The marker stays "running"; Progress shows it as stalled after stalledAfter.
		log.Error("managed discovery outcome not written", "error_class", errClass(err))
	}
}

// discover mints the clone token and runs the sandbox once. The token lives only in the job value,
// and no error text leaves this function: it may quote repository content.
func (s *service) discover(r api.Repo) (DiscoveryResult, string) {
	ctx, cancel := context.WithTimeout(context.Background(), s.jobTimeout)
	defer cancel()
	token, err := s.tokens.RepoReadToken(ctx, r.InstallationID, r.FullName)
	if err != nil {
		return DiscoveryResult{}, codeToken
	}
	result, err := s.sandbox.Discover(ctx, DiscoveryJob{
		CloneURL:    "https://github.com/" + r.FullName + ".git",
		Token:       token,
		Ref:         r.DefaultBranch,
		ImageDigest: manifest.Current().ImageDigest,
		Timeout:     s.jobTimeout,
	})
	switch {
	case errors.Is(err, ErrNoSandbox):
		return DiscoveryResult{}, codeUnavailable
	case err != nil && errors.Is(ctx.Err(), context.DeadlineExceeded):
		return DiscoveryResult{}, codeTimeout
	case err != nil:
		return DiscoveryResult{}, codeSandbox
	case checkDraft(result.DraftPolicy) != "":
		return DiscoveryResult{}, codeDraft
	}
	return result, ""
}

func runningBody(started time.Time) string {
	return managedBody("Managed discovery running since "+started.UTC().Format(time.RFC3339)+".",
		marker{State: markerRunning, Started: started})
}

func failedBody(code string) string {
	return managedBody("Managed discovery failed (`"+code+"`). Nothing was proposed; retry from the Archfit UI.",
		marker{State: markerFailed, Code: code})
}

func completeBody(consentChecked time.Time, notes []string) string {
	var b strings.Builder
	fmt.Fprintf(&b, "Managed discovery complete: the proposed `%s` is on this branch. It is a proposal to review, not measurement evidence.\n\n", policy.PolicyPath)
	fmt.Fprintf(&b, "Consent: `%s`, checked at %s.", account.ConsentSourceProcessing, consentChecked.UTC().Format(time.RFC3339))
	kept := 0
	for _, note := range notes {
		if kept == maxNotes {
			break
		}
		if !cleanNote(note) {
			continue
		}
		if kept == 0 {
			b.WriteString("\n\nEngine notes:\n")
		}
		// Engine-authored: a backtick or raw HTML would otherwise render as Markdown in the App's PR body.
		b.WriteString("\n- " + sanitize(note))
		kept++
	}
	return managedBody(b.String(), marker{State: markerComplete})
}

// cleanNote applies the draft line checks and keeps HTML comments out, so a note can never pose as the marker.
func cleanNote(note string) bool {
	return strings.TrimSpace(note) != "" && cleanLine(note) && !strings.Contains(note, "<!--") && !strings.Contains(note, "-->")
}

// managedBody is the job status, the PR 1 description and, last, the hidden marker line.
func managedBody(status string, m marker) string {
	return status + "\n\n" + onboardingBody(settings.DataManaged) + "\n" + m.String() + "\n"
}

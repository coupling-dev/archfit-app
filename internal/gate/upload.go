package gate

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"slices"
	"strconv"
	"strings"
	"time"

	"github.com/alexei-led/archfit-app/internal/gate/feedback"
	"github.com/alexei-led/archfit-app/internal/gate/ingest"
	"github.com/alexei-led/archfit-app/internal/gate/inputs"
	"github.com/alexei-led/archfit-app/internal/gate/sweep"
	"github.com/alexei-led/archfit-app/internal/gate/verdict"
	"github.com/alexei-led/archfit-app/internal/github/api"
	"github.com/alexei-led/archfit-app/internal/wire/envelope"
	"github.com/alexei-led/archfit-app/internal/wire/manifest"
	"github.com/alexei-led/archfit-app/internal/wire/policy"
	"github.com/alexei-led/archfit-app/internal/wire/report"
)

const (
	uploadDeadline    = 50 * time.Second // inside the 60 second feedback target
	retryAfterSeconds = 60

	codeRateLimited = "rate_limited"
	codeUnavailable = "unavailable"

	eventPush = "push" // the GitHub event of a run on a pushed branch
)

// accepted is the 2xx body: "accepted and published", not "passed".
type accepted struct {
	Conclusion verdict.Conclusion `json:"conclusion"`
	Reason     verdict.ReasonCode `json:"reason"`
}

type refused struct {
	Error string `json:"error"`
}

func (g *Gate) serveUpload(w http.ResponseWriter, req *http.Request) {
	start := g.now()
	ctx, cancel := context.WithTimeout(req.Context(), uploadDeadline)
	defer cancel()

	u, err := g.receiver.Receive(ctx, req, envelope.KindReport)
	var outcome verdict.Outcome
	if err == nil {
		outcome, err = g.decideAndPublish(ctx, u)
	}

	status, body := http.StatusOK, any(nil)
	var rejection *Rejection
	switch {
	case err == nil:
		body = accepted{Conclusion: outcome.Conclusion, Reason: outcome.Reasons[0].Code}
	case errors.As(err, &rejection):
		status, body = rejection.Status, refused{Error: rejection.Code}
	case errors.Is(err, api.ErrRateLimited):
		status, body = http.StatusServiceUnavailable, refused{Error: codeRateLimited}
		w.Header().Set("Retry-After", strconv.Itoa(retryAfterSeconds))
	default:
		status, body = http.StatusServiceUnavailable, refused{Error: codeUnavailable}
	}
	g.logUpload(ctx, u, outcome, status, g.now().Sub(start))

	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	// The status is out; a client that went away is not worth a log line.
	_ = json.NewEncoder(w).Encode(body)
}

// decideAndPublish is steps 2 to 8 of the report upload flow. Every error leaves the pending check as it is.
func (g *Gate) decideAndPublish(ctx context.Context, u Upload) (verdict.Outcome, error) {
	// The protected ref is always the default branch. A pull request's base is the branch a pull request asks to merge
	// into, and an agent controls it: the settings and policy read there could be loosened in that very change. A
	// pull request into any other base therefore never gets a success, which is what unsupported_event says.
	target := inputs.Target{
		Repo:        u.Binding.Repo,
		PullRequest: u.Binding.PullRequest,
		HeadSHA:     u.Binding.HeadSHA,
		BaseRef:     u.Binding.Repo.DefaultBranch,
		Fork:        u.Binding.Fork,
	}
	problems := u.Problems
	var shared []verdict.SharedPull
	if u.Binding.PullRequest != 0 {
		if u.Binding.BaseRef != u.Binding.Repo.DefaultBranch {
			problems = append(slices.Clone(problems), ingest.ProblemUnsupportedEvent)
		} else {
			// Checks are written per head commit: a pull request into another base on this head would show the
			// result. Only the default branch is a protected base, so a shared head never gets a success. A failed
			// listing is an error (503, the Action retries), never a success.
			pulls, err := g.pulls.OpenPulls(ctx, u.Binding.Repo.RepoRef)
			if err != nil {
				return verdict.Outcome{}, fmt.Errorf("gate: list open pull requests: %w", err)
			}
			shared = sweep.SharedWith(pulls, u.Binding.PullRequest, u.Binding.HeadSHA, u.Binding.Repo.DefaultBranch)
		}
	}
	trusted, err := g.resolver.Resolve(ctx, target, g.now())
	if err != nil {
		return verdict.Outcome{}, fmt.Errorf("gate: resolve inputs: %w", err)
	}
	outcome := verdict.Decide(verdict.Facts{
		Problems:         problems,
		BaselineDigest:   u.BaselineDigest,
		LabelsDigest:     u.LabelsDigest,
		Report:           u.Report,
		SharedHead:       shared,
		IdentityApproved: identityApproved(u),
		WorkflowApproved: workflowApproved(u.Binding, trusted),
		Trusted:          trusted,
	})
	if len(outcome.Reasons) == 0 {
		return verdict.Outcome{}, errors.New("gate: decision without a reason")
	}
	pub := feedback.Publication{
		Target:       target,
		Identity:     u.Identity,
		Outcome:      outcome,
		Receipt:      receipt(u, trusted, outcome),
		Report:       u.Report,
		Trusted:      trusted,
		PolicyChange: policyChangeFor(u, trusted),
		// A sweep may correct the check between this decision and its publication. Inside the lease feedback refuses
		// a decision whose inputs are no longer trusted (503 here); the Action's retry decides from the new inputs.
		Resolve: func(ctx context.Context) (inputs.Trusted, error) { return g.resolver.Resolve(ctx, target, g.now()) },
	}
	if err := g.publisher.Publish(ctx, pub); err != nil {
		return verdict.Outcome{}, fmt.Errorf("gate: publish: %w", err)
	}
	// The repair issue follows the published check and never changes the answer: it only notifies.
	if defaultBranchPush(u) {
		g.repairer.Repair(ctx, pub)
	}
	return outcome, nil
}

// defaultBranchPush reports a run on a push to the default branch. Other uploads without a pull request (a push to
// another branch, an unbound pull request run) say nothing about the default branch. A push that is not to a branch (a
// tag) keeps the default branch as its BaseRef but carries unsupported_event: it says nothing either.
func defaultBranchPush(u Upload) bool {
	b := u.Binding
	return b.Event == eventPush && b.PullRequest == 0 && b.BaseRef == b.Repo.DefaultBranch &&
		!slices.Contains(u.Problems, ingest.ProblemUnsupportedEvent)
}

// policyChangeFor passes on the Action's classification only when the App's own trusted inputs say this pull request
// changes the policy file: a card the App's diff does not back is never shown. Self-reported, display only.
func policyChangeFor(u Upload, t inputs.Trusted) *report.PolicyChange {
	if !slices.Contains(t.ProtectedPaths, policy.PolicyPath) {
		return nil
	}
	return u.PolicyChange
}

// identityApproved: the claimed identity is a row of the locked manifest and the report was measured
// with that row's profile. Unknown never compares as equal.
func identityApproved(u Upload) bool {
	if u.Report == nil {
		return false
	}
	entry, ok := manifest.Lookup(u.Identity)
	profile := u.Report.Comparison.MeasurementProfile
	return ok && profile != nil && entry.ProfileVersion != "" && profile.Version == entry.ProfileVersion
}

// workflowApproved: the run executed the workflow file the settings on the protected ref name.
// A reusable workflow of another repository has the path "".
func workflowApproved(b Binding, t inputs.Trusted) bool {
	return t.SettingsFound && b.WorkflowPath != "" && b.WorkflowPath == t.Settings.Workflow
}

func receipt(u Upload, t inputs.Trusted, o verdict.Outcome) feedback.Receipt {
	configHash := ""
	if u.Report != nil {
		configHash = u.Report.Comparison.ConfigHash
	}
	return feedback.Receipt{
		RunID:                u.Binding.RunID,
		RunAttempt:           u.Binding.RunAttempt,
		ReportDigest:         u.Digest,
		BaseSHA:              integratedBase(u.Binding),
		IdentityKey:          identityKey(u.Report),
		PolicyDigest:         acceptedDigest(t.PolicyDigests, configHash),
		BaselineDigest:       acceptedDigest(t.BaselineDigests, u.BaselineDigest),
		LabelsDigest:         acceptedDigest(t.LabelsDigests, u.LabelsDigest),
		EarliestWaiverExpiry: t.EarliestWaiverExpiry,
		Conclusion:           o.Conclusion,
		Reason:               o.Reasons[0].Code,
	}
}

// integratedBase is the default-branch commit the report was measured against: the base tip GitHub merged the head
// into, or, for a head that conflicts and was measured alone, the merge base. "" when no integration was measured.
func integratedBase(b Binding) string {
	switch b.Integration {
	case envelope.IntegrationMerged:
		return b.BaseSHA
	case envelope.IntegrationConflicts:
		return b.MergeBaseSHA
	}
	return ""
}

// identityKey names what a report's engine measured and under which inputs, from its result identity: the analysed
// tree, the policy, baseline and labels digests, the comparison base, the engine version and the measurement profile.
// "" when the report has no identity or lacks a field that makes it a key. The key is the engine's own statement and
// decides nothing: it is recorded so that a later decision can compare what was measured, never to skip feedback.
func identityKey(r *report.Report) string {
	if r == nil || r.Identity == nil {
		return ""
	}
	id := r.Identity
	if id.HeadTreeDigest == "" || id.PolicyDigest == "" || id.EngineVersion == "" || id.MeasurementProfileHash == "" {
		return ""
	}
	parts := []string{id.HeadTreeDigest, id.PolicyDigest, id.BaselineDigest, id.LabelsDigest, id.BaseSHA, id.EngineVersion, id.MeasurementProfileHash}
	sum := sha256.Sum256([]byte(strings.Join(parts, "\x00")))
	return hex.EncodeToString(sum[:])
}

// acceptedDigest is the claim when gate/inputs accepts it, else the first trusted digest.
// A receipt therefore only ever names digests of the protected ref or of an owner-approved head.
func acceptedDigest(trusted []string, claim string) string {
	if inputs.Accepts(trusted, claim) {
		return claim
	}
	return inputs.First(trusted)
}

func (g *Gate) logUpload(ctx context.Context, u Upload, o verdict.Outcome, status int, elapsed time.Duration) {
	codes := make([]string, len(o.Reasons))
	for i, r := range o.Reasons {
		codes[i] = string(r.Code)
	}
	slog.Default().InfoContext(ctx, "gate upload",
		"installation_id", u.Binding.Repo.InstallationID, "repository_id", u.Binding.Repo.ID,
		"pr", u.Binding.PullRequest, "head_sha", u.Binding.HeadSHA,
		"run_id", u.Binding.RunID, "run_attempt", u.Binding.RunAttempt, "digest", u.Digest,
		"conclusion", string(o.Conclusion), "reasons", codes,
		"status", status, "duration_ms", elapsed.Milliseconds())
}

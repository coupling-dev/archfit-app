package gate

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"slices"
	"strconv"
	"time"

	"github.com/alexei-led/archfit-app/internal/gate/feedback"
	"github.com/alexei-led/archfit-app/internal/gate/inputs"
	"github.com/alexei-led/archfit-app/internal/gate/verdict"
	"github.com/alexei-led/archfit-app/internal/github/api"
	"github.com/alexei-led/archfit-app/internal/wire/envelope"
	"github.com/alexei-led/archfit-app/internal/wire/manifest"
)

const (
	uploadDeadline    = 50 * time.Second // inside the 60 second feedback target
	retryAfterSeconds = 60

	codeRateLimited = "rate_limited"
	codeUnavailable = "unavailable"
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

// decideAndPublish is steps 2 to 7 of the report upload flow. Every error leaves the pending check as it is.
func (g *Gate) decideAndPublish(ctx context.Context, u Upload) (verdict.Outcome, error) {
	target := inputs.Target{
		Repo:        u.Binding.Repo,
		PullRequest: u.Binding.PullRequest,
		HeadSHA:     u.Binding.HeadSHA,
		BaseRef:     u.Binding.BaseRef,
		Fork:        u.Binding.Fork,
	}
	if target.PullRequest == 0 {
		// The binding names the branch the run executed on; only the default branch is a protected ref.
		target.BaseRef = u.Binding.Repo.DefaultBranch
	}
	trusted, err := g.resolver.Resolve(ctx, target, g.now())
	if err != nil {
		return verdict.Outcome{}, fmt.Errorf("gate: resolve inputs: %w", err)
	}
	outcome := verdict.Decide(verdict.Facts{
		Problems:         u.Problems,
		BaselineDigest:   u.BaselineDigest,
		LabelsDigest:     u.LabelsDigest,
		Report:           u.Report,
		IdentityApproved: identityApproved(u),
		WorkflowApproved: workflowApproved(u.Binding, trusted),
		Trusted:          trusted,
	})
	if len(outcome.Reasons) == 0 {
		return verdict.Outcome{}, errors.New("gate: decision without a reason")
	}
	err = g.publisher.Publish(ctx, feedback.Publication{
		Target:   target,
		Identity: u.Identity,
		Outcome:  outcome,
		Receipt:  receipt(u, trusted, outcome),
		Report:   u.Report,
		Trusted:  trusted,
	})
	if err != nil {
		return verdict.Outcome{}, fmt.Errorf("gate: publish: %w", err)
	}
	return outcome, nil
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
		PolicyDigest:         acceptedDigest(t.PolicyDigests, configHash),
		BaselineDigest:       acceptedDigest(t.BaselineDigests, u.BaselineDigest),
		LabelsDigest:         acceptedDigest(t.LabelsDigests, u.LabelsDigest),
		EarliestWaiverExpiry: t.EarliestWaiverExpiry,
		Conclusion:           o.Conclusion,
		Reason:               o.Reasons[0].Code,
	}
}

// acceptedDigest is the claim when the trusted list holds it, else the first trusted digest, else "".
// A receipt therefore only ever names digests of the protected ref or of an owner-approved head.
func acceptedDigest(trusted []string, claim string) string {
	switch {
	case len(trusted) == 0:
		return ""
	case slices.Contains(trusted, claim):
		return claim
	}
	return trusted[0]
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

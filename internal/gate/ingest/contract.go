// Package ingest authenticates an upload and binds it to authoritative GitHub data.
package ingest

import (
	"context"
	"fmt"
	"net/http"

	"github.com/alexei-led/archfit-app/internal/github/api"
	"github.com/alexei-led/archfit-app/internal/github/oidc"
	"github.com/alexei-led/archfit-app/internal/wire/envelope"
	"github.com/alexei-led/archfit-app/internal/wire/manifest"
	"github.com/alexei-led/archfit-app/internal/wire/report"
)

// Binding is the authoritative identity of an upload, resolved from OIDC claims and the GitHub API.
type Binding struct {
	Repo         api.Repo // resolved by the App, never from the envelope
	Event        string   // "pull_request" | "push" | "merge_group"; discovery, baseline and reanchor uploads also "workflow_dispatch"
	PullRequest  int      // 0 unless pull_request
	HeadSHA      string   // for merge_group the group commit
	BaseRef      string   // PR base branch; the default branch for a merge group; for other events the branch the run executed on
	BaseSHA      string
	MergeBaseSHA string
	// AnalyzedSHA and Integration are what GitHub says the engine must have measured: the head, or the test merge
	// commit of a pull request that merges. Both are "" for a pull request whose merge GitHub has not computed, so no
	// envelope matches.
	AnalyzedSHA  string
	Integration  string
	Fork         bool
	RunID        int64
	RunAttempt   int
	WorkflowPath string
	// MergeGroup is true for a merge_group report whose head GitHub's merge queue of the default branch lists as an
	// entry's commit when the upload arrived. Members and approvals are read by gate/inputs, never from here.
	MergeGroup bool
}

// Problem is an eligibility defect found after the tenant was authenticated.
// Problems never reject the request; they force the gate to action_required.
type Problem string

const (
	ProblemEnvelopeMismatch Problem = "envelope_mismatch" // envelope disagrees with authoritative data
	ProblemUnsupportedEvent Problem = "unsupported_event" // e.g. workflow_run
	ProblemFork             Problem = "fork"
	ProblemReportInvalid    Problem = "report_invalid" // payload failed no-source decoding
)

// problemOrder is the constant declaration order; Upload.Problems follows it.
var problemOrder = []Problem{ProblemEnvelopeMismatch, ProblemUnsupportedEvent, ProblemFork, ProblemReportInvalid}

type Upload struct {
	Binding        Binding
	Identity       manifest.Identity // claimed engine/image identity from the envelope
	Digest         string            // payload digest, verified against the envelope
	BaselineDigest string            // claimed by the envelope; "" = the producer used no baseline
	LabelsDigest   string            // claimed by the envelope; "" = the producer used no labels file
	Payload        []byte            // the exact verified bytes; the only content for KindDiscovery, KindBaseline and KindReanchor
	Report         *report.Report    // nil for KindDiscovery, KindBaseline and when ProblemReportInvalid
	// PolicyChange is the engine's classification of the pull request's policy change, as the Action sent it in the
	// envelope. nil when it sent none or sent one report.DecodePolicyChange refuses; neither is a Problem, because the
	// card it feeds is display-only. Self-reported: gate/inputs, not this, says whether the policy changed.
	PolicyChange *report.PolicyChange
	Problems     []Problem
}

// Rejection is returned when no tenant could be authenticated or the upload is stale.
// Nothing is published for a Rejection; the HTTP status is the whole answer.
type Rejection struct {
	Status int    // 400, 401, 403, 409, 413, 429, 503
	Code   string // stable machine-readable reason
}

func (r *Rejection) Error() string {
	return fmt.Sprintf("ingest: upload rejected: %d %s", r.Status, r.Code)
}

type Receiver interface {
	// Receive authenticates and binds one upload of the given kind. A stale upload
	// (head is no longer the PR head, an older attempt of the same run, or a merge group the queue no longer lists) is a
	// 409 Rejection.
	Receive(ctx context.Context, req *http.Request, kind envelope.Kind) (Upload, error)
}

// Admitter answers whether the App serves an installation; owner is its account login. inputs.Admissions implements it.
type Admitter interface {
	Admitted(ctx context.Context, installationID int64, owner string) (bool, error)
}

func New(repos api.Repos, pulls api.Pulls, actions api.Actions, queues api.MergeQueues, v oidc.Verifier, audience string, admit Admitter) Receiver {
	return &receiver{repos: repos, pulls: pulls, actions: actions, queues: queues, verifier: v, audience: audience, admit: admit}
}

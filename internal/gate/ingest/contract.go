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
	Event        string   // "pull_request" | "push"; discovery and baseline uploads also "workflow_dispatch"
	PullRequest  int      // 0 unless pull_request
	HeadSHA      string
	BaseRef      string // PR base branch; for other events the branch the run executed on
	BaseSHA      string
	MergeBaseSHA string
	Fork         bool
	RunID        int64
	RunAttempt   int
	WorkflowPath string
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
	Payload        []byte            // the exact verified bytes; the only content for KindDiscovery and KindBaseline
	Report         *report.Report    // nil for KindDiscovery, KindBaseline and when ProblemReportInvalid
	Problems       []Problem
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
	// (head is no longer the PR head, or an older attempt of the same run) is a 409 Rejection.
	Receive(ctx context.Context, req *http.Request, kind envelope.Kind) (Upload, error)
}

func New(repos api.Repos, pulls api.Pulls, actions api.Actions, v oidc.Verifier, audience string) Receiver {
	return &receiver{repos: repos, pulls: pulls, actions: actions, verifier: v, audience: audience}
}

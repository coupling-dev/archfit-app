// Package feedback publishes a gate outcome as check runs and one PR comment.
package feedback

import (
	"context"
	"fmt"
	"time"

	"github.com/alexei-led/archfit-app/internal/gate/inputs"
	"github.com/alexei-led/archfit-app/internal/github/api"
	"github.com/alexei-led/archfit-app/internal/store"
	"github.com/alexei-led/archfit-app/internal/wire/result"
)

// Receipt identifies an Actions run and attempt for publication ordering and re-checks.
type Receipt struct {
	RunID      int64
	RunAttempt int
}

// ErrStaleInputs means Publication4.Current found changed authority inside the lease; nothing was written.
var ErrStaleInputs = fmt.Errorf("feedback: trusted inputs changed after the decision: %w", errUnwritten)

// Publication4 is the feedback of one Archfit 4 (envelope 2) upload: archfit/gate on the head and, for a pull request,
// its comment. The run, the attempt and the digest name it, as the receipt of the check.
type Publication4 struct {
	Target      inputs.Target // Repo, PullRequest (0 for a push or a merge group) and HeadSHA
	RunID       int64
	RunAttempt  int
	Digest      string // the payload digest of the upload
	Advisory    bool   // the policy's gate is advisory: a fail shows as neutral, with the same records
	Fresh       bool   // create a fresh check instead of updating a reusable one
	StagedToken string // upload's pending marker, bound before publication
	Generated   bool   // result is made by the App, not the uploaded engine
	Source      string // upload, request, sweep, missing_upload or owner
	token       string
	Result      result.Result // Status is the status the App gave; the status line and the records are shown as given
	// Current, when set, is called inside the head's publication lease before anything is written. false means the
	// status was decided from inputs that are no longer current: Publish4 writes nothing and returns ErrStaleInputs, so
	// an upload decided earlier never replaces one of the same run and attempt decided later.
	Current func(context.Context) (bool, error)
}

// Publisher4 publishes Archfit 4 results. It writes no archfit/state check.
type Publisher4 interface {
	// Withdraw4 publishes only unknown, taking the live check receipt inside the head lease when the caller has no
	// matching pull record. Current still guards live policy/record authority; a stored record for another head
	// must not leave an old success on a force-pushed-back head.
	Withdraw4(ctx context.Context, p Publication4) error
	// Publish4 writes archfit/gate on p's head and, for a pull request, the one comment, in place, inside the head's
	// publication lease. It never overwrites the check or the comment of a newer run or attempt, and skips the comment
	// of a head that is no longer the pull request's.
	Publish4(ctx context.Context, p Publication4) error
}

// New4 serializes publication through a per-head Store lease. appURL links feedback to the App's pull page.
func New4(checks api.Checks, comments api.Comments, pulls api.Pulls, s store.Store, appURL string, now func() time.Time) Publisher4 {
	return &serialized{
		next:  &publisher{checks: checks, comments: comments, pulls: pulls, appURL: appURL, now: now},
		store: s,
		now:   now,
		wait:  pollWait,
	}
}

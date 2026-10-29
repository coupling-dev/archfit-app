package decisions

import (
	"context"
	"fmt"
	"log/slog"
	"strings"

	"github.com/alexei-led/archfit-app/internal/gate/inputs"
	"github.com/alexei-led/archfit-app/internal/wire/decision"
)

// statusDetail is the part of the status line in parentheses.
func statusDetail(rec Record) string {
	if rec.Status == decision.StatusApproved {
		return decision.ApprovedDetail(rec.ProposalPR)
	}
	return rec.Reason
}

// publish writes the status comment of a record and remembers its ID.
func (r *Requests) publish(ctx context.Context, t inputs.Target, rec Record) error {
	marker := decision.StatusMarker(rec.FindingID, rec.PolicyDigest)
	id, err := r.writeComment(ctx, t, marker, decision.StatusLine(rec.Status, statusDetail(rec)))
	if err != nil {
		return fmt.Errorf("decisions: status comment: %w", err)
	}
	if id != 0 && id != rec.StatusCommentID {
		r.saveCommentID(ctx, t, rec, id)
	}
	return nil
}

// Answer writes a status comment for a request that has no record: one that failed a check. It writes nothing when a
// record exists for the same IDs, so a bad refile never overwrites the status of a good request.
func (r *Requests) Answer(ctx context.Context, t inputs.Target, findingID, digest string, status decision.Status, reason string) error {
	if findingID != decision.Unknown && digest != decision.Unknown {
		raw, err := r.load(ctx, t, recordKey(t, t.PullRequest, findingID, digest))
		if err != nil || raw != nil {
			return err
		}
	}
	_, err := r.writeComment(ctx, t, decision.StatusMarker(findingID, digest), decision.StatusLine(status, reason))
	if err != nil {
		return fmt.Errorf("decisions: status comment: %w", err)
	}
	return nil
}

// writeComment creates the App's comment that starts with marker, or edits it in place when its text differs. It
// returns the comment's ID, 0 when the thread is full of status comments and none was written.
func (r *Requests) writeComment(ctx context.Context, t inputs.Target, marker, line string) (int64, error) {
	body := marker + "\n" + line + "\n" + ownerSentence
	all, err := r.comments.AppComments(ctx, t.Repo.RepoRef, t.PullRequest)
	if err != nil {
		return 0, err
	}
	var found struct {
		id   int64
		body string
	}
	statuses := 0
	for _, c := range all {
		if !strings.HasPrefix(c.Body, decision.StatusMarkerPrefix) {
			continue
		}
		statuses++
		if strings.HasPrefix(c.Body, marker+"\n") && (found.id == 0 || c.ID < found.id) {
			found.id, found.body = c.ID, c.Body
		}
	}
	switch {
	case found.id != 0 && found.body == body:
		return found.id, nil
	case found.id != 0:
		return found.id, r.comments.UpdateComment(ctx, t.Repo.RepoRef, found.id, body)
	case statuses >= maxStatusComments:
		slog.Default().WarnContext(ctx, "decisions: status comment limit reached",
			"installation_id", t.Repo.InstallationID, "pr", t.PullRequest)
		return 0, nil
	}
	c, err := r.comments.CreateComment(ctx, t.Repo.RepoRef, t.PullRequest, body)
	return c.ID, err
}

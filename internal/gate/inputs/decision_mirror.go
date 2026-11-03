package inputs

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"html"
	"strings"
	"time"
	"unicode"

	"github.com/alexei-led/archfit-app/internal/github/api"
	"github.com/alexei-led/archfit-app/internal/store"
)

// MirrorDecisions posts each stored owner decision as an App-authored PR comment. The marker makes a retry after an
// unknown GitHub write outcome idempotent. The decision remains authoritative even when this notification fails.
func (r *resolver) MirrorDecisions(ctx context.Context, t Target, comments api.Comments) error {
	rec, _, err := r.readDecisionRecord(ctx, t)
	if err != nil {
		return err
	}
	pending := false
	for _, entry := range rec.History {
		pending = pending || !entry.Published
	}
	if !pending {
		return nil
	}
	if comments == nil {
		return errors.New("inputs: comments unavailable for decision mirror")
	}
	current, err := comments.AppComments(ctx, t.Repo.RepoRef, t.PullRequest)
	if err != nil {
		return fmt.Errorf("inputs: list decision comments: %w", err)
	}
	for _, entry := range rec.History {
		if entry.Published {
			continue
		}
		marker := decisionCommentMarker(entry.ID)
		body := renderDecisionComment(entry)
		var found *api.Comment
		for i := range current {
			if strings.Contains(current[i].Body, marker) {
				found = &current[i]
				break
			}
		}
		var comment api.Comment
		switch {
		case found == nil:
			comment, err = comments.CreateComment(ctx, t.Repo.RepoRef, t.PullRequest, body)
			if err == nil {
				current = append(current, comment)
			}
		case found.Body != body:
			comment = *found
			err = comments.UpdateComment(ctx, t.Repo.RepoRef, found.ID, body)
			if err == nil {
				current[foundIndex(current, found.ID)].Body = body
			}
		default:
			comment = *found
		}
		if err != nil {
			return fmt.Errorf("inputs: mirror owner decision: %w", err)
		}
		if comment.ID <= 0 {
			return errors.New("inputs: decision comment has no GitHub ID")
		}
		if err := r.markDecisionPublished(ctx, t, entry.ID, comment.ID); err != nil {
			return err
		}
	}
	return nil
}

func (r *resolver) markDecisionPublished(ctx context.Context, t Target, id string, commentID int64) error {
	for range decisionAttempts {
		rec, old, err := r.readDecisionRecord(ctx, t)
		if err != nil {
			return err
		}
		found := false
		for i := range rec.History {
			if rec.History[i].ID == id {
				if rec.History[i].Published {
					return nil
				}
				rec.History[i].Published = true
				rec.History[i].CommentID = commentID
				found = true
				break
			}
		}
		if !found {
			return ErrDecisionInvalid
		}
		next, err := json.Marshal(rec)
		if err != nil {
			return fmt.Errorf("inputs: encode decision mirror state: %w", err)
		}
		ok, err := r.store.CompareAndSwap(ctx, store.Tenant(t.Repo.InstallationID), decisionKey(t), old, next)
		if err != nil {
			return fmt.Errorf("inputs: save decision mirror state: %w", err)
		}
		if ok {
			return nil
		}
	}
	return errDecisionContention
}

func decisionCommentMarker(id string) string {
	return "<!-- archfit-owner-decision:" + id + " -->"
}

func renderDecisionComment(entry decisionEntry) string {
	var label string
	switch entry.Action {
	case DecisionApprove:
		label = "Approve architecture change"
	case DecisionRequestChanges:
		label = "Request changes"
	case DecisionWithdraw:
		label = "Withdraw App approval"
	case DecisionOverride:
		label = "Override and approve architecture change"
	default:
		label = "Architecture decision"
	}
	var b strings.Builder
	b.WriteString(decisionCommentMarker(entry.ID))
	b.WriteString("\n## Archfit architecture decision: ")
	b.WriteString(label)
	b.WriteString("\n\n- By: &#64;")
	b.WriteString(html.EscapeString(entry.Login))
	b.WriteString("\n- Candidate commit: `")
	b.WriteString(html.EscapeString(entry.HeadSHA))
	b.WriteString("`\n- Protected settings commit: `")
	b.WriteString(html.EscapeString(entry.BaseSHA))
	b.WriteString("`\n- Recorded: ")
	b.WriteString(entry.At.UTC().Format(time.RFC3339))
	b.WriteString("\n\nThis decision does not merge or publish the pull request. A new push needs a new candidate decision.\n")
	if entry.Action == DecisionRequestChanges || entry.Action == DecisionWithdraw || entry.Action == DecisionOverride {
		b.WriteString("\n<details><summary>Owner reason (posted as text)</summary>\n\n<pre>")
		b.WriteString(escapeDecisionReason(entry.Reason))
		b.WriteString("</pre>\n</details>\n")
	}
	if entry.Action == DecisionOverride {
		b.WriteString("\n**Objection versions covered by this override:**\n")
		for _, objection := range entry.OverrodeObjections {
			b.WriteString("- `")
			b.WriteString(html.EscapeString(objection.ID))
			b.WriteString("` · &#64;")
			b.WriteString(html.EscapeString(objection.Login))
			b.WriteString(" · ")
			b.WriteString(html.EscapeString(objection.Source))
			if objection.HeadSHA != "" {
				b.WriteString(" · candidate `")
				b.WriteString(html.EscapeString(objection.HeadSHA))
				b.WriteString("`")
			}
			b.WriteByte('\n')
		}
		b.WriteString("\nAn Archfit override does not dismiss the native GitHub review or bypass GitHub branch protection. Original review/reason text remains in its source comment.\n")
	}
	return b.String()
}

func escapeDecisionReason(reason string) string {
	clean := strings.Map(func(r rune) rune {
		if unicode.IsControl(r) && r != '\n' && r != '\t' {
			return -1
		}
		// Remove directional controls that can make public audit text appear to say something else.
		if r >= 0x202A && r <= 0x202E || r >= 0x2066 && r <= 0x2069 || r == 0x200E || r == 0x200F {
			return -1
		}
		return r
	}, reason)
	return strings.ReplaceAll(html.EscapeString(clean), "@", "&#64;")
}

func foundIndex(comments []api.Comment, id int64) int {
	for i, comment := range comments {
		if comment.ID == id {
			return i
		}
	}
	return -1
}

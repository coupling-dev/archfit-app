package feedback

import (
	"context"
	"net/url"
	"strings"
	"time"

	"github.com/alexei-led/archfit-app/internal/gate/inputs"
	"github.com/alexei-led/archfit-app/internal/github/api"
	"github.com/alexei-led/archfit-app/internal/wire/policy"
	"github.com/alexei-led/archfit-app/internal/wire/settings"
)

const decisionBannerMarker = "<!-- archfit-owner-decision-banner:v1 -->"

// DecisionBanner makes protected-path authority visible before a report exists. The comment marker gives a webhook
// retry one stable App-authored comment; the required pending check carries the same owner/source/action summary.
func (s *serialized) DecisionBanner(ctx context.Context, t inputs.Target, trusted inputs.Trusted) error {
	return s.hold(ctx, t, func(ctx context.Context) error {
		return s.next.decisionBanner(ctx, t, trusted)
	})
}

func (s *publisher) decisionBanner(ctx context.Context, t inputs.Target, trusted inputs.Trusted) error {
	body := renderDecisionBanner(t, trusted, s.pullLink(t))
	if body == "" {
		return nil
	}
	comments, existing, found, err := s.findComment(ctx, t)
	if err != nil {
		return err
	}
	if !found {
		for _, comment := range comments {
			if strings.Contains(comment.Body, decisionBannerMarker) && (existing.ID == 0 || comment.ID < existing.ID) {
				existing = comment
			}
		}
	}
	if existing.ID == 0 {
		if _, err := s.comments.CreateComment(ctx, t.Repo.RepoRef, t.PullRequest, body); err != nil {
			return err
		}
	} else if updated := replaceDecisionBanner(existing.Body, body); updated != existing.Body {
		if err := s.comments.UpdateComment(ctx, t.Repo.RepoRef, existing.ID, updated); err != nil {
			return err
		}
	}
	if trusted.Settings.Gate != settings.GateRequired {
		return nil
	}
	return s.pendingDecisionCheck(ctx, t, ownerDecisionTitle(trusted), body)
}

func (s *publisher) pendingDecisionCheck(ctx context.Context, t inputs.Target, title, summary string) error {
	run, found, err := s.latestRun(ctx, t.Repo.RepoRef, t.HeadSHA, gateCheck)
	if err != nil {
		return err
	}
	if found && run.Status == statusCompleted {
		return nil
	}
	now := s.now()
	if !found {
		_, err := s.checks.CreateCheckRun(ctx, t.Repo.RepoRef, api.CheckRun{
			Name: gateCheck, HeadSHA: t.HeadSHA, DetailsURL: s.pullLink(t), Status: statusInProgress,
			Title: title, Summary: summary, StartedAt: now,
		})
		return err
	}
	if run.Title == title && run.Summary == summary && run.Status == statusInProgress {
		return nil
	}
	run.Name, run.Status, run.Conclusion = gateCheck, statusInProgress, ""
	run.Title, run.Summary, run.DetailsURL = title, summary, s.pullLink(t)
	if run.StartedAt.IsZero() {
		run.StartedAt = now
	}
	return s.checks.UpdateCheckRun(ctx, t.Repo.RepoRef, run)
}

func renderDecisionBanner(t inputs.Target, trusted inputs.Trusted, appLink string) string {
	if t.PullRequest <= 0 || !trusted.SettingsFound || len(trusted.ProtectedPaths) == 0 {
		return ""
	}
	var b strings.Builder
	b.WriteString(decisionBannerMarker)
	b.WriteString("\n## " + ownerDecisionTitle(trusted) + "\n\n")
	if trusted.Approval == inputs.ApprovalGranted {
		b.WriteString("Approved by " + code(trusted.ApprovedBy) + ". Evaluation and merge are separate.\n\n")
	}
	shown, total := 0, 0
	for _, objection := range trusted.Objections {
		if objection.Overridden {
			continue
		}
		total++
		if shown < 5 {
			b.WriteString("- Changes requested by " + code(objection.Login) + ": " + text(objection.Reason) + "\n")
			shown++
		}
	}
	if total > shown {
		b.WriteString("More objections are shown on the Archfit PR page.\n")
	}

	b.WriteString("**Why:** this pull request changes protected architecture inputs:\n")
	for _, path := range trusted.ProtectedPaths {
		b.WriteString("- ")
		b.WriteString(code(path))
		b.WriteString(" — ")
		b.WriteString(text(protectedPathKind(path)))
		b.WriteByte('\n')
	}
	b.WriteString("\n**Candidate head:** ")
	b.WriteString(code(t.HeadSHA))
	b.WriteString(". A new push needs a new decision.\n")
	b.WriteString("\n**Who can decide:** any one current eligible architecture owner or owner-team member named on the protected settings ref; not everyone listed. Agents never decide.\n")
	b.WriteString("\nConfigured owners:\n")
	for _, owner := range trusted.Settings.Owners {
		b.WriteString("- ")
		b.WriteString(code(owner))
		b.WriteByte('\n')
	}
	settingsSHA := t.BaseSHA
	if settingsSHA == "" {
		settingsSHA = trusted.SettingsSHA
	}
	b.WriteString("\nProtected settings source: ")
	if sourceURL := trustedSettingsURL(t, settingsSHA); sourceURL != "" {
		b.WriteString("[`.archfit-app.yaml` at commit ")
		b.WriteString(code(settingsSHA))
		b.WriteString("](")
		b.WriteString(sourceURL)
		b.WriteString(")\n")
	} else {
		b.WriteString("ref ")
		b.WriteString(code(t.BaseRef))
		b.WriteString(" (the exact commit is not available in this event context)\n")
	}
	if trusted.Settings.Gate == settings.GateRequired {
		b.WriteString("\n**Gate mode: required.** Archfit requires an owner approval before its gate can pass. GitHub's branch rules decide whether this blocks merging.\n")
	} else {
		b.WriteString("\n**Gate mode: advisory.** This architecture decision does not prevent merging.\n")
	}
	b.WriteString("\nAn eligible owner approves this exact commit with a GitHub review. GitHub does not accept a pull request author's own review, so another eligible owner approves a PR an owner opened.\n")
	if appLink != "" {
		b.WriteString("\n[See this pull request in Archfit](")
		b.WriteString(appLink)
		b.WriteString(")\n")
	}
	b.WriteString("\nThis decision does not merge or publish the pull request. A new push needs a new candidate decision.\n")
	b.WriteString("The App PR page shows the current check; a lower result in this comment may describe an earlier commit.\n")
	b.WriteString("\n" + decisionBannerEnd)
	return b.String()
}

func trustedSettingsURL(t inputs.Target, settingsSHA string) string {
	if settingsSHA == "" {
		return ""
	}
	owner, repo, ok := strings.Cut(t.Repo.FullName, "/")
	if !ok || owner == "" || repo == "" {
		return ""
	}
	return "https://github.com/" + url.PathEscape(owner) + "/" + url.PathEscape(repo) + "/blob/" + url.PathEscape(settingsSHA) + "/" + settings.Path
}

func protectedPathKind(path string) string {
	switch path {
	case policy.PolicyPath:
		return "policy"
	case policy.BaselinePath:
		return "baseline"
	case policy.LabelsPath:
		return "labels"
	case settings.Path:
		return "App owner and gate settings"
	default:
		return "workflow"
	}
}

const decisionBannerEnd = "<!-- archfit-owner-decision-banner:end -->"

func ownerDecisionTitle(trusted inputs.Trusted) string {
	for _, objection := range trusted.Objections {
		if !objection.Overridden {
			return "Architecture changes requested"
		}
	}
	if trusted.Approval == inputs.ApprovalGranted {
		return "Architecture approved"
	}
	return "Architecture decision needed"
}

func replaceDecisionBanner(existing, banner string) string {
	updated := ""
	if start := strings.Index(existing, decisionBannerMarker); start >= 0 {
		if end := strings.Index(existing[start:], decisionBannerEnd); end >= 0 {
			updated = existing[:start] + banner + existing[start+end+len(decisionBannerEnd):]
		}
	}
	lines := strings.SplitN(existing, "\n", 3)
	if updated == "" {
		if len(lines) == 3 {
			updated = join(lines[0]+"\n"+lines[1], banner, lines[2])
		} else {
			updated = join(existing, banner)
		}
	}
	reserve := len(waiverSection(time.DateOnly)) + 2
	if len(updated) <= maxBody-reserve {
		return updated
	}
	// A pre-report notice cannot re-render old report claims. Keep its receipt and waiver, and link to full feedback.
	prefix := commentMarker
	if len(lines) >= 2 {
		prefix = lines[0] + "\n" + lines[1]
	}
	updated = join(prefix, banner, "Previous report details omitted to fit GitHub's limit. Open the Archfit PR page for current feedback.")
	if date, ok := currentWaiver(existing); ok {
		updated = placeWaiver(updated, waiverSection(date))
	}
	return updated
}

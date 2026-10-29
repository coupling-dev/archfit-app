package feedback

import (
	"html"
	"regexp"
	"strings"
	"time"

	"github.com/alexei-led/archfit-app/internal/gate/inputs"
	"github.com/alexei-led/archfit-app/internal/gate/verdict"
	"github.com/alexei-led/archfit-app/internal/github/api"
	"github.com/alexei-led/archfit-app/internal/wire/manifest"
	"github.com/alexei-led/archfit-app/internal/wire/report"
	"github.com/alexei-led/archfit-app/internal/wire/settings"
)

const headingPrefix = "## Archfit: "

// commentHeading is the markers and the headline that open the comment.
func commentHeading(p Publication) string {
	return commentMarker + "\n" + headMarker(p.Target.HeadSHA, p.Receipt) + "\n" + headingPrefix + headline(p)
}

// screen is what the comment shows before its first fold: the head, the findings and the tail. renderComment and
// FirstScreen both read it, so the pull request page cannot word it differently from the comment.
type screen struct{ head, findings, tail string }

// screenOf puts the policy change card and the next step after the findings they answer, as the comment does. A zero
// now writes no entitlement notice; link "" writes no link and no sentence about it.
func screenOf(p Publication, heading, card string, now time.Time, link string) screen {
	head := commentHead(p, heading)
	notice := noticeLine(p.Trusted.Entitlement, now)
	// The next step sits after the violations it answers, in a block that is never cut; with none to answer it
	// follows the head. The notice comes before the link either way.
	var tail string
	if nextBelowFindings(p) {
		head = join(head, notice)
		tail = join(card, nextBlock(p), linkLine(link))
	} else {
		head = join(head, card, nextBlock(p), notice, linkLine(link))
	}
	identity := commentIdentity(p)
	if link != "" {
		identity += " The App link shows the current PR result/recovery diagnostics, not a historical snapshot."
	}
	return screen{head: head, findings: compactFindings(p), tail: join(tail, identity)}
}

// Line kinds of the first screen.
const (
	LineHeadline = "headline" // the result, as the comment's heading says it
	LineHeading  = "heading"  // a card's title
	LineText     = "text"
	LineItem     = "item" // a list entry: a finding, a change of the policy card, a next step
)

// Line is one line of the first screen as plain text: no Markdown, no HTML. A reader that renders it as text shows
// what the comment shows.
type Line struct {
	Kind string
	Text string
}

var markdownLink = regexp.MustCompile(`\[([^\]]*)\]\([^)]*\)`)

var markdownMarks = strings.NewReplacer("**", "", "`", "")

// plain is Markdown the App wrote, read as text. The values in it were sanitized when it was written; a link keeps its
// words and drops its target.
func plain(s string) string {
	return html.UnescapeString(markdownMarks.Replace(markdownLink.ReplaceAllString(s, "$1")))
}

func (s screen) lines() []Line {
	var out []Line
	for raw := range strings.Lines(join(s.head, s.findings, s.tail)) {
		raw = strings.TrimSpace(raw)
		switch rest, ok := strings.CutPrefix(raw, "- "); {
		case raw == "" || strings.HasPrefix(raw, "<!--"):
		case strings.HasPrefix(raw, headingPrefix):
			out = append(out, Line{LineHeadline, plain(strings.TrimPrefix(raw, headingPrefix))})
		case strings.HasPrefix(raw, "### "):
			out = append(out, Line{LineHeading, plain(strings.TrimPrefix(raw, "### "))})
		case ok:
			out = append(out, Line{LineItem, plain(rest)})
		default:
			out = append(out, Line{LineText, plain(raw)})
		}
	}
	return out
}

// ScreenReason is one reason a published check recorded.
type ScreenReason struct{ Code, Category, Detail, Next string }

// ScreenInput is what the pull request page read about a published result: the archfit/state check as GitHub holds it
// and the report the check names. Nothing here is a decision.
type ScreenInput struct {
	Repo, HeadSHA string // the repository's full name; the pull request's head
	PullRequest   int
	Title         string // the archfit/state title: the headline feedback wrote
	CheckID       int64  // the archfit/state check run
	Required      bool   // the protected settings require archfit/gate
	Reasons       []ScreenReason
	EngineVersion string // of the producer identity the check records
	PolicyDigest  string
	// BaselineDigest is the receipt's: a digest, or what the inputs section says when no baseline was used.
	BaselineDigest string
	RunID          int64
	RunAttempt     int
	PolicyCard     string // the "### Policy change" section of the check summary, Markdown as feedback wrote it
	Report         *report.Report
}

// FirstScreen is the comment's first screen for a published result, as lines of plain text. It is the comment's own
// code over the recorded result, so the page and the comment cannot say different things. It leaves out what only the
// comment carries: the link to the page and the entitlement and waiver notices. nil when the recorded reasons do not
// allow the report to be shown (the comment then shows none either) or the input names no reason.
func FirstScreen(in ScreenInput) []Line {
	if in.Report == nil || len(in.Reasons) == 0 {
		return nil
	}
	reasons := make([]verdict.Reason, len(in.Reasons))
	for i, r := range in.Reasons {
		reasons[i] = verdict.Reason{Code: verdict.ReasonCode(r.Code), Category: verdict.Category(r.Category), Detail: r.Detail, Next: r.Next}
	}
	tr := inputs.Trusted{SettingsFound: true, Settings: settings.Settings{Gate: settings.GateAdvisory}}
	if in.Required {
		tr.Settings.Gate = settings.GateRequired
	}
	if d := in.BaselineDigest; d != "" && !strings.HasPrefix(d, noBaseline) {
		tr.BaselineDigests = []string{d}
	}
	p := presentation(Publication{
		Target:   inputs.Target{Repo: api.Repo{RepoRef: api.RepoRef{FullName: in.Repo}}, PullRequest: in.PullRequest, HeadSHA: in.HeadSHA},
		Identity: manifest.Identity{EngineVersion: in.EngineVersion},
		Outcome:  verdict.Outcome{Conclusion: verdict.ConclusionOf(reasons[0].Code), Reasons: reasons},
		Receipt:  Receipt{RunID: in.RunID, RunAttempt: in.RunAttempt, PolicyDigest: in.PolicyDigest},
		Report:   in.Report, Trusted: tr, stateCheckID: in.CheckID,
	})
	if p.Report == nil {
		return nil
	}
	return screenOf(p, headingPrefix+sanitize(in.Title), in.PolicyCard, time.Time{}, "").lines()
}

// RecoveryStep is the next step of a recorded reason, worded and given to its role as the comment does.
func RecoveryStep(code, next string) string {
	return plain(recoveryStep(verdict.Reason{Code: verdict.ReasonCode(code), Next: next}))
}

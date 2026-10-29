package ui

import (
	"crypto/sha256"
	"encoding/hex"
	"os"
	"strings"
	"testing"

	"github.com/alexei-led/archfit-app/internal/github/api"
	"github.com/alexei-led/archfit-app/internal/wire/settings"
)

const policyCardText = "### Policy change\n\nThis pull request loosens the policy (1 change).\n\n- Allowlist widened: `a` depends_on: `none` → `[b]`\n\n" +
	"A change to the protected policy needs an architecture owner's approval of this exact commit. A loosening is never approved by an agent.\n"

// stateSummary is the archfit/state summary feedback wrote for the healthy fixture, with a policy change card after
// its prelude when withCard.
func stateSummary(t *testing.T, withCard bool) string {
	t.Helper()
	raw, err := os.ReadFile("../../gate/feedback/testdata/golden/healthy.state.md")
	if err != nil {
		t.Fatal(err)
	}
	if !withCard {
		return string(raw)
	}
	prelude, rest, ok := strings.Cut(string(raw), "\n\n### Findings")
	if !ok {
		t.Fatal("healthy.state.md changed shape")
	}
	return prelude + "\n\n" + policyCardText + "\n### Findings" + rest
}

type screenPage struct {
	Unavailable string `json:"unavailable"`
	FirstScreen []struct {
		Kind string `json:"kind"`
		Text string `json:"text"`
	} `json:"first_screen"`
}

func (p screenPage) texts() []string {
	var out []string
	for _, l := range p.FirstScreen {
		out = append(out, l.Kind+": "+l.Text)
	}
	return out
}

func TestPullPageFirstScreenIsWordedByTheComment(t *testing.T) {
	payload := artifact(t, "healthy")
	sum := sha256.Sum256(payload)
	externalID := "v1:88:1:sha256:" + hex.EncodeToString(sum[:])
	tests := []struct {
		name    string
		summary func(t *testing.T) string
		change  func(run *api.CheckRun)
		want    []string // lines the first screen holds
		absent  []string // fragments no line holds
		none    bool     // no first screen at all
	}{
		{
			name:    "the comment's lines from the recorded result",
			summary: func(t *testing.T) string { return stateSummary(t, false) },
			want: []string{
				"headline: Architecture gate passed",
				"Checked with engine v3.0.0 and policy digest 111111111111.",
				"text: Next: Nothing to do.",
				"HEAD " + sha(4) + " · Run 88 · attempt 1 · archfit/state check 9",
			},
		},
		{
			name:    "the policy change card follows the assessment",
			summary: func(t *testing.T) string { return stateSummary(t, true) },
			want: []string{
				"heading: Policy change", "text: This pull request loosens the policy (1 change).",
				"item: Allowlist widened: a depends_on: none → [b]", "A loosening is never approved by an agent.",
			},
			absent: []string{"`", "**"},
		},
		{
			name:    "a title from the check is text, not markup",
			summary: func(t *testing.T) string { return stateSummary(t, false) },
			change:  func(run *api.CheckRun) { run.Title = "Gate `x` <b>bold</b> <!-- y -->" },
			want:    []string{"headline: Gate x <b>bold</b>  y"},
			absent:  []string{"<!--", "`"},
		},
		{
			name: "a reason that is not report-derived shows no first screen",
			summary: func(t *testing.T) string {
				return strings.Replace(stateSummary(t, false), "`healthy` (architecture)", "`engine_error` (evidence)", 1)
			},
			none: true,
		},
		{
			name:    "a summary that records no reason shows no first screen",
			summary: func(*testing.T) string { return "" },
			none:    true,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			e := newEnv(t)
			e.gh.setFile(repoName, mainBranch, settings.Path, strings.Replace(settingsText, "gate: advisory", "gate: required", 1))
			run := api.CheckRun{
				ID: 9, Name: checkState, HeadSHA: sha(4), Status: "completed", Conclusion: "success",
				Title: "Architecture gate passed", ExternalID: externalID, Summary: tt.summary(t),
			}
			if tt.change != nil {
				tt.change(&run)
			}
			e.gh.checks[repoName+"@"+sha(4)+"#"+checkState] = []api.CheckRun{run}
			e.gh.artifacts[88] = payload
			var page screenPage
			e.as("alice", permRead).getJSON("/api/repos/"+repoName+"/pulls/4", &page)
			if tt.none {
				if len(page.FirstScreen) != 0 {
					t.Errorf("first screen = %q", page.texts())
				}
				return
			}
			joined := strings.Join(page.texts(), "\n")
			for _, w := range tt.want {
				if !strings.Contains(joined, w) {
					t.Errorf("first screen lacks %q:\n%s", w, joined)
				}
			}
			for _, a := range tt.absent {
				if strings.Contains(joined, a) {
					t.Errorf("first screen holds %q:\n%s", a, joined)
				}
			}
		})
	}
}

func TestPullPageFirstScreenNeedsAReport(t *testing.T) {
	e := newEnv(t)
	var page screenPage
	e.as("alice", permRead).getJSON("/api/repos/"+repoName+"/pulls/4", &page)
	if page.Unavailable != noResult || len(page.FirstScreen) != 0 {
		t.Errorf("page = %+v", page)
	}
}

func TestScreenInputReadsTheRecordedCheck(t *testing.T) {
	sc := scope{repo: api.Repo{RepoRef: api.RepoRef{FullName: repoName}}}
	pull := pullBody{
		Pull: api.Pull{Number: 4, HeadSHA: sha(4)}, Headline: "Architecture gate passed", CheckID: 9, Required: true,
		Check: &checkContextJSON{
			RunID: 88, RunAttempt: 2, Producer: "engine v3.0.0, image sha256:aa, linux/amd64", PolicyDigest: "(none)",
			BaselineDigest: "no baseline", reportDerived: true,
		},
	}
	in := screenInput(sc, pull, nil)
	if in.Repo != repoName || in.HeadSHA != sha(4) || in.PullRequest != 4 || in.Title != pull.Headline || in.CheckID != 9 || !in.Required ||
		in.EngineVersion != "v3.0.0" || in.PolicyDigest != "" || in.RunID != 88 || in.RunAttempt != 2 || in.BaselineDigest != "no baseline" {
		t.Errorf("input = %+v", in)
	}
	for _, producer := range []string{"no upload", "", "image sha256:aa"} {
		pull.Check.Producer = producer
		if got := screenInput(sc, pull, nil).EngineVersion; got != "" {
			t.Errorf("engine version of %q = %q", producer, got)
		}
	}
	pull.Check.reportDerived = false
	if got := screenInput(sc, pull, nil); got.Repo != "" || got.Title != "" {
		t.Errorf("input for a check that is not report-derived = %+v", got)
	}
}

func TestPolicyCardReadFromASummaryIsBounded(t *testing.T) {
	summary := "- Reason: `healthy` (architecture)\n\n" + policyCardHeading + "\n\nThis pull request loosens the policy (9 changes).\n\n" +
		strings.Repeat("- Rule removed: `"+strings.Repeat("r", 200)+"`\n", 200) + "\n### Findings\n\nnot the card\n"
	got := actionContext(api.CheckRun{Summary: summary, HeadSHA: sha(4)})
	if got.policySummary != "This pull request loosens the policy (9 changes)." {
		t.Errorf("summary line = %q", got.policySummary)
	}
	if len(got.policyCard) > maxPolicyCardBytes || !strings.HasPrefix(got.policyCard, policyCardHeading) || strings.Contains(got.policyCard, "not the card") {
		t.Errorf("card is %d bytes, starts %.30q", len(got.policyCard), got.policyCard)
	}
	if got := actionContext(api.CheckRun{Summary: "- Reason: `healthy` (architecture)\n"}); got.policyCard != "" || got.policySummary != "" {
		t.Errorf("a card from a summary without one: %+v", got)
	}
}

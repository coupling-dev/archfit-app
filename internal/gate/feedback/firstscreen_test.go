package feedback

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/alexei-led/archfit-app/internal/gate/verdict"
	"github.com/alexei-led/archfit-app/internal/wire/report"
)

// A report with the engine's result identity and the coupling gate's own reasons: the first screen says what measured
// the result and against which policy, the owner's chores sit in their own fold, and the agents' fold names the
// identity. Reviewed as a golden.
func TestCommentWithIdentityAndAttention(t *testing.T) {
	p := fixturePublication(t, healthyCase(t), headB)
	p.Report.Identity = &report.ResultIdentity{
		SchemaVersion: report.ResultIdentitySchemaV1, EngineVersion: p.Identity.EngineVersion, PolicyDigest: strings.Repeat("1", 64),
		HeadTreeDigest: strings.Repeat("e", 40), BaseSHA: strings.Repeat("d", 40),
		PolicyRef: &report.PolicyRef{Ref: "refs/heads/main", SHA: strings.Repeat("a", 40)},
	}
	p.Report.GateReasons = []string{
		"coupling gate: 1 new distributed-monolith seam",
		"distributed monolith: orders -> pricing crosses deploy units",
	}
	comment := renderComment(presentation(p), t0, "", "")
	golden(t, "identity-and-attention.comment", comment)

	head := comment[:strings.Index(comment, "<details>")]
	// The first screen states what the App checked, never the report's own claim about its policy.
	if !strings.Contains(head, "Checked with engine `"+p.Identity.EngineVersion+"` and policy digest `"+p.Receipt.PolicyDigest[:policyDigestLen]+"`") ||
		strings.Contains(head, "refs/heads/main") {
		t.Errorf("first screen:\n%s", head)
	}
	if strings.Contains(head, "distributed monolith") {
		t.Error("the owner's chores are on the first screen")
	}
	if !strings.Contains(comment, "<summary>Repository attention (for the owner)</summary>") ||
		!strings.Contains(comment, "Result identity, as the engine states it: engine `"+p.Identity.EngineVersion+"`") {
		t.Errorf("missing the attention fold or the agents' identity:\n%s", comment)
	}
}

func TestAttentionIsBounded(t *testing.T) {
	p := fixturePublication(t, healthyCase(t), headB)
	for range attentionStateCap + 5 {
		p.Report.GateReasons = append(p.Report.GateReasons, strings.Repeat("r", 5000))
	}
	for _, tt := range []struct {
		inComment bool
		limit     int
		more      string
	}{
		{true, attentionCap, "- and 45 more: read the archfit/state check"},
		{false, attentionStateCap, "- and 5 more: they are in the report artifact of this run"},
	} {
		block := attentionBlock(p, tt.inComment)
		if strings.Count(block, "\n- ") != tt.limit+1 || !strings.Contains(block, tt.more) {
			t.Errorf("comment %v: not capped at %d lines, or the rest is not pointed at:\n%s", tt.inComment, tt.limit, block[len(block)-200:])
		}
		if len(block) > tt.limit*(attentionRunes+10)+500 {
			t.Errorf("comment %v: attention is %d bytes", tt.inComment, len(block))
		}
	}
}

// A finding's why and fix read as one line: no doubled period, and a metric's why does not repeat its numbers.
func TestFindingLineWording(t *testing.T) {
	f := report.Finding{Kind: "gate", Status: "new", RuleID: "no_x", Why: "Forbidden by policy.", Constraint: "Move the code.",
		Edge: report.Edge{From: report.Endpoint{Module: "a"}, To: report.Endpoint{Module: "b"}}}
	p := fixturePublication(t, healthyCase(t), headB)
	p.Report.Findings = []report.Finding{f}
	got := compactFindings(p)
	if strings.Contains(got, "..") || !strings.Contains(got, "Why: Forbidden by policy. Fix: Move the code") {
		t.Errorf("finding line: %s", got)
	}
}

// A fork's contributor is asked to get a maintainer's help; the ruleset bypass is owner guidance, never in the comment.
func TestForkCommentAsksForAMaintainer(t *testing.T) {
	p := fixturePublication(t, healthyCase(t), headB)
	p.Report = nil
	p.Outcome = verdict.Without(verdict.ReasonForkUnsupported)
	comment := renderComment(presentation(p), t0, "", "")
	if strings.Contains(strings.ToLower(comment), "bypass") || !strings.Contains(comment, "Ask a maintainer to push the branch") {
		t.Errorf("fork comment:\n%s", comment)
	}
}

// The comment must read in a plain mail client: no table wider than four columns, and none of the coupling
// columns (strength, distance, volatility) that stay on the App page.
func TestCommentsReadInAMailClient(t *testing.T) {
	paths, err := filepath.Glob(filepath.Join("testdata", "golden", "*.comment.md"))
	if err != nil || len(paths) == 0 {
		t.Fatalf("no comment goldens: %v", err)
	}
	for _, path := range paths {
		raw, err := os.ReadFile(path)
		if err != nil {
			t.Fatal(err)
		}
		inFence := false
		for i, line := range strings.Split(string(raw), "\n") {
			if strings.HasPrefix(line, "```") {
				inFence = !inFence
			}
			if inFence {
				continue
			}
			if strings.HasPrefix(line, "|") && strings.Count(line, "|")-1 > 4 {
				t.Errorf("%s:%d: a table of %d columns", filepath.Base(path), i+1, strings.Count(line, "|")-1)
			}
			for _, word := range []string{"strength", "volatility", "| distance"} {
				if strings.Contains(strings.ToLower(line), word) {
					t.Errorf("%s:%d: %q belongs on the App page", filepath.Base(path), i+1, word)
				}
			}
		}
	}
}

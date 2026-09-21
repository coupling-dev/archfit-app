package feedback

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"testing"
	"time"
	"unicode/utf8"

	"github.com/alexei-led/archfit-app/internal/gate/verdict"
	"github.com/alexei-led/archfit-app/internal/github/api"
	"github.com/alexei-led/archfit-app/internal/wire/manifest"
	"github.com/alexei-led/archfit-app/internal/wire/report"
	"github.com/alexei-led/archfit-app/internal/wire/settings"
)

func TestReceiptRoundTrip(t *testing.T) {
	full := Receipt{
		RunID:                7,
		RunAttempt:           2,
		ReportDigest:         reportDigest,
		PolicyDigest:         policyDigest,
		BaselineDigest:       baselineDigest,
		LabelsDigest:         labelsDigest,
		EarliestWaiverExpiry: time.Date(2026, 10, 1, 12, 30, 0, 0, time.UTC),
		Conclusion:           verdict.Failure,
		Reason:               verdict.ReasonBlockingFindings,
	}
	if got, want := externalID(full), "v1:7:2:"+reportDigest; got != want {
		t.Errorf("externalID = %q, want %q", got, want)
	}

	p := blockedPublication(t, headB, 7, 2)
	p.Receipt = full
	for name, summary := range map[string]string{"gate": renderGate(p), "state": renderState(p)} {
		got, ok := parseReceipt(summary)
		if !ok || got != full {
			t.Errorf("%s: parsed %+v ok=%v, want %+v", name, got, ok, full)
		}
		lines := strings.Split(summary, "\n")
		if last := lines[len(lines)-1]; last != receiptMarker(full) {
			t.Errorf("%s: last line = %q, want the receipt marker", name, last)
		}
	}

	older := full
	older.RunID = 3
	if got, ok := parseReceipt(receiptMarker(older) + "\ntext\n" + receiptMarker(full)); !ok || got != full {
		t.Errorf("two markers: got %+v ok=%v, want the last one", got, ok)
	}
	for _, broken := range []string{"<!-- archfit:receipt {broken -->", "<!-- archfit:receipt {\"run_id\":1}", "no marker"} {
		if got, ok := parseReceipt(broken); ok || got != (Receipt{}) {
			t.Errorf("parseReceipt(%q) = %+v ok=%v, want the zero receipt", broken, got, ok)
		}
	}
}

func TestRunOrdering(t *testing.T) {
	completed := func(run int64, attempt int) api.CheckRun {
		return api.CheckRun{ID: 1, Status: statusCompleted, Summary: "text\n" + receiptMarker(Receipt{RunID: run, RunAttempt: attempt})}
	}
	tests := []struct {
		name     string
		existing api.CheckRun
		next     Receipt
		skip     bool
	}{
		{"equal", completed(7, 1), Receipt{RunID: 7, RunAttempt: 1}, false},
		{"new attempt higher", completed(7, 1), Receipt{RunID: 7, RunAttempt: 2}, false},
		{"new run higher", completed(7, 3), Receipt{RunID: 8, RunAttempt: 1}, false},
		{"new attempt lower", completed(7, 2), Receipt{RunID: 7, RunAttempt: 1}, true},
		{"new run lower", completed(7, 1), Receipt{RunID: 6, RunAttempt: 9}, true},
		{"existing pending", api.CheckRun{ID: 1, Status: statusInProgress}, Receipt{RunID: 1, RunAttempt: 1}, false},
		{"existing without marker", api.CheckRun{ID: 1, Status: statusCompleted, Summary: "hand-made"}, Receipt{RunID: 1, RunAttempt: 1}, false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := supersedes(tt.existing, tt.next); got != tt.skip {
				t.Errorf("supersedes = %v, want %v", got, tt.skip)
			}
		})
	}
}

func TestStateConclusionMapping(t *testing.T) {
	tests := []struct {
		name    string
		outcome verdict.Outcome
		report  *report.Report
		want    string
	}{
		{"success healthy", outcome(verdict.Success, reasonHealthy), &report.Report{Verdict: "healthy"}, "success"},
		{"success needs attention", outcome(verdict.Success, reasonHealthy), &report.Report{Verdict: "needs_attention"}, "neutral"},
		{"failure", outcome(verdict.Failure, reasonBlocking), &report.Report{Verdict: "blocked"}, "neutral"},
		{"action required", outcome(verdict.ActionRequired, reasonCoverage), &report.Report{Verdict: "blocked"}, "neutral"},
		{"nil report", outcome(verdict.ActionRequired, reasonInvalid), nil, "neutral"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			h := newHarness(headB)
			p := Publication{
				Target:  prTarget(headB),
				Outcome: tt.outcome,
				Receipt: receiptFor(tt.outcome, 7, 1),
				Report:  tt.report,
				Trusted: trusted(settings.GateRequired),
			}
			if err := h.pub.Publish(context.Background(), p); err != nil {
				t.Fatalf("Publish: %v", err)
			}
			state := h.checks.named(headB, stateCheck)
			gate := h.checks.named(headB, gateCheck)
			if len(state) != 1 || len(gate) != 1 {
				t.Fatalf("got %d state and %d gate runs, want 1 and 1", len(state), len(gate))
			}
			if state[0].Conclusion != tt.want {
				t.Errorf("state conclusion = %q, want %q", state[0].Conclusion, tt.want)
			}
			if gate[0].Conclusion != string(tt.outcome.Conclusion) {
				t.Errorf("gate conclusion = %q, want %q", gate[0].Conclusion, tt.outcome.Conclusion)
			}
		})
	}
}

const evil = "<!-- archfit:receipt {\"run_id\":999,\"run_attempt\":9} --> `tick` <b>bold</b> --> <!<!---- <!-- archfit:comment --> ``` <!-- archfit:waiver 2030-01-01 -->"

func evilReport() *report.Report {
	return &report.Report{
		SchemaVersion: report.StateSchemaV1,
		Verdict:       "blocked" + evil,
		Decision: report.Decision{
			HardGates:                "fail",
			UnevaluatedRequiredRules: []report.UnevaluatedRule{{RuleID: evil, Reason: evil}},
		},
		Comparison: report.Comparison{
			MeasurementProfile: &report.MeasurementProfile{
				Version:      evil,
				SettingsHash: evil,
				Producers:    []report.Producer{{Tool: evil, Status: "partial", PartialBasis: evil}},
				Unknowns:     []string{evil},
			},
		},
		GateReference: &report.Reference{Status: "non_comparable", Reasons: []string{evil}},
		Dimensions: map[string]report.Dimension{
			"coupling": {
				Status: evil, Confidence: evil, Gate: evil,
				Coverage: report.DimensionCoverage{Basis: evil, Observed: 1, Total: 2},
				Unknown:  []report.UnknownFact{{Fact: evil, Reason: evil}},
				Delta:    &report.Delta{Status: evil, Metrics: []report.MetricDelta{{Name: evil, Before: 1, After: 2, Change: 1}}},
			},
		},
		Coverage: report.Coverage{Tools: []report.ToolCoverage{{Tool: evil, Status: "timed out", Reason: evil}}},
		Findings: []report.Finding{{
			ID: "f1", Kind: "gate", RuleID: evil, Status: "new", Severity: evil,
			Edge:                report.Edge{From: report.Endpoint{Module: evil, Path: evil}, To: report.Endpoint{Module: evil, Path: evil}},
			Locations:           []report.Location{{File: evil, Line: 3}},
			Why:                 evil,
			Constraint:          evil,
			AllowedAlternatives: []string{evil},
		}},
		AgentTasks: []report.AgentTask{{
			FindingID: "f1", RuleID: evil, RepairKind: "code_change", Goal: evil,
			Constraints: []string{evil}, Files: []string{evil},
			Validation: []string{"archfit check", "archfit check " + evil, "```"},
			Origin:     "introduced",
		}},
	}
}

func TestSanitizeEngineStrings(t *testing.T) {
	for in, want := range map[string]string{
		"<!<!----":        "&lt;!--",
		"a\r\nb":          "ab",
		"`x` <i>":         "x &lt;i&gt;",
		"--> <!-- x -->":  "  x ",
		"plain rule/id_1": "plain rule/id_1",
	} {
		if got := sanitize(in); got != want {
			t.Errorf("sanitize(%q) = %q, want %q", in, got, want)
		}
	}

	h := newHarness(headB)
	o := outcome(verdict.Failure, verdict.Reason{Code: verdict.ReasonBlockingFindings, Category: verdict.CategoryArchitecture, Detail: evil, Next: evil})
	p := Publication{
		Target:   prTarget(headB),
		Identity: manifest.Identity{EngineVersion: evil, ImageDigest: evil, Platform: evil},
		Outcome:  o,
		Receipt:  receiptFor(o, 7, 1),
		Report:   evilReport(),
		Trusted:  trusted(settings.GateRequired),
	}
	p.Trusted.Approval = "granted"
	p.Trusted.ApprovedBy = evil
	if err := h.pub.Publish(context.Background(), p); err != nil {
		t.Fatalf("Publish: %v", err)
	}

	gate := h.checks.named(headB, gateCheck)[0]
	state := h.checks.named(headB, stateCheck)[0]
	comment := h.comments.all()[0].Body
	for name, tc := range map[string]struct {
		body    string
		markers int
	}{
		"gate":    {gate.Summary, 1},
		"state":   {state.Summary, 1},
		"comment": {comment, 4}, // comment, head, agent-tasks open and close
	} {
		if got := strings.Count(tc.body, "<!--"); got != tc.markers {
			t.Errorf("%s: %d marker openings, want %d", name, got, tc.markers)
		}
		if got := strings.Count(tc.body, "-->"); got != tc.markers {
			t.Errorf("%s: %d marker closings, want %d", name, got, tc.markers)
		}
		mustNotContain(t, tc.body, "<b>", "`tick`")
	}
	for _, title := range []string{gate.Title, state.Title} {
		mustNotContain(t, title, "<!--", "-->", "`")
	}
	for _, a := range state.Annotations {
		mustNotContain(t, a.Message, "<!--", "-->")
	}
	if got := strings.Count(comment, "```"); got != 4 {
		t.Errorf("comment has %d code fences, want 4 (commands and tasks)", got)
	}
	mustContain(t, section(comment, headCommands), "archfit check\n", "2 commands omitted")
	tasks := decodeTasks(t, comment)
	if len(tasks) != 1 || tasks[0]["goal"] != evil {
		t.Errorf("agent task goal was not preserved as decoded: %v", tasks)
	}

	got, err := h.pub.State(context.Background(), p.Target)
	if err != nil {
		t.Fatalf("State: %v", err)
	}
	if got.Receipt != p.Receipt {
		t.Errorf("State receipt = %+v, want the published %+v", got.Receipt, p.Receipt)
	}
}

// decodeTasks parses the agent task block of a comment.
func decodeTasks(t *testing.T, body string) []map[string]any {
	t.Helper()
	_, rest, ok := strings.Cut(body, tasksOpen+"\n```json\n")
	if !ok {
		t.Fatal("no agent task block")
	}
	raw, _, ok := strings.Cut(rest, "\n```\n"+tasksClose)
	if !ok {
		t.Fatal("agent task block is not closed")
	}
	var tasks []map[string]any
	if err := json.Unmarshal([]byte(raw), &tasks); err != nil {
		t.Fatalf("agent task block is not valid JSON: %v", err)
	}
	return tasks
}

func stressReport() *report.Report {
	long := strings.Repeat("é", 1024)
	r := evilReport()
	r.Verdict = "blocked"
	r.Findings = nil
	r.AgentTasks = nil
	for i := range 10000 {
		module := strings.Repeat("模", i%7+1)
		r.Findings = append(r.Findings, report.Finding{
			ID: fmt.Sprintf("%032x", i), Kind: "gate", RuleID: "rule/" + module, Status: []string{"new", "baseline", "fixed"}[i%3], Severity: "high",
			Edge:       report.Edge{From: report.Endpoint{Module: module, Path: "pkg/" + module}, To: report.Endpoint{Module: module + "二"}},
			Locations:  []report.Location{{File: "pkg/" + module + "/a.go", Line: i}},
			Why:        long,
			Constraint: long,
		})
	}
	for i := range 2000 {
		r.AgentTasks = append(r.AgentTasks, report.AgentTask{
			FindingID: fmt.Sprintf("%032x", i), RuleID: "rule", RepairKind: "code_change", Goal: long,
			Constraints: []string{long}, Files: []string{"pkg/模/a.go"},
			Validation: []string{fmt.Sprintf("archfit check -c .archfit.yaml --lang go # %d %s", i, long)},
		})
	}
	return r
}

func TestTruncation(t *testing.T) {
	o := outcome(verdict.Failure, reasonBlocking, reasonCoverage)
	p := Publication{
		Target:   prTarget(headB),
		Identity: fullIdentity(),
		Outcome:  o,
		Receipt:  receiptFor(o, 7, 1),
		Report:   stressReport(),
		Trusted:  trusted(settings.GateRequired),
	}
	comment := renderComment(p, t0, "2026-10-01")
	state := renderState(p)
	gate := renderGate(p)

	for name, body := range map[string]string{"comment": comment, "state": state, "gate": gate} {
		if len(body) > maxBody {
			t.Errorf("%s is %d bytes, over %d", name, len(body), maxBody)
		}
		if !utf8.ValidString(body) {
			t.Errorf("%s is not valid UTF-8", name)
		}
		mustContain(t, body, "## Archfit: Blocked by architecture findings", "blocking_findings", reasonBlocking.Next, "missing_required_coverage", fixedTrustSentence, policyDigest)
	}
	for _, body := range []string{comment, state} {
		mustContain(t, body, "Output truncated.")
		mustContain(t, section(body, headGaps), "timed out", "non_comparable")
	}
	for name, body := range map[string]string{"state": state, "gate": gate} {
		if got, ok := parseReceipt(body); !ok || got != p.Receipt {
			t.Errorf("%s: receipt marker damaged: %+v ok=%v", name, got, ok)
		}
	}
	mustContain(t, comment, waiverSection("2026-10-01"))

	// Tasks give way before findings are cut.
	if tasks := decodeTasks(t, comment); len(tasks) != 0 {
		t.Errorf("task block holds %d tasks while findings are cut, want 0", len(tasks))
	}
	mustContain(t, comment, "2000 more tasks did not fit")

	p.Report.Findings = p.Report.Findings[:3]
	comment = renderComment(p, t0, "")
	if len(comment) > maxBody || !utf8.ValidString(comment) {
		t.Errorf("comment is %d bytes or invalid UTF-8", len(comment))
	}
	mustNotContain(t, comment, "Output truncated.")
	tasks := decodeTasks(t, comment)
	if len(tasks) == 0 || len(tasks) >= 2000 {
		t.Errorf("task block holds %d tasks, want some but not all", len(tasks))
	}
	for _, task := range tasks {
		if len(task) != 8 {
			t.Fatalf("task has %d keys, want 8: %v", len(task), task)
		}
	}
	mustContain(t, comment, fmt.Sprintf("%d more tasks did not fit", 2000-len(tasks)))
}

func gapReport(withGaps bool) *report.Report {
	r := &report.Report{
		SchemaVersion: report.StateSchemaV1,
		Verdict:       "blocked",
		Comparison:    report.Comparison{MeasurementProfile: &report.MeasurementProfile{Version: "archfit.measurement.v1", SettingsHash: "abc"}},
		Dimensions:    map[string]report.Dimension{"coupling": {Status: "measured"}},
	}
	if !withGaps {
		r.Comparison.MeasurementProfile.Producers = []report.Producer{{Tool: "go/packages", Status: "ok"}}
		r.Coverage.Tools = []report.ToolCoverage{{Tool: "go/packages", Status: "ok"}}
		return r
	}
	r.Decision.UnevaluatedRequiredRules = []report.UnevaluatedRule{{RuleID: "layers/no_upward_imports", Reason: "required tool was absent"}}
	r.Coverage.Tools = []report.ToolCoverage{{Tool: "scip", Status: "timed out", Reason: "deadline of 60s passed"}}
	r.Dimensions["coupling"] = report.Dimension{Status: "partial", Unknown: []report.UnknownFact{{Fact: "cross-package calls", Reason: "no call graph producer"}}}
	r.Comparison.MeasurementProfile.Unknowns = []string{"jscpd semantics not pinned"}
	r.Comparison.MeasurementProfile.Producers = []report.Producer{{Tool: "dependency-cruiser", Status: "partial", PartialBasis: "unresolved_specifiers"}}
	return r
}

func TestEvidenceGapSources(t *testing.T) {
	base := blockedPublication(t, headB, 7, 1)
	gaps := func(p Publication) string { return section(renderComment(p, t0, ""), headGaps) }

	p := base
	p.Report = gapReport(true)
	mustContain(t, gaps(p),
		"layers/no_upward_imports", "required tool was absent",
		"scip", "timed out", "deadline of 60s passed",
		"coupling", "cross-package calls", "no call graph producer",
		"jscpd semantics not pinned",
		"dependency-cruiser", "partial", "unresolved_specifiers")
	mustNotContain(t, gaps(p), "None reported")

	p.Report = gapReport(false)
	mustContain(t, gaps(p), "None reported")

	p.Trusted.BaselineDigests = nil
	mustContain(t, gaps(p), "No baseline: every current violation blocks.")
	mustNotContain(t, gaps(p), "None reported")

	p = base
	p.Report = gapReport(false)
	p.Report.GateReference = &report.Reference{Status: "non_comparable", BaseRef: "baseline", Reasons: []string{"measurement profile changed"}}
	mustContain(t, gaps(p), "measurement profile changed")
	mustNotContain(t, gaps(p), "No baseline")

	p.Report = nil
	mustContain(t, gaps(p), "No usable report")
	mustContain(t, section(renderState(p), headGaps), "No usable report")
}

func producerLine(t *testing.T, body string) string {
	t.Helper()
	for _, line := range strings.Split(section(body, headInputs), "\n") {
		if strings.HasPrefix(line, "- Producer identity") {
			return line
		}
	}
	t.Fatal("no producer identity line")
	return ""
}

func TestIdentityLine(t *testing.T) {
	p := blockedPublication(t, headB, 7, 1)
	for name, render := range map[string]func(Publication) string{
		"comment": func(p Publication) string { return renderComment(p, t0, "") },
		"gate":    func(p Publication) string { return renderGate(p) },
		"state":   func(p Publication) string { return renderState(p) },
	} {
		t.Run(name, func(t *testing.T) {
			p.Identity = fullIdentity()
			full := producerLine(t, render(p))
			mustContain(t, full, "claimed: engine v2.3.1, image "+p.Identity.ImageDigest+", linux/amd64")

			p.Identity = manifest.Identity{}
			none := producerLine(t, render(p))
			mustContain(t, none, "no upload")

			p.Identity = manifest.Identity{EngineVersion: evil, ImageDigest: evil, Platform: evil}
			forged := producerLine(t, render(p))
			mustNotContain(t, forged, "<!--", "-->", "`", "<b>")

			for _, line := range []string{full, none, forged} {
				lower := strings.ToLower(line)
				mustNotContain(t, lower, "approved", "verified")
			}
		})
	}
}

func TestAnnotationSelection(t *testing.T) {
	locations := func(file string, n int) []report.Location {
		out := make([]report.Location, n)
		for i := range out {
			out[i] = report.Location{File: file, Line: i + 1}
		}
		return out
	}
	r := &report.Report{
		Verdict: "blocked",
		Findings: []report.Finding{
			{ID: "1", Kind: "advisory", RuleID: "adv", Status: "new", Why: "advisory why", Locations: locations("adv.go", 30)},
			{ID: "2", Kind: "gate", RuleID: "debt", Status: "baseline", Why: "debt why", Locations: locations("debt.go", 9)},
			{ID: "3", Kind: "gate", RuleID: "none", Status: "new", Why: "no location", Locations: []report.Location{}},
			{ID: "4", Kind: "gate", RuleID: "whole", Status: "expired_waiver", Why: "whole file", Locations: []report.Location{{File: "whole.go", Line: 0}}},
			{ID: "5", Kind: "gate", RuleID: "many", Status: "new", Why: "many why", Locations: locations("many.go", 40)},
		},
	}
	h := newHarness(headB)
	p := blockedPublication(t, headB, 7, 1)
	p.Report = r
	if err := h.pub.Publish(context.Background(), p); err != nil {
		t.Fatalf("Publish: %v", err)
	}
	if got := h.checks.named(headB, gateCheck)[0].Annotations; len(got) != 0 {
		t.Errorf("gate check carries %d annotations, want none", len(got))
	}
	got := h.checks.named(headB, stateCheck)[0].Annotations
	if len(got) != 50 {
		t.Fatalf("got %d annotations, want 50", len(got))
	}
	if got[0] != (api.Annotation{Path: "whole.go", Line: 1, Level: "failure", Message: "whole: whole file"}) {
		t.Errorf("first annotation = %+v", got[0])
	}
	for i, a := range got {
		want := "warning"
		if i < 41 {
			want = "failure"
		}
		if a.Level != want {
			t.Errorf("annotation %d (%s) level = %q, want %q", i, a.Path, a.Level, want)
		}
		if a.Path == "" || a.Line < 1 || strings.Contains(a.Message, "no location") {
			t.Errorf("annotation %d invented or lost a location: %+v", i, a)
		}
	}
	if got[41].Path != "adv.go" || got[49].Path != "adv.go" {
		t.Errorf("non-blocking annotations are not in report order: %q, %q", got[41].Path, got[49].Path)
	}
}

package feedback

import (
	"context"
	"encoding/json"
	"fmt"
	"maps"
	"slices"
	"strconv"
	"strings"
	"testing"
	"time"
	"unicode/utf8"

	"github.com/alexei-led/archfit-app/internal/gate/inputs"
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
		{"success needs attention", outcome(verdict.Success, reasonAttention), &report.Report{Verdict: "needs_attention"}, "neutral"},
		{"failure", outcome(verdict.Failure, reasonBlocking), &report.Report{Verdict: "blocked"}, "neutral"},
		{"action required", outcome(verdict.ActionRequired, reasonCoverage), &report.Report{Verdict: "blocked"}, "action_required"},
		{"action required beside a healthy report", outcome(verdict.ActionRequired, reasonIdentity), &report.Report{Verdict: "healthy"}, "action_required"},
		{"nil report", outcome(verdict.ActionRequired, reasonInvalid), nil, "action_required"},
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
	comment := renderComment(p, t0, "2026-10-01", "")
	state := renderState(p)
	gate := renderGate(p)

	for name, body := range map[string]string{"comment": comment, "state": state, "gate": gate} {
		if len(body) > maxBody {
			t.Errorf("%s is %d bytes, over %d", name, len(body), maxBody)
		}
		if !utf8.ValidString(body) {
			t.Errorf("%s is not valid UTF-8", name)
		}
		mustContain(t, body, "## Archfit: Architecture gate failing", "blocking_findings", reasonBlocking.Next, "missing_required_coverage", fixedTrustSentence, policyDigest)
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
	comment = renderComment(p, t0, "", "")
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
		Comparison:    report.Comparison{MeasurementProfile: &report.MeasurementProfile{Version: "archfit.measurement.v2", SettingsHash: "abc"}},
		Dimensions:    map[string]report.Dimension{"coupling": {Status: "measured"}},
	}
	if !withGaps {
		r.GateReference = &report.Reference{Status: "comparable", BaseRef: "baseline", BaselinePresent: true} // the trusted baseline was compared
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
	gaps := func(p Publication) string { return section(renderComment(p, t0, "", ""), headGaps) }

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
	p.Report.GateReference = &report.Reference{Status: "non_comparable", BaseRef: "baseline", Reasons: []string{"no baseline file was loaded"}}
	mustContain(t, gaps(p), "No baseline: every finding counts as new, including older debt.")
	mustNotContain(t, gaps(p), "None reported")

	p = base
	p.Report = gapReport(false)
	p.Report.GateReference = &report.Reference{Status: "non_comparable", BaseRef: "baseline", Reasons: []string{"measurement profile changed"}}
	mustContain(t, gaps(p), "measurement profile changed")
	mustNotContain(t, gaps(p), "No baseline")

	p.Report = nil
	mustContain(t, gaps(p), "No usable report")
	mustContain(t, section(renderState(p), headGaps), "No usable report")

	// The note and verdict step 7a are one rule: present exactly when Decide reports baseline_not_comparable.
	references := map[string]*report.Reference{
		"nil":                          nil,
		"comparable":                   {Status: "comparable"},
		"not_requested":                {Status: "not_requested"},
		"non_comparable":               {Status: "non_comparable", Reasons: []string{"measurement profile changed"}},
		"non_comparable without cause": {Status: "non_comparable"},
	}
	for name, ref := range references {
		for _, baseline := range [][]string{{baselineDigest}, nil} {
			p := base
			p.Report = gapReport(false)
			p.Report.GateReference = ref
			p.Trusted.BaselineDigests = baseline
			o := verdict.Decide(verdict.Facts{Report: p.Report, Trusted: p.Trusted})
			want := slices.ContainsFunc(o.Reasons, func(r verdict.Reason) bool { return r.Code == verdict.ReasonBaselineNotComparable })
			if got := strings.Contains(gaps(p), "Baseline comparison"); got != want {
				t.Errorf("%s, trusted baseline %v: comparison note = %v, verdict not comparable = %v", name, baseline != nil, got, want)
			}
		}
	}
	p = base
	p.Report = gapReport(false)
	p.Report.GateReference = nil
	mustContain(t, gaps(p), "Baseline comparison: the report has no gate reference")
	p.Report.GateReference = &report.Reference{Status: "not_requested"}
	mustContain(t, gaps(p), "Baseline comparison not_requested: no reason given")
}

func TestEvidenceGapsDropNoise(t *testing.T) {
	r := gapReport(false)
	r.Coverage.Tools = []report.ToolCoverage{
		{Tool: "go/packages", Status: "ok"},
		{Tool: "dependency-cruiser", Status: "absent"},
		{Tool: "cargo", Status: "absent"},
		{Tool: "jscpd", Status: "disabled", Reason: "clone detection disabled by config"},
		{Tool: "scip", Status: "timed out", Reason: "deadline of 60s passed"},
		{Tool: "deploy-unit", Status: "absent", Reason: "Go toolchain unavailable for deploy-unit discovery"},
	}
	r.Comparison.MeasurementProfile.Producers = []report.Producer{
		{Tool: "go/packages", Status: "ok"},
		{Tool: "cargo", Status: "absent"},
		{Tool: "jscpd", Status: "disabled"},
		{Tool: "scip", Status: "timed out"},
		{Tool: "git-history", Status: "absent"},
		{Tool: "dependency-cruiser", Status: "partial", PartialBasis: "unresolved_specifiers"},
	}
	p := blockedPublication(t, headB, 7, 1)
	p.Report = r
	for name, body := range map[string]string{"comment": renderComment(p, t0, "", ""), "state": renderState(p)} {
		gaps := section(body, headGaps)
		mustContain(t, gaps,
			"- Tool `scip`: timed out (deadline of 60s passed)\n",
			"- Tool `deploy-unit`: absent (Go toolchain unavailable for deploy-unit discovery)\n",
			"- Analyzers that did not run (absent or disabled): `dependency-cruiser`, `cargo`, `jscpd`\n",
			"- Producer `git-history`: absent\n",
			"- Producer `dependency-cruiser`: partial (unresolved_specifiers)\n")
		mustNotContain(t, gaps, "Tool `cargo`", "Tool `jscpd`", "Tool `dependency-cruiser`", "clone detection disabled",
			"Producer `cargo`", "Producer `jscpd`", "Producer `scip`", "Producer `go/packages`", "None reported")
		if t.Failed() {
			t.Fatalf("%s gaps:\n%s", name, gaps)
		}
	}
}

func TestGateSummaryNamesBlockers(t *testing.T) {
	active := func(id, rule, from, to string, locations ...report.Location) report.Finding {
		return report.Finding{ID: id, Kind: "gate", RuleID: rule, Status: "new", Locations: locations,
			Edge: report.Edge{From: report.Endpoint{Module: from, Path: "pkg/" + from}, To: report.Endpoint{Module: to}}}
	}
	r := &report.Report{
		Verdict: "blocked",
		Findings: []report.Finding{
			{ID: "aaaa0000aaaa0000aaaa0000aaaa0000", Kind: "advisory", RuleID: "bc/imbalanced_coupling", Status: "new"},
			active("ba3803eca947bf3c1b7efa8f37854d5e", "no_direct_b_dependency", "a", "b",
				report.Location{File: "pkg/a/a.go", Line: 3}, report.Location{File: "pkg/a/second.go", Line: 9}),
			{ID: "cccc0000cccc0000cccc0000cccc0000", Kind: "gate", RuleID: "accepted_rule", Status: "baseline"},
			active("dddd1111dddd1111dddd1111dddd1111", "whole_rule", "", "db", report.Location{File: "whole.go"}),
			active("eeee2222eeee2222eeee2222eeee2222", "nowhere", "api", "db"),
		},
		AgentTasks: []report.AgentTask{
			{FindingID: "ba3803eca947bf3c1b7efa8f37854d5e", Goal: "Remove the forbidden dependency."},
			{FindingID: "ba3803eca947bf3c1b7efa8f37854d5e", Goal: "a second task for the same finding"},
		},
	}
	p := blockedPublication(t, headB, 7, 1)
	p.Report = r
	gate := renderGate(p)
	want := headBlocking + "\n\n" +
		"- `no_direct_b_dependency`: `a` → `b` at `pkg/a/a.go:3`, finding `ba3803ec`. Goal: Remove the forbidden dependency.\n" +
		"- `whole_rule`: (no module) → `db` at `whole.go`, finding `dddd1111`. Goal: none reported\n" +
		"- `nowhere`: `api` → `db`, no location reported, finding `eeee2222`. Goal: none reported\n\n" + foldEvidence
	if !strings.Contains(gate, want) {
		t.Errorf("gate summary lacks the blocking findings block:\n%s", gate)
	}
	if !strings.HasSuffix(gate, "\n"+receiptMarker(p.Receipt)) {
		t.Errorf("the receipt marker is not the last line")
	}
	for name, body := range map[string]string{"comment": renderComment(p, t0, "", ""), "state": renderState(p)} {
		if strings.Contains(body, headBlocking) {
			t.Errorf("%s repeats the gate summary's block", name)
		}
	}

	t.Run("capped", func(t *testing.T) {
		p := p
		p.Report = &report.Report{Verdict: "blocked"}
		for i := range 25 {
			p.Report.Findings = append(p.Report.Findings, active(fmt.Sprintf("%032x", i), fmt.Sprintf("rule_%02d", i), "a", "b"))
		}
		block := section(renderGate(p), headBlocking)
		if got := strings.Count(block, "\n- `rule_"); got != blockerCap {
			t.Errorf("%d findings named, want %d", got, blockerCap)
		}
		mustContain(t, block, "`rule_19`", "- and 5 more blocking findings")
		mustNotContain(t, block, "`rule_20`")
	})
	t.Run("a blocking outcome without an active finding says so", func(t *testing.T) {
		p := p
		p.Report = &report.Report{Verdict: "blocked", Findings: []report.Finding{r.Findings[0], r.Findings[2]}}
		mustContain(t, section(renderGate(p), headBlocking), "No active gate finding is listed")
	})
	t.Run("nothing to name", func(t *testing.T) {
		healthy := p
		healthy.Outcome = outcome(verdict.Success, reasonHealthy)
		healthy.Report = &report.Report{Verdict: "healthy"}
		suspendedPub := p
		suspendedPub.Outcome = outcome(verdict.ActionRequired, reasonSuspend, reasonBlocking)
		noReport := p
		noReport.Report = nil
		for name, q := range map[string]Publication{"healthy": healthy, "suspended": suspendedPub, "no report": noReport} {
			if strings.Contains(renderGate(q), headBlocking) {
				t.Errorf("%s: the gate summary names blocking findings", name)
			}
		}
	})
	t.Run("only when the outcome says findings block", func(t *testing.T) {
		evidence := p
		evidence.Outcome = outcome(verdict.ActionRequired, reasonCoverage)
		if strings.Contains(renderGate(evidence), headBlocking) {
			t.Errorf("an outcome without blocking_findings names active findings as blocking")
		}
		behind := p
		behind.Outcome = outcome(verdict.ActionRequired, reasonIdentity, reasonBlocking)
		mustContain(t, section(renderGate(behind), headBlocking), "`no_direct_b_dependency`")
	})
}

func TestDigestSourceLabels(t *testing.T) {
	const producer, protected, noUpload = "(stated by the producer)", "(from the protected ref)", "(from the protected ref; no upload)"
	line := func(body, prefix string) string {
		t.Helper()
		for _, l := range strings.Split(section(body, headInputs), "\n") {
			if strings.HasPrefix(l, prefix) {
				return l
			}
		}
		t.Fatalf("no %q line", prefix)
		return ""
	}
	mismatch := func(code verdict.ReasonCode) verdict.Reason {
		return verdict.Reason{Code: code, Category: verdict.CategoryEvidence, Detail: "ff99", Next: "Run the workflow with the file from the base branch."}
	}
	tests := []struct {
		name             string
		change           func(*Publication)
		baseline, labels string
	}{
		{"claims accepted", func(*Publication) {}, producer, producer},
		{"baseline mismatch", func(p *Publication) {
			p.Outcome = outcome(verdict.ActionRequired, mismatch(verdict.ReasonBaselineMismatch), reasonBlocking)
		}, protected, producer},
		{"labels mismatch, not first", func(p *Publication) {
			p.Outcome = outcome(verdict.Failure, reasonBlocking, mismatch(verdict.ReasonLabelsMismatch))
		}, producer, protected},
		{"no upload", func(p *Publication) { p.Identity, p.Report = manifest.Identity{}, nil }, noUpload, noUpload},
		{"nothing on the protected ref", func(p *Publication) {
			p.Identity, p.Receipt.BaselineDigest, p.Receipt.LabelsDigest = manifest.Identity{}, "", ""
		}, "no baseline", "no labels file"},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			p := blockedPublication(t, headB, 7, 1)
			tc.change(&p)
			for _, body := range []string{renderComment(p, t0, "", ""), renderGate(p), renderState(p)} {
				baseline, labels := line(body, "- Baseline digest"), line(body, "- Labels digest")
				if !strings.HasSuffix(baseline, tc.baseline) || !strings.HasSuffix(labels, tc.labels) {
					t.Errorf("lines:\n%s\n%s\nwant the endings %q and %q", baseline, labels, tc.baseline, tc.labels)
				}
				if p.Identity == (manifest.Identity{}) {
					mustNotContain(t, body, "stated by the producer")
				}
			}
		})
	}
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
		"comment": func(p Publication) string { return renderComment(p, t0, "", "") },
		"gate":    func(p Publication) string { return renderGate(p) },
		"state":   func(p Publication) string { return renderState(p) },
	} {
		t.Run(name, func(t *testing.T) {
			p.Identity = fullIdentity()
			full := producerLine(t, render(p))
			mustContain(t, full, "claimed: engine v3.0.0, image "+p.Identity.ImageDigest+", linux/amd64")

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
	findings := []report.Finding{
		{ID: "1", Kind: "advisory", RuleID: "adv", Status: "new", Why: "advisory why", Locations: locations("adv.go", 30)},
		{ID: "2", Kind: "gate", RuleID: "debt", Status: "baseline", Why: "debt why", Locations: locations("debt.go", 9)},
		{ID: "3", Kind: "gate", RuleID: "none", Status: "new", Why: "no location", Locations: []report.Location{}},
		{ID: "4", Kind: "gate", RuleID: "whole", Status: "expired_waiver", Why: "whole file", Locations: []report.Location{{File: "whole.go", Line: 0}}},
		{ID: "5", Kind: "gate", RuleID: "many", Status: "new", Why: "many why", Locations: locations("many.go", 40)},
		{ID: "6", Kind: "advisory", RuleID: "waived", Status: "waived", Why: "waived why", Locations: locations("adv.go", 2)},
		{ID: "7", Kind: "gate", RuleID: "gone", Status: "fixed", Why: "fixed why", Locations: locations("many.go", 2)},
		{ID: "8", Kind: "gate", RuleID: "elsewhere", Status: "new", Why: "untouched file", Locations: locations("untouched.go", 5)},
	}
	changed := []string{"adv.go", "debt.go", "whole.go", "many.go"}
	publish := func(t *testing.T, target inputs.Target, changed []string, truncated bool, findings []report.Finding) (*harness, []api.Annotation) {
		t.Helper()
		h := newHarness(headB)
		h.pulls.changed, h.pulls.truncated = changed, truncated
		p := blockedPublication(t, headB, 7, 1)
		p.Target, p.Report = target, &report.Report{Verdict: "blocked", Findings: findings}
		if err := h.pub.Publish(context.Background(), p); err != nil {
			t.Fatalf("Publish: %v", err)
		}
		if gate := h.checks.named(headB, gateCheck); len(gate) > 0 && len(gate[0].Annotations) != 0 {
			t.Errorf("gate check carries %d annotations, want none", len(gate[0].Annotations))
		}
		return h, h.checks.named(headB, stateCheck)[0].Annotations
	}
	perPath := func(got []api.Annotation) map[string]int {
		out := map[string]int{}
		for _, a := range got {
			out[a.Path]++
		}
		return out
	}

	t.Run("live findings on files the pull request changed", func(t *testing.T) {
		h, got := publish(t, prTarget(headB), changed, false, findings)
		if want := map[string]int{"whole.go": 1, "many.go": 40, "adv.go": 30}; !maps.Equal(perPath(got), want) {
			t.Fatalf("annotations per file = %v, want %v", perPath(got), want)
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
			if a.Line < 1 || strings.Contains(a.Message, "no location") {
				t.Errorf("annotation %d invented or lost a location: %+v", i, a)
			}
		}
		if got[41].Path != "adv.go" || got[41].Line != 1 || got[70].Line != 30 {
			t.Errorf("non-blocking annotations are not in report order: %+v, %+v", got[41], got[70])
		}
		// The gate check, then the state check with 50, then one more request for the rest.
		if want := []int{0, 50, 21}; !slices.Equal(h.checks.annotationRequests, want) || h.pulls.listed != 1 {
			t.Errorf("annotations per request = %v, file lists = %d; want %v and 1", h.checks.annotationRequests, h.pulls.listed, want)
		}
	})
	t.Run("a truncated file list counts every file as changed", func(t *testing.T) {
		_, got := publish(t, prTarget(headB), nil, true, findings)
		if perPath(got)["untouched.go"] != 5 || len(got) != 76 {
			t.Errorf("annotations per file = %v", perPath(got))
		}
	})
	t.Run("a push annotates every file and lists none", func(t *testing.T) {
		h, got := publish(t, pushTarget(headB), nil, false, findings)
		if perPath(got)["untouched.go"] != 5 || len(got) != 76 || h.pulls.listed != 0 {
			t.Errorf("annotations per file = %v, file lists = %d", perPath(got), h.pulls.listed)
		}
		if want := []int{50, 26}; !slices.Equal(h.checks.annotationRequests, want) {
			t.Errorf("annotations per request = %v, want %v", h.checks.annotationRequests, want)
		}
	})
	t.Run("at most 150, blocking first", func(t *testing.T) {
		many := append([]report.Finding{{ID: "9", Kind: "advisory", RuleID: "adv", Status: "new", Locations: locations("adv.go", 100)}},
			report.Finding{ID: "10", Kind: "gate", RuleID: "many", Status: "new", Locations: locations("many.go", 100)})
		h, got := publish(t, prTarget(headB), changed, false, many)
		if want := map[string]int{"many.go": 100, "adv.go": 50}; !maps.Equal(perPath(got), want) {
			t.Errorf("annotations per file = %v, want %v", perPath(got), want)
		}
		if want := []int{0, 50, 50, 50}; !slices.Equal(h.checks.annotationRequests, want) {
			t.Errorf("annotations per request = %v, want %v", h.checks.annotationRequests, want)
		}
	})
	t.Run("no candidate, no file list", func(t *testing.T) {
		h, got := publish(t, prTarget(headB), changed, false, []report.Finding{findings[1], findings[2], findings[5], findings[6]})
		if len(got) != 0 || h.pulls.listed != 0 {
			t.Errorf("got %d annotations and %d file lists, want none", len(got), h.pulls.listed)
		}
	})
}

func TestHeadlineStatesTheEffectOnMerge(t *testing.T) {
	withReport := func(gate settings.GateMode, pull int, c verdict.Conclusion, v string, baseline bool) Publication {
		p := blockedPublication(t, headB, 7, 1)
		p.Target.PullRequest = pull
		p.Trusted.Settings.Gate = gate
		p.Outcome.Conclusion = c
		p.Report.Verdict = v
		if !baseline {
			p.Trusted.BaselineDigests = nil
		}
		return p
	}
	tests := []struct {
		name string
		p    Publication
		want string
	}{
		{"advisory blocked", withReport(settings.GateAdvisory, 7, verdict.Failure, "blocked", true), "Architecture violations found · advisory"},
		{"advisory blocked, no baseline", withReport(settings.GateAdvisory, 7, verdict.Failure, "blocked", false), "Architecture violations found · advisory · no baseline"},
		{"advisory attention", withReport(settings.GateAdvisory, 7, verdict.Success, "needs_attention", true), "Architecture needs attention · advisory"},
		{"advisory healthy", withReport(settings.GateAdvisory, 7, verdict.Success, "healthy", true), "Architecture healthy · advisory"},
		{"required failing", withReport(settings.GateRequired, 7, verdict.Failure, "blocked", true), "Architecture gate failing"},
		{"required attention", withReport(settings.GateRequired, 7, verdict.Success, "needs_attention", true), "Architecture gate passed · attention needed"},
		{"required healthy", withReport(settings.GateRequired, 7, verdict.Success, "healthy", true), "Architecture gate passed"},
		{"push", withReport(settings.GateRequired, 0, verdict.Failure, "blocked", true), "Architecture violations found"},
		{"advisory, hard gates fail beside a healthy verdict", withReport(settings.GateAdvisory, 7, verdict.Failure, "healthy", true), "Architecture violations found · advisory"},
		{"push, hard gates fail beside needs_attention", withReport(settings.GateRequired, 0, verdict.Failure, "needs_attention", true), "Architecture violations found"},
		{"action required wins", withReport(settings.GateAdvisory, 7, verdict.ActionRequired, "blocked", false), "Action required"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := headline(tt.p); got != tt.want {
				t.Errorf("headline = %q, want %q", got, tt.want)
			}
		})
	}
}

func TestAdvisoryCommentNeverSaysBlocking(t *testing.T) {
	p := blockedPublication(t, headB, 7, 1)
	p.Trusted.Settings.Gate = settings.GateAdvisory
	comment := renderComment(p, t0, "", "")
	mustContain(t, comment, "## Archfit: Architecture violations found · advisory", "This check does not block merging", "**violation**")
	mustNotContain(t, comment, "Blocked by", "**blocking**")
	if strings.Index(comment, "<details>") < strings.Index(comment, headFindings) {
		t.Errorf("findings are folded away: %q", comment)
	}
	findings := section(comment, headFindings)
	if v, a := strings.Index(findings, "**Rule violations**"), strings.Index(findings, " · never block</summary>"); v < 0 || a < 0 || a < v {
		t.Errorf("violations are not open before the folded advisories: %q", findings)
	}
}

func TestCutNeverLeavesAFoldOpen(t *testing.T) {
	block := headFindings + "\n\n<details>\n<summary>9 advisories · never block</summary>\n\n" + strings.Repeat("- advisory line\n", 200) + "\n" + foldClose
	got := cutLines(block, 400)
	if len(got) > 400 {
		t.Errorf("cut is %d bytes, over 400", len(got))
	}
	if strings.Count(got, "<details>") != strings.Count(got, foldClose) {
		t.Errorf("cut left a fold open:\n%s", got)
	}
	if !strings.HasSuffix(got, foldClose+"\n\n"+truncatedNote) {
		t.Errorf("cut does not close the fold before the note:\n%s", got)
	}
}

func TestAdvisoryOfUnknownStatusStaysOpen(t *testing.T) {
	r := &report.Report{Findings: []report.Finding{
		{ID: "a1", Kind: "advisory", RuleID: "bc/x", Status: "new"},
		{ID: "a2", Kind: "advisory", RuleID: "bc/y", Status: "regressed"},
	}}
	got := findingsBlock(r)
	open, folded, ok := strings.Cut(got, "<details>")
	if !ok {
		t.Fatalf("no advisory fold in %q", got)
	}
	mustContain(t, open, "**Rule violations** (1)", "`bc/y`")
	mustContain(t, folded, "1 advisory · never block", "`bc/x`")
	mustNotContain(t, folded, "`bc/y`")
}

func TestWaiverNoticeStaysOutsideFolds(t *testing.T) {
	body := "## Archfit: Action required\n\n<details>\n<summary>Evidence, inputs and validation</summary>\n\n### Inputs and identity\n\n</details>"
	got := placeWaiver(body, "NOTICE")
	if strings.Index(got, "NOTICE") > strings.Index(got, "<details>") {
		t.Errorf("waiver notice placed inside a fold:\n%s", got)
	}
}

func TestCycleDirectionsAreOneItem(t *testing.T) {
	gate := func(rule, from, to, file string) report.Finding {
		return report.Finding{ID: from + to, Kind: "gate", RuleID: rule, Status: "new", Severity: "high", Why: "cycle " + from + " " + to,
			Edge:      report.Edge{From: report.Endpoint{Module: from}, To: report.Endpoint{Module: to}},
			Locations: []report.Location{{File: file, Line: 3}}}
	}
	tests := []struct {
		name      string
		findings  []report.Finding
		items     int
		wantCycle bool
	}{
		{"both directions", []report.Finding{gate("no-module-cycles", "a", "b", "a.go"), gate("no-module-cycles", "b", "a", "b.go")}, 1, true},
		{"one direction", []report.Finding{gate("no-module-cycles", "a", "b", "a.go")}, 1, false},
		{"two rules", []report.Finding{gate("r1", "a", "b", "a.go"), gate("r2", "b", "a", "b.go")}, 2, false},
		{"same direction twice", []report.Finding{gate("r1", "a", "b", "a.go"), gate("r1", "a", "b", "c.go")}, 2, false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			block := findingsBlock(&report.Report{Findings: tt.findings})
			if got := strings.Count(block, "\n- **violation**"); got != tt.items {
				t.Errorf("items = %d, want %d:\n%s", got, tt.items, block)
			}
			if got := strings.Contains(block, "cycle `a` ⇄ `b`"); got != tt.wantCycle {
				t.Errorf("cycle line = %v, want %v:\n%s", got, tt.wantCycle, block)
			}
			if tt.wantCycle {
				mustContain(t, block, "  - `a` → `b`: `a.go:3`", "  - `b` → `a`: `b.go:3`", "**Rule violations** (2)")
			}
		})
	}

	t.Run("a partner past the cap is not pulled in", func(t *testing.T) {
		fs := []report.Finding{gate("no-module-cycles", "a", "b", "a.go")}
		for i := range groupCap - 1 {
			fs = append(fs, gate("r", "m"+strconv.Itoa(i), "n", "m.go"))
		}
		fs = append(fs, gate("no-module-cycles", "b", "a", "b.go"))
		block := findingsBlock(&report.Report{Findings: fs})
		if got := strings.Count(block, "\n- **violation**"); got != groupCap {
			t.Errorf("items = %d, want %d", got, groupCap)
		}
		mustContain(t, block, "- and 1 more")
		mustNotContain(t, block, "⇄")
	})

	t.Run("each direction keeps its path, and the repair keeps its alternatives", func(t *testing.T) {
		f, g := gate("no-module-cycles", "a", "b", "a.go"), gate("no-module-cycles", "b", "a", "b.go")
		f.Edge.From.Path, f.AllowedAlternatives = "a/x.go", []string{"invert the dependency"}
		block := findingsBlock(&report.Report{Findings: []report.Finding{f, g}})
		mustContain(t, block, "  - `a` (`a/x.go`) → `b`: `a.go:3`", "  - Allowed alternatives: invert the dependency")
	})
}

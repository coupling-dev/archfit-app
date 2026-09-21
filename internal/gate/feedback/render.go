package feedback

import (
	"encoding/json"
	"fmt"
	"strconv"
	"strings"
	"time"

	"github.com/alexei-led/archfit-app/internal/gate/inputs"
	"github.com/alexei-led/archfit-app/internal/gate/verdict"
	"github.com/alexei-led/archfit-app/internal/github/api"
	"github.com/alexei-led/archfit-app/internal/wire/manifest"
	"github.com/alexei-led/archfit-app/internal/wire/report"
)

const (
	headFindings   = "### Findings"
	headDimensions = "### Dimensions"
	headGaps       = "### Evidence gaps"
	headInputs     = "### Inputs and identity"
	headCommands   = "### Validation commands"
	headTasks      = "### Agent tasks"

	fixedTrustSentence = "Measurement is customer-attested: the producer is authenticated, the report content is not independently checked."
)

// Display caps. Each capped list ends with "and N more".
const (
	reasonCap     = 50
	groupCap      = 50
	locationCap   = 10
	gapCap        = 50
	metricCap     = 10
	annotationCap = 50
)

// dimensionOrder is the engine's fixed order of its nine dimension keys.
var dimensionOrder = [...]string{
	"intent", "structure", "modularity", "coupling", "change_locality", "complexity", "testability", "operations", "drift",
}

func verdictLine(c verdict.Conclusion) string {
	switch c {
	case verdict.Success:
		return "Passed"
	case verdict.Failure:
		return "Blocked by architecture findings"
	default:
		return "Action required"
	}
}

func gateTitle(p Publication) string { return verdictLine(p.Outcome.Conclusion) }

func stateTitle(p Publication) string {
	switch {
	case suspended(p.Outcome):
		return verdictLine(p.Outcome.Conclusion)
	case p.Report == nil:
		return "Architecture state: no usable report"
	default:
		return "Architecture state: " + text(p.Report.Verdict)
	}
}

// suspended reports whether paid output is withheld: only the reason and the recovery stay.
func suspended(o verdict.Outcome) bool {
	return len(o.Reasons) > 0 && o.Reasons[0].Category == verdict.CategorySubscription
}

func renderGate(p Publication) string {
	l := layout{head: headBlock(p, false), inputs: inputsBlock(p), receipt: receiptMarker(p.Receipt)}
	return l.assemble(maxBody)
}

func renderState(p Publication) string {
	l := layout{head: headBlock(p, false), inputs: inputsBlock(p), receipt: receiptMarker(p.Receipt)}
	if !suspended(p.Outcome) {
		l.findings, l.dims, l.gaps = findingsBlock(p.Report), dimensionsBlock(p.Report), gapsBlock(p)
	}
	return l.assemble(maxBody)
}

// renderComment renders the whole comment. waiver is the date of the notice section to carry, or "".
func renderComment(p Publication, now time.Time, waiver string) string {
	l := layout{head: headBlock(p, true), inputs: inputsBlock(p)}
	if notice := noticeLine(p.Trusted.Entitlement, now); notice != "" {
		l.head += "\n\n" + notice
	}
	if !suspended(p.Outcome) {
		l.findings, l.dims, l.gaps = findingsBlock(p.Report), dimensionsBlock(p.Report), gapsBlock(p)
		if p.Report != nil {
			l.commands = commands(p.Report.AgentTasks)
			l.tasks, l.hasTasks = encodeTasks(p.Report.AgentTasks), true
		}
	}
	// Room for the notice section is always kept, so Warn can add it later without a size check.
	reserve := len(waiverSection(time.DateOnly)) + 2
	body := l.assemble(maxBody - reserve)
	if waiver == "" {
		return body
	}
	return placeWaiver(body, waiverSection(waiver))
}

// headBlock is blocks 1 and 2: markers, verdict line, the deciding reason, then further reasons.
func headBlock(p Publication, markers bool) string {
	var b strings.Builder
	if markers {
		b.WriteString(commentMarker + "\n" + headMarker(p.Target.HeadSHA, p.Receipt) + "\n")
	}
	b.WriteString("## Archfit: " + verdictLine(p.Outcome.Conclusion))
	reasons := p.Outcome.Reasons
	if len(reasons) == 0 {
		return b.String()
	}
	first := reasons[0]
	fmt.Fprintf(&b, "\n\n- Reason: %s (%s)\n- Detail: %s\n- Next: %s", code(string(first.Code)), text(string(first.Category)), text(first.Detail), text(first.Next))
	if len(reasons) == 1 {
		return b.String()
	}
	b.WriteString("\n\nAlso reported:")
	for i, r := range reasons[1:] {
		if i == reasonCap {
			fmt.Fprintf(&b, "\n- and %d more", len(reasons)-1-reasonCap)
			break
		}
		fmt.Fprintf(&b, "\n- %s (%s): %s. Next: %s", code(string(r.Code)), text(string(r.Category)), text(r.Detail), text(r.Next))
	}
	return b.String()
}

func noticeLine(e inputs.Entitlement, now time.Time) string {
	if e.WarnFrom.IsZero() || now.Before(e.WarnFrom) || sanitize(e.Notice) == "" {
		return ""
	}
	return "> " + sanitize(e.Notice)
}

// blocking is a display heuristic only. The conclusion always comes from the Outcome.
func blocking(f report.Finding) bool {
	return f.Kind == "gate" && (f.Status == "new" || f.Status == "expired_waiver")
}

func findingsBlock(r *report.Report) string {
	if r == nil {
		return ""
	}
	var introduced, debt, fixed []report.Finding
	for _, f := range r.Findings {
		if blocking(f) {
			introduced = append(introduced, f)
		}
	}
	for _, f := range r.Findings {
		switch {
		case blocking(f):
		case f.Status == "baseline" || f.Status == "waived":
			debt = append(debt, f)
		case f.Status == "fixed":
			fixed = append(fixed, f)
		default: // never hide a finding whose status this version does not know
			introduced = append(introduced, f)
		}
	}
	var b strings.Builder
	b.WriteString(headFindings)
	if len(r.Findings) == 0 {
		b.WriteString("\n\nNo findings reported.")
	}
	for _, g := range []struct {
		title    string
		findings []report.Finding
	}{
		{"Introduced", introduced},
		{"Accepted debt — display only", debt},
		{"Fixed", fixed},
	} {
		if len(g.findings) == 0 {
			continue
		}
		fmt.Fprintf(&b, "\n\n**%s** (%d)", g.title, len(g.findings))
		for i, f := range g.findings {
			if i == groupCap {
				fmt.Fprintf(&b, "\n- and %d more", len(g.findings)-groupCap)
				break
			}
			writeFinding(&b, f)
		}
	}
	return b.String()
}

func writeFinding(b *strings.Builder, f report.Finding) {
	b.WriteString("\n- ")
	if blocking(f) {
		b.WriteString("**blocking** ")
	}
	fmt.Fprintf(b, "%s (%s, %s): %s → %s", code(f.RuleID), text(f.Severity), text(f.Status), endpoint(f.Edge.From), endpoint(f.Edge.To))
	if len(f.Locations) > 0 {
		b.WriteString("\n  - Locations: ")
		for i, loc := range f.Locations {
			if i == locationCap {
				fmt.Fprintf(b, " and %d more", len(f.Locations)-locationCap)
				break
			}
			if i > 0 {
				b.WriteString(", ")
			}
			if loc.Line > 0 {
				b.WriteString(code(loc.File + ":" + strconv.Itoa(loc.Line)))
			} else {
				b.WriteString(code(loc.File))
			}
		}
	} else {
		b.WriteString("\n  - Locations: none reported")
	}
	fmt.Fprintf(b, "\n  - Why: %s", text(f.Why))
	if f.Constraint != "" {
		fmt.Fprintf(b, "\n  - Constraint: %s", text(f.Constraint))
	}
	if len(f.AllowedAlternatives) > 0 {
		alternatives := make([]string, len(f.AllowedAlternatives))
		for i, a := range f.AllowedAlternatives {
			alternatives[i] = text(a)
		}
		fmt.Fprintf(b, "\n  - Allowed alternatives: %s", strings.Join(alternatives, "; "))
	}
}

func endpoint(e report.Endpoint) string {
	module := "(no module)"
	if e.Module != "" {
		module = code(e.Module)
	}
	if e.Path == "" {
		return module
	}
	return module + " (" + code(e.Path) + ")"
}

func dimensionsBlock(r *report.Report) string {
	if r == nil {
		return ""
	}
	var b strings.Builder
	b.WriteString(headDimensions + "\n\nMeasurement trust: customer-attested.\n")
	for _, key := range dimensionOrder {
		d, ok := r.Dimensions[key]
		if !ok {
			fmt.Fprintf(&b, "\n- `%s`: not reported", key)
			continue
		}
		fmt.Fprintf(&b, "\n- `%s`: %s; coverage %d/%d%s; confidence %s; gate %s", key,
			text(d.Status), d.Coverage.Observed, d.Coverage.Total, parens(d.Coverage.Basis), text(d.Confidence), text(d.Gate))
		if d.Delta != nil {
			b.WriteString("; change " + deltaText(*d.Delta))
		}
	}
	return b.String()
}

func deltaText(d report.Delta) string {
	parts := []string{text(d.Status)}
	for i, m := range d.Metrics {
		if i == metricCap {
			parts = append(parts, fmt.Sprintf("and %d more metrics", len(d.Metrics)-metricCap))
			break
		}
		parts = append(parts, fmt.Sprintf("%s %s → %s (%s)", text(m.Name), number(m.Before), number(m.After), signed(m.Change)))
	}
	parts = append(parts, fmt.Sprintf("%d new and %d resolved findings", len(d.NewFindings), len(d.ResolvedFindings)))
	return strings.Join(parts, ", ")
}

// parens is an optional engine-authored qualifier.
func parens(s string) string {
	if s = sanitize(s); s != "" {
		return " (" + s + ")"
	}
	return ""
}

func number(v float64) string { return strconv.FormatFloat(v, 'g', -1, 64) }

func signed(v float64) string {
	if v > 0 {
		return "+" + number(v)
	}
	return number(v)
}

// gapsBlock is block 7. It is always present: missing evidence is stated, never hidden.
func gapsBlock(p Publication) string {
	r := p.Report
	if r == nil {
		return headGaps + "\n\n- No usable report"
	}
	var lines []string
	add := func(source []string) {
		if len(source) > gapCap {
			source = append(source[:gapCap:gapCap], fmt.Sprintf("- and %d more", len(source)-gapCap))
		}
		lines = append(lines, source...)
	}

	var rules, tools, facts, unknowns, producers []string
	for _, u := range r.Decision.UnevaluatedRequiredRules {
		rules = append(rules, fmt.Sprintf("- Required rule %s was not evaluated: %s", code(u.RuleID), text(u.Reason)))
	}
	for _, t := range r.Coverage.Tools {
		if t.Status != "ok" {
			tools = append(tools, fmt.Sprintf("- Tool %s: %s%s", code(t.Tool), text(t.Status), parens(t.Reason)))
		}
	}
	for _, key := range dimensionOrder {
		for _, u := range r.Dimensions[key].Unknown {
			facts = append(facts, fmt.Sprintf("- Dimension `%s`, unknown fact: %s — %s", key, text(u.Fact), text(u.Reason)))
		}
	}
	if mp := r.Comparison.MeasurementProfile; mp != nil {
		for _, u := range mp.Unknowns {
			unknowns = append(unknowns, "- Measurement profile unknown: "+text(u))
		}
		for _, pr := range mp.Producers {
			if pr.Status == "ok" {
				continue
			}
			producers = append(producers, fmt.Sprintf("- Producer %s: %s%s", code(pr.Tool), text(pr.Status), parens(pr.PartialBasis)))
		}
	}
	for _, source := range [][]string{rules, tools, facts, unknowns, producers} {
		add(source)
	}
	switch ref := r.GateReference; {
	case len(p.Trusted.BaselineDigests) == 0:
		lines = append(lines, "- No baseline: every current violation blocks.")
	case ref != nil && ref.Status != "comparable" && ref.Status != "not_requested":
		reasons := []string{"no reason given"}
		if len(ref.Reasons) > 0 {
			reasons = ref.Reasons
		}
		var refLines []string
		for _, reason := range reasons {
			refLines = append(refLines, fmt.Sprintf("- Baseline comparison %s: %s", text(ref.Status), text(reason)))
		}
		add(refLines)
	}
	if len(lines) == 0 {
		lines = []string{"- None reported"}
	}
	return headGaps + "\n\n" + strings.Join(lines, "\n")
}

// inputsBlock is block 8. Digests are bare hex; the identity is a claim and is labeled so.
func inputsBlock(p Publication) string {
	lines := []string{"- Policy digest: " + code(p.Receipt.PolicyDigest)}
	if p.Receipt.BaselineDigest != "" {
		lines = append(lines, "- Baseline digest: "+code(p.Receipt.BaselineDigest)+" (stated by the producer)")
	} else {
		lines = append(lines, "- Baseline digest: no baseline")
	}
	if p.Receipt.LabelsDigest != "" {
		lines = append(lines, "- Labels digest: "+code(p.Receipt.LabelsDigest)+" (stated by the producer)")
	} else {
		lines = append(lines, "- Labels digest: no labels file")
	}
	profile := "missing"
	if p.Report != nil && p.Report.Comparison.MeasurementProfile != nil {
		mp := p.Report.Comparison.MeasurementProfile
		profile = "version " + code(mp.Version) + ", settings hash " + code(mp.SettingsHash)
	}
	lines = append(lines, "- Measurement profile: "+profile, identityLine(p.Identity))
	if p.Trusted.Approval == inputs.ApprovalGranted {
		lines = append(lines, "- Protected path change: owner approval by "+code(p.Trusted.ApprovedBy))
	}
	return headInputs + "\n\n" + strings.Join(lines, "\n") + "\n\n" + fixedTrustSentence
}

func identityLine(id manifest.Identity) string {
	if id == (manifest.Identity{}) {
		return "- Producer identity: no upload"
	}
	return fmt.Sprintf("- Producer identity, claimed: engine %s, image %s, %s", text(id.EngineVersion), text(id.ImageDigest), text(id.Platform))
}

// commands is the union of the tasks' validation commands in first-seen order. A command is printed
// verbatim or not at all: feedback never edits an engine-authored shell string.
func commands(tasks []report.AgentTask) commandList {
	var list commandList
	seen := map[string]bool{}
	for _, t := range tasks {
		for _, cmd := range t.Validation {
			if seen[cmd] {
				continue
			}
			seen[cmd] = true
			if cmd == "" || strings.Contains(cmd, "<!--") || strings.Contains(cmd, "-->") || strings.Contains(cmd, "```") || strings.ContainsAny(cmd, "\r\n") {
				list.unsafe++
				continue
			}
			list.items = append(list.items, cmd)
		}
	}
	return list
}

// backtickEscape is the JSON escape of a backtick.
const backtickEscape = "\\u00" + "60"

// agentTask fixes the eight keys of the agent task block; none is ever omitted.
type agentTask struct {
	FindingID   string   `json:"finding_id"`
	RuleID      string   `json:"rule_id"`
	RepairKind  string   `json:"repair_kind"`
	Goal        string   `json:"goal"`
	Constraints []string `json:"constraints"`
	Files       []string `json:"files"`
	Validation  []string `json:"validation"`
	Origin      string   `json:"origin"`
}

// encodeTasks keeps every value as decoded. encoding/json escapes "<", ">" and "&"; backticks are
// escaped on top, so no value can close the code fence or forge a marker.
func encodeTasks(tasks []report.AgentTask) []string {
	list := func(s []string) []string {
		if s == nil {
			return []string{}
		}
		return s
	}
	out := make([]string, 0, len(tasks))
	for _, t := range tasks {
		raw, err := json.Marshal(agentTask{
			FindingID: t.FindingID, RuleID: t.RuleID, RepairKind: t.RepairKind, Goal: t.Goal,
			Constraints: list(t.Constraints), Files: list(t.Files), Validation: list(t.Validation), Origin: t.Origin,
		})
		if err != nil { // unreachable: strings and string slices only
			continue
		}
		out = append(out, strings.ReplaceAll(string(raw), "`", backtickEscape))
	}
	return out
}

// annotations are for archfit/state only, and only from locations the engine reported.
func annotations(p Publication) []api.Annotation {
	if p.Report == nil || suspended(p.Outcome) {
		return nil
	}
	var out []api.Annotation
	for _, wantBlocking := range []bool{true, false} {
		for _, f := range p.Report.Findings {
			if blocking(f) != wantBlocking {
				continue
			}
			level := "warning"
			if wantBlocking {
				level = "failure"
			}
			for _, loc := range f.Locations {
				if len(out) == annotationCap {
					return out
				}
				out = append(out, api.Annotation{Path: loc.File, Line: max(loc.Line, 1), Level: level, Message: sanitize(f.RuleID) + ": " + sanitize(f.Why)})
			}
		}
	}
	return out
}

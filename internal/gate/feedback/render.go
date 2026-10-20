package feedback

import (
	"encoding/json"
	"fmt"
	"slices"
	"strconv"
	"strings"
	"time"

	"github.com/alexei-led/archfit-app/internal/gate/inputs"
	"github.com/alexei-led/archfit-app/internal/gate/verdict"
	"github.com/alexei-led/archfit-app/internal/github/api"
	"github.com/alexei-led/archfit-app/internal/wire/manifest"
	"github.com/alexei-led/archfit-app/internal/wire/report"
	"github.com/alexei-led/archfit-app/internal/wire/settings"
)

const (
	headBlocking   = "### Blocking findings"
	headDecision   = "### Decision"
	headFindings   = "### Findings"
	headDimensions = "### Dimensions"
	headGaps       = "### Evidence gaps"
	headInputs     = "### Inputs and identity"
	headCommands   = "### Validation commands"
	headTasks      = "### Agent tasks"

	fixedTrustSentence = "Measurement is customer-attested: the producer is authenticated, the report content is not independently checked."
	noActiveBlocker    = "- No active gate finding is listed. The engine blocked on a hard gate without one, for example a metric ratchet; read the archfit/state check."
	advisoryNote       = "This check does not block merging: the gate is advisory for this repository."
)

// Display caps. Each capped list ends with "and N more".
const (
	reasonCap   = 50
	groupCap    = report.ShownPerGroup
	locationCap = 10
	gapCap      = 50
	metricCap   = 10
	blockerCap  = 20 // findings named in the gate summary
)

// Annotation caps: GitHub takes at most 50 annotations in one check-run request; more go in further updates.
const (
	annotationsPerRequest = 50
	annotationCap         = 150 // in total, so one publication makes at most three requests per check
)

// Engine finding vocabulary that feedback reads for display; wire/report owns it.
const (
	statusBaseline = "baseline"
	statusWaived   = "waived"
	statusFixed    = "fixed"
	statusNew      = "new"
	statusExpired  = "expired_waiver"

	verdictHealthy = "healthy"
	verdictBlocked = "blocked"

	toolOK       = "ok"
	toolAbsent   = "absent"
	toolDisabled = "disabled"
)

// shortIDLen is the length of a finding ID prefix, as the engine prints it.
const shortIDLen = 8

// dimensionOrder is the engine's fixed order of its nine dimension keys.
var dimensionOrder = [...]string{
	"intent", "structure", "modularity", "coupling", "change_locality", "complexity", "testability", "operations", "drift",
}

func verdictLine(c verdict.Conclusion) string {
	switch c {
	case verdict.Success:
		return "Passed"
	case verdict.Failure:
		return "Architecture gate failing"
	default:
		return "Action required"
	}
}

func gateTitle(p Publication) string { return headline(p) }

// headline states the effect on merge. It reads the gate mode and the conclusion, never the verdict alone: in
// advisory mode a blocked verdict publishes a neutral check, so "blocked" would be false.
func headline(p Publication) string {
	c := p.Outcome.Conclusion
	if c == verdict.ActionRequired || suspended(p.Outcome) || p.Report == nil {
		return verdictLine(c)
	}
	var h string
	switch {
	case required(p) && c == verdict.Failure:
		h = "Architecture gate failing"
	case required(p) && p.Report.Verdict == verdictHealthy:
		h = "Architecture gate passed"
	case required(p):
		h = "Architecture gate passed · attention needed"
	case c == verdict.Failure || p.Report.Verdict == verdictBlocked: // hard_gates fail blocks whatever the verdict says
		h = "Architecture violations found"
	case p.Report.Verdict == verdictHealthy:
		h = "Architecture healthy"
	default:
		h = "Architecture needs attention"
	}
	if advisory(p) {
		h += " · advisory"
	}
	if len(p.Trusted.BaselineDigests) == 0 {
		h += " · no baseline"
	}
	return h
}

// required reports whether this publication writes archfit/gate; advisory, whether it is a pull request whose
// feedback never blocks. A push is neither: there is no merge to block.
func required(p Publication) bool {
	return p.Target.PullRequest != 0 && p.Trusted.SettingsFound && p.Trusted.Settings.Gate == settings.GateRequired
}

func advisory(p Publication) bool {
	return p.Target.PullRequest != 0 && p.Trusted.SettingsFound && p.Trusted.Settings.Gate != settings.GateRequired
}

func stateTitle(p Publication) string {
	switch {
	case suspended(p.Outcome):
		return verdictLine(p.Outcome.Conclusion)
	case p.Report == nil:
		return "Architecture state: no usable report"
	default:
		return headline(p)
	}
}

// suspended reports whether paid output is withheld: only the reason and the recovery stay.
func suspended(o verdict.Outcome) bool {
	return len(o.Reasons) > 0 && o.Reasons[0].Category == verdict.CategorySubscription
}

func renderGate(p Publication) string {
	l := layout{head: headBlock(p, false), findings: blockersBlock(p), inputs: inputsBlock(p), receipt: receiptMarker(p.Receipt)}
	return l.assemble(maxBody)
}

// blockersBlock names, in the gate summary, the findings shown as blocking (Finding.Active): rule, module pair,
// first location, finding ID prefix and the goal of its agent task, in report order. Display only, like block 5.
// It is written only when the outcome holds blocking_findings: otherwise nothing blocks on architecture grounds.
func blockersBlock(p Publication) string {
	if p.Report == nil || suspended(p.Outcome) || !slices.ContainsFunc(p.Outcome.Reasons, func(r verdict.Reason) bool {
		return r.Code == verdict.ReasonBlockingFindings
	}) {
		return ""
	}
	var active []report.Finding
	for _, f := range p.Report.Findings {
		if f.Active() {
			active = append(active, f)
		}
	}
	if len(active) == 0 {
		return headBlocking + "\n\n" + noActiveBlocker
	}
	goals := map[string]string{}
	for _, t := range p.Report.AgentTasks {
		if _, seen := goals[t.FindingID]; !seen {
			goals[t.FindingID] = t.Goal
		}
	}
	var b strings.Builder
	b.WriteString(headBlocking + "\n")
	for i, f := range active {
		if i == blockerCap {
			fmt.Fprintf(&b, "\n- and %d more blocking findings", len(active)-blockerCap)
			break
		}
		where := ", no location reported"
		if len(f.Locations) > 0 {
			where = " at " + location(f.Locations[0])
		}
		goal := "none reported"
		if g, ok := goals[f.ID]; ok {
			goal = text(g)
		}
		fmt.Fprintf(&b, "\n- %s: %s → %s%s, finding %s. Goal: %s",
			code(f.RuleID), module(f.Edge.From), module(f.Edge.To), where, code(shortID(f.ID)), goal)
	}
	return b.String()
}

func module(e report.Endpoint) string {
	if e.Module == "" {
		return "(no module)"
	}
	return code(e.Module)
}

// location is file:line, or the file alone for a whole-file location.
func location(loc report.Location) string {
	if loc.Line > 0 {
		return code(loc.File + ":" + strconv.Itoa(loc.Line))
	}
	return code(loc.File)
}

// shortID is the finding ID prefix the engine prints and `archfit explain` accepts.
func shortID(id string) string {
	if r := []rune(id); len(r) > shortIDLen {
		return string(r[:shortIDLen])
	}
	return id
}

func renderState(p Publication) string {
	l := layout{head: headBlock(p, false), inputs: inputsBlock(p), receipt: receiptMarker(p.Receipt)}
	if !suspended(p.Outcome) {
		l.findings, l.dims, l.gaps = findingsBlock(p.Report, false), dimensionsBlock(p.Report), gapsBlock(p)
	}
	return l.assemble(maxBody)
}

// renderComment renders the whole comment. waiver is the date of the notice section to carry, or "".
// link is the App's page of this pull request, or "" for none.
func renderComment(p Publication, now time.Time, waiver, link string) string {
	l := layout{head: headBlock(p, true), decision: decisionBlock(p), inputs: inputsBlock(p)}
	if notice := noticeLine(p.Trusted.Entitlement, now); notice != "" {
		l.head += "\n\n" + notice
	}
	if link != "" { // after the notice, as Layout item 3 orders them
		l.head += "\n\n[Details in Archfit](" + link + ")"
	}
	if !suspended(p.Outcome) {
		l.findings, l.dims, l.gaps = findingsBlock(p.Report, true), dimensionsBlock(p.Report), gapsBlock(p)
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

// headBlock is blocks 1 and 2. In a check summary (markers false): the advisory note, the deciding reason, then
// further reasons. In the comment (markers true): markers, the headline as a heading, the advisory alert, then
// commentHead, which writes the next steps without reason codes; decisionBlock carries those into the evidence fold.
func headBlock(p Publication, markers bool) string {
	// The comment carries the headline as its heading. A check summary does not: GitHub prints the check title, which
	// is the headline, right above it.
	var parts []string
	if markers {
		parts = append(parts, commentMarker+"\n"+headMarker(p.Target.HeadSHA, p.Receipt)+"\n## Archfit: "+headline(p))
	}
	if advisory(p) && p.Outcome.Conclusion != verdict.ActionRequired && !suspended(p.Outcome) {
		if markers { // a GitHub alert in the comment; a check summary keeps plain text
			parts = append(parts, "> [!NOTE]\n> "+advisoryNote)
		} else {
			parts = append(parts, advisoryNote)
		}
	}
	reasons := p.Outcome.Reasons
	if markers {
		return commentHead(p, parts)
	}
	var b strings.Builder
	b.WriteString(strings.Join(parts, "\n\n"))
	if len(reasons) == 0 {
		return b.String()
	}
	if b.Len() > 0 {
		b.WriteString("\n\n")
	}
	first := reasons[0]
	if first.Next != "" { // a healthy outcome has no next step; "(none)" in bold would lead the comment
		fmt.Fprintf(&b, "- **Next:** %s\n", text(first.Next))
	}
	fmt.Fprintf(&b, "- Reason: %s (%s)", code(string(first.Code)), text(string(first.Category)))
	if first.Detail != "" {
		fmt.Fprintf(&b, "\n- Detail: %s", text(first.Detail))
	}
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

// commentHead is the head of the comment, the part a person reads: what the pull request did, then the next steps.
// Reason codes, categories and details stay in the evidence fold (decisionBlock): they are for tools and support,
// and a detail such as "1 blocking finding" counts gate findings, not the violations the comment lists.
func commentHead(p Publication, parts []string) string {
	if !suspended(p.Outcome) {
		if s := leadSentence(p.Report); s != "" {
			parts = append(parts, s)
		}
	}
	var next []string
	for _, r := range p.Outcome.Reasons {
		if s := text(r.Next); r.Next != "" && !slices.Contains(next, s) {
			next = append(next, s)
		}
	}
	switch {
	case len(next) == 1:
		parts = append(parts, "**Next:** "+next[0])
	case len(next) > 1:
		if len(next) > reasonCap+1 {
			next = append(next[:reasonCap+1:reasonCap+1], fmt.Sprintf("and %d more", len(next)-reasonCap-1))
		}
		parts = append(parts, "**Next:**\n- "+strings.Join(next, "\n- "))
	}
	return strings.Join(parts, "\n\n")
}

// decisionBlock lists every reason of the outcome with its code, category, detail and next step. In the comment it
// sits in the evidence fold; a check summary shows the reasons in its head instead.
func decisionBlock(p Publication) string {
	if len(p.Outcome.Reasons) == 0 {
		return ""
	}
	lines := []string{headDecision, ""}
	for i, r := range p.Outcome.Reasons {
		if i == reasonCap+1 {
			lines = append(lines, fmt.Sprintf("- and %d more", len(p.Outcome.Reasons)-reasonCap-1))
			break
		}
		line := fmt.Sprintf("- %s (%s)", code(string(r.Code)), text(string(r.Category)))
		if r.Detail != "" {
			line += ": " + text(r.Detail)
		}
		if r.Next != "" {
			line += ". Next: " + text(r.Next)
		}
		lines = append(lines, line)
	}
	return strings.Join(lines, "\n")
}

func noticeLine(e inputs.Entitlement, now time.Time) string {
	if e.WarnFrom.IsZero() || now.Before(e.WarnFrom) || sanitize(e.Notice) == "" {
		return ""
	}
	return "> " + sanitize(e.Notice)
}

func dimensionsBlock(r *report.Report) string {
	if r == nil {
		return ""
	}
	// A delta without metrics changes nothing to show: its status alone ("comparable") is not a change.
	changed := func(d report.Dimension) bool { return d.Delta != nil && len(d.Delta.Metrics) > 0 }
	changes := slices.ContainsFunc(dimensionOrder[:], func(key string) bool { return changed(r.Dimensions[key]) })
	head := "| Dimension | Status | Coverage | Confidence | Gate |"
	sep := "| --- | --- | --- | --- | --- |"
	if changes {
		head, sep = head+" Change |", sep+" --- |"
	}
	var b strings.Builder
	b.WriteString(headDimensions + "\n\n" + head + "\n" + sep)
	for _, key := range dimensionOrder {
		d, ok := r.Dimensions[key]
		if !ok {
			row := "| `" + key + "` | not reported | | | |"
			if changes {
				row += " |"
			}
			b.WriteString("\n" + row)
			continue
		}
		row := []string{"`" + key + "`", text(d.Status), fmt.Sprintf("%d/%d%s", d.Coverage.Observed, d.Coverage.Total, parens(d.Coverage.Basis)),
			text(d.Confidence), gateCell(text(d.Gate))}
		if changes {
			change := ""
			if changed(d) {
				change = deltaText(*d.Delta)
			}
			row = append(row, change)
		}
		for i := range row {
			row[i] = cell(row[i])
		}
		b.WriteString("\n| " + strings.Join(row, " | ") + " |")
	}
	return b.String()
}

// gateCell makes a dimension that fails or warns stand out in the table.
func gateCell(g string) string {
	if g == "fail" || g == "warn" {
		return "**" + g + "**"
	}
	return g
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

	var rules, tools, idle, facts, unknowns, producers []string
	for _, u := range r.Decision.UnevaluatedRequiredRules {
		rules = append(rules, fmt.Sprintf("- Required rule %s was not evaluated: %s", code(u.RuleID), text(u.Reason)))
	}
	reported := map[[2]string]bool{} // (tool, status) of every coverage row
	var notRun []string
	for _, t := range r.Coverage.Tools {
		reported[[2]string{t.Tool, t.Status}] = true
		switch {
		case t.Status == toolOK:
		case t.Status == toolDisabled || t.Status == toolAbsent && t.Reason == "":
			// Switched off, or nothing to run on or with: one line names them all. What their absence leaves
			// unmeasured stays visible as dimension unknowns and unevaluated rules.
			notRun = append(notRun, code(t.Tool))
		default:
			tools = append(tools, fmt.Sprintf("- Tool %s: %s%s", code(t.Tool), text(t.Status), parens(t.Reason)))
		}
	}
	if len(notRun) > 0 {
		idle = []string{"- Analyzers that did not run (absent or disabled): " + strings.Join(notRun, ", ")}
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
			if pr.Status == toolOK || reported[[2]string{pr.Tool, pr.Status}] {
				continue // a producer row that repeats its coverage row adds nothing
			}
			producers = append(producers, fmt.Sprintf("- Producer %s: %s%s", code(pr.Tool), text(pr.Status), parens(pr.PartialBasis)))
		}
	}
	for _, source := range [][]string{rules, tools, idle, facts, unknowns, producers} {
		add(source)
	}
	trusted := len(p.Trusted.BaselineDigests) > 0
	switch ref := r.GateReference; {
	case !trusted && !verdict.ReferenceConflicts(ref, false):
		lines = append(lines, "- No baseline: every finding counts as new, including older debt. New does not mean added by this pull request.")
	case !verdict.ReferenceConflicts(ref, trusted): // the same rule as verdict step 7a
	case ref == nil:
		lines = append(lines, "- Baseline comparison: the report has no gate reference")
	case ref.Status == "comparable" && trusted:
		lines = append(lines, "- Baseline comparison: the engine did not load the trusted baseline")
	case ref.Status == "comparable":
		lines = append(lines, "- Baseline comparison: the engine loaded a baseline that the App does not trust")
	default:
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
		lines = append(lines, "- Baseline digest: "+code(p.Receipt.BaselineDigest)+digestSource(p, verdict.ReasonBaselineMismatch))
	} else {
		lines = append(lines, "- Baseline digest: no baseline")
	}
	if p.Receipt.LabelsDigest != "" {
		lines = append(lines, "- Labels digest: "+code(p.Receipt.LabelsDigest)+digestSource(p, verdict.ReasonLabelsMismatch))
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

// digestSource labels a receipt digest. The receipt does not carry its source, but the publication implies it:
// without an upload the App wrote the trusted digest, and gate writes the envelope's claim only when verdict
// reported no mismatch for it.
func digestSource(p Publication, mismatch verdict.ReasonCode) string {
	if p.Identity == (manifest.Identity{}) {
		return " (from the protected ref; no upload)"
	}
	for _, r := range p.Outcome.Reasons {
		if r.Code == mismatch {
			return " (from the protected ref)"
		}
	}
	return " (stated by the producer)"
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

// annotations are for archfit/state only, and only from locations the engine reported in files changed reports
// true for. Live findings only: accepted debt and fixed findings are not annotated. Blocking findings come first,
// then report order; at most annotationCap.
func annotations(p Publication, changed func(path string) bool) []api.Annotation {
	if p.Report == nil || suspended(p.Outcome) {
		return nil
	}
	var out []api.Annotation
	for _, wantBlocking := range []bool{true, false} {
		for _, f := range p.Report.Findings {
			if f.Active() != wantBlocking || !live(f) {
				continue
			}
			level := "warning"
			if wantBlocking {
				level = "failure"
			}
			for _, loc := range f.Locations {
				if !changed(loc.File) {
					continue
				}
				if len(out) == annotationCap {
					return out
				}
				out = append(out, api.Annotation{Path: loc.File, Line: max(loc.Line, 1), Level: level, Message: sanitize(f.RuleID) + ": " + sanitize(report.ReadableWhy(f.Why))})
			}
		}
	}
	return out
}

// live is false for accepted debt (baseline, waived) and fixed findings. A status this version does not know is
// live, as in block 5: a finding is never hidden for an unknown status.
func live(f report.Finding) bool {
	switch f.Status {
	case statusBaseline, statusWaived, statusFixed:
		return false
	}
	return true
}

func anyPath(string) bool { return true }

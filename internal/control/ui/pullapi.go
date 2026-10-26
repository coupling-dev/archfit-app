package ui

import (
	"cmp"
	"encoding/json"
	"net/http"
	"slices"
	"strconv"
	"strings"

	"github.com/alexei-led/archfit-app/internal/github/api"
	"github.com/alexei-led/archfit-app/internal/wire/report"
)

// pullJSON is the pull request page: the header, then the report of the run the head's archfit/state check names, in
// the groups and words of the PR comment. Engine strings are data; the web app renders them as text only.
type pullJSON struct {
	pullHead
	findingsJSON
}

// pullHead is the header the pull request page and its graph share: which pull request, and what its check says.
type pullHead struct {
	Number    int    `json:"number"`
	Title     string `json:"title"`
	Author    string `json:"author"`
	State     string `json:"state"` // open, draft, merged or closed
	HeadRef   string `json:"head_ref"`
	BaseRef   string `json:"base_ref"`
	HeadSHA   string `json:"head_sha"` // short
	URL       string `json:"url"`
	ChecksURL string `json:"checks_url"`

	Result      string            `json:"result"` // the archfit/state word, "" before a result
	Tone        string            `json:"tone"`
	Headline    string            `json:"headline"`
	Required    bool              `json:"required"`
	Unavailable string            `json:"unavailable"` // why there is no report to show; "" when there is
	RunID       int64             `json:"run_id"`
	NoBaseline  bool              `json:"no_baseline"`        // "new" means not accepted, not "added by this pull request"
	Compared    bool              `json:"compared"`           // the engine compared against the base branch, so origins mean something
	Decision    *decisionJSON     `json:"decision,omitempty"` // nil without a report
	Check       *checkContextJSON `json:"check,omitempty"`    // recovery context, never report admission
}

// decisionJSON is the engine's decision on the report, as it states it: its hard gates, how many findings block, the
// required rules it could not evaluate and what it discloses about attribution. The page words it.
type decisionJSON struct {
	Verdict       string                   `json:"verdict"`    // the engine's verdict in words: "blocked", "needs attention"
	Detail        string                   `json:"detail"`     // the rest of the comment's line: hard gates, blockers, rules
	HardGates     string                   `json:"hard_gates"` // pass, fail or unmeasured
	Blockers      int                      `json:"blockers"`   // decision.active_blockers
	Unevaluated   []report.UnevaluatedRule `json:"unevaluated"`
	OriginReasons []string                 `json:"origin_reasons"`
}

// findingsJSON is a report's findings in the comment's groups, its evidence and its agent tasks.
type findingsJSON struct {
	ViolationCount      int           `json:"violation_count"` // findings, a cycle's directions each counted
	Cycles              []cycleJSON   `json:"cycles"`
	Other               []findingJSON `json:"other"` // the shown violations on no cycle
	AdvisoryCount       int           `json:"advisory_count"`
	AdvisoriesElsewhere bool          `json:"advisories_elsewhere"` // some advisory joined a violation's row: the rest are on other dependencies
	Advisories          []findingJSON `json:"advisories"`
	AcceptedCount       int           `json:"accepted_count"`
	Accepted            []findingJSON `json:"accepted"`
	FixedCount          int           `json:"fixed_count"`
	Fixed               []findingJSON `json:"fixed"`   // known issues the report no longer finds, as the comment's last group
	Omitted             int           `json:"omitted"` // findings past findingsPerList, all groups together

	Dimensions []dimensionJSON `json:"dimensions"`
	Unknowns   []unknownRow    `json:"unknowns"`
	Tasks      string          `json:"tasks"` // the exact agent_tasks array, indented
	TaskCount  int             `json:"task_count"`
}

// findingJSON is one finding, or one direction of a cycle.
type findingJSON struct {
	Rule         string   `json:"rule"` // the readable name of the rule
	RuleID       string   `json:"rule_id"`
	Meta         string   `json:"meta"`      // severity, then the status when the group does not say it
	Violation    bool     `json:"violation"` // an active gate finding; a status this version does not know is not
	From         string   `json:"from"`      // the module, else the path
	To           string   `json:"to"`
	Origin       string   `json:"origin"` // finding identity: introduced/pre-existing encoded as PairAdded/PairExisting; no pair/site novelty
	Why          string   `json:"why"`
	Fix          string   `json:"fix"`
	Alternatives []string `json:"alternatives"`
	Locations    []string `json:"locations"` // "file:line", or "file" for a whole file
	// LocationsTotal is the engine's full location count (report.LocationsTotal); Locations holds at most 50 of them.
	LocationsTotal int    `json:"locations_total"`
	Strength       string `json:"strength"` // of the seam on this module pair, "" without one
	Distance       string `json:"distance"`
	Volatility     string `json:"volatility"`
	Move           string `json:"move"`   // the engine's balancing move for that seam, as the comment words it; "" for none
	Metric         string `json:"metric"` // a metric ratchet, which has no edge: "metric <name>: before → after (threshold)"
	// OwnerDecision: the engine's agent task for the finding needs the owner's decision (a policy change), not code.
	OwnerDecision bool `json:"owner_decision"`
	// Also: on a violation, the advisories the report has on the same dependency, as the comment words them ("rule ·
	// severity"). They are not listed again among the advisories (report.SplitCovered).
	Also []string `json:"also"`
}

// cycleJSON is one dependency cycle among the shown violations: its modules and a row per direction, with the reason
// once and each distinct repair once, as on the comment.
type cycleJSON struct {
	Modules      []string      `json:"modules"`
	Rule         string        `json:"rule"`
	RuleID       string        `json:"rule_id"`
	Meta         string        `json:"meta"`
	Violation    bool          `json:"violation"` // the directions block; one class per cycle, so all or none
	Why          string        `json:"why"`
	Fixes        []string      `json:"fixes"`
	Alternatives []string      `json:"alternatives"`
	Directions   []findingJSON `json:"directions"`
}

type dimensionJSON struct {
	Key        string `json:"key"`
	Name       string `json:"name"`
	Status     string `json:"status"`
	Observed   int    `json:"observed"`   // coverage: how many of the measurable units the engine measured
	Measurable int    `json:"measurable"` // the engine's coverage total
	Basis      string `json:"basis"`
	Confidence string `json:"confidence"`
	Gate       string `json:"gate"`
}

// unknownRow is one fact the engine could not measure, under "To measure more".
type unknownRow struct {
	Dimension string `json:"dimension"`
	Fact      string `json:"fact"`
	Reason    string `json:"reason"`
}

// dimensionOrder is the engine's nine dimensions with their readable names.
var dimensionOrder = []struct{ key, name string }{
	{"intent", "Intent"}, {"structure", "Structure"}, {"modularity", "Modularity"}, {"coupling", "Coupling"},
	{"change_locality", "Change locality"}, {"complexity", "Complexity"}, {"testability", "Testability"},
	{"operations", "Operations"}, {"drift", "Drift"},
}

const (
	verdictNeedsAttention = "needs_attention" // Report.Verdict
	hardGatesUnmeasured   = "unmeasured"      // Decision.HardGates
	dimensionMeasured     = "measured"
	dimensionPartial      = "partial"
)

// attentionReason says why the engine's verdict is needs_attention, in the order of the engine's own rule: active
// advisories, a required rule it could not evaluate, then every dimension not fully measured. "" for another verdict.
func attentionReason(rep report.Report) string {
	if rep.Verdict != verdictNeedsAttention {
		return ""
	}
	var reasons []string
	g := report.Group(rep, 0)
	// The advisories the page lists: one on a violation's dependency joins that row (report.SplitCovered), as on the
	// comment, so the reason counts the same advisories as the Findings tab.
	if rest, _ := report.SplitCovered(g.Violations, g.Advisories); len(rest) > 0 {
		reasons = append(reasons, plural(len(rest), "advisory", "advisories"))
	}
	if n := len(rep.Decision.UnevaluatedRequiredRules); n > 0 {
		reasons = append(reasons, plural(n, "required rule", "required rules")+" not evaluated")
	} else if rep.Decision.HardGates == hardGatesUnmeasured {
		reasons = append(reasons, "a hard gate could not be measured")
	}
	var partial, unmeasured []string
	for _, d := range dimensionOrder {
		dim, ok := rep.Dimensions[d.key]
		switch {
		case !ok || dim.Status == dimensionMeasured:
		case dim.Status == dimensionPartial:
			partial = append(partial, strings.ToLower(d.name))
		default:
			unmeasured = append(unmeasured, strings.ToLower(d.name))
		}
	}
	if len(partial) > 0 {
		reasons = append(reasons, andList(partial)+" "+plural2(len(partial), "is", "are")+" partially measured")
	}
	if len(unmeasured) > 0 {
		reasons = append(reasons, andList(unmeasured)+" "+plural2(len(unmeasured), "is", "are")+" not measured")
	}
	return strings.Join(reasons, "; ")
}

func plural(n int, one, many string) string {
	return strconv.Itoa(n) + " " + plural2(n, one, many)
}

func plural2(n int, one, many string) string {
	if n == 1 {
		return one
	}
	return many
}

// andList joins words as a sentence does: "a", "a and b", "a, b and c".
func andList(ws []string) string {
	if len(ws) < 2 {
		return strings.Join(ws, "")
	}
	return strings.Join(ws[:len(ws)-1], ", ") + " and " + ws[len(ws)-1]
}

// apiPull is the pull request page: GET /api/repos/{o}/{n}/pulls/{number}.
func (s *server) apiPull(r *http.Request, sc scope) (any, error) {
	number, ok := pullNumber(r)
	if !ok {
		return nil, api.ErrNotFound
	}
	pull, rep, err := s.readPull(r.Context(), sc, number)
	if err != nil {
		return nil, err
	}
	body := pullJSON{pullHead: headOf(sc, pull, rep)}
	if rep != nil {
		fillFindings(&body.findingsJSON, *rep)
	}
	return body, nil
}

// metricSubject names a metric ratchet finding as the PR comment does: the metric, then the engine's numbers when it
// gives both; "" for any other finding.
func metricSubject(f report.Finding) string {
	if !strings.HasPrefix(f.RuleID, report.MetricRulePrefix) {
		return ""
	}
	s := "metric " + cmp.Or(f.MatchedBy[report.MatchedByMetric], strings.TrimPrefix(f.RuleID, report.MetricRulePrefix))
	before, after, threshold := f.MatchedBy[report.MatchedByBefore], f.MatchedBy[report.MatchedByAfter], f.MatchedBy[report.MatchedByThreshold]
	if before == "" || after == "" {
		return s
	}
	s += ": " + before + " → " + after
	if threshold != "" {
		s += " (" + threshold + ")"
	}
	return s
}

// repairOwnerDecision is the engine's agent-task repair kind that leaves the move to the repository's owner.
const repairOwnerDecision = "needs_owner_decision"

// headOf is the header of a pull request and the report its check names; rep is nil when there is none.
func headOf(sc scope, pull pullBody, rep *report.Report) pullHead {
	p := pull.Pull
	h := pullHead{
		Number: p.Number, Title: p.Title, Author: p.Author, State: pullState(p), HeadRef: p.HeadRef, BaseRef: p.BaseRef,
		HeadSHA: p.HeadSHA[:min(len(p.HeadSHA), shortSHA)], URL: p.URL, ChecksURL: pull.CheckURL,
		Headline: pull.Headline, Required: pull.Required, Unavailable: pull.Unavailable, RunID: pull.RunID, Check: pull.Check,
	}
	if pull.Headline != "" || pull.Conclusion != "" {
		h.Result = stateWord(pull.Conclusion, pull.Headline)
		h.Tone = toneOf(h.Result)
	}
	if rep != nil {
		h.NoBaseline = noBaseline(*rep)
		h.Compared = rep.Comparison.OriginStatus == originComparable
		d := rep.Decision
		verdict, detail := report.EngineVerdict(*rep)
		detail += ". " + report.PRImpact(*rep) + " " + report.EvidenceSummary(*rep)
		h.Decision = &decisionJSON{Verdict: verdict, Detail: detail, HardGates: d.HardGates, Blockers: d.ActiveBlockers,
			Unevaluated:   append([]report.UnevaluatedRule{}, d.UnevaluatedRequiredRules...),
			OriginReasons: append([]string{}, rep.Comparison.OriginReasons...)}
	}
	return h
}

// noBaseline: without a baseline every finding counts as new, older debt included.
func noBaseline(rep report.Report) bool {
	return rep.GateReference == nil || !rep.GateReference.BaselinePresent
}

func pullState(p api.Pull) string {
	switch {
	case p.Merged:
		return "merged"
	case p.State == "closed":
		return "closed"
	case p.Draft:
		return "draft"
	}
	return "open"
}

func fillFindings(body *findingsJSON, rep report.Report) {
	// The comment's groups, order and cycle search (report.Group): the page shows the same 50 violations it does.
	g := report.Group(rep, findingsPerList)
	// An advisory on a violation's dependency joins the violation's row, as on the comment; the rest are the advisories.
	advisories, covered := report.SplitCovered(g.Violations, g.Advisories)
	violations, accepted := g.Violations, g.Accepted
	body.ViolationCount, body.AdvisoryCount, body.AcceptedCount = len(violations), len(advisories), len(accepted)
	body.FixedCount = len(g.Fixed)
	body.AdvisoriesElsewhere = len(covered) > 0
	also := func(r findingJSON, f report.Finding) findingJSON {
		for _, a := range covered[[2]string{f.Edge.From.Module, f.Edge.To.Module}] {
			if note := report.RuleName(a.RuleID) + " · " + report.FindingMeta(a); !slices.Contains(r.Also, note) {
				r.Also = append(r.Also, note)
			}
		}
		return r
	}
	ownerDecides := map[string]bool{}
	for _, t := range rep.AgentTasks {
		if t.RepairKind == repairOwnerDecision {
			ownerDecides[t.FindingID] = true
		}
	}
	seams := map[[2]string]report.Seam{}
	for _, sm := range rep.Seams {
		seams[[2]string{sm.FromModule, sm.ToModule}] = sm
	}
	row := func(f report.Finding, origin string) findingJSON {
		out := findingJSON{
			Rule: report.RuleName(f.RuleID), RuleID: f.RuleID, Meta: report.FindingMeta(f), Violation: f.Active(), From: endpointName(f.Edge.From),
			To: endpointName(f.Edge.To), Why: report.ReadableWhy(f.Why), Fix: f.Constraint, Alternatives: f.AllowedAlternatives,
			Locations: make([]string, len(f.Locations)), LocationsTotal: report.LocationsTotal(f), OwnerDecision: ownerDecides[f.ID],
			Metric: metricSubject(f),
		}
		if g.Compared {
			out.Origin = origin
		}
		for i, l := range f.Locations {
			out.Locations[i] = l.File
			if l.Line > 0 {
				out.Locations[i] += ":" + strconv.Itoa(l.Line)
			}
		}
		if sm, ok := seams[[2]string{f.Edge.From.Module, f.Edge.To.Module}]; ok {
			out.Strength, out.Distance, out.Volatility = sm.Strength, distanceWord(sm.Distance), sm.Volatility
			out.Move = report.BalancingMove(sm.Hypothesis)
		}
		return out
	}
	shown := func(fs []report.Finding) []report.Finding {
		body.Omitted += max(0, len(fs)-findingsPerList)
		return fs[:min(len(fs), findingsPerList)]
	}

	// Cycles among the shown violations, as the comment groups them; each direction's origin is report.PairOrigin,
	// the comment's rule, so both say the same about what this pull request added.
	vs := shown(violations)
	comp := report.Cycles(vs)
	var members [][]report.Finding
	for i, c := range comp {
		if c < 0 {
			body.Other = append(body.Other, also(row(vs[i], originWord(vs[i].Origin)), vs[i]))
			continue
		}
		for len(members) <= c {
			members = append(members, nil)
		}
		members[c] = append(members[c], vs[i])
	}
	for _, m := range members {
		cy := cycleJSON{Rule: report.RuleName(m[0].RuleID), RuleID: m[0].RuleID, Meta: report.FindingMeta(m[0]), Violation: m[0].Active()}
		for _, f := range m {
			for _, mod := range []string{f.Edge.From.Module, f.Edge.To.Module} {
				if !slices.Contains(cy.Modules, mod) {
					cy.Modules = append(cy.Modules, mod)
				}
			}
			if f.Constraint != "" && !slices.Contains(cy.Fixes, f.Constraint) {
				cy.Fixes = append(cy.Fixes, f.Constraint)
			}
			for _, a := range f.AllowedAlternatives {
				if !slices.Contains(cy.Alternatives, a) {
					cy.Alternatives = append(cy.Alternatives, a)
				}
			}
			cy.Directions = append(cy.Directions, also(row(f, originWord(f.Origin)), f))
		}
		cy.Why = report.CycleWhy(len(cy.Directions), len(cy.Modules))
		body.Cycles = append(body.Cycles, cy)
	}
	for _, f := range shown(advisories) {
		body.Advisories = append(body.Advisories, row(f, originWord(f.Origin)))
	}
	for _, f := range shown(accepted) {
		body.Accepted = append(body.Accepted, row(f, originWord(f.Origin)))
	}
	for _, f := range shown(g.Fixed) {
		body.Fixed = append(body.Fixed, row(f, ""))
	}

	for _, d := range dimensionOrder {
		dim, ok := rep.Dimensions[d.key]
		if !ok {
			continue
		}
		body.Dimensions = append(body.Dimensions, dimensionJSON{
			Key: d.key, Name: d.name, Status: dim.Status, Observed: dim.Coverage.Observed, Measurable: dim.Coverage.Total,
			Basis: dim.Coverage.Basis, Confidence: dim.Confidence, Gate: dim.Gate,
		})
		for _, u := range dim.Unknown {
			body.Unknowns = append(body.Unknowns, unknownRow{Dimension: d.name, Fact: u.Fact, Reason: u.Reason})
		}
	}
	if len(rep.AgentTasks) > 0 {
		raw, _ := json.MarshalIndent(rep.AgentTasks, "", "  ")
		body.Tasks, body.TaskCount = string(raw), len(rep.AgentTasks)
	}
}

// originWord is a finding's own origin, for a finding on no cycle: added or existing, "" when the report cannot say.
func originWord(o string) string {
	switch o {
	case "introduced":
		return report.PairAdded
	case "pre_existing":
		return report.PairExisting
	}
	return ""
}

func distanceWord(token string) string {
	if w, ok := report.DistanceWords[token]; ok {
		return w
	}
	return token
}

func endpointName(e report.Endpoint) string {
	if e.Module != "" {
		return e.Module
	}
	return e.Path
}

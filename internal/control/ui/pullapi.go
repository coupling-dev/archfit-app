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

	Result      string        `json:"result"` // the archfit/state word, "" before a result
	Tone        string        `json:"tone"`
	Headline    string        `json:"headline"`
	Required    bool          `json:"required"`
	Unavailable string        `json:"unavailable"` // why there is no report to show; "" when there is
	RunID       int64         `json:"run_id"`
	NoBaseline  bool          `json:"no_baseline"`        // "new" means not accepted, not "added by this pull request"
	Compared    bool          `json:"compared"`           // the engine compared against the base branch, so origins mean something
	Decision    *decisionJSON `json:"decision,omitempty"` // nil without a report
}

// decisionJSON is the engine's decision on the report, as it states it: its hard gates, how many findings block, the
// required rules it could not evaluate and what it discloses about attribution. The page words it.
type decisionJSON struct {
	HardGates     string                   `json:"hard_gates"` // pass, fail or unmeasured
	Blockers      int                      `json:"blockers"`   // decision.active_blockers
	Unevaluated   []report.UnevaluatedRule `json:"unevaluated"`
	OriginReasons []string                 `json:"origin_reasons"`
}

// findingsJSON is a report's findings in the comment's groups, its evidence and its agent tasks.
type findingsJSON struct {
	ViolationCount int           `json:"violation_count"` // findings, a cycle's directions each counted
	Cycles         []cycleJSON   `json:"cycles"`
	Other          []findingJSON `json:"other"` // the shown violations on no cycle
	AdvisoryCount  int           `json:"advisory_count"`
	Advisories     []findingJSON `json:"advisories"`
	AcceptedCount  int           `json:"accepted_count"`
	Accepted       []findingJSON `json:"accepted"`
	Omitted        int           `json:"omitted"` // findings past findingsPerList, all groups together

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
	Origin       string   `json:"origin"` // report.PairAdded, report.PairExisting or ""
	Why          string   `json:"why"`
	Fix          string   `json:"fix"`
	Alternatives []string `json:"alternatives"`
	Locations    []string `json:"locations"` // "file:line", or "file" for a whole file
	Strength     string   `json:"strength"`  // of the seam on this module pair, "" without one
	Distance     string   `json:"distance"`
	Volatility   string   `json:"volatility"`
	Move         string   `json:"move"`   // the engine's balancing move for that seam, as the comment words it; "" for none
	Metric       string   `json:"metric"` // a metric ratchet, which has no edge: "metric <name>: before → after (threshold)"
	// OwnerDecision: the engine's agent task for the finding needs the owner's decision (a policy change), not code.
	OwnerDecision bool `json:"owner_decision"`
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
		Headline: pull.Headline, Required: pull.Required, Unavailable: pull.Unavailable, RunID: pull.RunID,
	}
	if pull.Headline != "" || pull.Conclusion != "" {
		h.Result = conclusionWord(pull.Conclusion)
		if h.Result == conclusionWords["neutral"] && rep != nil && isBlocked(*rep) {
			h.Result = stateViolations // an advisory check is neutral, but the engine's verdict is never hidden
		}
		h.Tone = toneOf(h.Result)
	}
	if rep != nil {
		h.NoBaseline = noBaseline(*rep)
		h.Compared = rep.Comparison.OriginStatus == originComparable
		d := rep.Decision
		h.Decision = &decisionJSON{HardGates: d.HardGates, Blockers: d.ActiveBlockers,
			Unevaluated:   append([]report.UnevaluatedRule{}, d.UnevaluatedRequiredRules...),
			OriginReasons: append([]string{}, rep.Comparison.OriginReasons...)}
	}
	return h
}

// isBlocked is the engine's own verdict: violations that block when the gate is required, whatever the mode.
func isBlocked(rep report.Report) bool {
	return rep.Verdict == verdictBlocked || rep.Decision.HardGates == hardGatesFail
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
	violations, advisories, accepted := g.Violations, g.Advisories, g.Accepted
	body.ViolationCount, body.AdvisoryCount, body.AcceptedCount = len(violations), len(advisories), len(accepted)
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
			Rule: ruleName(f.RuleID), RuleID: f.RuleID, Meta: findingMeta(f), Violation: f.Active(), From: endpointName(f.Edge.From),
			To: endpointName(f.Edge.To), Why: report.ReadableWhy(f.Why), Fix: f.Constraint, Alternatives: f.AllowedAlternatives,
			Locations: make([]string, len(f.Locations)), OwnerDecision: ownerDecides[f.ID], Metric: metricSubject(f),
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
			body.Other = append(body.Other, row(vs[i], originWord(vs[i].Origin)))
			continue
		}
		for len(members) <= c {
			members = append(members, nil)
		}
		members[c] = append(members[c], vs[i])
	}
	for _, m := range members {
		cy := cycleJSON{Rule: ruleName(m[0].RuleID), RuleID: m[0].RuleID, Meta: findingMeta(m[0]), Violation: m[0].Active(), Why: report.ReadableWhy(m[0].Why)}
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
			cy.Directions = append(cy.Directions, row(f, report.PairOrigin(f, g.All, g.OnCycle)))
		}
		body.Cycles = append(body.Cycles, cy)
	}
	for _, f := range shown(advisories) {
		body.Advisories = append(body.Advisories, row(f, originWord(f.Origin)))
	}
	for _, f := range shown(accepted) {
		body.Accepted = append(body.Accepted, row(f, originWord(f.Origin)))
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

// findingMeta is the severity, then the status as a word when the group does not already say it, as on the comment.
func findingMeta(f report.Finding) string {
	switch f.Status {
	case "expired_waiver":
		return f.Severity + ", waiver expired"
	case "waived":
		return f.Severity + ", waiver"
	case "new", "fixed":
		return f.Severity
	}
	return f.Severity + ", " + f.Status
}

func ruleName(id string) string {
	if n, ok := report.EngineRuleNames[id]; ok {
		return n
	}
	return id
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

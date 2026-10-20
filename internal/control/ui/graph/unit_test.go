package graph

import (
	"reflect"
	"strconv"
	"strings"
	"testing"

	"github.com/alexei-led/archfit-app/internal/wire/report"
)

func TestNodesFromPolicyModules(t *testing.T) {
	tests := []struct {
		name   string
		policy string
		want   []Node
	}{
		{
			name:   "three modules in two layers",
			policy: threeModules,
			want:   []Node{{Name: "api", Layer: "app", PolicyLine: 3, Volatility: VolatilityUndeclared}, {Name: "db", Layer: "infra", PolicyLine: 6, Volatility: VolatilityUndeclared}, {Name: "ui", Layer: "app", PolicyLine: 9, Volatility: VolatilityUndeclared}},
		},
		{
			name:   "module without a layer",
			policy: "modules:\n  core:\n    paths: [core/**]\n",
			want:   []Node{{Name: "core", Layer: "", PolicyLine: 2, Volatility: VolatilityUndeclared}},
		},
		{
			name:   "quoted names",
			policy: "modules:\n  \"my-mod\":\n    layer: app\n  'other mod':\n    layer: infra\n",
			want:   []Node{{Name: "my-mod", Layer: "app", PolicyLine: 2, Volatility: VolatilityUndeclared}, {Name: "other mod", Layer: "infra", PolicyLine: 4, Volatility: VolatilityUndeclared}},
		},
		{
			name: "comments and anchors around the map",
			policy: "# top comment\n" +
				"shared: &paths\n" +
				"  - pkg/**\n" +
				"modules: # the map\n" +
				"  # first module\n" +
				"  a: &a\n" +
				"    layer: core\n" +
				"    paths: *paths\n" +
				"\n" +
				"  b:\n" +
				"    paths: *paths # reuse\n" +
				"# end\n",
			want: []Node{{Name: "a", Layer: "core", PolicyLine: 6, Volatility: VolatilityUndeclared}, {Name: "b", Layer: "", PolicyLine: 10, Volatility: VolatilityUndeclared}},
		},
		{
			name:   "modules is not the first key",
			policy: "version: 2\nrules: []\nmodules:\n  z:\n    layer: edge\n",
			want:   []Node{{Name: "z", Layer: "edge", PolicyLine: 4, Volatility: VolatilityUndeclared}},
		},
		{name: "no modules key", policy: "version: 2\nrules: []\n", want: []Node{}},
		{name: "empty modules map", policy: "version: 2\nmodules: {}\n", want: []Node{}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := Build(reportFor(tt.policy, nil, nil), []byte(tt.policy))
			if !reflect.DeepEqual(got.Nodes, tt.want) {
				t.Errorf("Nodes = %+v, want %+v", got.Nodes, tt.want)
			}
			if got.Stale {
				t.Error("Stale = true for a valid policy with the report's digest")
			}
		})
	}
}

func TestLinkNormalization(t *testing.T) {
	declared := []Node{{Name: "api", Layer: "app", PolicyLine: 3, Volatility: VolatilityUndeclared}, {Name: "db", Layer: "infra", PolicyLine: 6, Volatility: VolatilityUndeclared}, {Name: "ui", Layer: "app", PolicyLine: 9, Volatility: VolatilityUndeclared}}
	tests := []struct {
		name      string
		seams     []report.Seam
		wantLinks []Link
		wantNodes []Node
	}{
		{
			name:      "single seam",
			seams:     []report.Seam{seam("api", "db", 1)},
			wantLinks: []Link{link("api", "db", 1, StatusObserved)},
			wantNodes: declared,
		},
		{
			name:      "duplicate seams merge",
			seams:     []report.Seam{seam("api", "db", 2), seam("api", "db", 3)},
			wantLinks: []Link{link("api", "db", 5, StatusObserved)},
			wantNodes: declared,
		},
		{
			name:      "self seam is kept",
			seams:     []report.Seam{seam("api", "api", 4)},
			wantLinks: []Link{link("api", "api", 4, StatusObserved)},
			wantNodes: declared,
		},
		{
			name:      "seam to an undeclared module",
			seams:     []report.Seam{seam("api", "cache", 1)},
			wantLinks: []Link{link("api", "cache", 1, StatusObserved)},
			wantNodes: []Node{{Name: "api", Layer: "app", PolicyLine: 3, Volatility: VolatilityUndeclared}, {Name: "cache", Layer: "", PolicyLine: 0}, {Name: "db", Layer: "infra", PolicyLine: 6, Volatility: VolatilityUndeclared}, {Name: "ui", Layer: "app", PolicyLine: 9, Volatility: VolatilityUndeclared}},
		},
		{
			name:      "seam with an empty endpoint is dropped",
			seams:     []report.Seam{seam("", "db", 7), seam("api", "", 7), seam("ui", "db", 1)},
			wantLinks: []Link{link("ui", "db", 1, StatusObserved)},
			wantNodes: declared,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := Build(reportFor(threeModules, tt.seams, nil), []byte(threeModules))
			if !reflect.DeepEqual(got.Links, tt.wantLinks) {
				t.Errorf("Links = %+v, want %+v", got.Links, tt.wantLinks)
			}
			if !reflect.DeepEqual(got.Nodes, tt.wantNodes) {
				t.Errorf("Nodes = %+v, want %+v", got.Nodes, tt.wantNodes)
			}
		})
	}
}

func TestStatusFollowsFindings(t *testing.T) {
	tests := []struct {
		name     string
		findings []report.Finding
		status   LinkStatus
	}{
		{"gate new", []report.Finding{finding("gate", "new", "api", "db")}, StatusViolation},
		{"gate expired_waiver", []report.Finding{finding("gate", "expired_waiver", "api", "db")}, StatusViolation},
		{"gate baseline", []report.Finding{finding("gate", "baseline", "api", "db")}, StatusAccepted},
		{"gate waived", []report.Finding{finding("gate", "waived", "api", "db")}, StatusAccepted},
		{"gate fixed", []report.Finding{finding("gate", "fixed", "api", "db")}, StatusObserved},
		{"advisory new", []report.Finding{finding("advisory", "new", "api", "db")}, StatusAdvisory},
		{"advisory baseline", []report.Finding{finding("advisory", "baseline", "api", "db")}, StatusAccepted}, // report.Group's Accepted
		{"advisory fixed", []report.Finding{finding("advisory", "fixed", "api", "db")}, StatusObserved},
		{"unknown kind", []report.Finding{finding("guard", "new", "api", "db")}, StatusAdvisory},
		// Fail safe, as feedback treats an unknown status as live: a gate finding is never drawn as less than it may be.
		{"gate with an unknown status", []report.Finding{finding("gate", "suppressed", "api", "db")}, StatusViolation},
		{"unknown gate status beats accepted", []report.Finding{
			finding("gate", "baseline", "api", "db"), finding("gate", "suppressed", "api", "db"),
		}, StatusViolation},
		{"gate new in the other direction", []report.Finding{finding("gate", "new", "db", "api")}, StatusObserved},
		{"gate new with an empty To.Module", []report.Finding{finding("gate", "new", "api", "")}, StatusObserved},
		{"no finding", nil, StatusObserved},
		{"violation beats accepted and advisory", []report.Finding{
			finding("advisory", "new", "api", "db"), finding("gate", "baseline", "api", "db"), finding("gate", "new", "api", "db"),
		}, StatusViolation},
		{"accepted beats advisory", []report.Finding{
			finding("advisory", "new", "api", "db"), finding("gate", "waived", "api", "db"),
		}, StatusAccepted},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			for _, f := range tt.findings {
				if onPair := f.Edge.From.Module == "api" && f.Edge.To.Module == "db"; onPair && f.Active() && tt.status != StatusViolation {
					t.Fatalf("the table disagrees with report.Finding.Active(): an active finding is a violation")
				}
			}
			rep := reportFor(threeModules, []report.Seam{seam("api", "db", 1)}, tt.findings)
			got := Build(rep, []byte(threeModules))
			for _, l := range got.Links {
				if l.From == "api" && l.To == "db" {
					if l.Status != tt.status {
						t.Errorf("api -> db Status = %q, want %q", l.Status, tt.status)
					}
					return
				}
			}
			t.Fatalf("no api -> db link in %+v", got.Links)
		})
	}
}

func TestFindingWithoutSeamStillDraws(t *testing.T) {
	rep := reportFor(threeModules, nil, []report.Finding{
		finding("gate", "new", "ui", "db"),
		finding("gate", "baseline", "api", "ui"),
		finding("advisory", "new", "db", "api"),
		finding("gate", "fixed", "db", "ui"),
	})
	got := Build(rep, []byte(threeModules))
	want := []Link{accepted(link("api", "ui", 0, StatusAccepted), AcceptedViolation), link("db", "api", 0, StatusAdvisory), link("ui", "db", 0, StatusViolation)}
	if !reflect.DeepEqual(got.Links, want) {
		t.Errorf("Links = %+v, want %+v", got.Links, want)
	}
}

func TestStatusFollowsEngineSeamPolicy(t *testing.T) {
	withPolicy := func(from, to string, edges int, policy string) report.Seam {
		s := seam(from, to, edges)
		s.Policy = policy
		return s
	}
	tests := []struct {
		name     string
		seams    []report.Seam
		findings []report.Finding
		want     []Link
	}{
		{"violation", []report.Seam{withPolicy("api", "db", 1, "violation")}, nil, []Link{link("api", "db", 1, StatusViolation)}},
		{"accepted", []report.Seam{withPolicy("api", "db", 1, "accepted")}, nil, []Link{accepted(link("api", "db", 1, StatusAccepted), AcceptedViolation)}},
		// The engine's accepted is gate debt: an accepted advisory on the pair never makes it read as an advisory.
		{"engine accepted, an accepted advisory names the pair", []report.Seam{withPolicy("api", "db", 1, "accepted")},
			[]report.Finding{finding("advisory", "baseline", "api", "db")}, []Link{accepted(link("api", "db", 1, StatusAccepted), AcceptedViolation)}},
		{"advisory", []report.Seam{withPolicy("api", "db", 1, "advisory")}, nil, []Link{link("api", "db", 1, StatusAdvisory)}},
		{"allowed", []report.Seam{withPolicy("api", "db", 1, "allowed")}, nil, []Link{link("api", "db", 1, StatusAllowed)}},
		{"observed", []report.Seam{withPolicy("api", "db", 1, "observed")}, nil, []Link{link("api", "db", 1, StatusObserved)}},
		// The engine decides the pair's violations: graph does not overrule it with its own reading of the findings.
		{"engine allowed, a finding names the pair", []report.Seam{withPolicy("api", "db", 1, "allowed")},
			[]report.Finding{finding("gate", "new", "api", "db")}, []Link{link("api", "db", 1, StatusAllowed)}},
		// "observed" says no active finding: accepted debt and advisories still show.
		{"engine observed, a baselined advisory names the pair", []report.Seam{withPolicy("api", "db", 1, "observed")},
			[]report.Finding{finding("advisory", "baseline", "api", "db")}, []Link{accepted(link("api", "db", 1, StatusAccepted), AcceptedAdvisory)}},
		{"engine observed, a new advisory names the pair", []report.Seam{withPolicy("api", "db", 1, "observed")},
			[]report.Finding{finding("advisory", "new", "api", "db")}, []Link{link("api", "db", 1, StatusAdvisory)}},
		{"duplicate seams: the strongest status wins", []report.Seam{
			withPolicy("api", "db", 2, "allowed"), withPolicy("api", "db", 3, "violation"), withPolicy("api", "db", 1, "observed"),
		}, nil, []Link{link("api", "db", 6, StatusViolation)}},
		{"duplicate seams: allowed beats observed", []report.Seam{
			withPolicy("api", "db", 1, "observed"), withPolicy("api", "db", 1, "allowed"),
		}, nil, []Link{link("api", "db", 2, StatusAllowed)}},
		{"a seam without the key keeps the finding status", []report.Seam{seam("api", "db", 1)},
			[]report.Finding{finding("gate", "baseline", "api", "db")}, []Link{accepted(link("api", "db", 1, StatusAccepted), AcceptedViolation)}},
		{"a pair only a finding names keeps the finding status", []report.Seam{withPolicy("api", "db", 1, "allowed")},
			[]report.Finding{finding("gate", "new", "ui", "db")},
			[]Link{link("api", "db", 1, StatusAllowed), link("ui", "db", 0, StatusViolation)}},
		{"a seam with an empty endpoint is dropped", []report.Seam{withPolicy("", "db", 1, "violation")}, nil, []Link{}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := Build(reportFor(threeModules, tt.seams, tt.findings), []byte(threeModules))
			if !reflect.DeepEqual(got.Links, tt.want) {
				t.Errorf("Links = %+v, want %+v", got.Links, tt.want)
			}
		})
	}
}

func TestFindingsExplainDrawnLinks(t *testing.T) {
	why := func(f report.Finding, w string) report.Finding { f.Why = w; return f }
	named := func(f report.Finding, rule, severity string) report.Finding {
		f.RuleID, f.Severity = rule, severity
		return f
	}
	allowed := seam("api", "db", 2)
	allowed.Policy = "allowed"
	long := strings.Repeat("é", whyRunes+10)
	tests := []struct {
		name     string
		seams    []report.Seam
		findings []report.Finding
		want     []LinkFinding
	}{
		{"one finding on a seam", []report.Seam{seam("api", "db", 2)},
			[]report.Finding{why(finding("gate", "new", "api", "db"), "forbidden")},
			[]LinkFinding{{From: "api", To: "db", Kind: "gate", Status: "new", Why: "forbidden"}}},
		{"an engine distance reads in the tables' words", []report.Seam{seam("api", "db", 2)},
			[]report.Finding{why(finding("advisory", "new", "api", "db"), "cross_module distance at high volatility")},
			[]LinkFinding{{From: "api", To: "db", Kind: "advisory", Status: "new", Why: "distance across modules at high volatility"}}},
		{"the rule by its name and meta, as on the pull request page", []report.Seam{seam("api", "db", 2)},
			[]report.Finding{named(finding("advisory", "baseline", "api", "db"), "bc/imbalanced_coupling", "high")},
			[]LinkFinding{{From: "api", To: "db", RuleID: "bc/imbalanced_coupling", Rule: "Imbalanced coupling",
				Meta: "high, accepted in the baseline", Kind: "advisory", Status: "baseline"}}},
		{"a fixed finding explains nothing", []report.Seam{seam("api", "db", 2)},
			[]report.Finding{finding("gate", "fixed", "api", "db")}, []LinkFinding{}},
		{"a finding without a module pair is not on the graph", nil,
			[]report.Finding{finding("gate", "new", "api", "")}, []LinkFinding{}},
		{"an engine seam status keeps its findings", []report.Seam{allowed},
			[]report.Finding{finding("advisory", "new", "api", "db")},
			[]LinkFinding{{From: "api", To: "db", Kind: "advisory", Status: "new"}}},
		{"long engine text is clipped", []report.Seam{seam("api", "db", 2)},
			[]report.Finding{why(finding("advisory", "new", "api", "db"), long)},
			[]LinkFinding{{From: "api", To: "db", Kind: "advisory", Status: "new", Why: strings.Repeat("é", whyRunes-1) + "…"}}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			v := Build(reportFor(threeModules, tt.seams, tt.findings), []byte(threeModules))
			if !reflect.DeepEqual(v.Findings, tt.want) {
				t.Errorf("Findings = %+v, want %+v", v.Findings, tt.want)
			}
		})
	}

	t.Run("at most findingsPerLink per pair, in report order, pairs sorted", func(t *testing.T) {
		var fs []report.Finding
		for i := range findingsPerLink + 2 {
			fs = append(fs, why(finding("advisory", "new", "ui", "db"), strconv.Itoa(i)))
		}
		fs = append(fs, why(finding("gate", "new", "api", "db"), "first pair"))
		got := Build(reportFor(threeModules, nil, fs), []byte(threeModules)).Findings
		if len(got) != findingsPerLink+1 || got[0].From != "api" || got[1].Why != "0" || got[findingsPerLink].Why != strconv.Itoa(findingsPerLink-1) || got[findingsPerLink].More != 2 {
			t.Errorf("Findings = %+v", got)
		}
	})

	t.Run("the cap never drops the finding that set the status", func(t *testing.T) {
		var fs []report.Finding
		for i := range findingsPerLink {
			fs = append(fs, why(finding("advisory", "new", "api", "db"), strconv.Itoa(i)))
		}
		fs = append(fs, why(finding("gate", "new", "api", "db"), "the violation"))
		got := Build(reportFor(threeModules, nil, fs), []byte(threeModules)).Findings
		if len(got) != findingsPerLink || got[0].Why != "the violation" || got[findingsPerLink-1].More != 1 {
			t.Errorf("Findings = %+v, want the gate finding first and More 1", got)
		}
	})
}

func TestLinkOriginIsTheCommentsRule(t *testing.T) {
	origin := func(o, id string, f report.Finding) report.Finding { f.Origin, f.ID = o, id; return f }
	cycle := func(o1, o2 string) []report.Finding {
		return []report.Finding{
			origin(o1, "c1", finding("gate", "new", "api", "db")),
			origin(o2, "c2", finding("gate", "new", "db", "api")),
		}
	}
	// report.ShownPerGroup violations the comment shows, then a new cycle it folds into "and N more"
	var past []report.Finding
	for i := range report.ShownPerGroup {
		past = append(past, origin("pre_existing", "p"+strconv.Itoa(i), finding("gate", "new", "ui", "db")))
	}
	past = append(past, cycle("introduced", "introduced")...)
	// a shown cycle, then past the cap a new finding of another rule on one of its directions: the comment adds it
	shown := cycle("introduced", "introduced")
	for i := range report.ShownPerGroup {
		shown = append(shown, origin("pre_existing", "p"+strconv.Itoa(i), finding("gate", "new", "ui", "db")))
	}
	other := origin("introduced", "f9", finding("gate", "new", "api", "db"))
	other.RuleID = "other-rule"
	shown = append(shown, other)
	tests := []struct {
		name     string
		compared string
		findings []report.Finding
		want     map[string]string // "from>to" -> Link.Origin
	}{
		{"an introduced finding on a comparable run adds the dependency", "comparable",
			[]report.Finding{origin("introduced", "f1", finding("gate", "new", "api", "db"))},
			map[string]string{"api>db": "added"}},
		{"the same finding without a comparison claims nothing", "unknown",
			[]report.Finding{origin("introduced", "f1", finding("gate", "new", "api", "db"))},
			map[string]string{"api>db": ""}},
		{"a run that did not compare has no origin at all", "",
			[]report.Finding{origin("introduced", "f1", finding("gate", "new", "api", "db"))},
			map[string]string{"api>db": ""}},
		{"a pre-existing finding means the base had the dependency", "comparable",
			[]report.Finding{origin("pre_existing", "f1", finding("gate", "new", "api", "db"))},
			map[string]string{"api>db": "existing"}},
		{"a new finding on an old dependency does not make it new", "comparable",
			[]report.Finding{
				origin("pre_existing", "f1", finding("advisory", "new", "api", "db")),
				origin("introduced", "f2", finding("gate", "new", "api", "db")),
			},
			map[string]string{"api>db": "existing"}},
		{"a new cycle cannot tell which direction is new", "comparable", cycle("introduced", "introduced"),
			map[string]string{"api>db": "", "db>api": ""}},
		{"a direction of a new cycle the base had stays existing", "comparable",
			append(cycle("introduced", "introduced"), origin("pre_existing", "f3", finding("advisory", "new", "db", "api"))),
			map[string]string{"api>db": "", "db>api": "existing"}},
		{"a cycle past the shown violations cannot tell either", "comparable", past,
			map[string]string{"api>db": "", "db>api": "", "ui>db": "existing"}},
		{"on a shown cycle's pair a finding past the cap counts as the comment counts it", "comparable", shown,
			map[string]string{"api>db": "added", "db>api": "", "ui>db": "existing"}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			rep := reportFor(threeModules, []report.Seam{seam("api", "db", 2), seam("db", "api", 1)}, tt.findings)
			rep.Comparison.OriginStatus = tt.compared
			v := Build(rep, []byte(threeModules))
			for _, l := range v.Links {
				if want := tt.want[l.From+">"+l.To]; l.Origin != want {
					t.Errorf("%s -> %s Origin = %q, want %q", l.From, l.To, l.Origin, want)
				}
			}
		})
	}
}

func TestOriginNeverChangesTheStatus(t *testing.T) {
	gate := finding("gate", "new", "api", "db")
	gate.Origin = "introduced"
	allowed := seam("api", "db", 2)
	allowed.Policy = "allowed"
	rep := reportFor(threeModules, []report.Seam{allowed}, []report.Finding{gate})
	rep.Comparison.OriginStatus = "comparable"
	v := Build(rep, []byte(threeModules))
	if got := v.Links[0]; got.Status != StatusAllowed || got.Origin != report.PairAdded {
		t.Errorf("link = %+v, want the engine's allowed status and added", got)
	}
}

func TestCouplingComesFromTheStrongestSeam(t *testing.T) {
	coupling := func(from, to string, edges int, strength, distance, volatility, severity string) report.Seam {
		s := seam(from, to, edges)
		s.Strength, s.Distance, s.Volatility, s.Severity = strength, distance, volatility, severity
		return s
	}
	tests := []struct {
		name  string
		seams []report.Seam
		want  Link
	}{
		{"one seam, distance in words", []report.Seam{coupling("api", "db", 3, "model", "cross_module", "high", "")},
			Link{Strength: "model", Distance: "across modules", Volatility: "high"}},
		{"an unknown distance token is shown as written", []report.Seam{coupling("api", "db", 3, "model", "far_away", "", "")},
			Link{Strength: "model", Distance: "far_away"}},
		{"the stronger seam wins, the worst imbalance is kept", []report.Seam{
			coupling("api", "db", 10, "contract", "cross_module", "low", "medium"),
			coupling("api", "db", 2, "functional", "same_module", "high", "low"),
		}, Link{Strength: "functional", Distance: "same module", Volatility: "high", Imbalance: "medium"}},
		{"a known strength beats an unknown one", []report.Seam{
			coupling("api", "db", 9, "telepathic", "", "", ""),
			coupling("api", "db", 1, "contract", "", "", ""),
		}, Link{Strength: "contract"}},
		{"between equal strengths the seam with more dependencies", []report.Seam{
			coupling("api", "db", 1, "model", "same_module", "", ""),
			coupling("api", "db", 4, "model", "cross_module", "", ""),
		}, Link{Strength: "model", Distance: "across modules"}},
		{"symmetric ranks between functional and intrusive, as in the engine", []report.Seam{
			coupling("api", "db", 9, "functional", "", "", ""),
			coupling("api", "db", 1, "symmetric", "", "", ""),
		}, Link{Strength: "symmetric"}},
		{"intrusive beats symmetric", []report.Seam{
			coupling("api", "db", 1, "intrusive", "", "", ""),
			coupling("api", "db", 9, "symmetric", "", "", ""),
		}, Link{Strength: "intrusive"}},
		{"the engine's unknown ranks below contract", []report.Seam{
			coupling("api", "db", 9, "unknown", "", "", ""),
			coupling("api", "db", 1, "contract", "", "", ""),
		}, Link{Strength: "contract"}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			v := Build(reportFor(threeModules, tt.seams, nil), []byte(threeModules))
			got := v.Links[0]
			got.From, got.To, got.Status, got.Count = "", "", "", 0
			if got != tt.want {
				t.Errorf("coupling = %+v, want %+v", got, tt.want)
			}
		})
	}

	t.Run("a pair only a finding names has no coupling", func(t *testing.T) {
		v := Build(reportFor(threeModules, nil, []report.Finding{finding("gate", "new", "api", "db")}), []byte(threeModules))
		if l := v.Links[0]; l.Strength != "" || l.Distance != "" || l.Volatility != "" || l.Imbalance != "" {
			t.Errorf("link = %+v, want no coupling", l)
		}
	})
}

func TestNodeShowsWhatTheReportsPolicyDeclares(t *testing.T) {
	const declared = `version: 2
modules:
  chaos:
    volatility: high
  util:
    volatility: low
  domain:
    subdomain: core
  plain:
    paths: [plain/**]
`
	// A functional dependency takes the worse end's volatility: the seam into util is high, util stays low.
	functional := seam("chaos", "util", 2)
	functional.Strength, functional.Volatility, functional.VolatilityProvenance = "functional", "high", "declared"
	rep := reportFor(declared, []report.Seam{functional, seam("plain", "cache", 1)}, nil)
	type facts struct{ volatility, subdomain string }
	nodes := func(v View) map[string]facts {
		got := map[string]facts{}
		for _, n := range v.Nodes {
			got[n.Name] = facts{n.Volatility, n.Subdomain}
		}
		return got
	}

	want := map[string]facts{
		"chaos": {"high", ""}, "util": {"low", ""}, "domain": {"", "core"}, "plain": {VolatilityUndeclared, ""},
		"cache": {}, // not in the policy: the node claims nothing
	}
	if got := nodes(Build(rep, []byte(declared))); !reflect.DeepEqual(got, want) {
		t.Errorf("nodes = %v, want %v", got, want)
	}

	edited := strings.Replace(declared, "volatility: low", "volatility: medium", 1)
	v := Build(rep, []byte(edited))
	for name, f := range nodes(v) {
		if f != (facts{}) {
			t.Errorf("%s = %+v from a policy that is not the report's, want nothing", name, f)
		}
	}
	if !v.Stale {
		t.Error("an edited policy is not marked stale")
	}

	for _, l := range Build(rep, []byte(declared)).Links {
		if l.From == "chaos" && (l.Volatility != "high" || l.VolatilitySource != "declared") {
			t.Errorf("chaos -> util = %q from %q, want the seam's high from declared", l.Volatility, l.VolatilitySource)
		}
	}
}

func TestAcceptedNamesWhatItAccepts(t *testing.T) {
	observed := seam("api", "db", 1)
	observed.Policy = "observed"
	tests := []struct {
		name     string
		findings []report.Finding
		want     string
	}{
		{"an accepted advisory", []report.Finding{finding("advisory", "baseline", "api", "db")}, AcceptedAdvisory},
		{"a waived advisory", []report.Finding{finding("advisory", "waived", "api", "db")}, AcceptedAdvisory},
		{"an accepted violation", []report.Finding{finding("gate", "baseline", "api", "db")}, AcceptedViolation},
		{"gate debt outweighs an advisory, in either order", []report.Finding{
			finding("gate", "waived", "api", "db"), finding("advisory", "baseline", "api", "db"),
		}, AcceptedViolation},
		{"nothing accepted", []report.Finding{finding("advisory", "new", "api", "db")}, ""},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			v := Build(reportFor(threeModules, []report.Seam{observed}, tt.findings), []byte(threeModules))
			if len(v.Links) != 1 || v.Links[0].Accepted != tt.want {
				t.Errorf("Links = %+v, want one accepting %q", v.Links, tt.want)
			}
		})
	}
}

// The overview counts report.Group's Accepted; the graph shows each of those findings' pairs as accepted, also where
// the engine's seam policy is "observed" (no active finding) or "allowed", unless a violation on the pair outranks it.
func TestGraphAcceptsWhatTheOverviewCounts(t *testing.T) {
	withPolicy := func(from, to, policy string) report.Seam {
		s := seam(from, to, 1)
		s.Policy = policy
		return s
	}
	findings := []report.Finding{
		finding("advisory", "baseline", "api", "db"), finding("advisory", "waived", "ui", "db"),
		finding("gate", "baseline", "ui", "api"), finding("advisory", "new", "db", "ui"),
	}
	rep := reportFor(threeModules, []report.Seam{
		withPolicy("api", "db", "observed"), withPolicy("ui", "db", "allowed"), withPolicy("ui", "api", "observed"),
	}, findings)
	accepted := map[pair]bool{}
	for _, f := range report.Group(rep, report.ShownPerGroup).Accepted {
		accepted[pair{f.Edge.From.Module, f.Edge.To.Module}] = true
	}
	shown := map[pair]bool{}
	for _, l := range Build(rep, []byte(threeModules)).Links {
		if l.Status == StatusAccepted {
			shown[pair{l.From, l.To}] = true
		}
	}
	if len(accepted) != 3 || !reflect.DeepEqual(shown, accepted) {
		t.Errorf("accepted links %v, want report.Group's %v", shown, accepted)
	}

	// A violation on the same pair outranks its accepted debt.
	rep.Findings = append(rep.Findings, finding("gate", "new", "api", "db"))
	rep.Seams[0].Policy = "violation"
	for _, l := range Build(rep, []byte(threeModules)).Links {
		if l.From == "api" && l.To == "db" && (l.Status != StatusViolation || l.Accepted != "") {
			t.Errorf("api -> db = %q accepting %q, want the violation over its accepted advisory, accepting nothing", l.Status, l.Accepted)
		}
	}
}

func TestBalanceReadingComesFromTheStrongestSeam(t *testing.T) {
	reading := func(edges int, strength, hypothesis, quadrant, basis string, dm bool) report.Seam {
		s := seam("api", "db", edges)
		s.Strength, s.Hypothesis, s.Quadrant, s.DistributedMonolith = strength, hypothesis, quadrant, dm
		s.RawDistance.Basis = basis
		return s
	}
	tests := []struct {
		name  string
		seams []report.Seam
		want  Link
	}{
		{"the strongest seam's reading, basis split", []report.Seam{
			reading(1, "functional", "move_functionality", "tight", "module_boundary@system", true),
			reading(9, "contract", "balanced", "loose", "ownership@core", false),
		}, Link{Strength: "functional", Move: report.BalancingMoves["move_functionality"], Quadrant: "tight", Crossing: "module_boundary",
			Container: "system", DistributedMonolith: true}},
		{"the flag from any seam of the pair, a move this version does not know as written", []report.Seam{
			reading(1, "intrusive", "introduce_contract_v2", "tight", "deploy_unit@system", false),
			reading(9, "functional", "", "", "", true),
		}, Link{Strength: "intrusive", Move: "introduce_contract_v2", Quadrant: "tight", Crossing: "deploy_unit",
			Container: "system", DistributedMonolith: true}},
		{"no reading", []report.Seam{reading(1, "model", "", "", "", false)}, Link{Strength: "model"}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := Build(reportFor(threeModules, tt.seams, nil), []byte(threeModules)).Links[0]
			got.From, got.To, got.Status, got.Count = "", "", "", 0
			if got != tt.want {
				t.Errorf("link = %+v, want %+v", got, tt.want)
			}
		})
	}
}

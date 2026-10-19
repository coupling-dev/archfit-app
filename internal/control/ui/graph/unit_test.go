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
			want:   []Node{{"api", "app", 3, ""}, {"db", "infra", 6, ""}, {"ui", "app", 9, ""}},
		},
		{
			name:   "module without a layer",
			policy: "modules:\n  core:\n    paths: [core/**]\n",
			want:   []Node{{"core", "", 2, ""}},
		},
		{
			name:   "quoted names",
			policy: "modules:\n  \"my-mod\":\n    layer: app\n  'other mod':\n    layer: infra\n",
			want:   []Node{{"my-mod", "app", 2, ""}, {"other mod", "infra", 4, ""}},
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
			want: []Node{{"a", "core", 6, ""}, {"b", "", 10, ""}},
		},
		{
			name:   "modules is not the first key",
			policy: "version: 2\nrules: []\nmodules:\n  z:\n    layer: edge\n",
			want:   []Node{{"z", "edge", 4, ""}},
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
	declared := []Node{{"api", "app", 3, ""}, {"db", "infra", 6, ""}, {"ui", "app", 9, ""}}
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
			wantNodes: []Node{{"api", "app", 3, ""}, {"cache", "", 0, ""}, {"db", "infra", 6, ""}, {"ui", "app", 9, ""}},
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
		{"advisory baseline", []report.Finding{finding("advisory", "baseline", "api", "db")}, StatusAdvisory},
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
	want := []Link{link("api", "ui", 0, StatusAccepted), link("db", "api", 0, StatusAdvisory), link("ui", "db", 0, StatusViolation)}
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
		{"accepted", []report.Seam{withPolicy("api", "db", 1, "accepted")}, nil, []Link{link("api", "db", 1, StatusAccepted)}},
		{"advisory", []report.Seam{withPolicy("api", "db", 1, "advisory")}, nil, []Link{link("api", "db", 1, StatusAdvisory)}},
		{"allowed", []report.Seam{withPolicy("api", "db", 1, "allowed")}, nil, []Link{link("api", "db", 1, StatusAllowed)}},
		{"observed", []report.Seam{withPolicy("api", "db", 1, "observed")}, nil, []Link{link("api", "db", 1, StatusObserved)}},
		// The engine decided the pair: graph does not overrule it with its own reading of the findings.
		{"engine allowed, a finding names the pair", []report.Seam{withPolicy("api", "db", 1, "allowed")},
			[]report.Finding{finding("gate", "new", "api", "db")}, []Link{link("api", "db", 1, StatusAllowed)}},
		{"engine observed, a baselined advisory names the pair", []report.Seam{withPolicy("api", "db", 1, "observed")},
			[]report.Finding{finding("advisory", "baseline", "api", "db")}, []Link{link("api", "db", 1, StatusObserved)}},
		{"duplicate seams: the strongest status wins", []report.Seam{
			withPolicy("api", "db", 2, "allowed"), withPolicy("api", "db", 3, "violation"), withPolicy("api", "db", 1, "observed"),
		}, nil, []Link{link("api", "db", 6, StatusViolation)}},
		{"duplicate seams: allowed beats observed", []report.Seam{
			withPolicy("api", "db", 1, "observed"), withPolicy("api", "db", 1, "allowed"),
		}, nil, []Link{link("api", "db", 2, StatusAllowed)}},
		{"a seam without the key keeps the finding status", []report.Seam{seam("api", "db", 1)},
			[]report.Finding{finding("gate", "baseline", "api", "db")}, []Link{link("api", "db", 1, StatusAccepted)}},
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
			for _, l := range v.Links {
				if len(tt.seams) == 1 && tt.seams[0].Policy != "" && l.Status != LinkStatus(tt.seams[0].Policy) {
					t.Errorf("link %s -> %s Status = %q: findings changed the engine's status %q", l.From, l.To, l.Status, tt.seams[0].Policy)
				}
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

func TestNodeShowsTheVolatilityItsSeamsAgreeOn(t *testing.T) {
	into := func(from, to, volatility string) report.Seam {
		s := seam(from, to, 1)
		s.Volatility = volatility
		return s
	}
	seams := []report.Seam{
		into("api", "db", "high"), into("ui", "db", "high"), // agree
		into("db", "api", "high"), into("ui", "api", "low"), // disagree
		into("api", "ui", "undeclared"), // says nothing
		into("ui", "cache", "medium"),   // not in the policy, still a node
	}
	got := map[string]string{}
	for _, n := range Build(reportFor(threeModules, seams, nil), []byte(threeModules)).Nodes {
		got[n.Name] = n.Volatility
	}
	want := map[string]string{"api": "", "cache": "medium", "db": "high", "ui": ""}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("volatility = %v, want %v", got, want)
	}
}

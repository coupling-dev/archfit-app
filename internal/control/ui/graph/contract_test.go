package graph

import (
	"reflect"
	"slices"
	"strings"
	"testing"

	"github.com/alexei-led/archfit-app/internal/wire/policy"
	"github.com/alexei-led/archfit-app/internal/wire/report"
)

func TestBuildMapsReportToView(t *testing.T) {
	rep := loadReport(t, blockedFixture)
	got := Build(rep, []byte(fixturePolicy))

	defs, err := policy.Modules([]byte(fixturePolicy))
	if err != nil {
		t.Fatalf("policy.Modules: %v", err)
	}
	wantNodes := []Node{}
	for _, d := range defs {
		wantNodes = append(wantNodes, Node{Name: d.Name, Layer: d.Layer, PolicyLine: d.Line})
	}
	if !reflect.DeepEqual(got.Nodes, wantNodes) {
		t.Errorf("Nodes = %+v, want the policy's modules %+v", got.Nodes, wantNodes)
	}
	if want := []Node{{"a", "", 3}, {"b", "", 6}}; !reflect.DeepEqual(got.Nodes, want) {
		t.Errorf("Nodes = %+v, want %+v", got.Nodes, want)
	}

	if len(got.Links) != len(rep.Seams) {
		t.Fatalf("Links = %+v, want one per fixture seam %+v", got.Links, rep.Seams)
	}
	for i, s := range rep.Seams {
		l := got.Links[i]
		if l.From != s.FromModule || l.To != s.ToModule || l.Count != s.Edges {
			t.Errorf("Links[%d] = %+v, want seam %s -> %s x%d", i, l, s.FromModule, s.ToModule, s.Edges)
		}
	}

	active := map[[2]string]bool{}
	for _, f := range rep.Findings {
		if f.Kind == "gate" && (f.Status == "new" || f.Status == "expired_waiver") {
			active[[2]string{f.Edge.From.Module, f.Edge.To.Module}] = true
		}
	}
	if len(active) == 0 {
		t.Fatal("fixture has no active gate finding")
	}
	for _, l := range got.Links {
		if isViolation := l.Status == StatusViolation; isViolation != active[[2]string{l.From, l.To}] {
			t.Errorf("%s -> %s Status = %q, want a violation exactly for an active gate finding", l.From, l.To, l.Status)
		}
	}
	if got.Stale {
		t.Error("Stale = true, want false: the policy is the one the fixture was computed from")
	}
}

// TestStatusFromRealReports reads engine captures of the shared corpus, all computed from fixturePolicy.
func TestStatusFromRealReports(t *testing.T) {
	tests := []struct {
		fixture, release string
		want             []Link
	}{
		{"real-blocked-forbidden-and-allowed", corpusEngine, []Link{link("a", "b", 1, StatusViolation), link("a", "c", 1, StatusAdvisory)}},
		{"real-baselined-comparable", corpusEngine, []Link{link("a", "b", 1, StatusAccepted)}}, // a baselined forbidden edge is never "permitted"
		{"real-waiver-active", corpusEngine, []Link{link("a", "b", 1, StatusAccepted)}},
		{"real-waiver-expired", corpusEngine, []Link{link("a", "b", 1, StatusViolation)}},
		{"real-blocked-no-baseline-v250", "v2.5.0", []Link{link("a", "b", 1, StatusViolation)}}, // seams[].policy: violation
	}
	for _, tt := range tests {
		t.Run(tt.fixture, func(t *testing.T) {
			got := Build(corpusReport(t, tt.fixture, tt.release), []byte(fixturePolicy))
			if !reflect.DeepEqual(got.Links, tt.want) {
				t.Errorf("Links = %+v, want %+v", got.Links, tt.want)
			}
		})
	}
}

// TestStatusFromEngineSeamPolicy reads the v2.5.0 capture of the engine's hexagonal map scenario: every link shows
// the engine's seams[].policy, the same statuses `archfit map` prints for that run, allowed included.
func TestStatusFromEngineSeamPolicy(t *testing.T) {
	const name = "real-seam-policy-v250"
	got := Build(corpusReport(t, name, "v2.5.0"), corpusFile(t, name+".policy.yaml"))
	want := []Link{
		link("api", "billing", 1, StatusAccepted),
		link("api", "stripe", 1, StatusAllowed),
		link("billing", "stripe", 1, StatusViolation),
		link("orders", "audit", 1, StatusObserved),
		link("orders", "billing", 1, StatusAllowed),
		link("stripe", "orders", 1, StatusAllowed),
	}
	if !reflect.DeepEqual(got.Links, want) {
		t.Errorf("Links = %+v, want %+v", got.Links, want)
	}
	if got.Stale {
		t.Error("Stale = true, want false: the policy is the one the capture was computed from")
	}
	if !got.Incomplete {
		t.Error("Incomplete = false, want true: the capture did not evaluate its two required rules")
	}
}

func TestBuildIsDeterministic(t *testing.T) {
	seams := []report.Seam{
		seam("ui", "db", 1), seam("api", "db", 2), seam("ui", "api", 3), seam("api", "db", 4),
		seam("zeta", "api", 1), seam("api", "api", 1),
	}
	findings := []report.Finding{
		finding("gate", "new", "ui", "db"),
		finding("advisory", "new", "api", "db"),
		finding("gate", "expired_waiver", "db", "ui"),
		finding("gate", "baseline", "ui", "api"),
	}
	first := Build(reportFor(threeModules, seams, findings), []byte(threeModules))
	second := Build(reportFor(threeModules, seams, findings), []byte(threeModules))

	shuffledSeams, shuffledFindings := slices.Clone(seams), slices.Clone(findings)
	slices.Reverse(shuffledSeams)
	slices.Reverse(shuffledFindings)
	shuffled := Build(reportFor(threeModules, shuffledSeams, shuffledFindings), []byte(threeModules))

	if !reflect.DeepEqual(first, second) {
		t.Errorf("same input gave different views:\n%+v\n%+v", first, second)
	}
	if !reflect.DeepEqual(first, shuffled) {
		t.Errorf("shuffled input gave a different view:\n%+v\n%+v", first, shuffled)
	}
	if !slices.IsSortedFunc(first.Nodes, func(a, b Node) int { return strings.Compare(a.Name, b.Name) }) {
		t.Errorf("Nodes not sorted by Name: %+v", first.Nodes)
	}
	byPair := func(a, b Link) int {
		if c := strings.Compare(a.From, b.From); c != 0 {
			return c
		}
		return strings.Compare(a.To, b.To)
	}
	if !slices.IsSortedFunc(first.Links, byPair) {
		t.Errorf("Links not sorted by (From, To): %+v", first.Links)
	}
	if len(first.Links) != 6 || len(first.Nodes) != 4 {
		t.Errorf("got %d links and %d nodes, want 6 and 4: %+v", len(first.Links), len(first.Nodes), first)
	}
}

func TestStaleFollowsPolicyDigest(t *testing.T) {
	hash := policy.Digest([]byte(fixturePolicy))
	tests := []struct {
		name       string
		policy     string
		configHash string
		stale      bool
	}{
		{"same bytes", fixturePolicy, hash, false},
		{"one added space", fixturePolicy + " ", hash, true},
		{"one added comment", fixturePolicy + "# note\n", hash, true},
		{"empty policy", "", hash, true},
		{"empty policy with its own digest", "", policy.Digest(nil), true},
		{"empty ConfigHash", fixturePolicy, "", true},
		{"ConfigHash in upper case", fixturePolicy, strings.ToUpper(hash), true},
		{"ConfigHash with a sha256: prefix", fixturePolicy, "sha256:" + hash, true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			rep := report.Report{Comparison: report.Comparison{ConfigHash: tt.configHash}}
			if got := Build(rep, []byte(tt.policy)).Stale; got != tt.stale {
				t.Errorf("Stale = %v, want %v", got, tt.stale)
			}
		})
	}
}

// An observed link is not proof that the policy permits it when the engine did not finish evaluating its rules.
func TestIncompleteFollowsDecision(t *testing.T) {
	unevaluated := []report.UnevaluatedRule{{RuleID: "no_direct_b_dependency", Reason: "required tool go/packages was absent"}}
	tests := []struct {
		name       string
		decision   report.Decision
		incomplete bool
	}{
		{"hard gates pass", report.Decision{HardGates: "pass"}, false},
		{"hard gates pass with an unevaluated rule", report.Decision{HardGates: "pass", UnevaluatedRequiredRules: unevaluated}, true},
		{"hard gates unmeasured", report.Decision{HardGates: "unmeasured", UnevaluatedRequiredRules: unevaluated}, true},
		{"hard gates fail", report.Decision{HardGates: "fail"}, true},
		{"hard gates unknown", report.Decision{HardGates: "PASS"}, true},
		{"zero decision", report.Decision{}, true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			rep := reportFor(threeModules, []report.Seam{seam("api", "db", 1)}, nil)
			rep.Decision = tt.decision
			if got := Build(rep, []byte(threeModules)).Incomplete; got != tt.incomplete {
				t.Errorf("Incomplete = %v, want %v", got, tt.incomplete)
			}
		})
	}
	if Build(loadReport(t, healthyFixture), []byte(fixturePolicy)).Incomplete {
		t.Error("the healthy fixture is incomplete")
	}
	if !Build(loadReport(t, blockedFixture), []byte(fixturePolicy)).Incomplete {
		t.Error("the blocked fixture (hard_gates: fail) is complete")
	}
}

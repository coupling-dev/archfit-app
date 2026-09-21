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
		if l.From != s.FromModule || l.To != s.ToModule || l.Count != s.Edges || !l.Observed {
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
		if want := !active[[2]string{l.From, l.To}]; l.Permitted != want {
			t.Errorf("%s -> %s Permitted = %v, want %v", l.From, l.To, l.Permitted, want)
		}
	}
	if got.Stale {
		t.Error("Stale = true, want false: the policy is the one the fixture was computed from")
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

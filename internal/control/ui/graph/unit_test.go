package graph

import (
	"reflect"
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
			want:   []Node{{"api", "app", 3}, {"db", "infra", 6}, {"ui", "app", 9}},
		},
		{
			name:   "module without a layer",
			policy: "modules:\n  core:\n    paths: [core/**]\n",
			want:   []Node{{"core", "", 2}},
		},
		{
			name:   "quoted names",
			policy: "modules:\n  \"my-mod\":\n    layer: app\n  'other mod':\n    layer: infra\n",
			want:   []Node{{"my-mod", "app", 2}, {"other mod", "infra", 4}},
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
			want: []Node{{"a", "core", 6}, {"b", "", 10}},
		},
		{
			name:   "modules is not the first key",
			policy: "version: 2\nrules: []\nmodules:\n  z:\n    layer: edge\n",
			want:   []Node{{"z", "edge", 4}},
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
	declared := []Node{{"api", "app", 3}, {"db", "infra", 6}, {"ui", "app", 9}}
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
			wantNodes: []Node{{"api", "app", 3}, {"cache", "", 0}, {"db", "infra", 6}, {"ui", "app", 9}},
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

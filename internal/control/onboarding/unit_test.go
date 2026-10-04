package onboarding

import (
	"bytes"
	"context"
	"fmt"
	"regexp"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/alexei-led/archfit-app/internal/github/api"
	"github.com/alexei-led/archfit-app/internal/wire/manifest"
	"github.com/alexei-led/archfit-app/internal/wire/settings"
)

func TestStageDerivation(t *testing.T) {
	type setup struct {
		settings []byte
		workflow bool
		policy   bool
		run      bool
		check    bool
	}
	advisory := renderSettings(settings.GateAdvisory, workflowPath)
	required := renderSettings(settings.GateRequired, workflowPath)
	tests := []struct {
		name       string
		setup      setup
		stage      Stage
		diagnostic string
		noDraft    string // diagnostic without an open PR on draftOn
		draftOn    string // branch whose open PR must show as OpenDraft
	}{
		{"nothing", setup{}, StageInstalled, "Review and merge PR #1", "Start onboarding", onboardingBranch},
		{"settings only", setup{settings: advisory}, StageInstalled, "Review and merge PR #1", "Start onboarding", onboardingBranch},
		{"invalid settings", setup{settings: []byte("version: 7\nbogus: true\n"), workflow: true}, StageInstalled,
			"Review and merge PR #1", "`.archfit-app.yaml` on the default branch is invalid", onboardingBranch},
		{"settings and workflow", setup{settings: advisory, workflow: true}, StageWorkflowMerged,
			"Review and merge the policy PR #1", "Run discovery (workflow_dispatch with `discover`)", policyBranch},
		{"run without check", setup{settings: advisory, workflow: true, run: true}, StageWorkflowMerged,
			"Review and merge the policy PR #1", "Run discovery (workflow_dispatch with `discover`)", policyBranch},
		{"run with check", setup{settings: advisory, workflow: true, run: true, check: true}, StageReportReceived,
			"Review and merge the policy PR #1", "Run discovery (workflow_dispatch with `discover`)", policyBranch},
		{"policy without report", setup{settings: advisory, workflow: true, policy: true}, StagePolicyMerged,
			"", "Waiting for the first run on the default branch", ""},
		{"policy and report advisory", setup{settings: advisory, workflow: true, policy: true, run: true, check: true}, StageAdvisory,
			"", "Advisory feedback is active; an owner enables the gate by PR", ""},
		{"policy and report required", setup{settings: required, workflow: true, policy: true, run: true, check: true}, StageRequired,
			"", "Required gate is active", ""},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			f := newFixture(t)
			r := f.repo
			if tc.setup.settings != nil {
				f.hub.put(r, r.DefaultBranch, settings.Path, tc.setup.settings)
			}
			if tc.setup.workflow {
				f.hub.put(r, r.DefaultBranch, workflowPath, []byte("name: archfit\n"))
			}
			if tc.setup.policy {
				f.hub.put(r, r.DefaultBranch, ".archfit.yaml", []byte(validPolicy))
			}
			if tc.setup.run {
				run := api.WorkflowRun{ID: 9, HeadSHA: runHeadSHA}
				if tc.setup.check {
					f.hub.setReported(r, run, api.CheckRun{Name: stateCheck, HeadSHA: runHeadSHA})
				} else {
					f.hub.setReported(r, run)
				}
			}

			p := mustProgress(t, f.svc, r)
			if p.Stage != tc.stage || p.Diagnostic != tc.noDraft {
				t.Errorf("got %q %q, want %q %q", p.Stage, p.Diagnostic, tc.stage, tc.noDraft)
			}
			if p.OpenDraft != nil {
				t.Errorf("OpenDraft = %+v without an open PR", p.OpenDraft)
			}

			// An open PR on the other branch never shows; one on the matching branch does.
			// Stages past report_received carry no draft at all.
			other := policyBranch
			if tc.draftOn == policyBranch {
				other = onboardingBranch
			}
			proposeOn(t, f, other)
			if tc.draftOn != "" {
				f.hub.closePR(r, other)
				proposeOn(t, f, tc.draftOn)
			}
			p = mustProgress(t, f.svc, r)
			if tc.draftOn == "" {
				if p.OpenDraft != nil || p.Diagnostic != tc.noDraft {
					t.Errorf("got draft %+v %q, want none and %q", p.OpenDraft, p.Diagnostic, tc.noDraft)
				}
				return
			}
			if p.OpenDraft == nil || p.OpenDraft.Number != 2 {
				t.Fatalf("OpenDraft = %+v, want PR #2", p.OpenDraft)
			}
			want := strings.Replace(tc.diagnostic, "#1", "#2", 1)
			if p.Stage != tc.stage || p.Diagnostic != want {
				t.Errorf("got %q %q, want %q %q", p.Stage, p.Diagnostic, tc.stage, want)
			}

			// Merged and closed pull requests are not open drafts.
			f.hub.closePR(r, tc.draftOn)
			if p = mustProgress(t, f.svc, r); p.OpenDraft != nil || p.Diagnostic != tc.noDraft {
				t.Errorf("closed PR: got draft %+v %q, want none and %q", p.OpenDraft, p.Diagnostic, tc.noDraft)
			}
		})
	}
}

func proposeOn(t *testing.T, f *fixture, branch string) {
	t.Helper()
	_, err := fakeProposals{f.hub}.Propose(context.Background(),
		changeFor(f.repo, branch, "title", "body", "message", []api.FileChange{{Path: "x", Content: []byte("x")}}), "")
	if err != nil {
		t.Fatal(err)
	}
}

func TestManagedMarker(t *testing.T) {
	now := testStart
	running := func(age time.Duration) string {
		return "Managed discovery running\n\n" + marker{State: markerRunning, Started: now.Add(-age)}.String()
	}
	tests := []struct {
		name string
		pull api.Pull
		want string
	}{
		{"no marker", api.Pull{Number: 3, State: "open", Body: "Adds Archfit."}, ""},
		{"running at -5 min", api.Pull{Number: 3, State: "open", Body: running(5 * time.Minute)}, "Managed discovery running since 2026-09-21T09:55:00Z"},
		{"running at -12 min", api.Pull{Number: 3, State: "open", Body: running(12 * time.Minute)}, "Managed discovery stalled — retry"},
		{"failed", api.Pull{Number: 3, State: "open", Body: "x\n" + marker{State: markerFailed, Code: codeTimeout}.String()}, "Managed discovery failed (`timeout`) — retry"},
		{"complete", api.Pull{Number: 3, State: "open", Body: marker{State: markerComplete}.String() + "\n"}, "Review and merge PR #3"},
		{"garbled", api.Pull{Number: 3, State: "open", Body: "<!-- archfit:managed state=running started=yesterday -->"}, "Managed discovery stalled — retry"},
		{"unknown state", api.Pull{Number: 3, State: "open", Body: "<!-- archfit:managed state=paused -->"}, "Managed discovery stalled — retry"},
		{"closed PR", api.Pull{Number: 3, State: "closed", Body: running(5 * time.Minute)}, ""},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			if got := managedDiagnostic(tc.pull, now); got != tc.want {
				t.Errorf("got %q, want %q", got, tc.want)
			}
		})
	}

	for _, m := range []marker{
		{State: markerRunning, Started: now},
		{State: markerFailed, Code: codeGitHub},
		{State: markerComplete},
	} {
		got, found := parseMarker("Some text\n\n" + m.String() + "\n")
		if !found || got != m {
			t.Errorf("round trip of %+v: got %+v, found %v", m, got, found)
		}
	}

	// At most 20 notes reach the body, and the marker stays the last line.
	var notes []string
	for i := range 25 {
		notes = append(notes, fmt.Sprintf("note-%02d", i))
	}
	body := completeBody(now, notes)
	if !strings.Contains(body, "note-19") || strings.Contains(body, "note-20") {
		t.Errorf("notes are not capped at 20:\n%s", body)
	}
	if m, found := parseMarker(body); !found || m.State != markerComplete || !strings.HasSuffix(body, markerSuffix+"\n") {
		t.Errorf("complete body marker = %+v, %v", m, found)
	}
}

func TestWorkflowTemplate(t *testing.T) {
	entry := manifest.Entry{
		Identity:  manifest.Identity{EngineVersion: "v2.3.1", ImageDigest: "sha256:" + strings.Repeat("a1", 32), Platform: "linux/amd64"},
		ActionRef: strings.Repeat("c0de", 10),
	}
	raw, err := renderWorkflow(entry, "trunk", testBaseURL)
	if err != nil {
		t.Fatal(err)
	}
	got := string(raw)

	for name, re := range map[string]string{
		"action pinned by SHA": `(?m)^\s+- uses: \S+/archfit-action@` + entry.ActionRef + `$`,
		"image digest":         regexp.QuoteMeta(entry.ImageDigest),
		"engine version":       `v2\.3\.1`,
		"endpoint":             `(?m)^\s+endpoint: "https://archfit\.example"$`,
		"audience":             `(?m)^\s+audience: "https://archfit\.example"$`,
		"pull_request trigger": `(?m)^  pull_request:\s*$`,
		"push to trunk":        `(?m)^  push:\n    branches:\n      - "trunk"$`,
		"mode choice input": `(?m)^  workflow_dispatch:\n    inputs:\n      mode:\n(.+\n)*?\s+type: choice\n\s+options:\n` +
			`\s+- report\n\s+- discovery\n\s+- baseline\n\s+default: report$`,
		"discover input": `(?m)^      discover:\n(.+\n)*?\s+type: boolean\n\s+default: false$`,
		// discover: true is how the App's Discover button dispatches; it must not collide with the mode default.
		"mode passed on":     `(?m)^\s+mode: \$\{\{ inputs\.discover && 'discovery' \|\| inputs\.mode \}\}$`,
		"discover passed on": `(?m)^\s+discover: \$\{\{ inputs\.discover \}\}$`,
		"permissions":        `(?m)^permissions:\n  contents: read\n  id-token: write\n\n`,
	} {
		if !regexp.MustCompile(re).MatchString(got) {
			t.Errorf("%s: no match for %s in\n%s", name, re, got)
		}
	}

	// The Action runs only on these events and refuses every other one before the checkout.
	on := regexp.MustCompile(`(?ms)^on:\n(.*?)^\S`).FindStringSubmatch(got)
	if on == nil {
		t.Fatalf("no on: block in\n%s", got)
	}
	var triggers []string
	for _, m := range regexp.MustCompile(`(?m)^  ([a-z_]+):`).FindAllStringSubmatch(on[1], -1) {
		triggers = append(triggers, m[1])
	}
	if want := []string{"pull_request", "push", "workflow_dispatch"}; !slices.Equal(triggers, want) {
		t.Errorf("triggers = %q, want exactly %q", triggers, want)
	}

	header, _, _ := strings.Cut(got, "\nname:")
	prose := strings.Join(strings.Fields(strings.ReplaceAll(header, "\n#", " ")), " ") // the comment lines as one text
	for _, want := range []string{
		".archfit.yaml", ".archfit-baseline.json", ".archfit-labels.yaml", "baseline_digest", "labels_digest",
		"per file", "from the pull request head when the pull request changes", "from the tip of the base branch",
	} {
		if !strings.Contains(prose, want) {
			t.Errorf("header comment does not name %q", want)
		}
	}
	if strings.Contains(header, "base-ref") {
		t.Error("header comment says the inputs come from the base ref only")
	}
	for _, line := range strings.Split(header, "\n") {
		if !strings.HasPrefix(line, "#") {
			t.Errorf("header line is not a comment: %q", line)
		}
	}

	for _, banned := range []string{"secrets.", "workflow_run", "pull_request_target", "merge_group", "write-all"} {
		if strings.Contains(got, banned) {
			t.Errorf("workflow contains %q", banned)
		}
	}
	if regexp.MustCompile(`(?m)^\s*(- )?run:`).MatchString(got) {
		t.Error("workflow has a run: step")
	}
	if n := strings.Count(got, "uses:"); n != 1 {
		t.Errorf("workflow has %d uses: steps, want only the pinned Action", n)
	}
	if !regexp.MustCompile(`^[0-9a-f]{40}$`).MatchString(manifest.Current().ActionRef) {
		t.Errorf("manifest ActionRef %q is not a 40-hex commit", manifest.Current().ActionRef)
	}
}

func TestCodeownersAppend(t *testing.T) {
	const github, root, docs = ".github/CODEOWNERS", "CODEOWNERS", "docs/CODEOWNERS"
	protected := []string{"/.archfit.yaml", "/.archfit-baseline.json", "/.archfit-labels.yaml", "/.archfit-app.yaml", "/" + workflowPath}
	already := "* @bob\n# Archfit protected paths\n"
	for _, p := range protected {
		already += p + " @alice\n"
	}
	tests := []struct {
		name     string
		existing map[string]string
		owners   []string
		wantPath string
		wantAdd  int
	}{
		{"no file", nil, []string{"alice"}, github, 5},
		{"file in .github", map[string]string{github: "* @bob\n", root: "* @carol\n", docs: "* @dan\n"}, []string{"alice"}, github, 5},
		{"file at root only", map[string]string{root: "* @carol\n", docs: "* @dan\n"}, []string{"alice"}, root, 5},
		{"file in docs only", map[string]string{docs: "* @dan\n"}, []string{"alice"}, docs, 5},
		{"no trailing newline", map[string]string{github: "* @bob"}, []string{"alice"}, github, 5},
		{"entries already present", map[string]string{github: already}, []string{"alice"}, github, 0},
		{"team owner", nil, []string{"alice", "acme/architects"}, github, 5},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			f := newFixture(t)
			for path, content := range tc.existing {
				f.hub.put(f.repo, f.repo.DefaultBranch, path, []byte(content))
			}
			path, got, err := f.svc.codeowners(context.Background(), f.repo, tc.owners)
			if err != nil {
				t.Fatal(err)
			}
			if path != tc.wantPath {
				t.Errorf("path = %q, want %q", path, tc.wantPath)
			}
			original := tc.existing[tc.wantPath]
			if !bytes.HasPrefix(got, []byte(original)) {
				t.Errorf("original bytes are not a prefix of\n%s", got)
			}
			suffix := "@" + strings.Join(tc.owners, " @")
			added := strings.TrimPrefix(string(got), original)
			for _, p := range protected {
				line := p + " " + suffix + "\n"
				if n := strings.Count(string(got), "\n"+line) + boolInt(strings.HasPrefix(string(got), line)); n != 1 {
					t.Errorf("line %q appears %d times in\n%s", line, n, got)
				}
			}
			if n := strings.Count(added, " @"+tc.owners[0]); n != tc.wantAdd {
				t.Errorf("%d lines added, want %d:\n%s", n, tc.wantAdd, added)
			}
			if tc.wantAdd > 0 && !strings.Contains(added, "\n# Archfit protected paths\n") && !strings.HasPrefix(added, "# Archfit protected paths\n") {
				t.Errorf("no header line in the added text:\n%s", added)
			}
			if again := appendCodeowners(got, tc.owners); !bytes.Equal(again, got) {
				t.Errorf("second run changed the file:\n%s", again)
			}
		})
	}
}

func boolInt(b bool) int {
	if b {
		return 1
	}
	return 0
}

func TestDraftChecks(t *testing.T) {
	checkFixtures(t)
	// Seeded leakage fixtures: a tab-indented source block and a terminal log blob.
	sourceSnippet := "version: 1\nnotes: |\n  func Pay(c Card) error {\n  \tif c.Number == \"\" {\n  \t\treturn errSeededSource\n  \t}\n  }\n"
	logBlob := "version: 1\nlog: |\n  \x1b[31mERROR\x1b[0m seeded-log-line " + strings.Repeat("at internal/pay/card.go:42 ", 30) + "\n"
	tests := []struct {
		name    string
		payload []byte
		want    string
	}{
		{"valid policy", []byte(validPolicy), ""},
		{"exactly 1 MB", paddedPolicy(1 << 20), ""},
		{"1 MB + 1 byte", paddedPolicy(1<<20 + 1), "discovery_too_large"},
		{"empty", nil, "discovery_invalid"},
		{"invalid UTF-8", []byte("version: 1\nname: \xff\xfe\n"), "discovery_invalid"},
		{"NUL byte", []byte("version: 1\x00\n"), "discovery_invalid"},
		{"carriage return", []byte("version: 1\r\nmodules: {}\r\n"), "discovery_invalid"},
		{"500-byte line", []byte("version: 1\n# " + strings.Repeat("x", 498) + "\n"), ""},
		{"501-byte line", []byte("version: 1\n# " + strings.Repeat("x", 499) + "\n"), "discovery_invalid"},
		{"malformed YAML", []byte("modules: [core\n"), "discovery_invalid"},
		{"one waiver", []byte(waiverPolicy), "discovery_invalid"},
		{"JSON report", []byte("{\"verdict\": \"blocked\", \"logs\": \"seeded-log-line\"}\n"), "discovery_invalid"},
		{"no version", []byte("modules: {}\n"), "discovery_invalid"},
		{"unknown top-level key", []byte("version: 1\nsource: seeded\n"), "discovery_invalid"},
		{"source snippet block", []byte(sourceSnippet), "discovery_invalid"},
		{"log blob", []byte(logBlob), "discovery_invalid"},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			if got := checkDraft(tc.payload); got != tc.want {
				t.Errorf("got %q, want %q", got, tc.want)
			}
		})
	}
}

// paddedPolicy grows the valid policy with comment lines to exactly n bytes.
func paddedPolicy(n int) []byte {
	b := []byte(validPolicy)
	for len(b) < n {
		k := min(400, n-len(b))
		b = append(b, (strings.Repeat("#", k-1) + "\n")...)
	}
	return b
}

func TestOwnersValidation(t *testing.T) {
	tests := []struct {
		name   string
		owners []string
		ok     bool
	}{
		{"empty list", nil, false},
		{"bad owner", []string{"bad owner"}, false},
		{"too many segments", []string{"org/team/extra"}, false},
		{"valid login", []string{"alice"}, true},
		{"valid team", []string{"acme/architects"}, true},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			f := newFixture(t)
			_, errCustomer := f.svc.StartCustomer(context.Background(), f.repo, tc.owners)
			_, errManaged := f.svc.StartManaged(context.Background(), f.repo, tc.owners)
			f.svc.jobs.Wait()
			if !tc.ok {
				if errCustomer == nil || errManaged == nil {
					t.Errorf("errors = %v, %v; want both", errCustomer, errManaged)
				}
				if n := f.hub.count("Propose") + f.hub.count("SetBody"); n != 0 {
					t.Errorf("%d GitHub writes for invalid owners", n)
				}
				if len(f.sandbox.calls()) != 0 || len(f.tokens.requested()) != 0 {
					t.Error("a managed job ran for invalid owners")
				}
				return
			}
			if errCustomer != nil {
				t.Fatal(errCustomer)
			}
			raw, _ := f.hub.file(f.repo, onboardingBranch, settings.Path)
			s, err := settings.Parse(raw)
			if err != nil {
				t.Fatal(err)
			}
			if len(s.Owners) != 1 || s.Owners[0] != tc.owners[0] {
				t.Errorf("owners = %v, want %v", s.Owners, tc.owners)
			}
		})
	}
}

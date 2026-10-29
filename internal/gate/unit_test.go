package gate

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os/exec"
	"reflect"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/alexei-led/archfit-app/internal/gate/feedback"
	"github.com/alexei-led/archfit-app/internal/gate/inputs"
	"github.com/alexei-led/archfit-app/internal/gate/sweep"
	"github.com/alexei-led/archfit-app/internal/gate/verdict"
	"github.com/alexei-led/archfit-app/internal/github/api"
	"github.com/alexei-led/archfit-app/internal/github/webhook"
	"github.com/alexei-led/archfit-app/internal/wire/envelope"
	"github.com/alexei-led/archfit-app/internal/wire/manifest"
	"github.com/alexei-led/archfit-app/internal/wire/report"
	"github.com/alexei-led/archfit-app/internal/wire/settings"
)

const modulePath = "github.com/alexei-led/archfit-app"

func goList(t *testing.T, dir string, args ...string) []string {
	t.Helper()
	cmd := exec.Command("go", append([]string{"list"}, args...)...)
	cmd.Dir = dir
	out, err := cmd.Output()
	if err != nil {
		var exit *exec.ExitError
		if errors.As(err, &exit) {
			t.Fatalf("go list %v: %v\n%s", args, err, exit.Stderr)
		}
		t.Fatalf("go list %v: %v", args, err)
	}
	return strings.Fields(string(out))
}

func TestDisplayReasonFacadeRepublishesOnlyPrimitiveMetadata(t *testing.T) {
	for _, tt := range []struct {
		code, category string
		derived, known bool
	}{
		{"healthy", "architecture", true, true}, {"missing_required_coverage", "evidence", true, true}, {"policy_defect", "governance", true, true},
		{"unknown_engine_identity", "evidence", false, true}, {"owner_approval_missing", "governance", false, true},
		{"healthy", "evidence", false, false}, {"future_reason", "evidence", false, false},
	} {
		derived, known := ClassifyDisplayReason(tt.code, tt.category)
		if derived != tt.derived || known != tt.known {
			t.Errorf("facade %s/%s = %v,%v", tt.code, tt.category, derived, known)
		}
	}
}

func TestGateImportsNothingForbidden(t *testing.T) {
	allowed := []string{
		modulePath + "/internal/wire/", modulePath + "/internal/github/",
		modulePath + "/internal/store", modulePath + "/internal/gate",
	}
	// Third-party modules the allowed packages may bring in, per docs/tech-stack.md.
	thirdParty := []string{"go.yaml.in/yaml/v3"}
	forbiddenStd := []string{"html/template"}
	forbiddenWords := []string{"/internal/control", "/internal/app", "anthropic", "openai", "/llm", "/assist", "/ui"}

	deps := goList(t, ".", "-deps", "-f", `{{.ImportPath}}|{{.Standard}}`, "./...")
	if len(deps) == 0 {
		t.Fatal("go list returned nothing")
	}
	for _, line := range deps {
		path, standard, _ := strings.Cut(line, "|")
		for _, word := range forbiddenWords {
			if strings.Contains(path, word) {
				t.Errorf("gate depends on %s (forbidden: %s)", path, word)
			}
		}
		if standard == "true" {
			for _, std := range forbiddenStd {
				if path == std {
					t.Errorf("gate depends on %s", path)
				}
			}
			continue
		}
		ok := false
		for _, prefix := range append(allowed, thirdParty...) {
			ok = ok || path == prefix || strings.HasPrefix(path, prefix)
		}
		if !ok {
			t.Errorf("gate depends on %s, which is outside wire, github, store and gate", path)
		}
	}
}

func TestNobodyImportsGateChildren(t *testing.T) {
	root := goList(t, ".", "-m", "-f", "{{.Dir}}")
	if len(root) != 1 {
		t.Fatalf("module root = %v", root)
	}
	gatePath := modulePath + "/internal/gate"
	// -e: packages of modules that are mid-edit still list their imports.
	lines := goList(t, root[0], "-e", "-f",
		`{{.ImportPath}}|{{join .Imports ","}},{{join .TestImports ","}},{{join .XTestImports ","}}`, "./...")
	seen := false
	for _, line := range lines {
		importer, imports, _ := strings.Cut(line, "|")
		seen = seen || importer == gatePath
		if importer == gatePath || strings.HasPrefix(importer, gatePath+"/") {
			continue
		}
		for _, imp := range strings.Split(imports, ",") {
			if strings.HasPrefix(imp, gatePath+"/") {
				t.Errorf("%s imports the gate child %s", importer, imp)
			}
		}
	}
	if !seen {
		t.Fatal("the listing does not contain internal/gate: the check saw nothing")
	}
}

func reportWithProfile(version string) *report.Report {
	r := &report.Report{}
	if version != "-" {
		r.Comparison.MeasurementProfile = &report.MeasurementProfile{Version: version}
	}
	return r
}

func TestIdentityApproved(t *testing.T) {
	entry := manifest.Current()
	with := func(edit func(*manifest.Identity)) manifest.Identity {
		id := entry.Identity
		edit(&id)
		return id
	}
	unknown := manifest.Identity{EngineVersion: "unknown", ImageDigest: "unknown", Platform: "unknown"}

	tests := []struct {
		name     string
		identity manifest.Identity
		report   *report.Report
		want     bool
	}{
		{"manifest hit with matching profile", entry.Identity, reportWithProfile(entry.ProfileVersion), true},
		{"hit with another profile version", entry.Identity, reportWithProfile(entry.ProfileVersion + ".next"), false},
		{"hit with no profile", entry.Identity, reportWithProfile("-"), false},
		{"unknown image digest", with(func(id *manifest.Identity) { id.ImageDigest = "sha256:" + strings.Repeat("0", 64) }), reportWithProfile(entry.ProfileVersion), false},
		{"empty engine version", with(func(id *manifest.Identity) { id.EngineVersion = "" }), reportWithProfile(entry.ProfileVersion), false},
		{"empty platform", with(func(id *manifest.Identity) { id.Platform = "" }), reportWithProfile(entry.ProfileVersion), false},
		{"both unknown", unknown, reportWithProfile("unknown"), false},
		{"both empty", manifest.Identity{}, reportWithProfile(""), false},
		{"nil report", entry.Identity, nil, false},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			if got := identityApproved(Upload{Identity: tc.identity, Report: tc.report}); got != tc.want {
				t.Errorf("identityApproved = %v, want %v", got, tc.want)
			}
		})
	}
}

func TestWorkflowApproved(t *testing.T) {
	onboarded := inputs.Trusted{SettingsFound: true, Settings: settings.Settings{Workflow: testWorkflow}}
	tests := []struct {
		name    string
		path    string
		trusted inputs.Trusted
		want    bool
	}{
		{"binding path equals settings", testWorkflow, onboarded, true},
		{"differs", ".github/workflows/other.yaml", onboarded, false},
		{"not onboarded, both empty", "", inputs.Trusted{}, false},
		{"reusable workflow from another repository", "", onboarded, false},
		{"settings found with an empty workflow", "", inputs.Trusted{SettingsFound: true}, false},
		{"path matches but settings are not found", testWorkflow, inputs.Trusted{Settings: settings.Settings{Workflow: testWorkflow}}, false},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			if got := workflowApproved(Binding{WorkflowPath: tc.path}, tc.trusted); got != tc.want {
				t.Errorf("workflowApproved = %v, want %v", got, tc.want)
			}
		})
	}
}

func TestReceiptConstruction(t *testing.T) {
	const (
		basePol  = "aa11"
		headPol  = "bb22"
		baseline = "cc33"
		labels   = "dd44"
		other    = "ee55"
	)
	expiry := time.Date(2026, 10, 1, 23, 59, 59, 0, time.UTC)
	withHash := func(h string) *report.Report { return &report.Report{Comparison: report.Comparison{ConfigHash: h}} }
	success := verdict.Outcome{Conclusion: verdict.Success, Reasons: []verdict.Reason{{Code: verdict.ReasonHealthy}}}
	blocked := verdict.Outcome{Conclusion: verdict.ActionRequired, Reasons: []verdict.Reason{
		{Code: verdict.ReasonOwnerApprovalMissing}, {Code: verdict.ReasonPolicyMismatch},
	}}

	tests := []struct {
		name    string
		upload  Upload
		trusted inputs.Trusted
		outcome verdict.Outcome
		want    feedback.Receipt
	}{
		{
			name:    "matching policy, baseline and labels",
			upload:  Upload{Report: withHash(basePol), BaselineDigest: baseline, LabelsDigest: labels},
			trusted: inputs.Trusted{PolicyDigests: []string{basePol}, BaselineDigests: []string{baseline}, LabelsDigests: []string{labels}, EarliestWaiverExpiry: expiry},
			outcome: success,
			want:    feedback.Receipt{PolicyDigest: basePol, BaselineDigest: baseline, LabelsDigest: labels, EarliestWaiverExpiry: expiry, Conclusion: verdict.Success, Reason: verdict.ReasonHealthy},
		},
		{
			name:    "approved head policy digest",
			upload:  Upload{Report: withHash(headPol)},
			trusted: inputs.Trusted{PolicyDigests: []string{basePol, headPol}},
			outcome: success,
			want:    feedback.Receipt{PolicyDigest: headPol, Conclusion: verdict.Success, Reason: verdict.ReasonHealthy},
		},
		{
			name:    "mismatching config hash",
			upload:  Upload{Report: withHash(other)},
			trusted: inputs.Trusted{PolicyDigests: []string{basePol}},
			outcome: blocked,
			want:    feedback.Receipt{PolicyDigest: basePol, Conclusion: verdict.ActionRequired, Reason: verdict.ReasonOwnerApprovalMissing},
		},
		{
			name:    "claimed baseline not on the protected ref",
			upload:  Upload{Report: withHash(basePol), BaselineDigest: other, LabelsDigest: other},
			trusted: inputs.Trusted{PolicyDigests: []string{basePol}, BaselineDigests: []string{baseline}, LabelsDigests: []string{labels}},
			outcome: blocked,
			want:    feedback.Receipt{PolicyDigest: basePol, BaselineDigest: baseline, LabelsDigest: labels, Conclusion: verdict.ActionRequired, Reason: verdict.ReasonOwnerApprovalMissing},
		},
		{
			name:    "a claim while the protected ref has no such file",
			upload:  Upload{Report: withHash(basePol), BaselineDigest: other, LabelsDigest: other},
			trusted: inputs.Trusted{PolicyDigests: []string{basePol}},
			outcome: blocked,
			want:    feedback.Receipt{PolicyDigest: basePol, Conclusion: verdict.ActionRequired, Reason: verdict.ReasonOwnerApprovalMissing},
		},
		{
			name:    "no claim while the protected ref has the file",
			upload:  Upload{Report: withHash(basePol)},
			trusted: inputs.Trusted{PolicyDigests: []string{basePol}, BaselineDigests: []string{baseline}, LabelsDigests: []string{labels}},
			outcome: blocked,
			want:    feedback.Receipt{PolicyDigest: basePol, BaselineDigest: baseline, LabelsDigest: labels, Conclusion: verdict.ActionRequired, Reason: verdict.ReasonOwnerApprovalMissing},
		},
		{
			name:    "no baseline, no labels and no policy anywhere",
			upload:  Upload{Report: withHash(other)},
			trusted: inputs.Trusted{},
			outcome: blocked,
			want:    feedback.Receipt{Conclusion: verdict.ActionRequired, Reason: verdict.ReasonOwnerApprovalMissing},
		},
		{
			name:    "nil report",
			upload:  Upload{Report: nil, BaselineDigest: baseline},
			trusted: inputs.Trusted{PolicyDigests: []string{basePol, headPol}, BaselineDigests: []string{baseline}},
			outcome: blocked,
			want:    feedback.Receipt{PolicyDigest: basePol, BaselineDigest: baseline, Conclusion: verdict.ActionRequired, Reason: verdict.ReasonOwnerApprovalMissing},
		},
		{
			name: "a merged pull request names the base tip it was merged into",
			upload: Upload{Report: withHash(basePol), Binding: Binding{
				Integration: envelope.IntegrationMerged, BaseSHA: "tip", MergeBaseSHA: "fork-point"}},
			trusted: inputs.Trusted{PolicyDigests: []string{basePol}},
			outcome: success,
			want:    feedback.Receipt{PolicyDigest: basePol, BaseSHA: "tip", Conclusion: verdict.Success, Reason: verdict.ReasonHealthy},
		},
		{
			name: "a head that conflicts names its merge base",
			upload: Upload{Report: withHash(basePol), Binding: Binding{
				Integration: envelope.IntegrationConflicts, BaseSHA: "tip", MergeBaseSHA: "fork-point"}},
			trusted: inputs.Trusted{PolicyDigests: []string{basePol}},
			outcome: success,
			want:    feedback.Receipt{PolicyDigest: basePol, BaseSHA: "fork-point", Conclusion: verdict.Success, Reason: verdict.ReasonHealthy},
		},
		{
			name: "a push has no base",
			upload: Upload{Report: withHash(basePol), Binding: Binding{
				BaseSHA: "tip", MergeBaseSHA: "fork-point"}},
			trusted: inputs.Trusted{PolicyDigests: []string{basePol}},
			outcome: success,
			want:    feedback.Receipt{PolicyDigest: basePol, Conclusion: verdict.Success, Reason: verdict.ReasonHealthy},
		},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			tc.upload.Binding.RunID, tc.upload.Binding.RunAttempt = 5001, 2
			tc.upload.Digest = "sha256:" + strings.Repeat("9", 64)
			tc.want.RunID, tc.want.RunAttempt, tc.want.ReportDigest = 5001, 2, tc.upload.Digest
			if got := receipt(tc.upload, tc.trusted, tc.outcome); !reflect.DeepEqual(got, tc.want) {
				t.Errorf("receipt =\n %+v, want\n %+v", got, tc.want)
			}
		})
	}
}

// pipelineFakes is a Gate over hand-written fakes of every child.
type pipelineFakes struct {
	receiver  *fakeReceiver
	resolver  *fakeResolver
	publisher *fakePublisher
	repairer  *fakeRepairer
	sweeper   *fakeSweeper
	actions   *fakeActions
	gate      *Gate
}

func newPipelineFakes() *pipelineFakes {
	f := &pipelineFakes{
		receiver: &fakeReceiver{}, resolver: &fakeResolver{}, publisher: &fakePublisher{}, repairer: &fakeRepairer{},
		sweeper: &fakeSweeper{}, actions: &fakeActions{},
	}
	f.gate = newGate(f.receiver, f.resolver, f.publisher, f.repairer, f.sweeper, f.actions, newFakeGitHub(), &fakeAdmitter{refused: map[int64]bool{}}, func() time.Time { return testStart })
	return f
}

const reportMarker = "REPORT_CONTENT_MARKER"

func postMarker() *http.Request {
	return httptest.NewRequest(http.MethodPost, "/v1/reports", strings.NewReader(reportMarker))
}

// The receipt is what the sweep later tests with inputs.Accepts, and verdict reports a mismatch with the
// same function: the three sites must agree for every combination.
func TestReceiptDigestsAreAlwaysAccepted(t *testing.T) {
	lists := [][]string{nil, {"aa11"}, {"aa11", "bb22"}}
	claims := []string{"", "aa11", "bb22", "ff99", "sha256:aa11"}
	mismatch := func(o verdict.Outcome, code verdict.ReasonCode) bool {
		return slices.ContainsFunc(o.Reasons, func(r verdict.Reason) bool { return r.Code == code })
	}
	for _, list := range lists {
		for _, claim := range claims {
			u := Upload{
				BaselineDigest: claim, LabelsDigest: claim,
				Report: &report.Report{Comparison: report.Comparison{ConfigHash: claim}},
			}
			tr := inputs.Trusted{SettingsFound: true, PolicyDigests: list, BaselineDigests: list, LabelsDigests: list}
			o := verdict.Decide(verdict.Facts{BaselineDigest: claim, LabelsDigest: claim, Report: u.Report, Trusted: tr})
			rc := receipt(u, tr, o)

			want := inputs.First(list)
			if inputs.Accepts(list, claim) {
				want = claim
			}
			for field, got := range map[string]string{"policy": rc.PolicyDigest, "baseline": rc.BaselineDigest, "labels": rc.LabelsDigest} {
				if got != want || !inputs.Accepts(list, got) {
					t.Errorf("%s: receipt digest for claim %q against %v = %q, want %q", field, claim, list, got, want)
				}
			}
			for _, code := range []verdict.ReasonCode{verdict.ReasonBaselineMismatch, verdict.ReasonLabelsMismatch} {
				if got := mismatch(o, code); got != !inputs.Accepts(list, claim) {
					t.Errorf("%s for claim %q against %v = %v", code, claim, list, got)
				}
			}
			// An empty policy list is policy_missing, never policy_mismatch.
			if got := mismatch(o, verdict.ReasonPolicyMismatch); got != (len(list) > 0 && !inputs.Accepts(list, claim)) {
				t.Errorf("policy_mismatch for claim %q against %v = %v", claim, list, got)
			}
		}
	}
}

// gate/inputs owns the protected path set. No path is named here, so a sixth one is covered as it is.
func TestProtectedPushFollowsInputs(t *testing.T) {
	push := func(paths ...string) int {
		t.Helper()
		f := newPipelineFakes()
		e := webhook.Event{Kind: webhook.DefaultBranchPush, InstallationID: 11, Repo: shopName, ChangedPaths: paths}
		if err := f.gate.HandleEvent(context.Background(), e); err != nil {
			t.Fatal(err)
		}
		return len(f.sweeper.repos)
	}
	set := inputs.ProtectedPaths(settings.Settings{Workflow: testWorkflow})
	if len(set) < 5 {
		t.Fatalf("ProtectedPaths = %v", set)
	}
	for _, path := range set {
		if got := push("pkg/a/a.go", path); got != 1 {
			t.Errorf("a push of %s swept %d repositories, want 1", path, got)
		}
	}
	if got := push("pkg/a/a.go", "docs/readme.md"); got != 0 {
		t.Errorf("an unprotected push swept %d repositories", got)
	}
	if got := push(); got != 1 {
		t.Errorf("a push without paths swept %d repositories, want 1", got)
	}
}

// A default-branch push that changes no protected path still moves the target, so open pull requests are rechecked
// against the new tip (sweep rule 5c); a protected one runs the full repository pass, which includes the recheck.
func TestDefaultBranchPushRechecksTheTarget(t *testing.T) {
	const tip = "1111111111111111111111111111111111111111"
	tests := []struct {
		name         string
		kind         webhook.EventKind
		paths        []string
		wantSwept    int
		wantRechecks []recheckCall
	}{
		{"unprotected push", webhook.DefaultBranchPush, []string{"pkg/a/a.go"}, 0, []recheckCall{{api.Repo{RepoRef: api.RepoRef{InstallationID: 11, FullName: shopName}, DefaultBranch: testBranch}, tip}}},
		{"protected push", webhook.DefaultBranchPush, []string{"pkg/a/a.go", settings.Path}, 1, nil},
		{"push to another branch", webhook.BranchPush, []string{"pkg/a/a.go"}, 0, nil},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			f := newPipelineFakes()
			e := webhook.Event{Kind: tt.kind, InstallationID: 11, Repo: shopName, DefaultBranch: testBranch, HeadSHA: tip, ChangedPaths: tt.paths}
			if err := f.gate.HandleEvent(context.Background(), e); err != nil {
				t.Fatal(err)
			}
			if len(f.sweeper.repos) != tt.wantSwept || !reflect.DeepEqual(f.sweeper.rechecks, tt.wantRechecks) {
				t.Errorf("swept %d repositories, rechecks %+v; want %d, %+v", len(f.sweeper.repos), f.sweeper.rechecks, tt.wantSwept, tt.wantRechecks)
			}
		})
	}

	t.Run("a failed recheck is returned so the delivery is retried", func(t *testing.T) {
		f := newPipelineFakes()
		f.sweeper.err = api.ErrRateLimited
		e := webhook.Event{Kind: webhook.DefaultBranchPush, InstallationID: 11, Repo: shopName, HeadSHA: tip, ChangedPaths: []string{"a.go"}}
		if err := f.gate.HandleEvent(context.Background(), e); !errors.Is(err, api.ErrRateLimited) {
			t.Errorf("err = %v, want the rate limit", err)
		}
	})
}

func TestHTTPMapping(t *testing.T) {
	// The fakes return a zero Upload and a zero Trusted: not onboarded.
	published := `{"conclusion":"action_required","reason":"settings_missing"}`
	tests := []struct {
		name       string
		receiveErr error
		resolveErr error
		publishErr error
		status     int
		body       string
		retryAfter string
	}{
		{name: "outcome published", status: 200, body: published},
		{name: "publication skipped by feedback (nil error)", status: 200, body: published},
		{name: "rate limited while resolving", resolveErr: fmt.Errorf("inputs: %w", api.ErrRateLimited), status: 503, body: `{"error":"rate_limited"}`, retryAfter: "60"},
		{name: "rate limited while publishing", publishErr: fmt.Errorf("feedback: %w", api.ErrRateLimited), status: 503, body: `{"error":"rate_limited"}`, retryAfter: "60"},
		{name: "other error while resolving", resolveErr: errors.New(reportMarker), status: 503, body: `{"error":"unavailable"}`},
		{name: "other error while publishing", publishErr: api.ErrNotFound, status: 503, body: `{"error":"unavailable"}`},
		{name: "plain error from Receive", receiveErr: errors.New("mint failed: " + reportMarker), status: 503, body: `{"error":"unavailable"}`},
	}
	for _, status := range []int{400, 401, 403, 409, 413, 429, 503} {
		code := fmt.Sprintf("code_%d", status)
		tests = append(tests, struct {
			name       string
			receiveErr error
			resolveErr error
			publishErr error
			status     int
			body       string
			retryAfter string
		}{name: fmt.Sprintf("rejection %d", status), receiveErr: fmt.Errorf("wrapped: %w", &Rejection{Status: status, Code: code}), status: status, body: `{"error":"` + code + `"}`})
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			f := newPipelineFakes()
			f.receiver.err, f.resolver.err, f.publisher.publishErr = tc.receiveErr, tc.resolveErr, tc.publishErr
			rec := httptest.NewRecorder()
			f.gate.UploadHandler().ServeHTTP(rec, postMarker())

			if rec.Code != tc.status {
				t.Errorf("status = %d, want %d", rec.Code, tc.status)
			}
			if got := strings.TrimSpace(rec.Body.String()); got != tc.body {
				t.Errorf("body = %s, want %s", got, tc.body)
			}
			if got := rec.Header().Get("Retry-After"); got != tc.retryAfter {
				t.Errorf("Retry-After = %q, want %q", got, tc.retryAfter)
			}
			if got := rec.Header().Get("Content-Type"); got != "application/json" {
				t.Errorf("Content-Type = %q", got)
			}
			if strings.Contains(rec.Body.String(), reportMarker) {
				t.Errorf("body carries request or error content: %s", rec.Body.String())
			}
			if tc.receiveErr != nil && (len(f.resolver.targets) > 0 || len(f.publisher.published) > 0) {
				t.Error("a failed Receive reached Resolve or Publish")
			}
			if tc.resolveErr != nil && len(f.publisher.published) > 0 {
				t.Error("a failed Resolve reached Publish")
			}
		})
	}
}

func TestUploadPipeline(t *testing.T) {
	repo := api.Repo{RepoRef: api.RepoRef{InstallationID: 11, FullName: shopName}, ID: 101, DefaultBranch: "trunk"}
	entry := manifest.Current()
	healthy := &report.Report{Verdict: "healthy", Decision: report.Decision{HardGates: "pass"}, Comparison: report.Comparison{
		ConfigHash: "aa11", MeasurementProfile: &report.MeasurementProfile{Version: entry.ProfileVersion},
	}}
	trusted := inputs.Trusted{
		SettingsFound: true, Settings: settings.Settings{Gate: settings.GateRequired, Workflow: testWorkflow},
		PolicyDigests: []string{"aa11"}, Approval: inputs.ApprovalNotRequired,
	}

	t.Run("pull request upload", func(t *testing.T) {
		f := newPipelineFakes()
		f.receiver.upload = Upload{
			Binding:  Binding{Repo: repo, Event: "pull_request", PullRequest: 7, HeadSHA: "head", BaseRef: "trunk", RunID: 9, RunAttempt: 2, WorkflowPath: testWorkflow},
			Identity: entry.Identity, Digest: "sha256:d", Report: healthy,
		}
		f.resolver.trusted = trusted
		rec := httptest.NewRecorder()
		f.gate.UploadHandler().ServeHTTP(rec, postMarker())

		if got := strings.TrimSpace(rec.Body.String()); rec.Code != 200 || got != `{"conclusion":"success","reason":"healthy"}` {
			t.Fatalf("answer = %d %s", rec.Code, got)
		}
		if !reflect.DeepEqual(f.receiver.kinds, []envelope.Kind{envelope.KindReport}) {
			t.Errorf("Receive kinds = %v", f.receiver.kinds)
		}
		deadline, ok := f.receiver.ctxs[0].Deadline()
		if !ok || time.Until(deadline) > 50*time.Second || time.Until(deadline) < 40*time.Second {
			t.Errorf("handler deadline = %v (set %v), want about 50 s", time.Until(deadline), ok)
		}
		wantTarget := inputs.Target{Repo: repo, PullRequest: 7, HeadSHA: "head", BaseRef: "trunk"}
		if !reflect.DeepEqual(f.resolver.targets, []inputs.Target{wantTarget}) || !f.resolver.nows[0].Equal(testStart) {
			t.Errorf("Resolve(%+v, %v)", f.resolver.targets, f.resolver.nows)
		}
		if len(f.publisher.published) != 1 {
			t.Fatalf("published %d times", len(f.publisher.published))
		}
		pub := f.publisher.published[0]
		want := feedback.Publication{
			Target: wantTarget, Identity: entry.Identity, Report: healthy, Trusted: trusted,
			Outcome: verdict.Decide(verdict.Facts{Report: healthy, IdentityApproved: true, WorkflowApproved: true, Trusted: trusted}),
			Receipt: feedback.Receipt{RunID: 9, RunAttempt: 2, ReportDigest: "sha256:d", PolicyDigest: "aa11", Conclusion: verdict.Success, Reason: verdict.ReasonHealthy},
		}
		if pub.Resolve == nil {
			t.Fatal("publication without Resolve: feedback cannot check the inputs again inside the lease")
		}
		f.resolver.trusted.PolicyDigests = []string{"bb22"}
		if fresh, err := pub.Resolve(context.Background()); err != nil || !reflect.DeepEqual(fresh.PolicyDigests, []string{"bb22"}) {
			t.Errorf("Resolve = %v, %v, want the resolver's current inputs", fresh.PolicyDigests, err)
		}
		if got := f.resolver.targets[len(f.resolver.targets)-1]; !reflect.DeepEqual(got, wantTarget) {
			t.Errorf("Resolve read %+v, want the decision's target %+v", got, wantTarget)
		}
		pub.Resolve = nil
		if !reflect.DeepEqual(pub, want) {
			t.Errorf("publication =\n %+v, want\n %+v", pub, want)
		}
	})

	t.Run("non-PR upload resolves against the default branch", func(t *testing.T) {
		f := newPipelineFakes()
		f.receiver.upload = Upload{
			Binding:  Binding{Repo: repo, Event: "workflow_run", HeadSHA: "head", BaseRef: "feature", Fork: true, RunID: 9, RunAttempt: 1},
			Problems: []Problem{ProblemUnsupportedEvent, ProblemFork},
		}
		f.resolver.trusted = trusted
		rec := httptest.NewRecorder()
		f.gate.UploadHandler().ServeHTTP(rec, postMarker())

		want := inputs.Target{Repo: repo, HeadSHA: "head", BaseRef: "trunk", Fork: true}
		if !reflect.DeepEqual(f.resolver.targets, []inputs.Target{want}) {
			t.Errorf("target = %+v, want %+v", f.resolver.targets, want)
		}
		if got := strings.TrimSpace(rec.Body.String()); got != `{"conclusion":"action_required","reason":"fork_unsupported"}` {
			t.Errorf("answer = %s", got)
		}
	})

	// A pull request into another branch is judged against the default branch, never its own base, and never succeeds:
	// the base could carry a policy the same change loosened.
	t.Run("pull request into another base never succeeds", func(t *testing.T) {
		f := newPipelineFakes()
		f.receiver.upload = Upload{
			Binding:  Binding{Repo: repo, Event: "pull_request", PullRequest: 7, HeadSHA: "head", BaseRef: "release", RunID: 9, RunAttempt: 2, WorkflowPath: testWorkflow},
			Identity: entry.Identity, Digest: "sha256:d", Report: healthy,
		}
		f.resolver.trusted = trusted
		rec := httptest.NewRecorder()
		f.gate.UploadHandler().ServeHTTP(rec, postMarker())

		if got := strings.TrimSpace(rec.Body.String()); got != `{"conclusion":"action_required","reason":"unsupported_event"}` {
			t.Errorf("answer = %s", got)
		}
		want := inputs.Target{Repo: repo, PullRequest: 7, HeadSHA: "head", BaseRef: "trunk"}
		if !reflect.DeepEqual(f.resolver.targets, []inputs.Target{want}) {
			t.Errorf("Resolve targets = %+v, want the default branch %+v", f.resolver.targets, want)
		}
		for _, pub := range f.publisher.published {
			if pub.Outcome.Conclusion == verdict.Success {
				t.Errorf("published a success for a pull request into another base: %+v", pub.Receipt)
			}
		}
	})

	// Only a run on a push to the default branch says something about the default branch, and only once its decision
	// is published.
	t.Run("only a published default-branch push reaches the repair issue", func(t *testing.T) {
		tests := []struct {
			name       string
			binding    Binding
			publishErr error
			want       int
		}{
			{"push to the default branch", Binding{Repo: repo, Event: "push", HeadSHA: "head", BaseRef: "trunk"}, nil, 1},
			{"push to another branch", Binding{Repo: repo, Event: "push", HeadSHA: "head", BaseRef: "feature"}, nil, 0},
			{"pull request", Binding{Repo: repo, Event: "pull_request", PullRequest: 7, HeadSHA: "head", BaseRef: "trunk"}, nil, 0},
			{"unbound pull request run", Binding{Repo: repo, Event: "pull_request", HeadSHA: "head", BaseRef: "trunk"}, nil, 0},
			{"dispatched run on the default branch", Binding{Repo: repo, Event: "workflow_dispatch", HeadSHA: "head", BaseRef: "trunk"}, nil, 0},
			{"publication failed", Binding{Repo: repo, Event: "push", HeadSHA: "head", BaseRef: "trunk"}, errors.New("checks down"), 0},
		}
		for _, tt := range tests {
			t.Run(tt.name, func(t *testing.T) {
				f := newPipelineFakes()
				f.receiver.upload = Upload{Binding: tt.binding, Identity: entry.Identity, Report: healthy}
				f.resolver.trusted = trusted
				f.publisher.publishErr = tt.publishErr
				f.gate.UploadHandler().ServeHTTP(httptest.NewRecorder(), postMarker())
				if len(f.repairer.repaired) != tt.want {
					t.Fatalf("repaired %d times, want %d", len(f.repairer.repaired), tt.want)
				}
				if tt.want == 1 && !reflect.DeepEqual(f.repairer.repaired[0].Receipt, f.publisher.published[0].Receipt) {
					t.Errorf("repaired %+v, want the published receipt %+v", f.repairer.repaired[0].Receipt, f.publisher.published[0].Receipt)
				}
			})
		}
	})

	t.Run("claimed digests reach the decision unchanged", func(t *testing.T) {
		f := newPipelineFakes()
		f.receiver.upload = Upload{
			Binding:  Binding{Repo: repo, PullRequest: 7, HeadSHA: "head", BaseRef: "trunk", WorkflowPath: testWorkflow},
			Identity: entry.Identity, Report: healthy, BaselineDigest: "claimed-baseline",
		}
		f.resolver.trusted = trusted
		rec := httptest.NewRecorder()
		f.gate.UploadHandler().ServeHTTP(rec, postMarker())
		if got := strings.TrimSpace(rec.Body.String()); got != `{"conclusion":"action_required","reason":"baseline_mismatch"}` {
			t.Errorf("answer = %s", got)
		}
	})
}

func TestHandleEventRules(t *testing.T) {
	ctx := context.Background()
	pullEvent := func(kind webhook.EventKind) webhook.Event {
		return webhook.Event{Kind: kind, InstallationID: 11, Repo: shopName, RepoPrivate: true, PullRequest: 7,
			HeadSHA: "head", HeadRepo: shopName, BaseRef: "main"}
	}
	wantTarget := inputs.Target{
		Repo:        api.Repo{RepoRef: api.RepoRef{InstallationID: 11, FullName: shopName}, Private: true},
		PullRequest: 7, HeadSHA: "head", BaseRef: "main",
	}
	required := inputs.Trusted{SettingsFound: true, Settings: settings.Settings{Gate: settings.GateRequired}}
	missing := feedback.CheckState{Exists: true, Receipt: feedback.Receipt{RunID: 9, RunAttempt: 2, Reason: verdict.ReasonOwnerApprovalMissing}}
	granted := inputs.Trusted{SettingsFound: true, Approval: inputs.ApprovalGranted}
	boom := errors.New("boom")

	t.Run("target is built from the verified event", func(t *testing.T) {
		f := newPipelineFakes()
		f.resolver.trusted = required
		if err := f.gate.HandleEvent(ctx, pullEvent(webhook.PullOpened)); err != nil {
			t.Fatal(err)
		}
		if !reflect.DeepEqual(f.resolver.targets, []inputs.Target{wantTarget}) || !reflect.DeepEqual(f.publisher.pended, []inputs.Target{wantTarget}) {
			t.Errorf("resolved %+v, pended %+v", f.resolver.targets, f.publisher.pended)
		}
	})

	t.Run("a fork of a repository that is not onboarded gets nothing", func(t *testing.T) {
		f := newPipelineFakes()
		e := pullEvent(webhook.PullOpened)
		e.HeadRepo = "mallory/shop"
		if err := f.gate.HandleEvent(ctx, e); err != nil {
			t.Fatal(err)
		}
		if len(f.publisher.published)+len(f.publisher.pended) != 0 {
			t.Errorf("published %d, pended %d", len(f.publisher.published), len(f.publisher.pended))
		}
	})

	t.Run("a deleted fork repository is a fork", func(t *testing.T) {
		f := newPipelineFakes()
		f.resolver.trusted = inputs.Trusted{
			SettingsFound: true, Settings: settings.Settings{Gate: settings.GateRequired},
			PolicyDigests: []string{"aa11", "bb22"}, BaselineDigests: []string{"cc33"}, EarliestWaiverExpiry: testStart.Add(time.Hour),
		}
		e := pullEvent(webhook.PullSynchronized)
		e.HeadRepo = ""
		if err := f.gate.HandleEvent(ctx, e); err != nil {
			t.Fatal(err)
		}
		if len(f.publisher.pended) != 0 || len(f.publisher.published) != 1 {
			t.Fatalf("pended %d, published %d", len(f.publisher.pended), len(f.publisher.published))
		}
		forked := wantTarget
		forked.Fork = true
		want := feedback.Publication{
			Target: forked, Outcome: verdict.Without(verdict.ReasonForkUnsupported), Trusted: f.resolver.trusted,
			Receipt: feedback.Receipt{PolicyDigest: "aa11", BaselineDigest: "cc33", EarliestWaiverExpiry: testStart.Add(time.Hour),
				Conclusion: verdict.ActionRequired, Reason: verdict.ReasonForkUnsupported},
		}
		if !reflect.DeepEqual(f.publisher.published[0], want) {
			t.Errorf("publication =\n %+v, want\n %+v", f.publisher.published[0], want)
		}
	})

	noRun := feedback.CheckState{Exists: true, Receipt: feedback.Receipt{Reason: verdict.ReasonOwnerApprovalMissing}}
	rerun := []struct {
		name    string
		state   feedback.CheckState
		trusted inputs.Trusted
		run     api.WorkflowRun
		reran   bool
	}{
		{"receipt without a run", noRun, granted, api.WorkflowRun{Status: runCompleted}, false},
		{"completed run at the receipt's attempt", missing, granted, api.WorkflowRun{Status: runCompleted, Attempt: 2}, true},
		{"run in progress", missing, granted, api.WorkflowRun{Status: runInProgress, Attempt: 2}, false},
		{"newer attempt already ran", missing, granted, api.WorkflowRun{Status: runCompleted, Attempt: 3}, false},
		{"approval still missing", missing, inputs.Trusted{SettingsFound: true, Approval: inputs.ApprovalMissing}, api.WorkflowRun{Status: runCompleted, Attempt: 2}, false},
		{"approval no longer needed", missing, inputs.Trusted{SettingsFound: true, Approval: inputs.ApprovalNotRequired}, api.WorkflowRun{Status: runCompleted, Attempt: 2}, true},
		{"another receipt reason", feedback.CheckState{Exists: true, Receipt: feedback.Receipt{RunID: 9, RunAttempt: 2, Reason: verdict.ReasonPolicyMismatch}}, granted, api.WorkflowRun{Status: runCompleted, Attempt: 2}, false},
		{"pending check", feedback.CheckState{Exists: true, Pending: true}, granted, api.WorkflowRun{Status: runCompleted}, false},
		{"no check", feedback.CheckState{}, granted, api.WorkflowRun{Status: runCompleted}, false},
	}
	for _, tc := range rerun {
		t.Run("review: "+tc.name, func(t *testing.T) {
			f := newPipelineFakes()
			f.publisher.state, f.resolver.trusted, f.actions.run = tc.state, tc.trusted, tc.run
			if err := f.gate.HandleEvent(ctx, pullEvent(webhook.ReviewChanged)); err != nil {
				t.Fatal(err)
			}
			if got := len(f.actions.reran) == 1; got != tc.reran {
				t.Errorf("reran = %v, want %v", f.actions.reran, tc.reran)
			}
			if tc.reran && (f.actions.reran[0] != 9 || f.actions.refs[0] != wantTarget.Repo.RepoRef) {
				t.Errorf("re-ran run %v of %+v", f.actions.reran, f.actions.refs)
			}
			if len(f.publisher.published)+len(f.publisher.pended) != 0 {
				t.Error("a review event wrote feedback")
			}
			if tc.state.Receipt.RunID == 0 && len(f.actions.ran) != 0 {
				t.Errorf("a receipt without a run read runs %v", f.actions.ran)
			}
		})
	}

	// One behavior with the sweep: a run that is gone is skipped, and rule 7b of the next pass repairs it.
	t.Run("review: run not found is skipped", func(t *testing.T) {
		f := newPipelineFakes()
		f.publisher.state, f.resolver.trusted = missing, granted
		f.actions.runErr = fmt.Errorf("run 9: %w", api.ErrNotFound)
		if err := f.gate.HandleEvent(ctx, pullEvent(webhook.ReviewChanged)); err != nil {
			t.Fatalf("err = %v, want nil", err)
		}
		if len(f.actions.ran) != 1 || len(f.actions.reran) != 0 || len(f.publisher.published) != 0 {
			t.Errorf("ran %v, reran %v, published %d", f.actions.ran, f.actions.reran, len(f.publisher.published))
		}
	})

	green := feedback.CheckState{Exists: true, Receipt: feedback.Receipt{
		RunID: 9, RunAttempt: 2, ReportDigest: "rd", PolicyDigest: "aa11", Conclusion: verdict.Success, Reason: verdict.ReasonHealthy,
	}}
	withdrawn := inputs.Trusted{
		SettingsFound: true, Settings: settings.Settings{Gate: settings.GateRequired}, PolicyDigests: []string{"aa11"},
		ProtectedPaths: []string{settings.Path}, Approval: inputs.ApprovalMissing,
	}
	t.Run("review: a withdrawn approval ends a green receipt", func(t *testing.T) {
		f := newPipelineFakes()
		f.publisher.state, f.resolver.trusted = green, withdrawn
		if err := f.gate.HandleEvent(ctx, pullEvent(webhook.ReviewChanged)); err != nil {
			t.Fatal(err)
		}
		if len(f.publisher.published) != 1 || len(f.actions.ran)+len(f.actions.reran) != 0 {
			t.Fatalf("published %d, ran %v, reran %v; want one publication and no re-run",
				len(f.publisher.published), f.actions.ran, f.actions.reran)
		}
		pub := f.publisher.published[0]
		want := sweep.Without(verdict.ReasonOwnerApprovalMissing, wantTarget, withdrawn, green.Receipt)
		if !reflect.DeepEqual(pub, want) {
			t.Errorf("publication =\n %+v, want\n %+v", pub, want)
		}
		if !slices.Equal(f.resolver.nows, []time.Time{testStart}) || len(f.publisher.states) != 1 {
			t.Errorf("resolved at %v after %d check reads, want one of each inside Reconcile", f.resolver.nows, len(f.publisher.states))
		}
	})
	quiet := []struct {
		name    string
		state   feedback.CheckState
		trusted inputs.Trusted
	}{
		{"the approval still holds", green, func() inputs.Trusted { tr := withdrawn; tr.Approval = inputs.ApprovalGranted; return tr }()},
		{"advisory mode", green, func() inputs.Trusted { tr := withdrawn; tr.Settings.Gate = settings.GateAdvisory; return tr }()},
		{"the check already asks for action", feedback.CheckState{Exists: true, Receipt: feedback.Receipt{
			RunID: 9, RunAttempt: 2, Conclusion: verdict.ActionRequired, Reason: verdict.ReasonPolicyMismatch,
		}}, withdrawn},
	}
	for _, tc := range quiet {
		t.Run("review: nothing to withdraw: "+tc.name, func(t *testing.T) {
			f := newPipelineFakes()
			f.publisher.state, f.resolver.trusted = tc.state, tc.trusted
			if err := f.gate.HandleEvent(ctx, pullEvent(webhook.ReviewChanged)); err != nil {
				t.Fatal(err)
			}
			if len(f.publisher.published) != 0 || len(f.actions.reran) != 0 {
				t.Errorf("published %d, reran %v", len(f.publisher.published), f.actions.reran)
			}
		})
	}

	errs := []struct {
		name  string
		kind  webhook.EventKind
		setup func(*pipelineFakes)
	}{
		{"resolve", webhook.PullOpened, func(f *pipelineFakes) { f.resolver.err = boom }},
		{"pending", webhook.PullOpened, func(f *pipelineFakes) { f.resolver.trusted = required; f.publisher.pendingErr = boom }},
		{"state", webhook.ReviewChanged, func(f *pipelineFakes) { f.publisher.stateErr = boom }},
		{"review resolve", webhook.ReviewChanged, func(f *pipelineFakes) { f.publisher.state = missing; f.resolver.err = boom }},
		{"run", webhook.ReviewChanged, func(f *pipelineFakes) {
			f.publisher.state, f.resolver.trusted, f.actions.runErr = missing, granted, boom
		}},
		{"rerun", webhook.ReviewChanged, func(f *pipelineFakes) {
			f.publisher.state, f.resolver.trusted, f.actions.rerunErr = missing, granted, boom
			f.actions.run = api.WorkflowRun{Status: runCompleted, Attempt: 2}
		}},
		{"repository sweep", webhook.DefaultBranchPush, func(f *pipelineFakes) { f.sweeper.err = boom }},
		{"withdrawal", webhook.ReviewChanged, func(f *pipelineFakes) {
			f.publisher.state, f.resolver.trusted, f.publisher.publishErr = green, withdrawn, boom
		}},
	}
	for _, tc := range errs {
		t.Run("error is returned: "+tc.name, func(t *testing.T) {
			f := newPipelineFakes()
			tc.setup(f)
			if err := f.gate.HandleEvent(ctx, pullEvent(tc.kind)); !errors.Is(err, boom) {
				t.Errorf("err = %v, want boom", err)
			}
			if len(f.publisher.published) != 0 {
				t.Error("an error path published")
			}
		})
	}

	t.Run("sweep passes the clock and the summary through", func(t *testing.T) {
		f := newPipelineFakes()
		f.sweeper.summary = Summary{Repos: 3, RateLimited: true}
		got, err := f.gate.Sweep(ctx)
		if err != nil || got != f.sweeper.summary || len(f.sweeper.runs) != 1 || !f.sweeper.runs[0].Equal(testStart) {
			t.Errorf("Sweep = %+v, %v (runs %v)", got, err, f.sweeper.runs)
		}
		f.sweeper.err = boom
		if _, err := f.gate.Sweep(ctx); !errors.Is(err, boom) {
			t.Errorf("err = %v, want boom", err)
		}
	})
}

// TestNewComposesTheChildren drives the real constructor as far as it goes without GitHub.
func TestNewComposesTheChildren(t *testing.T) {
	g := New(Deps{GitHub: api.New(nil, nil), OIDC: &fakeVerifier{audience: testAudience}, Audience: testAudience})
	if g.now == nil || g.now().IsZero() {
		t.Fatal("a nil Deps.Now must mean time.Now")
	}
	req := httptest.NewRequest(http.MethodPost, "/v1/reports", strings.NewReader("{}"))
	if a := serve(t, g, req); a.status != 400 || a.Error == "" {
		t.Errorf("no envelope: %d %s", a.status, a.raw)
	}
}

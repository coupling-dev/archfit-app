package onboarding

import (
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"reflect"
	"slices"
	"strings"
	"testing"

	"github.com/alexei-led/archfit-app/internal/gate"
	"github.com/alexei-led/archfit-app/internal/github/api"
	"github.com/alexei-led/archfit-app/internal/wire/envelope"
	"github.com/alexei-led/archfit-app/internal/wire/manifest"
	"github.com/alexei-led/archfit-app/internal/wire/policy"
	"github.com/alexei-led/archfit-app/internal/wire/settings"
)

// The shared corpus: real engine v3.0.0 captures, defined in internal/wire/module.md.
const (
	corpusReport = "../../wire/testdata/report"
	capturedTwo  = "real-baselined-comparable" // two accepted findings
	capturedNone = "real-attention-baseline"   // no accepted finding, same policy
	fingerprintA = "ba3803eca947bf3c1b7efa8f37854d5e"
	fingerprintB = "07af466df8bfd6e77ee485ac01c50a17"
)

func corpus(t *testing.T, name, suffix string) []byte {
	t.Helper()
	raw, err := os.ReadFile(filepath.Join(corpusReport, name+suffix))
	if err != nil {
		t.Fatal(err)
	}
	return raw
}

// syntheticBaseline is a v3 baseline captured under policyText with the given accepted findings.
func syntheticBaseline(policyText []byte, profile string, accepted ...[2]string) []byte {
	return syntheticBaselineWith(policyText, profile, nil, "{}", accepted...)
}

// syntheticBaselineWith is a v3 baseline with the given qualifying seams and metrics block, a raw JSON object.
func syntheticBaselineWith(policyText []byte, profile string, seams []string, metrics string, accepted ...[2]string) []byte {
	entries := make([]string, len(accepted))
	for i, a := range accepted {
		entries[i] = fmt.Sprintf(`{"fingerprint":%q,"rule_id":%q,"kind":"gate"}`, a[0], a[1])
	}
	quoted := make([]string, len(seams))
	for i, id := range seams {
		quoted[i] = fmt.Sprintf("%q", id)
	}
	return fmt.Appendf(nil, `{"schema_version":"archfit.baseline.v3","accepted":[%s],"metrics":%s,`+
		`"state":{"config_hash":%q,"qualifying_seam_ids":[%s],"measurement_profile":{"version":%q}}}`,
		strings.Join(entries, ","), metrics, policy.Digest(policyText), strings.Join(quoted, ","), profile)
}

// protect puts the protected inputs on the default branch at runHeadSHA, the head of the capturing run.
// A nil labels or baseline means no such file.
func (f *fixture) protect(policyText, labels, baseline []byte) {
	r := f.repo
	f.hub.setHead(r, r.DefaultBranch, runHeadSHA)
	f.hub.put(r, runHeadSHA, settings.Path, renderSettings(settings.GateAdvisory, workflowPath))
	f.hub.put(r, runHeadSHA, workflowPath, []byte("name: archfit\n"))
	f.hub.put(r, runHeadSHA, policy.PolicyPath, policyText)
	if labels != nil {
		f.hub.put(r, runHeadSHA, policy.LabelsPath, labels)
	}
	if baseline != nil {
		f.hub.put(r, runHeadSHA, policy.BaselinePath, baseline)
	}
}

func baselineUpload(r api.Repo, payload []byte, labelsDigest string) gate.Upload {
	return gate.Upload{
		Binding: gate.Binding{
			Repo: r, Event: "workflow_dispatch", HeadSHA: runHeadSHA, BaseRef: r.DefaultBranch,
			RunID: 9002, RunAttempt: 1, WorkflowPath: workflowPath,
		},
		Identity:     manifest.Current().Identity,
		Digest:       envelope.PayloadDigest(payload),
		LabelsDigest: labelsDigest,
		Payload:      payload,
	}
}

func (f *fixture) postBaseline(t *testing.T) uploadResponse {
	t.Helper()
	req := httptest.NewRequest(http.MethodPost, "/v1/baselines", strings.NewReader("ignored: the fake Receiver supplies the upload"))
	rec := httptest.NewRecorder()
	f.svc.BaselineHandler().ServeHTTP(rec, req)
	out := uploadResponse{status: rec.Code, raw: rec.Body.String()}
	if err := json.Unmarshal(rec.Body.Bytes(), &out); err != nil {
		t.Fatalf("response is not JSON: %q", out.raw)
	}
	return out
}

// wantProtectedUntouched: the App wrote nothing to the default branch, neither by name nor at its head commit.
func (f *fixture) wantProtectedUntouched(t *testing.T, before map[string][]byte) {
	t.Helper()
	if got := f.hub.paths(f.repo, f.repo.DefaultBranch); len(got) != 0 {
		t.Errorf("files written to %s: %v", f.repo.DefaultBranch, got)
	}
	for path, want := range before {
		if got, _ := f.hub.file(f.repo, runHeadSHA, path); string(got) != string(want) {
			t.Errorf("%s changed on the protected head", path)
		}
	}
}

func (f *fixture) protectedSnapshot() map[string][]byte {
	out := map[string][]byte{}
	for _, p := range f.hub.paths(f.repo, runHeadSHA) {
		b, _ := f.hub.file(f.repo, runHeadSHA, p)
		out[p] = b
	}
	return out
}

func TestBaselineUploadOpensBaselinePR(t *testing.T) {
	policyText, labels := corpus(t, capturedTwo, ".policy.yaml"), []byte(nil)
	capture, current := corpus(t, capturedNone, ".baseline.json"), corpus(t, capturedTwo, ".baseline.json")

	f := newFixture(t)
	f.protect(policyText, labels, current)
	before := f.protectedSnapshot()
	f.receiver.upload = baselineUpload(f.repo, capture, "")

	res := f.postBaseline(t)
	if res.status != 200 || res.Number != 1 || !strings.HasSuffix(res.URL, "/pull/1") {
		t.Fatalf("response = %d %s", res.status, res.raw)
	}
	if !reflect.DeepEqual(f.receiver.kinds, []envelope.Kind{envelope.KindBaseline}) {
		t.Errorf("Receive kinds = %v", f.receiver.kinds)
	}
	if n := f.hub.count("Propose"); n != 1 {
		t.Errorf("%d Propose calls, want 1", n)
	}
	pr := f.hub.newest(f.repo.FullName, baselineBranch)
	if pr == nil || pr.base != f.repo.DefaultBranch || !pr.open {
		t.Fatalf("pull request = %+v, want one open PR from %s into %s", pr, baselineBranch, f.repo.DefaultBranch)
	}
	if got := f.hub.paths(f.repo, baselineBranch); !reflect.DeepEqual(got, []string{policy.BaselinePath}) {
		t.Errorf("files = %v, want only the baseline", got)
	}
	if got, _ := f.hub.file(f.repo, baselineBranch, policy.BaselinePath); string(got) != string(capture) {
		t.Error("the committed baseline differs from the uploaded bytes")
	}
	f.wantProtectedUntouched(t, before)

	body := f.hub.body(f.repo, baselineBranch)
	for _, want := range []string{
		"Approving accepts no finding", "| Removed | 2 |", "| Added | 0 |", fingerprintA, fingerprintB,
		"`no_direct_b_dependency`", "9002", f.receiver.upload.Digest, runHeadSHA, policy.Digest(policyText),
		"architecture owner", "exact head commit", "customer-attested",
	} {
		if !strings.Contains(body, want) {
			t.Errorf("PR body does not mention %q:\n%s", want, body)
		}
	}
	if strings.Contains(strings.ToLower(body), "verified") {
		t.Errorf("PR body claims verification:\n%s", body)
	}
	if n := f.hub.forbiddenCalls(); n != 0 {
		t.Errorf("%d check or Actions write calls", n)
	}
}

func TestBaselineFirstAdoptionCountsEveryFindingAsAdded(t *testing.T) {
	policyText, labels := corpus(t, "real-baselined-with-labels", ".policy.yaml"), corpus(t, "real-baselined-with-labels", ".labels.yaml")
	capture := corpus(t, "real-baselined-with-labels", ".baseline.json")
	f := newFixture(t)
	f.protect(policyText, labels, nil)
	f.receiver.upload = baselineUpload(f.repo, capture, policy.Digest(labels))

	if res := f.postBaseline(t); res.status != 200 || res.Number != 1 {
		t.Fatalf("response = %d %s", res.status, res.raw)
	}
	body := f.hub.body(f.repo, baselineBranch)
	for _, want := range []string{"Approving accepts 1 finding", "| Added | 1 |", "| Removed | 0 |", "no baseline", policy.Digest(labels)} {
		if !strings.Contains(body, want) {
			t.Errorf("PR body does not mention %q:\n%s", want, body)
		}
	}
}

func TestBaselineUnreadableProtectedBaselineOvercountsAdded(t *testing.T) {
	policyText := corpus(t, capturedTwo, ".policy.yaml")
	f := newFixture(t)
	f.protect(policyText, nil, []byte(`{"schema_version":"archfit.baseline.v1","accepted":[]}`))
	f.receiver.upload = baselineUpload(f.repo, corpus(t, capturedTwo, ".baseline.json"), "")

	if res := f.postBaseline(t); res.status != 200 {
		t.Fatalf("response = %d %s", res.status, res.raw)
	}
	body := f.hub.body(f.repo, baselineBranch)
	for _, want := range []string{"not a readable", "Approving accepts 2 findings", "| Removed | unknown |"} {
		if !strings.Contains(body, want) {
			t.Errorf("PR body does not mention %q:\n%s", want, body)
		}
	}
}

func TestBaselineUnchangedOpensNothing(t *testing.T) {
	policyText, current := corpus(t, capturedTwo, ".policy.yaml"), corpus(t, capturedTwo, ".baseline.json")
	f := newFixture(t)
	f.protect(policyText, nil, current)
	f.receiver.upload = baselineUpload(f.repo, current, "")

	res := f.postBaseline(t)
	if res.status != 200 || !res.Unchanged || res.Number != 0 {
		t.Fatalf("response = %d %s, want 200 unchanged", res.status, res.raw)
	}
	if n := f.hub.count("Propose") + f.hub.count("SetBody"); n != 0 {
		t.Errorf("%d write calls for an unchanged baseline", n)
	}
	if n := f.hub.prCount(f.repo, ""); n != 0 {
		t.Errorf("%d pull requests opened", n)
	}
}

func TestBaselineReuploadUpdatesTheSamePR(t *testing.T) {
	policyText := corpus(t, capturedTwo, ".policy.yaml")
	first := syntheticBaseline(policyText, "archfit.measurement.v2", [2]string{fingerprintA, "no_direct_b_dependency"})
	second := syntheticBaseline(policyText, "archfit.measurement.v2",
		[2]string{fingerprintA, "no_direct_b_dependency"}, [2]string{fingerprintB, "bc/imbalanced_coupling"})

	f := newFixture(t)
	f.protect(policyText, nil, nil)
	f.receiver.upload = baselineUpload(f.repo, first, "")
	for range 2 { // the producer retries the same capture
		if res := f.postBaseline(t); res.status != 200 || res.Number != 1 {
			t.Fatalf("response = %d %s", res.status, res.raw)
		}
	}
	firstBody := f.hub.body(f.repo, baselineBranch)

	f.receiver.upload = baselineUpload(f.repo, second, "")
	if res := f.postBaseline(t); res.status != 200 || res.Number != 1 {
		t.Fatalf("re-upload response = %d %s", res.status, res.raw)
	}
	if n := f.hub.prCount(f.repo, baselineBranch); n != 1 {
		t.Errorf("%d pull requests on %s, want 1", n, baselineBranch)
	}
	if got, _ := f.hub.file(f.repo, baselineBranch, policy.BaselinePath); string(got) != string(second) {
		t.Error("the branch does not carry the newest capture")
	}
	body := f.hub.body(f.repo, baselineBranch)
	if body == firstBody || !strings.Contains(body, "Approving accepts 2 findings") || !strings.Contains(body, envelope.PayloadDigest(second)) {
		t.Errorf("the body was not rewritten for the newest capture:\n%s", body)
	}
	if got := f.hub.paths(f.repo, f.repo.DefaultBranch); len(got) != 0 {
		t.Errorf("files written to the default branch: %v", got)
	}
}

func TestBaselineStatusMapping(t *testing.T) {
	policyText := corpus(t, capturedTwo, ".policy.yaml")
	capture := corpus(t, capturedTwo, ".baseline.json")
	labels := []byte("labels: []\n")
	tests := []struct {
		name       string
		receiveErr error
		mutate     func(f *fixture, u *gate.Upload)
		status     int
		code       string
		proposes   int
	}{
		{name: "rejection 401", receiveErr: &gate.Rejection{Status: 401, Code: "token_invalid"}, status: 401, code: "token_invalid"},
		{name: "rejection 409", receiveErr: &gate.Rejection{Status: 409, Code: "stale_attempt"}, status: 409, code: "stale_attempt"},
		{name: "rejection 413", receiveErr: &gate.Rejection{Status: 413, Code: "payload_too_large"}, status: 413, code: "payload_too_large"},
		{name: "receive rate limited", receiveErr: api.ErrRateLimited, status: 503, code: "rate_limited"},
		{name: "receive error", receiveErr: errors.New("boom"), status: 500, code: "internal"},
		{name: "pull_request run", mutate: func(_ *fixture, u *gate.Upload) {
			u.Binding.Event = "pull_request"
			u.Problems = []gate.Problem{gate.ProblemUnsupportedEvent}
		}, status: 400, code: "unsupported_event"},
		{name: "envelope mismatch", mutate: func(_ *fixture, u *gate.Upload) { u.Problems = []gate.Problem{gate.ProblemEnvelopeMismatch} }, status: 400, code: "envelope_mismatch"},
		{name: "fork", mutate: func(_ *fixture, u *gate.Upload) { u.Problems = []gate.Problem{gate.ProblemFork} }, status: 400, code: "fork"},
		{name: "wrong branch", mutate: func(_ *fixture, u *gate.Upload) { u.Binding.BaseRef = "feature" }, status: 403, code: "not_default_branch"},
		{name: "default branch moved", mutate: func(f *fixture, _ *gate.Upload) {
			f.hub.setHead(f.repo, f.repo.DefaultBranch, strings.Repeat("2", 40))
		}, status: 409, code: "stale_head"},
		{name: "branch head rate limited", mutate: func(f *fixture, _ *gate.Upload) { f.hub.failOn("BranchHead", api.ErrRateLimited) }, status: 503, code: "rate_limited"},
		{name: "settings missing", mutate: func(f *fixture, _ *gate.Upload) {
			f.hub.put(f.repo, runHeadSHA, settings.Path, []byte("owners: nobody knows\n"))
		}, status: 403, code: "settings_missing"},
		{name: "settings only on the run's branch name", mutate: func(f *fixture, _ *gate.Upload) {
			f.hub.put(f.repo, f.repo.DefaultBranch, settings.Path, renderSettings(settings.GateAdvisory, ".github/workflows/ci.yaml"))
		}, status: 200, proposes: 1},
		{name: "other workflow", mutate: func(_ *fixture, u *gate.Upload) { u.Binding.WorkflowPath = ".github/workflows/ci.yaml" }, status: 403, code: "workflow_not_approved"},
		{name: "unlisted image", mutate: func(_ *fixture, u *gate.Upload) { u.Identity.ImageDigest = "sha256:" + strings.Repeat("0", 64) }, status: 403, code: "unknown_engine_identity"},
		{name: "unapproved profile", mutate: func(_ *fixture, u *gate.Upload) {
			u.Payload = syntheticBaseline(policyText, "archfit.measurement.v9")
		}, status: 403, code: "unknown_engine_identity"},
		{name: "oversized", mutate: func(_ *fixture, u *gate.Upload) {
			u.Payload = append(capture, make([]byte, policy.MaxBaselineBytes)...)
		}, status: 413, code: "baseline_too_large"},
		{name: "a policy, not a baseline", mutate: func(_ *fixture, u *gate.Upload) { u.Payload = policyText }, status: 400, code: "baseline_invalid"},
		{name: "an engine report", mutate: func(_ *fixture, u *gate.Upload) { u.Payload = corpus(t, capturedTwo, ".json") }, status: 400, code: "baseline_invalid"},
		{name: "schema v1", mutate: func(_ *fixture, u *gate.Upload) {
			u.Payload = []byte(strings.Replace(string(capture), "archfit.baseline.v3", "archfit.baseline.v1", 1))
		}, status: 400, code: "baseline_invalid"},
		{name: "policy missing", mutate: func(f *fixture, _ *gate.Upload) { f.hub.remove(f.repo, runHeadSHA, policy.PolicyPath) }, status: 403, code: "policy_missing"},
		{name: "captured under another policy", mutate: func(f *fixture, _ *gate.Upload) {
			f.hub.put(f.repo, runHeadSHA, policy.PolicyPath, append(policyText, []byte("# edited\n")...))
		}, status: 403, code: "policy_mismatch"},
		{name: "head policy on the branch name is ignored", mutate: func(f *fixture, _ *gate.Upload) {
			f.hub.put(f.repo, f.repo.DefaultBranch, policy.PolicyPath, []byte("version: 2\n"))
		}, status: 200, proposes: 1},
		{name: "labels claimed, none protected", mutate: func(_ *fixture, u *gate.Upload) { u.LabelsDigest = policy.Digest(labels) }, status: 403, code: "labels_mismatch"},
		{name: "labels protected, none claimed", mutate: func(f *fixture, _ *gate.Upload) {
			f.hub.put(f.repo, runHeadSHA, policy.LabelsPath, labels)
		}, status: 403, code: "labels_mismatch"},
		{name: "labels protected and claimed", mutate: func(f *fixture, u *gate.Upload) {
			f.hub.put(f.repo, runHeadSHA, policy.LabelsPath, labels)
			u.LabelsDigest = policy.Digest(labels)
		}, status: 200, proposes: 1},
		{name: "Propose rate limited", mutate: func(f *fixture, _ *gate.Upload) { f.hub.failOn("Propose", api.ErrRateLimited) }, status: 503, code: "rate_limited", proposes: 1},
		{name: "Propose fails", mutate: func(f *fixture, _ *gate.Upload) { f.hub.failOn("Propose", errors.New("boom")) }, status: 500, code: "internal", proposes: 1},
		{name: "SetBody fails", mutate: func(f *fixture, _ *gate.Upload) { f.hub.failOn("SetBody", errors.New("boom")) }, status: 500, code: "internal", proposes: 1},
	}
	permanent := map[int]bool{400: true, 401: true, 403: true, 409: true, 413: true}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			f := newFixture(t)
			f.protect(policyText, nil, nil)
			f.receiver.upload = baselineUpload(f.repo, capture, "")
			f.receiver.err = tc.receiveErr
			if tc.mutate != nil {
				tc.mutate(f, &f.receiver.upload)
			}
			res := f.postBaseline(t)
			if res.status != tc.status || res.Error != tc.code {
				t.Errorf("got %d %q, want %d %q (%s)", res.status, res.Error, tc.status, tc.code, res.raw)
			}
			if res.status != 200 && !permanent[res.status] && res.status != 503 && res.status != 500 {
				t.Errorf("status %d is outside the producer's sets", res.status)
			}
			if n := f.hub.count("Propose"); n != tc.proposes {
				t.Errorf("%d Propose calls, want %d", n, tc.proposes)
			}
			if res.status != 200 && f.hub.newest(f.repo.FullName, baselineBranch) != nil && tc.proposes == 0 {
				t.Error("a refused upload opened a pull request")
			}
			if got := f.hub.paths(f.repo, f.repo.DefaultBranch); res.status != 200 && slices.Contains(got, policy.BaselinePath) {
				t.Error("a baseline was written to the default branch")
			}
		})
	}
}

func TestBaselineBodyAndLogsCarryOnlyIDs(t *testing.T) {
	const marker = "LEAK-BASELINE-7f3a"
	logs := captureLogs(t)
	policyText := corpus(t, capturedTwo, ".policy.yaml")
	// The engine ignores unknown keys and so does the App; the bytes are committed, the text never echoed.
	payload := []byte(strings.Replace(string(corpus(t, capturedTwo, ".baseline.json")),
		`"accepted": [`, `"note": "`+marker+`", "accepted": [`, 1))
	f := newFixture(t)
	f.protect(policyText, nil, nil)
	f.receiver.upload = baselineUpload(f.repo, payload, "")

	res := f.postBaseline(t)
	if res.status != 200 {
		t.Fatalf("response = %d %s", res.status, res.raw)
	}
	if got, _ := f.hub.file(f.repo, baselineBranch, policy.BaselinePath); !strings.Contains(string(got), marker) {
		t.Fatal("the fixture edit did not apply; the assertion below checks nothing")
	}
	if strings.Contains(f.hub.body(f.repo, baselineBranch), marker) || strings.Contains(res.raw, marker) {
		t.Error("the marker reached the PR body or the response")
	}
	if strings.Contains(logs.String(), marker) {
		t.Error("the marker reached a log line")
	}
	if !strings.Contains(logs.String(), "baseline upload proposed") {
		t.Error("the upload was not logged; the log assertion checked nothing")
	}
}

// Git ref names may hold a backtick, "<", ">", "!", "-" and "|". The default branch is GitHub-supplied and reaches
// the baseline PR body in code spans, the table included.
func TestBaselineBodySanitizesDefaultBranch(t *testing.T) {
	capture := policy.Baseline{Accepted: []policy.Accepted{{Fingerprint: fingerprintA, RuleID: "layers/no_upward"}}}
	for _, tt := range []struct {
		branch, want string
	}{
		{"main", "`main`"},
		{"rel`ease", "`release`"},
		{"a<!--b", "`ab`"},
		{"x-->y", "`xy`"},
		{"<!<!---->--x", ""},
		{"x<img src=y>", "`x&lt;img src=y&gt;`"},
		{"a|b", "`a|b`"},
	} {
		t.Run(tt.branch, func(t *testing.T) {
			r := api.Repo{RepoRef: api.RepoRef{InstallationID: 7, FullName: "acme/shop"}, ID: 42, DefaultBranch: tt.branch}
			body := baselineBody(baselineUpload(r, []byte("{}"), ""),
				baselineCheck{head: runHeadSHA, capture: capture, state: protectedAbsent}, capture.Accepted, nil)
			if tt.want != "" && !strings.Contains(body, "approved workflow on "+tt.want+" at") {
				t.Errorf("branch rendered other than %s:\n%s", tt.want, body)
			}
			for _, banned := range []string{"<!--", "-->", "<img"} {
				if strings.Contains(body, banned) {
					t.Errorf("body contains %q", banned)
				}
			}
			for i, line := range strings.Split(body, "\n") {
				if strings.Count(line, "`")%2 != 0 {
					t.Errorf("line %d leaves a code span open: %q", i+1, line)
				}
				// Every table row has exactly three cell borders; an escaped pipe is cell content.
				if strings.HasPrefix(line, "| ") && strings.Count(strings.ReplaceAll(line, `\|`, ""), "|") != 3 {
					t.Errorf("line %d splits the table row: %q", i+1, line)
				}
			}
		})
	}
}

func TestBaselineBodyStaysUnderGitHubLimit(t *testing.T) {
	policyText := corpus(t, capturedTwo, ".policy.yaml")
	longRule := strings.Repeat("r", 128) // the longest rule ID ReadBaseline accepts
	findings := func(from, n int) [][2]string {
		out := make([][2]string, n)
		for i := range out {
			out[i] = [2]string{fmt.Sprintf("%032x", from+i), longRule}
		}
		return out
	}
	f := newFixture(t)
	f.protect(policyText, nil, syntheticBaseline(policyText, "archfit.measurement.v2", findings(0, 300)...))
	f.receiver.upload = baselineUpload(f.repo, syntheticBaseline(policyText, "archfit.measurement.v2", findings(300, 3000)...), "")

	if res := f.postBaseline(t); res.status != 200 {
		t.Fatalf("response = %d %s", res.status, res.raw)
	}
	body := f.hub.body(f.repo, baselineBranch)
	if len(body) > 65536 {
		t.Errorf("body is %d bytes; GitHub refuses more than 65536 characters", len(body))
	}
	for _, want := range []string{
		"Approving accepts 3000 findings", "| Added | 3000 |", "| Removed | 300 |", "and 2850 more", "and 150 more",
	} {
		if !strings.Contains(body, want) {
			t.Errorf("PR body does not mention %q", want)
		}
	}
}

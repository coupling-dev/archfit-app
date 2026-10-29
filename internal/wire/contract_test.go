package wire_test

import (
	"encoding/json"
	"errors"
	"go/ast"
	"go/parser"
	"go/token"
	"os/exec"
	"path/filepath"
	"reflect"
	"slices"
	"sort"
	"strings"
	"testing"
	"time"

	"github.com/alexei-led/archfit-app/internal/wire/envelope"
	"github.com/alexei-led/archfit-app/internal/wire/manifest"
	"github.com/alexei-led/archfit-app/internal/wire/policy"
	"github.com/alexei-led/archfit-app/internal/wire/report"
	"github.com/alexei-led/archfit-app/internal/wire/settings"
)

const (
	wirePackage     = "github.com/alexei-led/archfit-app/internal/wire"
	realCapture     = "real-v320-blocked-no-baseline"
	verdictBlocked  = "blocked"
	statusNonCompar = "non_comparable"
)

var wireChildren = []string{"decision", "envelope", "manifest", "policy", "report", "settings"}

// reportFields are the decision and identity values a report or leakage expectation records.
type reportFields struct {
	Verdict             string   `json:"verdict"`
	HardGates           string   `json:"hard_gates"`
	UnevaluatedRuleIDs  []string `json:"unevaluated_rule_ids"`
	HasProfile          bool     `json:"has_profile"`
	ProfileVersion      string   `json:"profile_version"`
	GateReferenceStatus string   `json:"gate_reference_status"`
}

func fieldsOf(r report.Report) reportFields {
	f := reportFields{Verdict: r.Verdict, HardGates: r.Decision.HardGates, UnevaluatedRuleIDs: []string{}}
	for _, rule := range r.Decision.UnevaluatedRequiredRules {
		f.UnevaluatedRuleIDs = append(f.UnevaluatedRuleIDs, rule.RuleID)
	}
	if p := r.Comparison.MeasurementProfile; p != nil {
		f.HasProfile, f.ProfileVersion = true, p.Version
	}
	if r.GateReference != nil {
		f.GateReferenceStatus = r.GateReference.Status
	}
	return f
}

type policyModule struct {
	Name  string `json:"name"`
	Layer string `json:"layer"`
	Line  int    `json:"line"`
}

type policyWaiver struct {
	Rule       string    `json:"rule"`
	From       string    `json:"from"`
	To         string    `json:"to"`
	Reason     string    `json:"reason"`
	ApprovedBy string    `json:"approved_by"`
	Expires    time.Time `json:"expires"`
}

type policyFields struct {
	Modules []policyModule `json:"modules"`
	Waivers []policyWaiver `json:"waivers"`
}

func readPolicy(raw []byte) (policyFields, error) {
	modules, modulesErr := policy.Modules(raw)
	waivers, waiversErr := policy.Waivers(raw)
	if err := errors.Join(modulesErr, waiversErr); err != nil {
		return policyFields{}, err
	}
	got := policyFields{Modules: []policyModule{}, Waivers: []policyWaiver{}}
	for _, m := range modules {
		got.Modules = append(got.Modules, policyModule{Name: m.Name, Layer: m.Layer, Line: m.Line})
	}
	for _, w := range waivers {
		got.Waivers = append(got.Waivers, policyWaiver{
			Rule: w.Rule, From: w.From, To: w.To, Reason: w.Reason, ApprovedBy: w.ApprovedBy, Expires: w.Expires.UTC(),
		})
	}
	return got, nil
}

func strictFields(t *testing.T, f fixture, into any) {
	t.Helper()
	dec := json.NewDecoder(strings.NewReader(string(f.Expect.Fields)))
	dec.DisallowUnknownFields()
	if err := dec.Decode(into); err != nil {
		t.Fatalf("%s: fields: %v", f.id(), err)
	}
}

// decodeFixture routes a fixture to its child decoder by folder and returns what `fields` is compared with.
func decodeFixture(t *testing.T, f fixture) (got, want any, err error) {
	t.Helper()
	raw := f.bytes(t)
	switch f.Folder {
	case folderEnvelope:
		var wantEnv envelope.Envelope
		if f.Expect.Decode == decodeOK {
			strictFields(t, f, &wantEnv)
		}
		env, err := envelope.Decode(raw)
		return env, wantEnv, err
	case folderPolicy:
		wantPolicy := policyFields{}
		if f.Expect.Decode == decodeOK {
			strictFields(t, f, &wantPolicy)
		}
		gotPolicy, err := readPolicy(raw)
		return gotPolicy, wantPolicy, err
	default:
		var wantReport reportFields
		if f.Expect.Decode == decodeOK {
			strictFields(t, f, &wantReport)
		}
		r, err := report.Decode(raw, f.engine(t))
		if err != nil {
			return nil, wantReport, err
		}
		return fieldsOf(r), wantReport, nil
	}
}

func TestEveryFixtureDecodesAsRecorded(t *testing.T) {
	for _, f := range loadCorpus(t) {
		t.Run(f.id(), func(t *testing.T) {
			got, want, err := decodeFixture(t, f)
			if f.Expect.Decode == decodeError {
				if err == nil {
					t.Fatal("decoded, want an error")
				}
				return
			}
			if err != nil {
				t.Fatalf("decode: %v", err)
			}
			if !reflect.DeepEqual(got, want) {
				t.Errorf("fields\n got %+v\nwant %+v", got, want)
			}
		})
	}
}

func TestRealEngineCaptureDecodes(t *testing.T) {
	var capture fixture
	for _, f := range inFolder(loadCorpus(t), folderReport) {
		if f.Name == realCapture {
			capture = f
		}
	}
	if capture.Name == "" {
		t.Fatalf("report fixture %q is missing", realCapture)
	}
	if _, reason, err := capture.Expect.engineProducer(); err != nil || reason != "" {
		t.Fatalf("producer must be the engine capture, got synthetic %q (err %v)", reason, err)
	}

	r, err := report.Decode(capture.bytes(t), capture.engine(t))
	if err != nil {
		t.Fatalf("report.Decode on the unmodified capture: %v", err)
	}
	if r.Verdict != verdictBlocked {
		t.Errorf("verdict = %q, want %q", r.Verdict, verdictBlocked)
	}
	if r.GateReference == nil || r.GateReference.Status != statusNonCompar {
		t.Errorf("gate_reference = %+v, want status %q", r.GateReference, statusNonCompar)
	}

	policyBytes, ok := capture.sibling(t, suffixPolicy)
	if !ok {
		t.Fatal("the capture ships no policy file")
	}
	if got := policy.Digest(policyBytes); got != r.Comparison.ConfigHash {
		t.Errorf("policy.Digest = %s, config_hash = %s", got, r.Comparison.ConfigHash)
	}
	modules, err := policy.Modules(policyBytes)
	if err != nil {
		t.Fatalf("policy.Modules: %v", err)
	}
	var names []string
	for _, m := range modules {
		names = append(names, m.Name)
	}
	if want := []string{"a", "b"}; !reflect.DeepEqual(names, want) {
		t.Errorf("modules = %v, want %v", names, want)
	}
	if len(r.Seams) != 1 || r.Seams[0].FromModule != "a" || r.Seams[0].ToModule != "b" {
		t.Errorf("seams = %+v, want one seam from a to b", r.Seams)
	}
}

// TestRealPullRequestCaptureCarriesOrigin: the Action runs a pull request check with --base <merge base>. The real
// v3.0.1 capture keeps every finding's and task's origin, and the gate's inputs stay the head's: config_hash is the
// digest of the policy the head ran under, and the measurement profile is the approved one.
func TestRealPullRequestCaptureCarriesOrigin(t *testing.T) {
	var capture fixture
	for _, f := range inFolder(loadCorpus(t), folderReport) {
		if f.Name == "real-pr-origin" {
			capture = f
		}
	}
	if capture.Name == "" {
		t.Fatal("report fixture real-pr-origin is missing")
	}
	r, err := report.Decode(capture.bytes(t), capture.engine(t))
	if err != nil {
		t.Fatalf("report.Decode: %v", err)
	}
	const mergeBase = "7a3a7a335ed968cdb3876eac1ba27b3992da249f"
	if c := r.Comparison; c.Status != "comparable" || c.BaseRef != mergeBase || c.OriginStatus != "comparable" {
		t.Errorf("comparison status %q, base_ref %q, origin_status %q; want comparable against %s",
			c.Status, c.BaseRef, c.OriginStatus, mergeBase)
	}
	policyBytes, _ := capture.sibling(t, suffixPolicy)
	if got := policy.Digest(policyBytes); got != r.Comparison.ConfigHash {
		t.Errorf("config_hash = %s, want the head policy digest %s", r.Comparison.ConfigHash, got)
	}
	if p := r.Comparison.MeasurementProfile; p == nil || p.Version != manifest.Current().ProfileVersion {
		t.Errorf("measurement profile = %+v, want version %s", p, manifest.Current().ProfileVersion)
	}

	origins := map[string][]string{}
	for _, f := range r.Findings {
		origins[f.RuleID] = append(origins[f.RuleID], f.Origin)
	}
	want := map[string][]string{
		"no_direct_b_dependency": {"pre_existing"},
		"no_c_dependency":        {"introduced"},
		"bc/imbalanced_coupling": {"pre_existing", "introduced"},
	}
	if !reflect.DeepEqual(origins, want) {
		t.Errorf("finding origins = %v, want %v", origins, want)
	}
	for _, task := range r.AgentTasks {
		if want := map[string]string{"no_direct_b_dependency": "pre_existing", "no_c_dependency": "introduced"}[task.RuleID]; task.Origin != want {
			t.Errorf("task %s origin = %q, want %q", task.RuleID, task.Origin, want)
		}
	}
}

// TestEveryApprovedEngineVersionIsReadable: the manifest approves only a release whose state key set the report
// decoder knows, so an approved identity never meets a decoder that cannot read its report.
func TestEveryApprovedEngineVersionIsReadable(t *testing.T) {
	var capture []byte
	for _, f := range inFolder(loadCorpus(t), folderReport) {
		if f.Name == realCapture {
			capture = f.bytes(t)
		}
	}
	for _, release := range manifest.EngineVersions() {
		if !slices.Contains(report.EngineVersions(), release) {
			t.Errorf("the manifest approves %s, the report decoder does not read it", release)
			continue
		}
		if _, err := report.Decode(capture, release); err != nil {
			t.Errorf("the real capture does not decode as a %s document: %v", release, err)
		}
	}
}

// TestEveryApprovedEngineVersionHasACapture: a release enters the manifest only together with an unmodified
// capture of its own output in the corpus, which then has to decode as recorded.
func TestEveryApprovedEngineVersionHasACapture(t *testing.T) {
	captured := map[string]bool{}
	for _, f := range inFolder(loadCorpus(t), folderReport) {
		if p, reason, err := f.Expect.engineProducer(); err == nil && reason == "" && f.Expect.Decode == decodeOK {
			captured[p.EngineVersion] = true
		}
	}
	for _, release := range manifest.EngineVersions() {
		if !captured[release] {
			t.Errorf("the manifest approves %s, the corpus holds no decodable capture of it", release)
		}
	}
}

// TestPolicySchemaFollowsTheCurrentEngine: wire/policy embeds one engine config schema, for the editor and for
// Validate: the schema of the release new workflows pin. Moving manifest Current to another release fails here
// until the schema and its SCHEMA_SOURCE are re-pinned.
func TestPolicySchemaFollowsTheCurrentEngine(t *testing.T) {
	note := readFile(t, filepath.Join("policy", "SCHEMA_SOURCE"))
	tag := ""
	for line := range strings.Lines(string(note)) {
		if value, ok := strings.CutPrefix(strings.TrimSpace(line), "tag: "); ok {
			tag = value
		}
	}
	if want := manifest.Current().EngineVersion; tag != want {
		t.Errorf("policy schema pinned at %q, manifest Current is engine %q", tag, want)
	}
}

func goList(t *testing.T, args ...string) []string {
	t.Helper()
	out, err := exec.Command("go", append([]string{"list"}, args...)...).Output() //nolint:gosec // fixed tool, fixed arguments
	if err != nil {
		t.Fatalf("go list %v: %v", args, err)
	}
	return strings.Fields(string(out))
}

func TestPublishedPackagesExist(t *testing.T) {
	got := goList(t, "./...")
	want := []string{wirePackage}
	for _, child := range wireChildren {
		want = append(want, wirePackage+"/"+child)
	}
	sort.Strings(want)
	sort.Strings(got)
	if !reflect.DeepEqual(got, want) {
		t.Errorf("packages\n got %v\nwant %v", got, want)
	}

	pkgs, err := parser.ParseDir(token.NewFileSet(), ".", nil, 0) //nolint:staticcheck // one directory, no type information needed
	if err != nil {
		t.Fatal(err)
	}
	parent, ok := pkgs["wire"]
	if !ok {
		t.Fatal("package wire has no non-test file")
	}
	for name, file := range parent.Files {
		if strings.HasSuffix(name, "_test.go") {
			continue
		}
		if len(file.Decls) != 0 || ast.FileExports(file) {
			t.Errorf("%s: the parent package holds no declarations", name)
		}
	}
}

func TestUnknownVersionRejectedEverywhere(t *testing.T) {
	all := loadCorpus(t)
	pick := func(folder, name string) fixture {
		for _, f := range inFolder(all, folder) {
			if f.Name == name {
				return f
			}
		}
		t.Fatalf("fixture %s/%s is missing", folder, name)
		return fixture{}
	}
	bump := func(raw []byte, from, to string) []byte {
		if !strings.Contains(string(raw), from) {
			t.Fatalf("version identifier %q not found", from)
		}
		return []byte(strings.Replace(string(raw), from, to, 1))
	}

	env := pick(folderEnvelope, "pr-report").bytes(t)
	if _, err := envelope.Decode(env); err != nil {
		t.Fatalf("v1 envelope must decode: %v", err)
	}
	if _, err := envelope.Decode(bump(env, envelope.SchemaV1, "archfit.report-envelope.v2")); err == nil {
		t.Error("envelope v2 decoded")
	}

	rep := pick(folderReport, realCapture).bytes(t)
	for _, release := range report.EngineVersions() {
		if _, err := report.Decode(bump(rep, report.StateSchemaV1, "archfit.architecture-state.v2"), release); err == nil {
			t.Errorf("report v2 decoded as a %s document", release)
		}
	}
	if _, err := report.Decode(rep, "v2.6.0"); err == nil {
		t.Error("a report of an engine release the decoder does not read decoded")
	}

	const settingsV1 = "version: 1\nowners:\n  - octo-owner\nworkflow: .github/workflows/archfit.yml\n"
	if _, err := settings.Parse([]byte(settingsV1)); err != nil {
		t.Fatalf("v1 settings must parse: %v", err)
	}
	if _, err := settings.Parse(bump([]byte(settingsV1), "version: 1", "version: 2")); err == nil {
		t.Error("settings version 2 parsed")
	}
}

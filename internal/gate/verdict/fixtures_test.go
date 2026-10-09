package verdict

import (
	"bytes"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"github.com/alexei-led/archfit-app/internal/gate/ingest"
	"github.com/alexei-led/archfit-app/internal/gate/inputs"
	"github.com/alexei-led/archfit-app/internal/wire/policy"
	"github.com/alexei-led/archfit-app/internal/wire/report"
)

// fixtureDir is the shared corpus; internal/wire/module.md owns its names and the .expect.json format.
const fixtureDir = "../../wire/testdata/report"

// fixtureFacts are the overrides to the clean facts. A nil field keeps the clean value.
type fixtureFacts struct {
	IdentityApproved       *bool            `json:"identity_approved"`
	WorkflowApproved       *bool            `json:"workflow_approved"`
	Problems               []ingest.Problem `json:"problems"`
	BaselineDigest         *string          `json:"baseline_digest"`
	LabelsDigest           *string          `json:"labels_digest"`
	TrustedPolicyDigests   *[]string        `json:"trusted_policy_digests"`
	TrustedBaselineDigests *[]string        `json:"trusted_baseline_digests"`
	TrustedLabelsDigests   *[]string        `json:"trusted_labels_digests"`
}

type fixtureExpect struct {
	Producer json.RawMessage `json:"producer"`
	Decode   string          `json:"decode"`
	Fields   json.RawMessage `json:"fields"`
	Gate     struct {
		Conclusion Conclusion   `json:"conclusion"`
		Reasons    []ReasonCode `json:"reasons"`
	} `json:"gate"`
	Facts fixtureFacts `json:"facts"`
}

// fixtureEngine is the release a synthetic fixture is a document of; a capture names its own release.
const fixtureEngine = "v3.0.0"

func loadReport(t *testing.T, name string, e fixtureExpect) *report.Report {
	t.Helper()
	payload, err := os.ReadFile(filepath.Join(fixtureDir, name+".json"))
	if err != nil {
		t.Fatal(err)
	}
	engine := fixtureEngine
	var producer struct {
		EngineVersion string `json:"engine_version"`
	}
	if json.Unmarshal(e.Producer, &producer) == nil {
		engine = producer.EngineVersion
	}
	r, err := report.Decode(payload, engine)
	if err != nil {
		t.Fatalf("decode %s: %v", name, err)
	}
	return &r
}

func loadExpect(t *testing.T, name string) fixtureExpect {
	t.Helper()
	raw, err := os.ReadFile(filepath.Join(fixtureDir, name+".expect.json"))
	if err != nil {
		t.Fatal(err)
	}
	var e fixtureExpect
	dec := json.NewDecoder(bytes.NewReader(raw))
	dec.DisallowUnknownFields()
	if err := dec.Decode(&e); err != nil {
		t.Fatalf("%s.expect.json: %v", name, err)
	}
	return e
}

// siblingDigests is what the protected ref yields for a sibling file: its digest, or nothing without the file.
func siblingDigests(t *testing.T, name, ext string) []string {
	t.Helper()
	raw, err := os.ReadFile(filepath.Join(fixtureDir, name+ext))
	if errors.Is(err, os.ErrNotExist) {
		return nil
	}
	if err != nil {
		t.Fatal(err)
	}
	return []string{policy.Digest(raw)}
}

// fixtureCase builds the facts of a fixture: the clean facts of internal/wire/module.md, then its overrides.
func fixtureCase(t *testing.T, name string) (Facts, fixtureExpect) {
	t.Helper()
	e := loadExpect(t, name)
	f := Facts{
		IdentityApproved: true,
		WorkflowApproved: true,
		Trusted: inputs.Trusted{
			SettingsFound:   true,
			PolicyDigests:   []string{basePolicy}, // a fixture that does not decode has no config_hash
			BaselineDigests: siblingDigests(t, name, ".baseline.json"),
			LabelsDigests:   siblingDigests(t, name, ".labels.yaml"),
			Approval:        inputs.ApprovalNotRequired,
		},
	}
	if e.Decode == "ok" {
		f.Report = loadReport(t, name, e)
		f.Trusted.PolicyDigests = []string{f.Report.Comparison.ConfigHash}
	}
	f.BaselineDigest, f.LabelsDigest = inputs.First(f.Trusted.BaselineDigests), inputs.First(f.Trusted.LabelsDigests)

	o := e.Facts
	f.Problems = o.Problems
	for _, set := range []struct {
		from *[]string
		to   *[]string
	}{
		{o.TrustedPolicyDigests, &f.Trusted.PolicyDigests},
		{o.TrustedBaselineDigests, &f.Trusted.BaselineDigests},
		{o.TrustedLabelsDigests, &f.Trusted.LabelsDigests},
	} {
		if set.from != nil {
			*set.to = *set.from
		}
	}
	if o.IdentityApproved != nil {
		f.IdentityApproved = *o.IdentityApproved
	}
	if o.WorkflowApproved != nil {
		f.WorkflowApproved = *o.WorkflowApproved
	}
	if o.BaselineDigest != nil {
		f.BaselineDigest = *o.BaselineDigest
	}
	if o.LabelsDigest != nil {
		f.LabelsDigest = *o.LabelsDigest
	}
	return f, e
}

func fixtureNames(t *testing.T) []string {
	t.Helper()
	paths, err := filepath.Glob(filepath.Join(fixtureDir, "*.expect.json"))
	if err != nil {
		t.Fatal(err)
	}
	var names []string
	for _, p := range paths {
		names = append(names, strings.TrimSuffix(filepath.Base(p), ".expect.json"))
	}
	return names
}

func TestDecideWireFixtures(t *testing.T) {
	// The fixtures module.md names, with the conclusion and first reason it states for them.
	named := map[string]struct {
		conclusion Conclusion
		first      ReasonCode
	}{
		"healthy":                 {Success, ReasonHealthy},
		"attention-pass":          {Success, ReasonAttentionOnly},
		"blocked":                 {Failure, ReasonBlockingFindings},
		"missing-evidence":        {ActionRequired, ReasonMissingCoverage},
		"unknown-identity":        {ActionRequired, ReasonProfileMissing},
		"profile-incompatible":    {ActionRequired, ReasonBaselineNotComparable},
		"invalid-input":           {ActionRequired, ReasonReportInvalid},
		"blocker-and-unevaluated": {Failure, ReasonBlockingFindings},
		"non-applicable-rule":     {Success, ReasonHealthy},
	}
	rest := []string{"profile-comparable", "locations-empty", "matched-by-populated", "repair-kinds", "waiver-expiry-order"}

	names := fixtureNames(t)
	for name := range named {
		if !slices.Contains(names, name) {
			t.Errorf("fixture %s is missing", name)
		}
	}
	for _, name := range rest {
		if !slices.Contains(names, name) {
			t.Errorf("fixture %s is missing", name)
		}
	}

	for _, name := range names {
		t.Run(name, func(t *testing.T) {
			f, expect := fixtureCase(t, name)
			o := decide(t, f)
			wantCodes(t, o, expect.Gate.Conclusion, expect.Gate.Reasons...)
			if want, ok := named[name]; ok && (o.Conclusion != want.conclusion || o.Reasons[0].Code != want.first) {
				t.Errorf("module.md wants %s/%s, got %s/%s", want.conclusion, want.first, o.Conclusion, o.Reasons[0].Code)
			}
		})
	}
}

func TestReasonsZeroDeterminesConclusion(t *testing.T) {
	check := func(o Outcome) {
		t.Helper()
		checkInvariants(t, o)
		want := ActionRequired
		switch o.Reasons[0].Code {
		case ReasonHealthy, ReasonAttentionOnly:
			want = Success
		case ReasonBlockingFindings:
			want = Failure
		}
		if o.Conclusion != want {
			t.Fatalf("conclusion %q with first reason %q", o.Conclusion, o.Reasons[0].Code)
		}
	}
	for _, f := range corpus() {
		check(Decide(f))
	}
	for _, name := range fixtureNames(t) {
		f, _ := fixtureCase(t, name)
		check(Decide(f))
	}
	for _, code := range append([]ReasonCode{"", "neutral"}, allCodes...) {
		check(Without(code))
	}
}

func TestInvalidInputFixture(t *testing.T) {
	f := with(nilReport, problem(ingest.ProblemReportInvalid), identityOff)
	o := decide(t, f)
	wantCodes(t, o, ActionRequired, ReasonReportInvalid)
	if o.Reasons[0].Category != CategoryEvidence {
		t.Errorf("category = %q, want evidence", o.Reasons[0].Category)
	}
}

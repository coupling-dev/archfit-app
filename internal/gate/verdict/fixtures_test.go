package verdict

import (
	"bytes"
	"encoding/json"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"github.com/alexei-led/archfit-app/internal/gate/ingest"
	"github.com/alexei-led/archfit-app/internal/gate/inputs"
	"github.com/alexei-led/archfit-app/internal/wire/report"
)

const fixtureDir = "testdata/report"

// fixtureFacts is the one eligibility fact a fixture exercises; everything else stays clean.
type fixtureFacts struct {
	IdentityUnapproved bool `json:"identity_unapproved"`
	TrustedBaseline    bool `json:"trusted_baseline"` // a baseline is on the protected ref and the producer used it
}

type fixtureExpect struct {
	Gate struct {
		Conclusion Conclusion   `json:"conclusion"`
		Reasons    []ReasonCode `json:"reasons"`
		Facts      fixtureFacts `json:"facts"`
	} `json:"gate"`
}

func loadReport(t *testing.T, name string) *report.Report {
	t.Helper()
	payload, err := os.ReadFile(filepath.Join(fixtureDir, name+".json"))
	if err != nil {
		t.Fatal(err)
	}
	r, err := report.Decode(payload)
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

// cleanFacts are the facts of an eligible upload of r: matching trusted digests, approved workflow and
// identity, no problems, not suspended.
func cleanFacts(r *report.Report, ff fixtureFacts) Facts {
	f := Facts{
		LabelsDigest:     baseLabels,
		Report:           r,
		IdentityApproved: !ff.IdentityUnapproved,
		WorkflowApproved: true,
		Trusted: inputs.Trusted{
			SettingsFound: true,
			PolicyDigests: []string{r.Comparison.ConfigHash},
			LabelsDigests: []string{baseLabels},
			Approval:      inputs.ApprovalNotRequired,
		},
	}
	if ff.TrustedBaseline {
		f.BaselineDigest, f.Trusted.BaselineDigests = baseBaseline, []string{baseBaseline}
	}
	return f
}

func fixtureNames(t *testing.T) []string {
	t.Helper()
	paths, err := filepath.Glob(filepath.Join(fixtureDir, "*.json"))
	if err != nil {
		t.Fatal(err)
	}
	var names []string
	for _, p := range paths {
		if base := filepath.Base(p); !strings.HasSuffix(base, ".expect.json") {
			names = append(names, strings.TrimSuffix(base, ".json"))
		}
	}
	return names
}

func TestDecideWireFixtures(t *testing.T) {
	// The fixtures module.md names, with the conclusion and first reason it states for them.
	// comparable-profile is `blocked` in the engine fixture, so it follows "the fixture's own decision".
	named := map[string]struct {
		conclusion Conclusion
		first      ReasonCode
	}{
		"healthy":                   {Success, ReasonHealthy},
		"attention-warning-only":    {Success, ReasonHealthy},
		"blocked":                   {Failure, ReasonBlockingFindings},
		"missing-required-evidence": {ActionRequired, ReasonMissingCoverage},
		"unknown-identity":          {ActionRequired, ReasonProfileMissing},
		"incompatible-profile":      {ActionRequired, ReasonBaselineNotComparable},
		"blocker-plus-unevaluated":  {Failure, ReasonBlockingFindings},
		"non-applicable-rule":       {Success, ReasonHealthy},
	}
	rest := []string{"comparable-profile", "locations-empty", "matched-by-populated", "both-repair-kinds", "waiver-expiry-and-order"}

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
			expect := loadExpect(t, name)
			o := decide(t, cleanFacts(loadReport(t, name), expect.Gate.Facts))
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
		case ReasonHealthy:
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
		check(Decide(cleanFacts(loadReport(t, name), loadExpect(t, name).Gate.Facts)))
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

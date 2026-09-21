package verdict

import (
	"math/rand/v2"
	"slices"
	"strings"
	"testing"

	"github.com/alexei-led/archfit-app/internal/gate/ingest"
	"github.com/alexei-led/archfit-app/internal/gate/inputs"
	"github.com/alexei-led/archfit-app/internal/wire/report"
)

var (
	basePolicy   = strings.Repeat("a", 64)
	headPolicy   = strings.Repeat("b", 64)
	baseBaseline = strings.Repeat("c", 64)
	headBaseline = strings.Repeat("d", 64)
	baseLabels   = strings.Repeat("e", 64)
	headLabels   = strings.Repeat("f", 64)
)

const (
	refNonComparable = "non_comparable"
	refNotRequested  = "not_requested"
	noBaselineLoaded = "no baseline file was loaded"
)

var allCodes = []ReasonCode{
	ReasonHealthy, ReasonBlockingFindings, ReasonEngineError, ReasonMissingCoverage,
	ReasonBaselineNotComparable, ReasonBaselineMismatch, ReasonLabelsMismatch, ReasonPolicyMismatch,
	ReasonPolicyMissing, ReasonUnknownIdentity, ReasonProfileMissing, ReasonWorkflowNotApproved,
	ReasonOwnerApprovalMissing, ReasonSettingsMissing, ReasonForkUnsupported, ReasonUnsupportedEvent,
	ReasonEnvelopeMismatch, ReasonReportInvalid, ReasonMissingUpload, ReasonStaleInputs,
	ReasonWaiverExpired, ReasonSuspended,
}

var withoutCodes = []ReasonCode{
	ReasonMissingUpload, ReasonStaleInputs, ReasonWaiverExpired, ReasonForkUnsupported, ReasonSuspended,
}

// healthyFacts is a passing pull request on a repository with a policy, a baseline and a labels file.
func healthyFacts() Facts {
	return Facts{
		BaselineDigest: baseBaseline,
		LabelsDigest:   baseLabels,
		Report: &report.Report{
			SchemaVersion: report.StateSchemaV1,
			Verdict:       verdictHealthy,
			Decision:      report.Decision{HardGates: gatesPass},
			Comparison: report.Comparison{
				Status:             refNotRequested,
				ConfigHash:         basePolicy,
				MeasurementProfile: &report.MeasurementProfile{Version: "archfit.measurement.v1"},
			},
			GateReference: &report.Reference{Status: refComparable, BaseRef: "baseline"},
		},
		IdentityApproved: true,
		WorkflowApproved: true,
		Trusted: inputs.Trusted{
			SettingsFound:   true,
			PolicyDigests:   []string{basePolicy},
			BaselineDigests: []string{baseBaseline},
			LabelsDigests:   []string{baseLabels},
			Approval:        inputs.ApprovalNotRequired,
		},
	}
}

func with(muts ...func(*Facts)) Facts {
	f := healthyFacts()
	for _, m := range muts {
		m(&f)
	}
	return f
}

func suspended(f *Facts)       { f.Trusted.Suspended = true }
func settingsMissing(f *Facts) { f.Trusted.SettingsFound = false }
func nilReport(f *Facts)       { f.Report = nil }
func workflowOff(f *Facts)     { f.WorkflowApproved = false }
func identityOff(f *Facts)     { f.IdentityApproved = false }
func noProfile(f *Facts)       { f.Report.Comparison.MeasurementProfile = nil }
func policyMismatch(f *Facts)  { f.Report.Comparison.ConfigHash = headPolicy }
func baselineMismatch(f *Facts) {
	f.BaselineDigest = headBaseline
}
func labelsMismatch(f *Facts) { f.LabelsDigest = headLabels }
func approvalMissing(f *Facts) {
	f.Trusted.Approval = inputs.ApprovalMissing
	f.Trusted.ProtectedPaths = []string{".archfit.yaml"}
}
func unevaluated(f *Facts) {
	f.Report.Decision.UnevaluatedRequiredRules = []report.UnevaluatedRule{
		{RuleID: "layers/no_upward_imports", Reason: "required tool `dependency-cruiser` was absent"},
	}
}

func problem(p ingest.Problem) func(*Facts) {
	return func(f *Facts) { f.Problems = append(f.Problems, p) }
}

func engine(verdict, hardGates string) func(*Facts) {
	return func(f *Facts) {
		f.Report.Verdict = verdict
		f.Report.Decision.HardGates = hardGates
	}
}

func gateRef(ref *report.Reference) func(*Facts) {
	return func(f *Facts) { f.Report.GateReference = ref }
}

func nonComparable(f *Facts) {
	f.Report.GateReference = &report.Reference{
		Status: refNonComparable, BaseRef: "baseline", Reasons: []string{"measurement profile differs"},
	}
}

func codes(o Outcome) []ReasonCode {
	out := make([]ReasonCode, 0, len(o.Reasons))
	for _, r := range o.Reasons {
		out = append(out, r.Code)
	}
	return out
}

func find(o Outcome, code ReasonCode) (Reason, bool) {
	for _, r := range o.Reasons {
		if r.Code == code {
			return r, true
		}
	}
	return Reason{}, false
}

// checkInvariants holds for every outcome of the package.
func checkInvariants(t *testing.T, o Outcome) {
	t.Helper()
	if o.Conclusion != Success && o.Conclusion != Failure && o.Conclusion != ActionRequired {
		t.Fatalf("conclusion %q is outside the closed set", o.Conclusion)
	}
	if len(o.Reasons) == 0 {
		t.Fatalf("reasons are empty")
	}
	first, ok := lookup(o.Reasons[0].Code)
	if !ok || first.conclusion != o.Conclusion {
		t.Fatalf("conclusion %q does not follow Reasons[0] %q", o.Conclusion, o.Reasons[0].Code)
	}
	seen := map[ReasonCode]bool{}
	for _, r := range o.Reasons {
		if seen[r.Code] {
			t.Fatalf("reason %q appears twice in %v", r.Code, codes(o))
		}
		seen[r.Code] = true
		e, ok := lookup(r.Code)
		if !ok || e.category != r.Category {
			t.Fatalf("reason %q has category %q", r.Code, r.Category)
		}
		if (r.Next == "") != (r.Code == ReasonHealthy) {
			t.Fatalf("reason %q has Next %q", r.Code, r.Next)
		}
		if strings.Contains(strings.ToLower(r.Detail+r.Next), "verified") {
			t.Fatalf("reason %q says verified", r.Code)
		}
	}
	if seen[ReasonHealthy] && len(o.Reasons) != 1 {
		t.Fatalf("healthy beside other reasons: %v", codes(o))
	}
}

// decide calls Decide and checks the invariants of every outcome.
func decide(t *testing.T, f Facts) Outcome {
	t.Helper()
	o := Decide(f)
	checkInvariants(t, o)
	return o
}

func wantCodes(t *testing.T, o Outcome, conclusion Conclusion, want ...ReasonCode) {
	t.Helper()
	if o.Conclusion != conclusion {
		t.Errorf("conclusion = %q, want %q (reasons %v)", o.Conclusion, conclusion, codes(o))
	}
	if !slices.Equal(codes(o), want) {
		t.Errorf("reasons = %v, want %v", codes(o), want)
	}
}

// successAllowed restates the preconditions of Success independently of Decide.
func successAllowed(f Facts) bool {
	r := f.Report
	if f.Trusted.Suspended || !f.Trusted.SettingsFound || len(f.Problems) > 0 || r == nil {
		return false
	}
	if !f.WorkflowApproved || !f.IdentityApproved {
		return false
	}
	if f.Trusted.Approval != inputs.ApprovalNotRequired && f.Trusted.Approval != inputs.ApprovalGranted {
		return false
	}
	in := func(list []string, d string) bool { return d != "" && slices.Contains(list, d) }
	if !in(f.Trusted.PolicyDigests, r.Comparison.ConfigHash) {
		return false
	}
	for _, p := range []struct {
		trusted []string
		claimed string
	}{{f.Trusted.BaselineDigests, f.BaselineDigest}, {f.Trusted.LabelsDigests, f.LabelsDigest}} {
		if len(p.trusted) == 0 && p.claimed != "" || len(p.trusted) > 0 && !in(p.trusted, p.claimed) {
			return false
		}
	}
	if len(f.Trusted.BaselineDigests) > 0 && (r.GateReference == nil || r.GateReference.Status != refComparable) {
		return false
	}
	return r.Decision.HardGates == gatesPass && len(r.Decision.UnevaluatedRequiredRules) == 0 &&
		(r.Verdict == verdictHealthy || r.Verdict == verdictAttention)
}

func pick[T any](rng *rand.Rand, values ...T) T { return values[rng.IntN(len(values))] }

// fuzzFacts mutates the healthy facts field by field, so that both passing and failing facts are common.
func fuzzFacts(rng *rand.Rand) Facts {
	f := healthyFacts()
	hit := func() bool { return rng.IntN(8) == 0 }
	digests := []string{"", basePolicy, headPolicy, baseBaseline, headBaseline, baseLabels, "sha256:" + baseBaseline}
	list := func() []string {
		out := []string(nil)
		for range rng.IntN(3) {
			out = append(out, pick(rng, digests...))
		}
		return out
	}
	if hit() {
		for range 1 + rng.IntN(3) {
			f.Problems = append(f.Problems, pick(rng, ingest.ProblemEnvelopeMismatch, ingest.ProblemUnsupportedEvent,
				ingest.ProblemFork, ingest.ProblemReportInvalid, ingest.Problem("neutral"), ingest.Problem("")))
		}
	}
	if hit() {
		f.BaselineDigest = pick(rng, digests...)
	}
	if hit() {
		f.LabelsDigest = pick(rng, digests...)
	}
	if hit() {
		f.IdentityApproved = false
	}
	if hit() {
		f.WorkflowApproved = false
	}
	if hit() {
		f.Trusted.Suspended = true
		f.Trusted.Entitlement.Reason = pick(rng, "", "Trial ended 2026-10-20.")
	}
	if hit() {
		f.Trusted.SettingsFound = false
	}
	if hit() {
		f.Trusted.PolicyDigests = list()
	}
	if hit() {
		f.Trusted.BaselineDigests = list()
	}
	if hit() {
		f.Trusted.LabelsDigests = list()
	}
	if hit() {
		f.Trusted.Approval = pick(rng, inputs.ApprovalGranted, inputs.ApprovalMissing, inputs.Approval(""), inputs.Approval("skipped"))
		f.Trusted.ProtectedPaths = []string{".archfit.yaml", ".archfit-app.yaml"}
	}
	if hit() {
		f.Report = nil
		return f
	}
	r := f.Report
	if hit() {
		r.Verdict = pick(rng, verdictAttention, verdictBlocked, "", "BLOCKED", "error", "neutral", "skipped")
	}
	if hit() {
		r.Decision.HardGates = pick(rng, gatesFail, gatesUnmeasured, "", "PASS", "neutral")
	}
	if hit() {
		for i := range 1 + rng.IntN(2) {
			r.Decision.UnevaluatedRequiredRules = append(r.Decision.UnevaluatedRequiredRules,
				report.UnevaluatedRule{RuleID: pick(rng, "rule/a", "rule/b") + string(rune('0'+i))})
		}
	}
	if hit() {
		r.Comparison.ConfigHash = pick(rng, digests...)
	}
	if hit() {
		r.Comparison.MeasurementProfile = nil
	}
	if hit() {
		r.Comparison.Status = pick(rng, refComparable, refNonComparable)
	}
	if hit() {
		r.GateReference = pick(rng, nil,
			&report.Reference{Status: refNonComparable, Reasons: []string{noBaselineLoaded}},
			&report.Reference{Status: refNotRequested},
			&report.Reference{Status: ""},
			&report.Reference{Status: "neutral", Reasons: slices.Repeat([]string{strings.Repeat("x", 150)}, 5)})
	}
	return f
}

const fuzzRounds = 20000

func fuzzRNG() *rand.Rand { return rand.New(rand.NewPCG(20260921, 15)) }

// corpus is every facts value the tests of this package decide on, fuzzed facts included.
func corpus() []Facts {
	var out []Facts
	for _, c := range orderCases() {
		out = append(out, c.facts)
	}
	for _, c := range matrixCases() {
		out = append(out, c.facts)
	}
	for _, c := range preconditionCases() {
		out = append(out, c.facts)
	}
	rng := fuzzRNG()
	for range fuzzRounds {
		out = append(out, fuzzFacts(rng))
	}
	return out
}

package verdict

import (
	"fmt"
	"slices"
	"strings"
	"testing"
	"unicode/utf8"

	"github.com/alexei-led/archfit-app/internal/gate/ingest"
	"github.com/alexei-led/archfit-app/internal/gate/inputs"
	"github.com/alexei-led/archfit-app/internal/wire/report"
	"github.com/alexei-led/archfit-app/internal/wire/settings"
)

type decideCase struct {
	name       string
	facts      Facts
	conclusion Conclusion
	reasons    []ReasonCode
}

func runCases(t *testing.T, cases []decideCase) {
	t.Helper()
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			wantCodes(t, decide(t, c.facts), c.conclusion, c.reasons...)
		})
	}
}

func orderCases() []decideCase {
	return []decideCase{
		{"suspended + blocked", with(suspended, engine(verdictBlocked, gatesFail)),
			ActionRequired, []ReasonCode{ReasonSuspended, ReasonBlockingFindings}},
		{"settings missing + fork", with(settingsMissing, problem(ingest.ProblemFork)),
			ActionRequired, []ReasonCode{ReasonSettingsMissing, ReasonForkUnsupported}},
		{"approval missing + policy mismatch", with(approvalMissing, policyMismatch),
			ActionRequired, []ReasonCode{ReasonOwnerApprovalMissing, ReasonPolicyMismatch}},
		{"approval missing + baseline mismatch", with(approvalMissing, baselineMismatch),
			ActionRequired, []ReasonCode{ReasonOwnerApprovalMissing, ReasonBaselineMismatch}},
		{"baseline mismatch + labels mismatch", with(baselineMismatch, labelsMismatch),
			ActionRequired, []ReasonCode{ReasonBaselineMismatch, ReasonLabelsMismatch}},
		{"policy mismatch + not comparable", with(policyMismatch, nonComparable),
			ActionRequired, []ReasonCode{ReasonPolicyMismatch, ReasonBaselineNotComparable}},
		{"out-of-enum verdict + unmeasured", with(engine("error", gatesUnmeasured)),
			ActionRequired, []ReasonCode{ReasonEngineError, ReasonMissingCoverage}},
		{"missing coverage + blocker", with(engine(verdictBlocked, gatesPass), unevaluated),
			ActionRequired, []ReasonCode{ReasonMissingCoverage, ReasonBlockingFindings}},
		{"workflow + identity + approval", with(workflowOff, identityOff, approvalMissing),
			ActionRequired, []ReasonCode{ReasonWorkflowNotApproved, ReasonUnknownIdentity, ReasonOwnerApprovalMissing}},
		{"all problems, any order", with(problem(ingest.ProblemReportInvalid), problem(ingest.ProblemEnvelopeMismatch),
			problem(ingest.ProblemUnsupportedEvent), problem(ingest.ProblemFork), problem(ingest.ProblemFork)),
			ActionRequired, []ReasonCode{ReasonForkUnsupported, ReasonUnsupportedEvent, ReasonEnvelopeMismatch, ReasonReportInvalid}},
		{"unknown problem", with(problem("neutral")),
			ActionRequired, []ReasonCode{ReasonEnvelopeMismatch}},
		{"settings missing skips steps 4 to 8", with(settingsMissing, workflowOff, approvalMissing, policyMismatch, engine(verdictBlocked, gatesFail)),
			ActionRequired, []ReasonCode{ReasonSettingsMissing}},
		{"settings missing + nil report", with(settingsMissing, nilReport),
			ActionRequired, []ReasonCode{ReasonSettingsMissing, ReasonReportInvalid}},
	}
}

func TestDecideOrder(t *testing.T) {
	runCases(t, orderCases())
}

func matrixCases() []decideCase {
	blocking := []ReasonCode{ReasonBlockingFindings}
	missing := []ReasonCode{ReasonMissingCoverage}
	cases := []decideCase{
		{"healthy/pass/none", with(), Success, []ReasonCode{ReasonHealthy}},
		{"needs_attention/pass/none", with(engine(verdictAttention, gatesPass)), Success, []ReasonCode{ReasonAttentionOnly}},
		{"healthy/pass/some", with(unevaluated), ActionRequired, missing},
		{"needs_attention/pass/some", with(engine(verdictAttention, gatesPass), unevaluated), ActionRequired, missing},
		{"blocked/pass/none", with(engine(verdictBlocked, gatesPass)), Failure, blocking},
		{"blocked/pass/some", with(engine(verdictBlocked, gatesPass), unevaluated),
			ActionRequired, []ReasonCode{ReasonMissingCoverage, ReasonBlockingFindings}},
		{"blocked/unmeasured/none", with(engine(verdictBlocked, gatesUnmeasured)),
			ActionRequired, []ReasonCode{ReasonMissingCoverage, ReasonBlockingFindings}},
		{"blocked/unmeasured/some", with(engine(verdictBlocked, gatesUnmeasured), unevaluated),
			ActionRequired, []ReasonCode{ReasonMissingCoverage, ReasonBlockingFindings}},
	}
	for _, v := range []string{verdictHealthy, verdictAttention} {
		cases = append(cases,
			decideCase{v + "/unmeasured/none", with(engine(v, gatesUnmeasured)), ActionRequired, missing},
			decideCase{v + "/unmeasured/some", with(engine(v, gatesUnmeasured), unevaluated), ActionRequired, missing})
	}
	for _, v := range []string{verdictHealthy, verdictAttention, verdictBlocked} {
		cases = append(cases,
			decideCase{v + "/fail/none", with(engine(v, gatesFail)), Failure, blocking},
			decideCase{v + "/fail/some", with(engine(v, gatesFail), unevaluated),
				Failure, []ReasonCode{ReasonBlockingFindings, ReasonMissingCoverage}})
	}
	for _, v := range []string{"", "BLOCKED", "error"} {
		cases = append(cases,
			decideCase{"verdict " + v + "/pass", with(engine(v, gatesPass)), ActionRequired, []ReasonCode{ReasonEngineError}},
			decideCase{"verdict " + v + "/fail/some", with(engine(v, gatesFail), unevaluated),
				ActionRequired, []ReasonCode{ReasonEngineError, ReasonBlockingFindings, ReasonMissingCoverage}},
			decideCase{"verdict " + v + "/unmeasured", with(engine(v, gatesUnmeasured)),
				ActionRequired, []ReasonCode{ReasonEngineError, ReasonMissingCoverage}})
	}
	for _, g := range []string{"", "PASS"} {
		cases = append(cases,
			decideCase{"healthy/gates " + g, with(engine(verdictHealthy, g)), ActionRequired, []ReasonCode{ReasonEngineError}},
			decideCase{"blocked/gates " + g, with(engine(verdictBlocked, g)),
				ActionRequired, []ReasonCode{ReasonEngineError, ReasonBlockingFindings}},
			decideCase{"healthy/gates " + g + "/some", with(engine(verdictHealthy, g), unevaluated),
				ActionRequired, []ReasonCode{ReasonEngineError, ReasonMissingCoverage}})
	}
	return cases
}

func TestEngineDecisionMatrix(t *testing.T) {
	runCases(t, matrixCases())

	t.Run("engine_error detail is cut", func(t *testing.T) {
		long := strings.Repeat("é", 40)
		o := decide(t, with(engine(long, long)))
		r, _ := find(o, ReasonEngineError)
		want := "verdict=" + strings.Repeat("é", 32) + " hard_gates=" + strings.Repeat("é", 32)
		if r.Detail != want {
			t.Errorf("detail = %q, want %q", r.Detail, want)
		}
	})
	t.Run("missing coverage names the rules", func(t *testing.T) {
		f := with(unevaluated)
		f.Report.Decision.UnevaluatedRequiredRules = append(f.Report.Decision.UnevaluatedRequiredRules,
			report.UnevaluatedRule{RuleID: "no_direct_b_dependency", Reason: "absent"})
		r, _ := find(decide(t, f), ReasonMissingCoverage)
		if want := "layers/no_upward_imports, no_direct_b_dependency"; r.Detail != want {
			t.Errorf("detail = %q, want %q", r.Detail, want)
		}
	})
	// The reason block is never cut from feedback, so its Detail stays small however many rules the report lists.
	t.Run("rule lists are capped", func(t *testing.T) {
		ids := func(n int) []string {
			out := make([]string, n)
			for i := range out {
				out[i] = fmt.Sprintf("rule%02d", i)
			}
			return out
		}
		for _, tt := range []struct {
			n    int
			want string
		}{
			{20, strings.Join(ids(20), ", ")},
			{21, strings.Join(ids(20), ", ") + " and 1 more"},
			{25, strings.Join(ids(20), ", ") + " and 5 more"},
		} {
			for code, reason := range map[ReasonCode]string{ReasonMissingCoverage: "absent", ReasonPolicyDefect: deadReason} {
				f := healthyFacts()
				for _, id := range ids(tt.n) {
					f.Report.Decision.UnevaluatedRequiredRules = append(f.Report.Decision.UnevaluatedRequiredRules,
						report.UnevaluatedRule{RuleID: id, Reason: reason})
				}
				if r, _ := find(decide(t, f), code); r.Detail != tt.want {
					t.Errorf("%s with %d rules: detail = %q, want %q", code, tt.n, r.Detail, tt.want)
				}
			}
		}
	})
}

func TestAttentionOnly(t *testing.T) {
	measuredExcept := func(statuses map[string]string) map[string]report.Dimension {
		dims := map[string]report.Dimension{}
		for _, key := range []string{"intent", "structure", "modularity", "coupling", "change_locality", "complexity", "testability", "operations", "drift"} {
			status := "measured"
			if s, ok := statuses[key]; ok {
				status = s
			}
			dims[key] = report.Dimension{Name: key, Status: status}
		}
		return dims
	}
	without := func(dims map[string]report.Dimension, key string) map[string]report.Dimension {
		delete(dims, key)
		return dims
	}
	finding := func(kind, status string) report.Finding {
		return report.Finding{Kind: kind, Status: status, RuleID: marker, Why: marker}
	}
	cases := []struct {
		name     string
		dims     map[string]report.Dimension
		findings []report.Finding
		detail   string
	}{
		{"dimensions in engine order", measuredExcept(map[string]string{"drift": "unmeasured", "testability": "partial"}), nil,
			"dimensions not fully measured: testability, drift; active diagnostics: 0"},
		{"only live advisories count", measuredExcept(nil), []report.Finding{
			finding("advisory", "new"), finding("advisory", "expired_waiver"), finding("advisory", "baseline"),
			finding("advisory", "waived"), finding("advisory", "fixed"), finding("gate", "baseline"), finding(marker, "new"),
		}, "dimensions not fully measured: none; active diagnostics: 2"},
		{"a dimension the report lacks is not measured", without(measuredExcept(nil), "operations"), nil,
			"dimensions not fully measured: operations; active diagnostics: 0"},
		{"a key outside the nine is never named", func() map[string]report.Dimension {
			dims := measuredExcept(map[string]string{"coupling": marker})
			dims[marker] = report.Dimension{Status: "unmeasured"}
			return dims
		}(), nil, "dimensions not fully measured: coupling; active diagnostics: 0"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			f := with(engine(verdictAttention, gatesPass), func(f *Facts) {
				f.Report.Dimensions, f.Report.Findings = c.dims, c.findings
			})
			o := decide(t, f)
			wantCodes(t, o, Success, ReasonAttentionOnly)
			r := o.Reasons[0]
			if r.Category != CategoryArchitecture || r.Detail != c.detail {
				t.Errorf("reason = %+v, want category architecture and detail %q", r, c.detail)
			}
			if !strings.Contains(r.Next, "Advisories never block") || strings.Contains(r.Detail+r.Next, marker) {
				t.Errorf("next = %q", r.Next)
			}

			f.Report.Verdict = verdictHealthy
			o = decide(t, f)
			wantCodes(t, o, Success, ReasonHealthy)
			if o.Reasons[0].Detail != "" {
				t.Errorf("healthy detail = %q, want empty", o.Reasons[0].Detail)
			}
		})
	}

	t.Run("never beside another reason", func(t *testing.T) {
		attention := engine(verdictAttention, gatesPass)
		for _, muts := range [][]func(*Facts){{unevaluated}, {deadSelector}, {nonComparable}, {approvalMissing}, {suspended}} {
			if _, hit := find(decide(t, with(append(muts, attention)...)), ReasonAttentionOnly); hit {
				t.Errorf("attention_only beside an action")
			}
		}
	})
}

func TestPolicyDefect(t *testing.T) {
	unmeasured := engine(verdictAttention, gatesUnmeasured)
	runCases(t, []decideCase{
		{"dead selector only", with(unmeasured, deadSelector), ActionRequired, []ReasonCode{ReasonPolicyDefect}},
		{"dead selector and missing evidence", with(unmeasured, unevaluated, deadSelector),
			ActionRequired, []ReasonCode{ReasonPolicyDefect, ReasonMissingCoverage}},
		{"passing gates with a dead selector", with(deadSelector), ActionRequired, []ReasonCode{ReasonPolicyDefect}},
		{"blocker keeps the defect", with(engine(verdictBlocked, gatesFail), deadSelector),
			Failure, []ReasonCode{ReasonBlockingFindings, ReasonPolicyDefect}},
		{"blocker keeps both", with(engine(verdictBlocked, gatesFail), unevaluated, deadSelector),
			Failure, []ReasonCode{ReasonBlockingFindings, ReasonPolicyDefect, ReasonMissingCoverage}},
		{"blocked verdict, unmeasured gates", with(engine(verdictBlocked, gatesUnmeasured), deadSelector),
			ActionRequired, []ReasonCode{ReasonPolicyDefect, ReasonBlockingFindings}},
		{"engine error first", with(engine("error", gatesUnmeasured), deadSelector),
			ActionRequired, []ReasonCode{ReasonEngineError, ReasonPolicyDefect}},
		{"unmeasured without rules is missing evidence", with(unmeasured), ActionRequired, []ReasonCode{ReasonMissingCoverage}},
	})

	t.Run("each reason names its own rules", func(t *testing.T) {
		second := func(f *Facts) {
			f.Report.Decision.UnevaluatedRequiredRules = append(f.Report.Decision.UnevaluatedRequiredRules,
				report.UnevaluatedRule{RuleID: "layers/no_upward_typo", Reason: "selector matches nothing: from layer api"})
		}
		o := decide(t, with(unmeasured, unevaluated, deadSelector, second))
		defect, _ := find(o, ReasonPolicyDefect)
		missing, _ := find(o, ReasonMissingCoverage)
		if defect.Detail != "no_store_to_github, layers/no_upward_typo" || missing.Detail != "layers/no_upward_imports" {
			t.Errorf("details = %q and %q", defect.Detail, missing.Detail)
		}
		if defect.Category != CategoryGovernance || !strings.Contains(defect.Next, "owner-approved policy PR") ||
			!strings.Contains(defect.Next, "guard: true") || strings.Contains(defect.Detail+defect.Next, "gihub") {
			t.Errorf("policy_defect = %+v", defect)
		}
	})

	t.Run("near misses lack evidence", func(t *testing.T) {
		for _, reason := range []string{
			"Selector matches nothing: to internal/x/**", " selector matches nothing: to internal/x/**",
			"the selector matches nothing", "rule scope cannot be established from the supported source inventory", "",
		} {
			f := with(unmeasured, func(f *Facts) {
				f.Report.Decision.UnevaluatedRequiredRules = []report.UnevaluatedRule{{RuleID: "rule/a", Reason: reason}}
			})
			wantCodes(t, decide(t, f), ActionRequired, ReasonMissingCoverage)
		}
	})
}

func TestReasonCodesAreUnique(t *testing.T) {
	want := []ReasonCode{ReasonMissingCoverage, ReasonBlockingFindings}
	wantCodes(t, decide(t, with(engine(verdictBlocked, gatesPass), unevaluated)), ActionRequired, want...)
	wantCodes(t, decide(t, with(engine(verdictBlocked, gatesUnmeasured))), ActionRequired, want...)

	rng := fuzzRNG()
	for range fuzzRounds {
		decide(t, fuzzFacts(rng)) // checkInvariants fails on a repeated code
	}
}

func TestDigestPredicate(t *testing.T) {
	const match, other = "match", "other"
	kinds := []struct {
		name  string
		code  ReasonCode
		apply func(f *Facts, trusted []string, claimed string)
	}{
		{"baseline", ReasonBaselineMismatch, func(f *Facts, trusted []string, claimed string) {
			f.Trusted.BaselineDigests, f.BaselineDigest = trusted, claimed
			if len(trusted) == 0 {
				f.Report.GateReference = &report.Reference{Status: refNonComparable, Reasons: []string{noBaselineLoaded}}
			}
		}},
		{"labels", ReasonLabelsMismatch, func(f *Facts, trusted []string, claimed string) {
			f.Trusted.LabelsDigests, f.LabelsDigest = trusted, claimed
		}},
	}
	lists := map[string][]string{"empty": nil, "one": {baseBaseline}, "two": {baseBaseline, headBaseline}}
	claims := map[string]string{"none": "", match: baseBaseline, other: strings.Repeat("9", 64), "prefixed": "sha256:" + baseBaseline}

	for _, k := range kinds {
		for listName, trusted := range lists {
			for claimName, claimed := range claims {
				mismatch := !inputs.Accepts(trusted, claimed)
				if want := (len(trusted) == 0 && claimed != "") || (len(trusted) > 0 && claimName != match); mismatch != want {
					t.Fatalf("inputs.Accepts(%s, %s) = %v", listName, claimName, !mismatch)
				}
				for _, withReport := range []bool{true, false} {
					name := k.name + "/" + listName + "/" + claimName
					if !withReport {
						name += "/nil report"
					}
					t.Run(name, func(t *testing.T) {
						f := healthyFacts()
						k.apply(&f, trusted, claimed)
						if !withReport {
							f.Report = nil
						}
						o := decide(t, f)
						r, got := find(o, k.code)
						if got != mismatch {
							t.Fatalf("%s present = %v, want %v (reasons %v)", k.code, got, mismatch, codes(o))
						}
						if withReport && !mismatch && o.Conclusion != Success {
							t.Errorf("matching digests gave %v", codes(o))
						}
						wantDetail := claimed
						if claimed == "" {
							wantDetail = "none"
						}
						if got && r.Detail != wantDetail {
							t.Errorf("detail = %q, want %q", r.Detail, wantDetail)
						}
					})
				}
			}
		}
	}

	t.Run("an empty digest never matches", func(t *testing.T) {
		f := with(func(f *Facts) { f.Trusted.LabelsDigests, f.LabelsDigest = []string{""}, "" })
		wantCodes(t, decide(t, f), ActionRequired, ReasonLabelsMismatch)
	})
}

func TestComparabilityRule(t *testing.T) {
	long := strings.Repeat("r", 90)
	cases := []struct {
		name   string
		ref    *report.Reference
		detail string
		hit    bool
	}{
		{"nil", nil, "no gate reference", true},
		{"comparable", &report.Reference{Status: refComparable, BaselinePresent: true}, "", false},
		{"non_comparable", &report.Reference{Status: refNonComparable, Reasons: []string{"model hash differs", "measurement profile differs"}, Drift: []string{"model_hash", "measurement_profile"}, BaselinePresent: true},
			"model hash differs; measurement profile differs", true},
		{"not_requested", &report.Reference{Status: refNotRequested, BaselinePresent: true}, "", true},
		{"unknown status", &report.Reference{Status: "COMPARABLE", BaselinePresent: true}, "", true},
		{"four long reasons", &report.Reference{Status: refNonComparable, Reasons: []string{long, long, long, "fourth"}, BaselinePresent: true},
			(long + "; " + long + "; " + long)[:200], true},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			o := decide(t, with(gateRef(c.ref)))
			r, got := find(o, ReasonBaselineNotComparable)
			if got != c.hit {
				t.Fatalf("baseline_not_comparable present = %v, want %v", got, c.hit)
			}
			if !c.hit {
				wantCodes(t, o, Success, ReasonHealthy)
				return
			}
			wantCodes(t, o, ActionRequired, ReasonBaselineNotComparable)
			if r.Detail != c.detail {
				t.Errorf("detail = %q, want %q", r.Detail, c.detail)
			}
			if utf8.RuneCountInString(r.Detail) > 200 || strings.Contains(r.Detail, "fourth") {
				t.Errorf("detail is not capped: %q", r.Detail)
			}
		})
	}

	t.Run("no baseline anywhere is ignored", func(t *testing.T) {
		f := with(func(f *Facts) {
			f.Trusted.BaselineDigests, f.BaselineDigest = nil, ""
			f.Report.GateReference = &report.Reference{Status: refNonComparable, Reasons: []string{noBaselineLoaded}}
		})
		wantCodes(t, decide(t, f), Success, ReasonHealthy)
		f.Report.GateReference = nil
		wantCodes(t, decide(t, f), Success, ReasonHealthy)
	})
	t.Run("Comparison.Status is never read", func(t *testing.T) {
		for _, status := range []string{refNonComparable, refComparable, refNotRequested, "neutral"} {
			f := with(func(f *Facts) { f.Report.Comparison.Status = status })
			wantCodes(t, decide(t, f), Success, ReasonHealthy)
			f = with(nonComparable, func(f *Facts) { f.Report.Comparison.Status = status })
			wantCodes(t, decide(t, f), ActionRequired, ReasonBaselineNotComparable)
		}
	})
}

func TestPolicyDigestSet(t *testing.T) {
	approved := func(f *Facts) {
		f.Trusted.Approval = inputs.ApprovalGranted
		f.Trusted.PolicyDigests = []string{basePolicy, headPolicy}
	}
	noPolicy := func(f *Facts) { f.Trusted.PolicyDigests = nil }

	wantCodes(t, decide(t, with()), Success, ReasonHealthy)
	wantCodes(t, decide(t, with(approved, policyMismatch)), Success, ReasonHealthy)

	o := decide(t, with(func(f *Facts) { f.Report.Comparison.ConfigHash = strings.Repeat("9", 64) }))
	wantCodes(t, o, ActionRequired, ReasonPolicyMismatch)
	if r, _ := find(o, ReasonPolicyMismatch); r.Detail != strings.Repeat("9", 64) {
		t.Errorf("detail = %q, want the reported config_hash", r.Detail)
	}

	wantCodes(t, decide(t, with(func(f *Facts) { f.Report.Comparison.ConfigHash = "" })), ActionRequired, ReasonPolicyMismatch)
	wantCodes(t, decide(t, with(func(f *Facts) {
		f.Trusted.PolicyDigests = []string{""}
		f.Report.Comparison.ConfigHash = ""
	})), ActionRequired, ReasonPolicyMismatch)

	wantCodes(t, decide(t, with(noPolicy)), ActionRequired, ReasonPolicyMissing)
	wantCodes(t, decide(t, with(noPolicy, nilReport)), ActionRequired, ReasonReportInvalid, ReasonPolicyMissing)
	wantCodes(t, decide(t, with(nilReport)), ActionRequired, ReasonReportInvalid)
}

func TestPolicyMissingWindow(t *testing.T) {
	window := func(f *Facts) { f.Trusted.PolicyDigests = nil }

	o := decide(t, with(window))
	wantCodes(t, o, ActionRequired, ReasonPolicyMissing)
	if r := o.Reasons[0]; r.Category != CategoryGovernance || !strings.Contains(r.Next, "policy PR") {
		t.Errorf("policy_missing = %+v", r)
	}

	wantCodes(t, decide(t, with(window, approvalMissing, policyMismatch)),
		ActionRequired, ReasonOwnerApprovalMissing, ReasonPolicyMissing)

	wantCodes(t, decide(t, with(policyMismatch, func(f *Facts) {
		f.Trusted.Approval = inputs.ApprovalGranted
		f.Trusted.PolicyDigests = []string{headPolicy}
	})), Success, ReasonHealthy)
}

func TestIdentityReasons(t *testing.T) {
	wantCodes(t, decide(t, with(identityOff, noProfile)), ActionRequired, ReasonProfileMissing)
	wantCodes(t, decide(t, with(identityOff)), ActionRequired, ReasonUnknownIdentity)
	wantCodes(t, decide(t, with(identityOff, nilReport)), ActionRequired, ReasonReportInvalid)
}

func TestReasonCatalogComplete(t *testing.T) {
	categories := map[Category][]ReasonCode{
		CategoryArchitecture: {ReasonHealthy, ReasonAttentionOnly, ReasonBlockingFindings},
		CategoryGovernance:   {ReasonPolicyMissing, ReasonPolicyDefect, ReasonOwnerApprovalMissing, ReasonSettingsMissing, ReasonWaiverExpired},
		CategorySubscription: {ReasonSuspended},
	}
	want := map[ReasonCode]Category{}
	for category, list := range categories {
		for _, code := range list {
			want[code] = category
		}
	}
	if len(allCodes) != 26 {
		t.Fatalf("contract has %d reason codes, want 26", len(allCodes))
	}
	seen := map[ReasonCode]bool{}
	for _, code := range allCodes {
		if seen[code] {
			t.Fatalf("%q listed twice", code)
		}
		seen[code] = true
		e, ok := lookup(code)
		if !ok {
			t.Errorf("%q is not in the catalog", code)
			continue
		}
		category, listed := want[code]
		if !listed {
			category = CategoryEvidence
		}
		if e.category != category {
			t.Errorf("%q category = %q, want %q", code, e.category, category)
		}
		if (e.next == "") != (code == ReasonHealthy) {
			t.Errorf("%q next = %q", code, e.next)
		}
		wantConclusion := ActionRequired
		switch code {
		case ReasonHealthy, ReasonAttentionOnly:
			wantConclusion = Success
		case ReasonBlockingFindings:
			wantConclusion = Failure
		}
		if e.conclusion != wantConclusion {
			t.Errorf("%q conclusion = %q, want %q", code, e.conclusion, wantConclusion)
		}
		if strings.Contains(strings.ToLower(e.next), "verified") {
			t.Errorf("%q next says verified", code)
		}
	}
	if _, ok := lookup("neutral"); ok {
		t.Errorf("catalog knows a code outside the contract")
	}
	for _, code := range []ReasonCode{ReasonWorkflowNotApproved, ReasonSettingsMissing} {
		if e, _ := lookup(code); !strings.Contains(e.next, "`"+settings.Path+"`") {
			t.Errorf("%q next = %q, want it to name %s", code, e.next, settings.Path)
		}
	}
	// An installed workflow keeps the pin onboarding wrote, so the identity reasons point to the repin section of the
	// user guide, never back to the onboarding workflow.
	const repinSection = `"Move an installed workflow to a new engine"`
	for _, code := range []ReasonCode{ReasonUnknownIdentity, ReasonProfileMissing} {
		if e, _ := lookup(code); !strings.Contains(e.next, repinSection) || strings.Contains(e.next, "onboarding") {
			t.Errorf("%q next = %q, want it to name %s and not the onboarding workflow", code, e.next, repinSection)
		}
	}
}

func TestSuspended(t *testing.T) {
	for _, e := range []inputs.Entitlement{{Reason: "Trial ended. Open the Archfit App settings to subscribe."}, {}} {
		o := Suspended(e)
		checkInvariants(t, o)
		wantCodes(t, o, ActionRequired, ReasonSuspended)
		wantNext := e.Reason
		if wantNext == "" {
			wantNext = Without(ReasonSuspended).Reasons[0].Next
		}
		if r := o.Reasons[0]; r.Category != CategorySubscription || r.Next != wantNext {
			t.Errorf("Suspended(%+v) = %+v", e, r)
		}
		decided := decide(t, with(func(f *Facts) { f.Trusted.Suspended, f.Trusted.Entitlement = true, e }))
		if decided.Reasons[0] != o.Reasons[0] {
			t.Errorf("Decide gives %+v, Suspended gives %+v", decided.Reasons[0], o.Reasons[0])
		}
	}
}

func TestWithout(t *testing.T) {
	for _, code := range withoutCodes {
		o := Without(code)
		checkInvariants(t, o)
		wantCodes(t, o, ActionRequired, code)
	}
	for _, code := range []ReasonCode{ReasonHealthy, "neutral", "", ReasonBlockingFindings, ReasonReportInvalid} {
		o := Without(code)
		checkInvariants(t, o)
		wantCodes(t, o, ActionRequired, ReasonEngineError)
	}
	if r := Without(ReasonSuspended).Reasons[0]; r.Category != CategorySubscription || r.Next != "Archfit subscription inactive." {
		t.Errorf("suspended = %+v", r)
	}
}

func TestTargetMoved(t *testing.T) {
	long := strings.Repeat("é", 81)
	tests := []struct {
		name       string
		cycle      []string
		base, tip  string
		wantDetail string
		wantNext   string // a part of Next; "" = the catalog's, which claims no cycle
	}{
		{"two modules", []string{"a", "b"}, "0123456789abcdef", "fedcba9876543210",
			"a, b; 0123456789ab..fedcba987654", "The target branch moved, and with it this pull request closes a module cycle: a, b. "},
		{"five modules are all shown", []string{"a", "b", "c", "d", "e"}, "base", "tip",
			"a, b, c, d, e; base..tip", "closes a module cycle: a, b, c, d, e. "},
		{"the sixth module is counted", []string{"a", "b", "c", "d", "e", "f", "g"}, "base", "tip",
			"a, b, c, d, e, and 2 more; base..tip", "closes a module cycle: a, b, c, d, e, and 2 more. "},
		{"a long module name is cut", []string{long}, "base", "tip",
			strings.Repeat("é", 80) + "…; base..tip", "closes a module cycle: " + strings.Repeat("é", 80) + "…. "},
		{"no cycle known", nil, "base", "tip", "base..tip", ""},
		{"no cycle known, no base", nil, "", "tip", "..tip", ""},
		{"nothing known", nil, "", "", "", ""},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			o := TargetMoved(tt.cycle, tt.base, tt.tip)
			checkInvariants(t, o)
			wantCodes(t, o, ActionRequired, ReasonTargetMoved)
			r := o.Reasons[0]
			if r.Category != CategoryEvidence || r.Detail != tt.wantDetail {
				t.Errorf("category = %q, Detail = %q; want evidence, %q", r.Category, r.Detail, tt.wantDetail)
			}
			catalogNext, _ := lookup(ReasonTargetMoved)
			if tt.wantNext == "" && r.Next != catalogNext.next {
				t.Errorf("Next = %q, want the catalog's %q", r.Next, catalogNext.next)
			}
			if !strings.Contains(r.Next, tt.wantNext) {
				t.Errorf("Next = %q, want it to contain %q", r.Next, tt.wantNext)
			}
			if !strings.Contains(r.Next, "gh pr update-branch") {
				t.Errorf("Next = %q, want the recovery", r.Next)
			}
		})
	}
	if ReportDerivedAction(ReasonTargetMoved) {
		t.Error("target_moved is report-derived: a page would show a report that was measured on another base")
	}
	if derived, known := ClassifyDisplayReason(ReasonTargetMoved, CategoryEvidence); derived || !known {
		t.Errorf("ClassifyDisplayReason = %v, %v, want known and not report-derived", derived, known)
	}
}

func TestSharedHead(t *testing.T) {
	long := strings.Repeat("é", 81)
	pr := func(n int, base string) SharedPull { return SharedPull{n, base} }
	tests := []struct {
		name       string
		others     []SharedPull
		wantDetail string
		wantBase   string // the base named in Next
	}{
		{"one", []SharedPull{pr(8, "loosened")}, "#8 into loosened", "loosened"},
		{"five are all shown", []SharedPull{pr(1, "a"), pr(2, "b"), pr(3, "c"), pr(4, "d"), pr(5, "e")},
			"#1 into a, #2 into b, #3 into c, #4 into d, #5 into e", "a"},
		{"the sixth is counted", []SharedPull{pr(1, "a"), pr(2, "b"), pr(3, "c"), pr(4, "d"), pr(5, "e"), pr(6, "f"), pr(7, "g")},
			"#1 into a, #2 into b, #3 into c, #4 into d, #5 into e, and 2 more", "a"},
		{"backticks and control characters are dropped", []SharedPull{pr(8, "x`\n`y\x00z")}, "#8 into xyz", "xyz"},
		{"a long base is cut", []SharedPull{pr(8, long)}, "#8 into " + strings.Repeat("é", 80) + "…", strings.Repeat("é", 80) + "…"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			o := SharedHead(tt.others)
			checkInvariants(t, o)
			wantCodes(t, o, ActionRequired, ReasonSharedHead)
			r := o.Reasons[0]
			if r.Category != CategoryEvidence || r.Detail != tt.wantDetail {
				t.Errorf("category = %q, Detail = %q; want evidence, %q", r.Category, r.Detail, tt.wantDetail)
			}
			wantNext := "Another open pull request into `" + tt.wantBase + "` has this head commit, so its checks would show this result. " +
				"Close that pull request or change its base to the default branch, then run the Archfit workflow again."
			if r.Next != wantNext {
				t.Errorf("Next = %q, want %q", r.Next, wantNext)
			}
		})
	}
	if ReportDerivedAction(ReasonSharedHead) {
		t.Error("shared_head is report-derived: a page would show a report under a result that is not about it")
	}
}

// A shared head ends in action_required whatever else the decision found, success included.
func TestDecideSharedHeadNeverSucceeds(t *testing.T) {
	f := healthyFacts()
	if o := Decide(f); o.Conclusion != Success {
		t.Fatalf("healthy facts = %+v", o)
	}
	f.SharedHead = []SharedPull{{8, "loosened"}}
	o := Decide(f)
	checkInvariants(t, o)
	wantCodes(t, o, ActionRequired, ReasonSharedHead)

	f.Report.Verdict, f.Report.Decision.HardGates = verdictBlocked, gatesFail
	if o := Decide(f); o.Conclusion != ActionRequired || o.Reasons[0].Code != ReasonSharedHead {
		t.Errorf("blocked report with a shared head = %+v, want shared_head first", o)
	}
}

func TestApprovalMissing(t *testing.T) {
	for _, paths := range [][]string{{".archfit-app.yaml"}, {".archfit.yaml", ".github/workflows/archfit.yaml"}, nil} {
		tr := inputs.Trusted{SettingsFound: true, Approval: inputs.ApprovalMissing, ProtectedPaths: paths}
		o := ApprovalMissing(tr)
		checkInvariants(t, o)
		wantCodes(t, o, ActionRequired, ReasonOwnerApprovalMissing)
		decided := decide(t, with(approvalMissing, func(f *Facts) { f.Trusted.ProtectedPaths = paths }))
		if decided.Reasons[0] != o.Reasons[0] {
			t.Errorf("Decide gives %+v, ApprovalMissing gives %+v", decided.Reasons[0], o.Reasons[0])
		}
	}
}

func TestUnapproved(t *testing.T) {
	for approval, want := range map[inputs.Approval]bool{
		inputs.ApprovalNotRequired: false,
		inputs.ApprovalGranted:     false,
		inputs.ApprovalMissing:     true,
		"":                         true,
		"skipped":                  true,
	} {
		tr := inputs.Trusted{Approval: approval}
		if got := Unapproved(tr); got != want {
			t.Errorf("Unapproved(%q) = %v, want %v", approval, got, want)
		}
		decided := decide(t, with(func(f *Facts) { f.Trusted.Approval = approval }))
		if got := slices.ContainsFunc(decided.Reasons, func(r Reason) bool { return r.Code == ReasonOwnerApprovalMissing }); got != want {
			t.Errorf("Decide with approval %q names owner_approval_missing: %v, want %v", approval, got, want)
		}
	}
}

// TestReferenceConflictsWithTheTrustedBaseline: the engine loaded a baseline exactly when the App trusts one, and a
// trusted baseline needs a reference that compares. Every other pairing conflicts.
func TestReferenceConflictsWithTheTrustedBaseline(t *testing.T) {
	cases := []struct {
		name    string
		ref     *report.Reference
		trusted bool
		want    bool
	}{
		{"no reference, no baseline", nil, false, false},
		{"no reference, trusted baseline", nil, true, true},
		{"comparable, present, trusted", &report.Reference{Status: refComparable, BaselinePresent: true}, true, false},
		{"comparable, no baseline, none trusted", &report.Reference{Status: refComparable}, false, false},
		{"comparable, present, none trusted", &report.Reference{Status: refComparable, BaselinePresent: true}, false, true},
		{"comparable, no baseline, trusted", &report.Reference{Status: refComparable}, true, true},
		{"not comparable, present, trusted", &report.Reference{Status: refNonComparable, BaselinePresent: true}, true, true},
		{"not comparable, no baseline, none trusted", &report.Reference{Status: refNonComparable}, false, false},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			if got := ReferenceConflicts(c.ref, c.trusted); got != c.want {
				t.Errorf("ReferenceConflicts = %v, want %v", got, c.want)
			}
		})
	}
}

// TestNextNamesTheOwnerActionFromDrift: the drift classes, never the reason text, pick the owner action. A re-anchor
// crosses a scoring epoch or a measurement profile only; every other class needs a full capture.
func TestNextNamesTheOwnerActionFromDrift(t *testing.T) {
	cases := []struct {
		name  string
		drift []string
		next  string
	}{
		{"profile", []string{"measurement_profile"}, nextReanchor},
		{"rubric", []string{"rubric_version"}, nextReanchor},
		{"profile and rubric", []string{"measurement_profile", "rubric_version"}, nextReanchor},
		{"classification", []string{"classification_hash"}, nextFullCapture},
		{"model", []string{"model_hash"}, nextFullCapture},
		{"labels", []string{"labels_hash"}, nextFullCapture},
		{"incomplete reference", []string{"reference_incomplete"}, nextFullCapture},
		{"profile with a policy change", []string{"measurement_profile", "classification_hash"}, nextFullCapture},
		{"no class given", nil, nextFullCapture},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			o := decide(t, with(gateRef(&report.Reference{Status: refNonComparable, BaselinePresent: true, Drift: c.drift})))
			r, ok := find(o, ReasonBaselineNotComparable)
			if !ok {
				t.Fatalf("baseline_not_comparable missing: %v", codes(o))
			}
			if r.Next != c.next {
				t.Errorf("Next = %q, want %q", r.Next, c.next)
			}
		})
	}
}

// TestPresenceMismatchNamesTheSide: when the engine and the protected ref disagree about the baseline, the detail
// names the side that holds one, and the next action is a full capture whatever the drift says.
func TestPresenceMismatchNamesTheSide(t *testing.T) {
	cases := []struct {
		name   string
		facts  Facts
		detail string
	}{
		{"protected ref has one, engine loaded none",
			with(gateRef(&report.Reference{Status: refComparable, BaselinePresent: false, Drift: []string{"measurement_profile"}})),
			presenceDetail(true)},
		{"engine loaded one, protected ref has none",
			with(func(f *Facts) { f.Trusted.BaselineDigests = nil }, gateRef(&report.Reference{Status: refComparable, BaselinePresent: true})),
			presenceDetail(false)},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			o := decide(t, c.facts)
			r, ok := find(o, ReasonBaselineNotComparable)
			if !ok {
				t.Fatalf("baseline_not_comparable missing: %v", codes(o))
			}
			if r.Detail != c.detail {
				t.Errorf("detail = %q, want %q", r.Detail, c.detail)
			}
			if r.Next != nextFullCapture {
				t.Errorf("next = %q, want the full capture", r.Next)
			}
		})
	}
}

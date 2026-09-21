package verdict

import (
	"go/parser"
	"go/token"
	"os"
	"reflect"
	"strconv"
	"strings"
	"testing"

	"github.com/alexei-led/archfit-app/internal/gate/ingest"
	"github.com/alexei-led/archfit-app/internal/gate/inputs"
	"github.com/alexei-led/archfit-app/internal/wire/report"
)

func TestConclusionSetIsClosed(t *testing.T) {
	rng := fuzzRNG()
	for range fuzzRounds {
		decide(t, fuzzFacts(rng))
	}
	for _, f := range []Facts{{}, {Report: &report.Report{}}, {Trusted: inputs.Trusted{SettingsFound: true}, Report: &report.Report{}}} {
		if o := decide(t, f); o.Conclusion != ActionRequired {
			t.Errorf("zero facts gave %q", o.Conclusion)
		}
	}
	for _, code := range append([]ReasonCode{"", "neutral", "skipped", "success"}, allCodes...) {
		o := Without(code)
		checkInvariants(t, o)
		if o.Conclusion != ActionRequired {
			t.Errorf("Without(%q) = %q", code, o.Conclusion)
		}
	}
}

func preconditionCases() []decideCase {
	one := func(name string, first ReasonCode, muts ...func(*Facts)) decideCase {
		return decideCase{name: name, facts: with(muts...), reasons: []ReasonCode{first}}
	}
	return []decideCase{
		one("suspended", ReasonSuspended, suspended),
		one("settings missing", ReasonSettingsMissing, settingsMissing),
		one("fork", ReasonForkUnsupported, problem(ingest.ProblemFork)),
		one("unsupported event", ReasonUnsupportedEvent, problem(ingest.ProblemUnsupportedEvent)),
		one("envelope mismatch", ReasonEnvelopeMismatch, problem(ingest.ProblemEnvelopeMismatch)),
		one("report invalid", ReasonReportInvalid, problem(ingest.ProblemReportInvalid)),
		one("unknown problem", ReasonEnvelopeMismatch, problem("skipped")),
		one("workflow", ReasonWorkflowNotApproved, workflowOff),
		one("identity", ReasonUnknownIdentity, identityOff),
		one("identity without profile", ReasonProfileMissing, identityOff, noProfile),
		one("policy digest", ReasonPolicyMismatch, policyMismatch),
		one("policy missing", ReasonPolicyMissing, func(f *Facts) { f.Trusted.PolicyDigests = nil }),
		one("baseline digest", ReasonBaselineMismatch, baselineMismatch),
		one("baseline not used", ReasonBaselineMismatch, func(f *Facts) { f.BaselineDigest = "" }),
		one("labels digest", ReasonLabelsMismatch, labelsMismatch),
		one("approval missing", ReasonOwnerApprovalMissing, approvalMissing),
		one("approval unknown", ReasonOwnerApprovalMissing, func(f *Facts) { f.Trusted.Approval = "skipped" }),
		one("not comparable", ReasonBaselineNotComparable, nonComparable),
		one("no gate reference", ReasonBaselineNotComparable, gateRef(nil)),
		one("unmeasured", ReasonMissingCoverage, engine(verdictHealthy, gatesUnmeasured)),
		one("unevaluated rule", ReasonMissingCoverage, unevaluated),
		one("verdict blocked", ReasonBlockingFindings, engine(verdictBlocked, gatesPass)),
		one("hard gates fail", ReasonBlockingFindings, engine(verdictHealthy, gatesFail)),
		one("out-of-enum verdict", ReasonEngineError, engine("error", gatesPass)),
		one("out-of-enum hard gates", ReasonEngineError, engine(verdictHealthy, "PASS")),
		one("nil report", ReasonReportInvalid, nilReport),
	}
}

func TestSuccessRequiresEverything(t *testing.T) {
	wantCodes(t, decide(t, healthyFacts()), Success, ReasonHealthy)

	for _, c := range preconditionCases() {
		t.Run(c.name, func(t *testing.T) {
			o := decide(t, c.facts)
			if o.Conclusion == Success {
				t.Fatalf("success with %s off", c.name)
			}
			if o.Reasons[0].Code != c.reasons[0] {
				t.Errorf("first reason = %q, want %q", o.Reasons[0].Code, c.reasons[0])
			}
		})
	}

	rng := fuzzRNG()
	successes := 0
	for range fuzzRounds {
		f := fuzzFacts(rng)
		o := decide(t, f)
		if got, want := o.Conclusion == Success, successAllowed(f); got != want {
			t.Fatalf("success = %v, preconditions hold = %v, reasons %v, facts %+v", got, want, codes(o), f)
		}
		if o.Conclusion == Success {
			successes++
		}
	}
	if successes == 0 || successes == fuzzRounds {
		t.Fatalf("fuzz saw %d successes in %d rounds: the implication was not exercised", successes, fuzzRounds)
	}
}

func TestDecideIsPure(t *testing.T) {
	rng, twin := fuzzRNG(), fuzzRNG()
	for range 2000 {
		f, untouched := fuzzFacts(rng), fuzzFacts(twin) // two deep copies of the same facts
		first, second := Decide(f), Decide(f)
		if !reflect.DeepEqual(first, second) {
			t.Fatalf("outcomes differ: %v and %v", first, second)
		}
		if !reflect.DeepEqual(f, untouched) {
			t.Fatalf("facts were mutated: %+v", f)
		}
		if other := Decide(untouched); !reflect.DeepEqual(first, other) {
			t.Fatalf("equal facts gave %v and %v", first, other)
		}
	}

	const internal = "github.com/alexei-led/archfit-app/"
	allowed := map[string]bool{
		internal + "internal/wire/report": true,
		internal + "internal/gate/ingest": true,
		internal + "internal/gate/inputs": true,
	}
	forbidden := []string{"time", "net", "os", "log", "io", "sync", "context", "math/rand", "unsafe", "syscall", "runtime"}
	files, err := os.ReadDir(".")
	if err != nil {
		t.Fatal(err)
	}
	checked := 0
	for _, file := range files {
		name := file.Name()
		if !strings.HasSuffix(name, ".go") || strings.HasSuffix(name, "_test.go") {
			continue
		}
		parsed, err := parser.ParseFile(token.NewFileSet(), name, nil, parser.ImportsOnly)
		if err != nil {
			t.Fatal(err)
		}
		checked++
		for _, spec := range parsed.Imports {
			path, _ := strconv.Unquote(spec.Path.Value)
			if strings.Contains(path, ".") && !allowed[path] {
				t.Errorf("%s imports %s", name, path)
			}
			for _, bad := range forbidden {
				if path == bad || strings.HasPrefix(path, bad+"/") {
					t.Errorf("%s imports %s", name, path)
				}
			}
		}
	}
	if checked == 0 {
		t.Fatal("no source file inspected")
	}
}

const (
	marker      = "LEAKMARKER"
	markerScore = 987654
)

// poison overwrites every string and number below v with a marker.
func poison(v reflect.Value) {
	switch v.Kind() {
	case reflect.String:
		v.SetString(marker)
	case reflect.Int, reflect.Int64:
		v.SetInt(markerScore)
	case reflect.Float64:
		v.SetFloat(markerScore + 0.5)
	case reflect.Pointer:
		if !v.IsNil() {
			poison(v.Elem())
		}
	case reflect.Struct:
		for i := range v.NumField() {
			if v.Field(i).CanSet() {
				poison(v.Field(i))
			}
		}
	case reflect.Slice:
		for i := range v.Len() {
			poison(v.Index(i))
		}
	case reflect.Map:
		for _, key := range v.MapKeys() {
			value := reflect.New(v.Type().Elem()).Elem()
			value.Set(v.MapIndex(key))
			poison(value)
			v.SetMapIndex(key, value)
		}
	}
}

func TestDetailCarriesOnlyIdentifiers(t *testing.T) {
	poisoned := func(name string) *report.Report {
		r := loadReport(t, name)
		if len(r.Findings) == 0 || len(r.AgentTasks) == 0 || len(r.Dimensions) == 0 {
			t.Fatalf("%s has nothing to poison", name)
		}
		for _, part := range []any{&r.Findings, &r.AgentTasks, &r.Dimensions, &r.Coverage, &r.Seams} {
			poison(reflect.ValueOf(part).Elem())
		}
		r.Decision.ActiveBlockers, r.Decision.AttentionDimensions, r.Decision.UnknownDimensions = markerScore, markerScore, markerScore
		for i := range r.Decision.UnevaluatedRequiredRules {
			r.Decision.UnevaluatedRequiredRules[i].Reason = marker
		}
		c := &r.Comparison
		c.BaseRef, c.ModelHash, c.LabelsHash, c.RubricVersion, c.TaskOriginStatus = marker, marker, marker, marker, marker
		c.Reasons, c.TaskOriginReasons = []string{marker}, []string{marker}
		return r
	}
	for _, name := range []string{"all-keys", "blocked", "blocker-plus-unevaluated"} {
		for _, mutate := range []func(*Facts){
			func(*Facts) {},
			func(f *Facts) {
				*f = with(nonComparable, approvalMissing, baselineMismatch, labelsMismatch, identityOff, workflowOff, suspended)
			},
		} {
			f := cleanFacts(poisoned(name), fixtureFacts{})
			poisonedReport := f.Report
			mutate(&f)
			f.Report = poisonedReport
			o := decide(t, f)
			for _, r := range o.Reasons {
				text := r.Detail + " " + r.Next
				if strings.Contains(text, marker) || strings.Contains(text, strconv.Itoa(markerScore)) ||
					strings.Contains(strings.ToLower(text), "score") {
					t.Errorf("%s: reason %q leaks report text: %q", name, r.Code, text)
				}
			}
		}
	}
}

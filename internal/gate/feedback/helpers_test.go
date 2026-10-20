package feedback

import (
	"context"
	"log/slog"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/alexei-led/archfit-app/internal/gate/inputs"
	"github.com/alexei-led/archfit-app/internal/gate/verdict"
	"github.com/alexei-led/archfit-app/internal/github/api"
	"github.com/alexei-led/archfit-app/internal/store"
	"github.com/alexei-led/archfit-app/internal/wire/manifest"
	"github.com/alexei-led/archfit-app/internal/wire/report"
	"github.com/alexei-led/archfit-app/internal/wire/settings"
)

const (
	headA = "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa"
	headB = "bbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb"

	policyDigest   = "1111111111111111111111111111111111111111111111111111111111111111"
	baselineDigest = "2222222222222222222222222222222222222222222222222222222222222222"
	labelsDigest   = "3333333333333333333333333333333333333333333333333333333333333333"
	reportDigest   = "4444444444444444444444444444444444444444444444444444444444444444"
)

// TestMain keeps publication logs out of the test output.
func TestMain(m *testing.M) {
	slog.SetDefault(slog.New(slog.DiscardHandler))
	os.Exit(m.Run())
}

var t0 = time.Date(2026, 9, 21, 10, 0, 0, 0, time.UTC)

// clock is a settable fixed clock.
type clock struct {
	mu sync.Mutex
	t  time.Time
}

func (c *clock) now() time.Time {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.t
}

func (c *clock) set(t time.Time) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.t = t
}

type harness struct {
	checks   *fakeChecks
	comments *fakeComments
	pulls    *fakePulls
	clock    *clock
	store    store.Store
	pub      Publisher
}

// appURL is the App base URL the harness publisher links to.
const appURL = "https://app.example"

func newHarness(prHead string) *harness {
	h := &harness{
		checks:   &fakeChecks{},
		comments: &fakeComments{},
		pulls:    &fakePulls{headSHA: prHead},
		clock:    &clock{t: t0},
		store:    store.NewMemory(),
	}
	h.pub = New(h.checks, h.comments, h.pulls, h.store, appURL, h.clock.now)
	return h
}

// onContention replaces the wait between lease polls: f runs on each wait, then the wait returns at once.
func (h *harness) onContention(f func()) {
	h.pub.(*serialized).wait = func(ctx context.Context) error {
		f()
		return ctx.Err()
	}
}

func prTarget(head string) inputs.Target {
	return inputs.Target{
		Repo:        api.Repo{RepoRef: api.RepoRef{InstallationID: 1, FullName: "acme/shop"}, ID: 42, DefaultBranch: "main"},
		PullRequest: 7,
		HeadSHA:     head,
		BaseRef:     "main",
	}
}

func pushTarget(head string) inputs.Target {
	t := prTarget(head)
	t.PullRequest = 0
	return t
}

func trusted(gate settings.GateMode) inputs.Trusted {
	return inputs.Trusted{
		Settings:        settings.Settings{Version: 1, Owners: []string{"alice"}, Gate: gate},
		SettingsFound:   true,
		PolicyDigests:   []string{policyDigest},
		BaselineDigests: []string{baselineDigest},
		LabelsDigests:   []string{labelsDigest},
		Approval:        inputs.ApprovalNotRequired,
	}
}

func fullIdentity() manifest.Identity {
	return manifest.Identity{
		EngineVersion: "v3.0.0",
		ImageDigest:   "sha256:9f86d081884c7d659a2feaa0c55ad015a3bf4f1b2b0b822cd15d6c15b0f00a08",
		Platform:      "linux/amd64",
	}
}

func outcome(c verdict.Conclusion, reasons ...verdict.Reason) verdict.Outcome {
	return verdict.Outcome{Conclusion: c, Reasons: reasons}
}

var (
	reasonHealthy  = verdict.Reason{Code: verdict.ReasonHealthy, Category: verdict.CategoryArchitecture, Detail: "report " + reportDigest, Next: "Nothing to do."}
	reasonBlocking = verdict.Reason{Code: verdict.ReasonBlockingFindings, Category: verdict.CategoryArchitecture, Next: "Fix the rule violations, then run archfit check again."} // the catalog's Next
	reasonCoverage = verdict.Reason{Code: verdict.ReasonMissingCoverage, Category: verdict.CategoryEvidence, Detail: "layers/no_upward_imports", Next: "Install the required tool in the workflow, then re-run it."}
	reasonInvalid  = verdict.Reason{Code: verdict.ReasonReportInvalid, Category: verdict.CategoryEvidence, Detail: "report " + reportDigest, Next: "Re-run the Archfit workflow with the pinned action."}
	reasonIdentity = verdict.Reason{Code: verdict.ReasonUnknownIdentity, Category: verdict.CategoryEvidence, Detail: "image sha256:0000", Next: "Use the pinned archfit-action version."}
	reasonProfile  = verdict.Reason{Code: verdict.ReasonBaselineNotComparable, Category: verdict.CategoryEvidence, Detail: "measurement profile differs", Next: "Refresh the baseline on the protected branch."}
	reasonWaiver   = verdict.Reason{Code: verdict.ReasonWaiverExpired, Category: verdict.CategoryGovernance, Detail: "no_direct_b_dependency", Next: "Renew the waiver or fix the finding."}
	reasonSuspend  = verdict.Reason{Code: verdict.ReasonSuspended, Category: verdict.CategorySubscription, Detail: "installation 1", Next: "Trial ended. Choose a plan to resume the gate."}

	reasonAttention = verdict.Reason{Code: verdict.ReasonAttentionOnly, Category: verdict.CategoryArchitecture,
		Detail: "dimensions not fully measured: change_locality, testability, operations, drift; active diagnostics: 1",
		Next:   "Advisories never block. Review the listed dimensions and diagnostics on the archfit/state check."}
)

// fixtureCase is one wire fixture with the outcome the gate would hand to feedback.
type fixtureCase struct {
	name     string // file name without extension; "invalid-input" has no report
	outcome  verdict.Outcome
	identity manifest.Identity
	waiver   time.Time
	advisory bool   // gate mode advisory instead of required
	base     string // the pull request's base branch; "main" when empty
}

func fixtureCases() []fixtureCase {
	id := fullIdentity()
	return []fixtureCase{
		{name: "healthy", outcome: outcome(verdict.Success, reasonHealthy), identity: id},
		{name: "attention-warning-only", outcome: outcome(verdict.Success, reasonAttention), identity: id},
		{name: "blocked", outcome: outcome(verdict.Failure, reasonBlocking), identity: id},
		{name: "missing-required-evidence", outcome: outcome(verdict.ActionRequired, reasonCoverage), identity: id},
		{name: "unknown-identity", outcome: outcome(verdict.ActionRequired, reasonIdentity), identity: manifest.Identity{EngineVersion: "v0.0.0", ImageDigest: "sha256:0000", Platform: "linux/amd64"}},
		{name: "incompatible-profile", outcome: outcome(verdict.ActionRequired, reasonProfile), identity: id},
		{name: "invalid-input", outcome: outcome(verdict.ActionRequired, reasonInvalid), identity: id},
		{name: "blocker-plus-unevaluated", outcome: outcome(verdict.Failure, reasonBlocking, reasonCoverage), identity: id},
		{name: "non-applicable-rule", outcome: outcome(verdict.Success, reasonHealthy), identity: id},
		{name: "locations-empty", outcome: outcome(verdict.Failure, reasonBlocking), identity: id},
		{name: "matched-by-populated", outcome: outcome(verdict.Failure, reasonBlocking), identity: id},
		{name: "both-repair-kinds", outcome: outcome(verdict.Failure, reasonBlocking), identity: id},
		{name: "waiver-expiry-and-order", outcome: outcome(verdict.Failure, reasonBlocking, reasonWaiver), identity: id, waiver: time.Date(2026, 10, 1, 0, 0, 0, 0, time.UTC)},
		// A tripped metric ratchet is one finding without a module edge, in required mode.
		{name: "metric-ratchet", outcome: outcome(verdict.Failure, reasonBlocking), identity: id},
		// Two advisories whose seams carry different balancing moves, in advisory mode with nothing blocking.
		{name: "advisory-balancing-move", outcome: outcome(verdict.Success, reasonAttention), identity: id, advisory: true},
		// The pilot's PR #11 report (engine v3.0.1): one dependency cycle among three modules, coupling advisories and
		// accepted debt, in advisory mode with a baseline.
		{name: "pilot-pr-cycle", outcome: outcome(verdict.Failure, reasonBlocking), identity: id, advisory: true, base: "master"},
	}
}

func loadReport(t *testing.T, name string) *report.Report {
	t.Helper()
	if name == "invalid-input" {
		return nil
	}
	raw, err := os.ReadFile(filepath.Join("testdata", "report", name+".json"))
	if err != nil {
		t.Fatalf("read fixture %s: %v", name, err)
	}
	r, err := report.Decode(raw, "v3.0.0")
	if err != nil {
		t.Fatalf("decode fixture %s: %v", name, err)
	}
	return &r
}

func receiptFor(o verdict.Outcome, run int64, attempt int) Receipt {
	r := Receipt{
		RunID:          run,
		RunAttempt:     attempt,
		ReportDigest:   reportDigest,
		PolicyDigest:   policyDigest,
		BaselineDigest: baselineDigest,
		LabelsDigest:   labelsDigest,
		Conclusion:     o.Conclusion,
	}
	if len(o.Reasons) > 0 {
		r.Reason = o.Reasons[0].Code
	}
	return r
}

func fixturePublication(t *testing.T, c fixtureCase, head string) Publication {
	t.Helper()
	mode := settings.GateRequired
	if c.advisory {
		mode = settings.GateAdvisory
	}
	tr := trusted(mode)
	tr.EarliestWaiverExpiry = c.waiver
	rc := receiptFor(c.outcome, 7, 1)
	rc.EarliestWaiverExpiry = c.waiver
	target := prTarget(head)
	if c.base != "" {
		target.BaseRef = c.base
	}
	return Publication{
		Target:   target,
		Identity: c.identity,
		Outcome:  c.outcome,
		Receipt:  rc,
		Report:   loadReport(t, c.name),
		Trusted:  tr,
	}
}

// blockedPublication is the workhorse of the behavior tests.
func blockedPublication(t *testing.T, head string, run int64, attempt int) Publication {
	t.Helper()
	o := outcome(verdict.Failure, reasonBlocking)
	return Publication{
		Target:   prTarget(head),
		Identity: fullIdentity(),
		Outcome:  o,
		Receipt:  receiptFor(o, run, attempt),
		Report:   loadReport(t, "both-repair-kinds"),
		Trusted:  trusted(settings.GateRequired),
	}
}

func mustContain(t *testing.T, body string, wants ...string) {
	t.Helper()
	for _, w := range wants {
		if !strings.Contains(body, w) {
			t.Errorf("body lacks %q", w)
		}
	}
}

func mustNotContain(t *testing.T, body string, bans ...string) {
	t.Helper()
	for _, b := range bans {
		if strings.Contains(body, b) {
			t.Errorf("body contains %q", b)
		}
	}
}

// section returns the text of one "### " block of a body.
func section(body, heading string) string {
	_, rest, ok := strings.Cut(body, heading+"\n")
	if !ok {
		return ""
	}
	if i := strings.Index(rest, "\n### "); i >= 0 {
		rest = rest[:i]
	}
	return rest
}

package ui

import (
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"slices"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/alexei-led/archfit-app/internal/control/account"
	"github.com/alexei-led/archfit-app/internal/control/assist"
	"github.com/alexei-led/archfit-app/internal/control/onboarding"
	"github.com/alexei-led/archfit-app/internal/control/proposal"
	"github.com/alexei-led/archfit-app/internal/control/ui/session"
	"github.com/alexei-led/archfit-app/internal/github/api"

	"github.com/alexei-led/archfit-app/internal/wire/report"
)

func TestCSRFToken(t *testing.T) {
	a := session.User{Login: "a", UserID: 1, Token: "tok-a"}
	b := session.User{Login: "b", UserID: 2, Token: "tok-b"}
	relogin := session.User{Login: "a", UserID: 1, Token: "tok-a-2"}
	token := csrfToken(a)
	tests := []struct {
		name string
		user session.User
		got  string
		want bool
	}{
		{"own token", a, token, true},
		{"token of A sent by B", b, token, false},
		{"empty", a, "", false},
		{"truncated", a, token[:len(token)-1], false},
		{"A after re-login with a new OAuth token", relogin, token, false},
		{"no session token", session.User{}, csrfToken(session.User{}), false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := validCSRF(tt.user, tt.got); got != tt.want {
				t.Errorf("validCSRF = %v, want %v", got, tt.want)
			}
		})
	}
	if len(token) != 64 {
		t.Errorf("token length = %d, want hex SHA-256", len(token))
	}
	// The comparison must stay constant-time.
	src, err := os.ReadFile("csrf.go")
	if err != nil || !strings.Contains(string(src), "hmac.Equal(") {
		t.Errorf("csrf.go does not compare with hmac.Equal (%v)", err)
	}
}

func TestEditBranchName(t *testing.T) {
	tests := []struct {
		login, requested, want string
	}{
		{"Alice", "", "archfit/edit-alice"},
		{"mary-ann", "", "archfit/edit-mary-ann"},
		{"alice", "archfit/policy", "archfit/policy"},
		{"alice", "main", ""},
		{"alice", "archfit/../main", ""},
		{"alice", "archfit/a b", ""},
		{"alice", "archfit/", ""},
		{"alice", "archfit//x", ""},
	}
	for _, tt := range tests {
		t.Run(tt.login+" "+tt.requested, func(t *testing.T) {
			got, err := editBranch(tt.login, tt.requested)
			if tt.want != "" {
				if err != nil || got != tt.want {
					t.Fatalf("editBranch = %q, %v; want %q", got, err, tt.want)
				}
				return
			}
			if status, _, _ := explain(err); status != http.StatusUnprocessableEntity {
				t.Errorf("status = %d (%v), want 422", status, err)
			}
			e := newEnv(t)
			res := e.as("alice", permWrite).get("/r/" + repoName + "/policy?branch=" + url.QueryEscape(tt.requested))
			wantStatus(t, res, http.StatusUnprocessableEntity)
		})
	}

	t.Run("the rule is proposal's", func(t *testing.T) {
		for _, requested := range []string{"archfit/policy", "archfit/a/b.c_d", "archfit/./x", "archfit/a//b", "archfit/a/", "archfit/ä", "archfit"} {
			_, err := editBranch("alice", requested)
			if got, want := err == nil, proposal.ValidBranch(requested); got != want {
				t.Errorf("editBranch(%q) accepted = %v, want proposal.ValidBranch = %v", requested, got, want)
			}
		}
	})
}

func TestStatusMapping(t *testing.T) {
	const text = policyText + "# my edit\n"
	viaAssist := func(err error) func(*env) { return func(e *env) { e.assist.propose = failingAssist(err) } }
	tests := []struct {
		name       string
		inject     func(*env)
		path       string
		form       url.Values
		want       int
		retryAfter bool
		textKept   bool
	}{
		{"forbidden", func(e *env) { e.grant("alice", repoName, permRead) }, "/policy", formOf("", "", text), 404, false, false},
		{"stale from proposal", func(e *env) { e.props.fail["Propose"] = proposal.ErrStale }, "/policy", formOf("", "", text), 409, false, true},
		{"head_sha is not the draft head", func(e *env) { e.props.push(repoName, "archfit/edit-alice", policyText) }, "/policy", formOf("", sha(1), text), 409, false, true},
		{"invalid form", nil, "/policy", formOf("main", "", text), 422, false, true},
		{"unparseable YAML", nil, "/policy", formOf("", "", "# my edit\nmodules: ["), 422, false, true},
		{"assist invalid request", viaAssist(assist.ErrInvalidRequest), "/assist", assistForm(text), 422, false, true},
		{"assist invalid proposal", viaAssist(assist.ErrInvalidProposal), "/assist", assistForm(text), 422, false, true},
		{"assist no draft", viaAssist(assist.ErrNoDraft), "/assist", assistForm(text), 409, false, true},
		{"assist no consent", viaAssist(account.ErrNoConsent), "/assist", assistForm(text), 409, false, true},
		{"assist not managed", viaAssist(assist.ErrNotManaged), "/assist", assistForm(text), 409, false, true},
		{"assist policy too large", viaAssist(assist.ErrPolicyTooLarge), "/assist", assistForm(text), 409, false, true},
		{"assist no workflow", viaAssist(assist.ErrNoAssistWorkflow), "/assist", assistForm(text), 409, false, true},
		{"assist stale", viaAssist(proposal.ErrStale), "/assist", assistForm(text), 409, false, true},
		{"assist busy", viaAssist(assist.ErrBusy), "/assist", assistForm(text), 429, false, true},
		{"assist managed unavailable", viaAssist(assist.ErrManagedUnavailable), "/assist", assistForm(text), 503, false, true},
		{"assist budget exhausted", viaAssist(account.ErrBudgetExhausted), "/assist", assistForm(text), 503, false, true},
		{"assist provider", viaAssist(assist.ErrProvider), "/assist", assistForm(text), 503, false, true},
		{"rate limited", func(e *env) { e.gh.fail["File"] = api.ErrRateLimited }, "/policy", formOf("", "", text), 503, true, true},
		{"rate limited through assist", viaAssist(fmt.Errorf("wrapped: %w", api.ErrRateLimited)), "/assist", assistForm(text), 503, true, true},
		{"other upstream error", func(e *env) { e.props.fail["Get"] = errors.New("boom") }, "/policy", formOf("", "", text), 502, false, true},
		{"no sandbox", func(e *env) {
			e.onb.startErr = onboarding.ErrNoSandbox
			_ = e.consents.Record(t.Context(), installA, account.Consent{Kind: account.ConsentSourceProcessing})
		}, "/onboarding", url.Values{"mode": {"managed"}, "owners": {"alice"}}, 409, false, false},
		{"already onboarded", func(e *env) { e.onb.startErr = fmt.Errorf("wrapped: %w", onboarding.ErrAlreadyOnboarded) },
			"/onboarding", url.Values{"mode": {"customer"}, "owners": {"alice"}}, 409, false, false},
		{"onboarding busy", func(e *env) {
			e.onb.startErr = onboarding.ErrBusy
			_ = e.consents.Record(t.Context(), installA, account.Consent{Kind: account.ConsentSourceProcessing})
		}, "/onboarding", url.Values{"mode": {"managed"}, "owners": {"alice"}}, 409, false, false},
		{"not found at GitHub", func(e *env) { e.onb.startErr = fmt.Errorf("wrapped: %w", api.ErrNotFound) },
			"/onboarding", url.Values{"mode": {"customer"}, "owners": {"alice"}}, 404, false, false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			e := newEnv(t)
			c := e.as("alice", permAdmin)
			if tt.inject != nil {
				tt.inject(e)
			}
			var res *httptest.ResponseRecorder
			if tt.path == "/onboarding" { // an /api write: the form names the JSON fields
				res = c.postJSON("/api/repos/"+repoName+tt.path, onboardingJSON{Mode: tt.form.Get("mode"), Owners: tt.form.Get("owners")})
				wantContains(t, res.Body.String(), `{"error":"`)
			} else {
				res = c.post("/r/"+repoName+tt.path, tt.form)
			}
			wantStatus(t, res, tt.want)
			if got := res.Header().Get("Retry-After"); (got == "60") != tt.retryAfter {
				t.Errorf("Retry-After = %q, want set: %v", got, tt.retryAfter)
			}
			if tt.textKept {
				wantContains(t, res.Body.String(), "# my edit", "<textarea")
			}
		})
	}

	t.Run("missing CSRF token", func(t *testing.T) {
		e := newEnv(t)
		res := e.as("alice", permAdmin).post("/r/"+repoName+"/policy", url.Values{"csrf": {""}, "content": {text}})
		wantStatus(t, res, http.StatusForbidden)
	})
	t.Run("GET rate limited", func(t *testing.T) {
		e := newEnv(t)
		c := e.as("alice", permRead)
		e.gh.fail["UserRepos"] = api.ErrRateLimited
		res := c.get("/api/repos")
		wantStatus(t, res, http.StatusServiceUnavailable)
		if res.Header().Get("Retry-After") != "60" {
			t.Error("no Retry-After")
		}
	})
	t.Run("panic", func(t *testing.T) {
		e := newEnv(t)
		c := e.as("alice", permRead)
		e.onb.panics = true
		res := c.get("/api/repos/" + repoName + "/overview")
		wantStatus(t, res, http.StatusInternalServerError)
		if ct := res.Header().Get("Content-Type"); ct != mediaJSON || res.Body.String() != `{"error":"internal error"}` {
			t.Errorf("panic on /api answered %q %q, want the JSON error", ct, res.Body.String())
		}
	})
}

func assistForm(content string) url.Values {
	f := formOf("", "", content)
	f.Set("mode", "customer")
	f.Set("instruction", "split module a")
	return f
}

func failingAssist(err error) func(assist.Mode, assist.Request) (assist.Result, error) {
	return func(assist.Mode, assist.Request) (assist.Result, error) { return assist.Result{}, err }
}

func TestWording(t *testing.T) {
	t.Run("draft states", func(t *testing.T) {
		want := map[proposal.DraftState]string{
			proposal.DraftNone: "", proposal.DraftOpen: "draft", proposal.DraftInReview: "awaiting review",
			proposal.DraftMerged: "published", proposal.DraftClosed: "closed",
		}
		for state, word := range want {
			if got := draftWords[state]; got != word {
				t.Errorf("draftWords[%q] = %q, want %q", state, got, word)
			}
		}
	})
	t.Run("plans", func(t *testing.T) {
		day := time.Date(2026, 10, 1, 0, 0, 0, 0, time.UTC)
		st := account.Status{TrialEnds: day, SuspendAfter: day.AddDate(0, 0, 7)}
		wantWord := map[account.Plan]string{
			account.PlanPublic: "", account.PlanGranted: "", account.PlanTrial: "1 October 2026",
			account.PlanGrace: "8 October 2026", account.PlanSuspended: "suspended",
		}
		for plan, word := range wantWord {
			st.Plan = plan
			got := banner(st)
			if (word == "") != (got == "") || !strings.Contains(got, word) {
				t.Errorf("banner(%q) = %q, want it to contain %q", plan, got, word)
			}
		}
	})
	t.Run("check conclusions", func(t *testing.T) {
		want := map[string]string{"success": "healthy", "neutral": "attention", "failure": "violations found", "": "in progress", "bogus": "unknown"}
		for conclusion, word := range want {
			if got := conclusionWord(conclusion); got != word {
				t.Errorf("conclusionWord(%q) = %q, want %q", conclusion, got, word)
			}
		}
	})
	t.Run("state tones", func(t *testing.T) {
		want := map[string]string{"success": "success", "neutral": "attention", "failure": "danger", "": "accent", "stale": "neutral"}
		for conclusion, tone := range want {
			if got := toneOf(conclusionWord(conclusion)); got != tone {
				t.Errorf("tone of %q = %q, want %q", conclusion, got, tone)
			}
		}
		words := map[string]bool{stateNone: true, stateUnknown: true}
		for _, w := range conclusionWords {
			words[w] = true
		}
		for w := range stateTones {
			if !words[w] {
				t.Errorf("stateTones colours %q, which no state reads", w)
			}
		}
	})
	t.Run("one distinct sentence per sentinel", func(t *testing.T) {
		errs := []error{
			assist.ErrInvalidRequest, assist.ErrNoDraft, assist.ErrManagedUnavailable, account.ErrNoConsent, assist.ErrNotManaged,
			assist.ErrPolicyTooLarge, assist.ErrBusy, assist.ErrProvider, assist.ErrInvalidProposal, assist.ErrNoAssistWorkflow,
			proposal.ErrStale, account.ErrBudgetExhausted, onboarding.ErrNoSandbox, api.ErrRateLimited,
		}
		_, _, unknown := explain(errors.New("boom"))
		seen := map[string]error{unknown: errors.New("unknown")}
		for _, err := range errs {
			_, _, sentence := explain(fmt.Errorf("wrapped: %w", err))
			if prev, dup := seen[sentence]; dup || sentence == "" {
				t.Errorf("%v shares the sentence %q with %v", err, sentence, prev)
			}
			seen[sentence] = err
		}
	})
}

func TestPullPageGroupsCyclesAsTheCommentDoes(t *testing.T) {
	dir := func(from, to, file, origin string) report.Finding {
		return report.Finding{ID: from + to, Kind: "gate", RuleID: "no-module-cycles", Status: "new", Severity: "high", Why: "a cycle",
			Constraint: "Break " + from + " → " + to, Origin: origin,
			Edge:      report.Edge{From: report.Endpoint{Module: from}, To: report.Endpoint{Module: to}},
			Locations: []report.Location{{File: file, Line: 3}, {File: file}}}
	}
	accepted := func(f report.Finding) report.Finding { f.Status = "baseline"; return f }
	existing := dir("pkg_container", "pkg_chaos", "old.go", "pre_existing")
	existing.ID, existing.RuleID = "other-rule", "layering" // the base had this dependency: another rule says so
	rep := report.Report{
		Comparison: report.Comparison{OriginStatus: originComparable},
		Findings: []report.Finding{
			dir("pkg_chaos", "pkg_container", "chaos.go", "introduced"), dir("pkg_container", "pkg_chaos", "flags.go", "introduced"),
			dir("api", "db", "api.go", "introduced"), existing,
			accepted(dir("x", "y", "x.go", "")), accepted(dir("y", "x", "y.go", "")),
		},
		Seams: []report.Seam{{FromModule: "pkg_chaos", ToModule: "pkg_container", Strength: "model", Distance: "cross_module", Volatility: "high"}},
		Dimensions: map[string]report.Dimension{
			"coupling":        {Status: "partial", Coverage: report.DimensionCoverage{Observed: 3, Total: 4, Basis: "modules"}, Unknown: []report.UnknownFact{{Fact: "coupling volatility", Reason: "volatility is undeclared"}}},
			"change_locality": {Status: "measured"},
		},
	}
	var b findingsJSON
	fillFindings(&b, rep)
	if b.ViolationCount != 4 || b.AcceptedCount != 2 || len(b.Accepted) != 2 {
		t.Fatalf("counts = %d violations, %d accepted (%d rows)", b.ViolationCount, b.AcceptedCount, len(b.Accepted))
	}
	if len(b.Cycles) != 1 || !slices.Equal(b.Cycles[0].Modules, []string{"pkg_chaos", "pkg_container"}) || !b.Cycles[0].Violation {
		t.Fatalf("cycles = %+v, want one between pkg_chaos and pkg_container", b.Cycles)
	}
	cy := b.Cycles[0]
	if len(cy.Directions) != 2 || len(cy.Fixes) != 2 || cy.Why != "a cycle" {
		t.Errorf("cycle = %+v, want two directions and each repair once", cy)
	}
	d0, d1 := cy.Directions[0], cy.Directions[1]
	if d0.Origin != "" || d1.Origin != report.PairExisting {
		t.Errorf("origins = %q, %q; want the comment's rule: unknown, then existing", d0.Origin, d1.Origin)
	}
	if !slices.Equal(d0.Locations, []string{"chaos.go:3", "chaos.go"}) || d0.Strength != "model" || d0.Distance != "across modules" || d1.Strength != "" {
		t.Errorf("direction = %+v", d0)
	}
	var other []string
	for _, f := range b.Other {
		other = append(other, f.From+">"+f.To+":"+f.Origin)
	}
	if !slices.Equal(other, []string{"api>db:added", "pkg_container>pkg_chaos:existing"}) {
		t.Errorf("other violations = %v", other)
	}
	if len(b.Dimensions) != 2 || b.Dimensions[0].Name != "Coupling" || b.Dimensions[0].Observed != 3 || b.Dimensions[0].Measurable != 4 || b.Dimensions[0].Basis != "modules" || b.Dimensions[1].Key != "change_locality" {
		t.Errorf("dimensions = %+v, want the engine order with readable names", b.Dimensions)
	}
	if len(b.Unknowns) != 1 || b.Unknowns[0].Dimension != "Coupling" {
		t.Errorf("unknowns = %+v", b.Unknowns)
	}
}

func TestPullPageHidesOriginsWithoutAComparisonAndShowsActiveFirst(t *testing.T) {
	f := func(id, status, origin, from, to string) report.Finding {
		return report.Finding{ID: id, Kind: "gate", RuleID: "r", Status: status, Severity: "high", Origin: origin,
			Edge: report.Edge{From: report.Endpoint{Module: from}, To: report.Endpoint{Module: to}}}
	}
	rep := report.Report{Findings: []report.Finding{
		f("u", "bogus", "introduced", "p", "q"), // a status this version does not know: shown, not a violation
		f("a", "new", "introduced", "x", "y"), f("b", "new", "pre_existing", "y", "x"), f("c", "new", "introduced", "api", "db"),
	}}
	var b findingsJSON
	fillFindings(&b, rep)
	if len(b.Cycles) != 1 || len(b.Other) != 2 {
		t.Fatalf("%d cycles, %d other", len(b.Cycles), len(b.Other))
	}
	for _, d := range append(b.Cycles[0].Directions, b.Other...) {
		if d.Origin != "" {
			t.Errorf("%s → %s reads %q without a comparison", d.From, d.To, d.Origin)
		}
	}
	if b.Other[0].From != "api" || !b.Other[0].Violation || b.Other[1].From != "p" || b.Other[1].Violation {
		t.Errorf("other = %+v, want the active finding first and the unknown status last, not a violation", b.Other)
	}
}

func TestPullPageCapsEachGroup(t *testing.T) {
	var rep report.Report
	for i := range findingsPerList + 7 {
		rep.Findings = append(rep.Findings, report.Finding{ID: strconv.Itoa(i), Kind: "advisory", Status: "new", RuleID: "r"})
	}
	var b findingsJSON
	fillFindings(&b, rep)
	if len(b.Advisories) != findingsPerList || b.AdvisoryCount != findingsPerList+7 || b.Omitted != 7 {
		t.Errorf("%d rows of %d, %d omitted", len(b.Advisories), b.AdvisoryCount, b.Omitted)
	}
}

func TestEscapeRef(t *testing.T) {
	for _, tt := range []struct{ ref, want string }{
		{"main", "main"},
		{"release/1.2", "release/1.2"},
		{"release#42", "release%2342"},
		{"a?b/c d", "a%3Fb/c%20d"},
		{"0123456789abcdef0123456789abcdef01234567", "0123456789abcdef0123456789abcdef01234567"},
	} {
		if got := escapeRef(tt.ref); got != tt.want {
			t.Errorf("escapeRef(%q) = %q, want %q", tt.ref, got, tt.want)
		}
	}
}

package ui

import (
	"errors"
	"fmt"
	"net/http"
	"net/url"
	"os"
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
			res := c.post("/r/"+repoName+tt.path, tt.form)
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
		res := c.get("/")
		wantStatus(t, res, http.StatusServiceUnavailable)
		if res.Header().Get("Retry-After") != "60" {
			t.Error("no Retry-After")
		}
	})
	t.Run("panic", func(t *testing.T) {
		e := newEnv(t)
		c := e.as("alice", permRead)
		e.onb.panics = true
		wantStatus(t, c.get("/r/"+repoName), http.StatusInternalServerError)
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
		want := map[string]string{"success": "healthy", "neutral": "attention", "failure": "blocked", "": "in progress", "bogus": "unknown"}
		for conclusion, word := range want {
			if got := conclusionWord(conclusion); got != word {
				t.Errorf("conclusionWord(%q) = %q, want %q", conclusion, got, word)
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

func TestPullPageFoldsACycleAndListsWhatToMeasure(t *testing.T) {
	dir := func(from, to, file string) report.Finding {
		return report.Finding{ID: from + to, Kind: "gate", RuleID: "no-module-cycles", Status: "new", Severity: "high", Why: "a cycle",
			Edge:      report.Edge{From: report.Endpoint{Module: from}, To: report.Endpoint{Module: to}},
			Locations: []report.Location{{File: file, Line: 3}}}
	}
	rep := report.Report{
		Findings: []report.Finding{dir("pkg_chaos", "pkg_container", "chaos.go"), dir("pkg_container", "pkg_chaos", "flags.go"), dir("api", "db", "api.go")},
		Dimensions: map[string]report.Dimension{
			"coupling": {Status: "partial", Unknown: []report.UnknownFact{{Fact: "coupling volatility", Reason: "volatility is undeclared"}}},
		},
	}
	var body pullBody
	fillFindings(&body, rep)
	if len(body.Violations) != 2 || body.Violations[0].Reverse == nil || body.Violations[0].Reverse.Edge.From.Module != "pkg_container" || body.Violations[1].Reverse != nil {
		t.Fatalf("violations = %+v, want the cycle as one item and api -> db alone", body.Violations)
	}
	if len(body.Unknowns) != 1 || body.Unknowns[0].Dimension != "coupling" {
		t.Fatalf("unknowns = %+v", body.Unknowns)
	}
	var out strings.Builder
	if err := parsePages()["pull"].ExecuteTemplate(&out, "layout", page{Body: body}); err != nil {
		t.Fatalf("render: %v", err)
	}
	html := out.String()
	for _, want := range []string{"cycle <code>pkg_chaos</code> ⇄ <code>pkg_container</code>", "<code>chaos.go:3</code>", "<code>flags.go:3</code>",
		`id="measure-more"`, "To measure more (1)", "coupling volatility"} {
		if !strings.Contains(html, want) {
			t.Errorf("page lacks %q", want)
		}
	}
}

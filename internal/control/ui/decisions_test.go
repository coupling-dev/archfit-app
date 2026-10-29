package ui

import (
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"unicode"

	"github.com/alexei-led/archfit-app/internal/gate"
	"github.com/alexei-led/archfit-app/internal/github/api"
	"github.com/alexei-led/archfit-app/internal/wire/decision"
)

const (
	findingID    = "0a0a0a0a0a0a0a0a0a0a0a0a0a0a0a0a"
	policyDigest = "5b5b5b5b5b5b5b5b5b5b5b5b5b5b5b5b5b5b5b5b5b5b5b5b5b5b5b5b5b5b5b5b"
	otherFinding = "0b0b0b0b0b0b0b0b0b0b0b0b0b0b0b0b"
	proposed     = "version: 2\nmodules:\n  a: {paths: [\"pkg/a/**\"]}\n"

	decisionAPI = pullAPI + "/decisions/" + findingID + "/" + policyDigest
	approveReq  = decisionAPI + "/approve"
	declineReq  = decisionAPI + "/decline"
)

func pendingRequest() gate.DecisionRequest {
	return gate.DecisionRequest{CanDecide: true, DecisionRecord: gate.DecisionRecord{
		PullRequest: 4, FindingID: findingID, PolicyDigest: policyDigest, RuleID: "boundary.no-import",
		EdgeFrom: "a", EdgeTo: "b", HeadSHA: sha(4), Status: decision.StatusPending, Requester: "agent-7",
		Why: "a needs b for the read model", AlternativesTried: []string{"moved the type"},
	}}
}

func (e *env) withRequest(r gate.DecisionRequest) {
	e.sameRepoHead()
	e.decisions.rows = []gate.DecisionRequest{r}
	e.decisions.policy = proposed
}

func TestPullJSONCarriesTheDecisionRequests(t *testing.T) {
	approved := pendingRequest()
	approved.Status, approved.ProposalPR, approved.CanDecide = decision.StatusApproved, 31, false
	tests := []struct {
		name string
		rows []gate.DecisionRequest
		want []decisionRequestJSON
	}{
		{name: "none"},
		{name: "pending, and the user may decide", rows: []gate.DecisionRequest{pendingRequest()}, want: []decisionRequestJSON{{
			FindingID: findingID, PolicyDigest: policyDigest, RuleID: "boundary.no-import", From: "a", To: "b", Status: "pending",
			Requester: "agent-7", Why: "a needs b for the read model", Alternatives: []string{"moved the type"}, CanDecide: true,
		}}},
		{name: "approved, with the proposal pull request", rows: []gate.DecisionRequest{approved}, want: []decisionRequestJSON{{
			FindingID: findingID, PolicyDigest: policyDigest, RuleID: "boundary.no-import", From: "a", To: "b", Status: "approved",
			Requester: "agent-7", Why: "a needs b for the read model", Alternatives: []string{"moved the type"},
			ProposalPR: 31, ProposalURL: "https://github.com/" + repoName + "/pull/31",
		}}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			e := newEnv(t)
			e.sameRepoHead()
			e.decisions.rows = tt.rows
			var body pullJSON
			e.as("alice", permRead).getJSON(pullAPI, &body)
			if len(body.Decisions) != len(tt.want) {
				t.Fatalf("decisions = %+v, want %+v", body.Decisions, tt.want)
			}
			for i, got := range body.Decisions {
				if want := tt.want[i]; got.FindingID != want.FindingID || got.Status != want.Status || got.CanDecide != want.CanDecide ||
					got.ProposalPR != want.ProposalPR || got.ProposalURL != want.ProposalURL || got.Why != want.Why ||
					got.Requester != want.Requester || got.From != want.From || got.To != want.To || got.RuleID != want.RuleID ||
					got.PolicyDigest != want.PolicyDigest || strings.Join(got.Alternatives, "|") != strings.Join(want.Alternatives, "|") {
					t.Errorf("decision = %+v, want %+v", got, want)
				}
			}
			call := e.decisions.listed[0]
			if call.login != "alice" || call.target.PullRequest != 4 || call.target.HeadSHA != sha(4) || call.target.BaseRef != mainBranch {
				t.Errorf("listed %+v, want the signed-in login and the pull request as GitHub reports it", call)
			}
		})
	}
	t.Run("the list cannot be read", func(t *testing.T) {
		e := newEnv(t)
		e.decisions.listErr = errors.New("store down")
		wantStatus(t, e.as("alice", permRead).get(pullAPI), http.StatusBadGateway)
	})
}

func TestOwnerApprovesADecisionRequest(t *testing.T) {
	e := newEnv(t)
	e.withRequest(pendingRequest())
	res := e.as("alice", permWrite).postJSON(approveReq, struct{}{})
	wantStatus(t, res, http.StatusOK)

	if len(e.props.proposes) != 1 {
		t.Fatalf("%d proposals, want exactly one", len(e.props.proposes))
	}
	c := e.props.proposes[0]
	if c.change.Branch != proposalBranchOf(pendingRequest().DecisionRecord) || c.change.Base != mainBranch || c.change.Repo.FullName != repoName ||
		len(c.change.Files) != 1 || c.change.Files[0].Path != ".archfit.yaml" || string(c.change.Files[0].Content) != proposed || c.expectHead != "" {
		t.Errorf("Propose(%+v, %q), want the decision branch, the default branch and the candidate policy as .archfit.yaml", c.change, c.expectHead)
	}
	for _, want := range []string{
		findingID, "boundary.no-import", "`a` -> `b`", "agent-7", "a needs b for the read model", "moved the type",
		"https://github.com/" + repoName + "/pull/4", "only an architecture owner can approve it",
	} {
		if !strings.Contains(c.change.Body, want) {
			t.Errorf("proposal body lacks %q:\n%s", want, c.change.Body)
		}
	}
	if len(e.decisions.approves) != 1 || e.decisions.approves[0].login != "alice" || e.decisions.approves[0].target.PullRequest != 4 {
		t.Errorf("approves = %+v, want one for the signed-in login", e.decisions.approves)
	}
	if len(e.decisions.approved) != 1 || e.decisions.approved[0].proposalPR != 1 || e.decisions.approved[0].head != sha(4) {
		t.Errorf("approved = %+v, want the proposal pull request settled on the record's head", e.decisions.approved)
	}
	if n := e.props.readied; len(n) != 0 {
		t.Errorf("a proposal was marked ready: %v", n)
	}
	body := decodeAs[decidedJSON](t, res)
	if len(body.Decisions) != 1 || body.Decisions[0].Status != "approved" || body.Decisions[0].ProposalPR != 1 || body.Notice != notices["decision_approved"] {
		t.Errorf("answer = %+v", body)
	}
}

// The proposal goes to the default branch, whatever base the pull request being decided is on.
func TestProposalBasesOnTheDefaultBranch(t *testing.T) {
	e := newEnv(t)
	e.withRequest(pendingRequest())
	e.gh.mu.Lock()
	p := e.gh.pulls[repoName+"#4"]
	p.BaseRef = "release"
	e.gh.pulls[repoName+"#4"] = p
	e.gh.mu.Unlock()
	wantStatus(t, e.as("alice", permWrite).postJSON(approveReq, struct{}{}), http.StatusOK)
	if len(e.props.proposes) != 1 || e.props.proposes[0].change.Base != mainBranch {
		t.Errorf("proposes = %+v, want one on base %s", e.props.proposes, mainBranch)
	}
}

func TestApprovingADecisionRequestTwiceOpensOneProposal(t *testing.T) {
	e := newEnv(t)
	e.withRequest(pendingRequest())
	e.decisions.approvedErr, e.decisions.approvedOnce = errors.New("store down after the proposal"), true
	c := e.as("alice", permWrite)

	wantStatus(t, c.postJSON(approveReq, struct{}{}), http.StatusBadGateway) // crash between propose and settle
	if len(e.decisions.approved) != 0 {
		t.Fatal("the request was settled")
	}
	wantStatus(t, c.postJSON(approveReq, struct{}{}), http.StatusOK)

	if len(e.props.drafts) != 1 || len(e.props.proposes) != 2 {
		t.Errorf("%d drafts from %d proposals, want one draft found again by its branch", len(e.props.drafts), len(e.props.proposes))
	}
	if len(e.decisions.approved) != 1 || e.decisions.approved[0].proposalPR != 1 {
		t.Errorf("approved = %+v", e.decisions.approved)
	}
}

func TestDecisionRefusals(t *testing.T) {
	tests := []struct {
		name        string
		path        string
		permission  string
		body        any
		approveErr  error
		declineErr  error
		approvedErr error
		proposeErr  error
		status      int
		sentence    string
		proposals   int  // Propose calls
		settled     bool // the request was settled
	}{
		{name: "not an owner", path: approveReq, approveErr: gate.ErrNotOwner, status: http.StatusForbidden, sentence: "Only an architecture owner"},
		{name: "an agent identity", path: approveReq, approveErr: gate.ErrAgentIdentity, status: http.StatusForbidden, sentence: "An agent identity cannot approve"},
		{name: "the requester", path: approveReq, approveErr: gate.ErrDecisionRequester, status: http.StatusForbidden, sentence: "The login that filed a request cannot decide it"},
		{name: "stale", path: approveReq, approveErr: gate.ErrDecisionStale, status: http.StatusConflict, sentence: "out of date and no proposal was opened"},
		{name: "already decided", path: approveReq, approveErr: gate.ErrDecisionSettled, status: http.StatusConflict, sentence: "already decided or is out of date"},
		{name: "no such request", path: approveReq, approveErr: gate.ErrDecisionNotFound, status: http.StatusNotFound, sentence: "Not found."},
		{name: "GitHub refuses the proposal", path: approveReq, proposeErr: api.ErrRateLimited, status: http.StatusServiceUnavailable, sentence: "rate limiting", proposals: 1},
		{name: "proposal failure", path: approveReq, proposeErr: errors.New("github down"), status: http.StatusBadGateway, sentence: "Nothing was changed", proposals: 1},
		{name: "proposal opened, status comment failed", path: approveReq, approvedErr: gate.ErrStatusComment, status: http.StatusBadGateway,
			sentence: "The decision was recorded", proposals: 1},
		{name: "the pull request opened is not the draft expected", path: approveReq, approvedErr: gate.ErrDecisionProposal, status: http.StatusBadGateway,
			sentence: "not the draft Archfit expected", proposals: 1},
		{name: "a read-only user", path: approveReq, permission: permRead, status: http.StatusNotFound, sentence: "Not found."},
		{name: "a body naming a repository", path: approveReq, body: map[string]any{"repository": otherRepo}, status: http.StatusUnprocessableEntity,
			sentence: "The request could not be read."},
		{name: "decline: not an owner", path: declineReq, declineErr: gate.ErrNotOwner, status: http.StatusForbidden, sentence: "Only an architecture owner"},
		{name: "decline: the requester", path: declineReq, declineErr: gate.ErrDecisionRequester, status: http.StatusForbidden, sentence: "cannot decide it"},
		{name: "decline: already decided", path: declineReq, declineErr: gate.ErrDecisionSettled, status: http.StatusConflict, sentence: "already decided"},
		{name: "decline: status comment failed", path: declineReq, declineErr: gate.ErrStatusComment, status: http.StatusBadGateway, sentence: "The decision was recorded"},
		{name: "decline: a read-only user", path: declineReq, permission: permRead, status: http.StatusNotFound, sentence: "Not found."},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			e := newEnv(t)
			e.withRequest(pendingRequest())
			e.decisions.approveErr, e.decisions.declineErr, e.decisions.approvedErr = tt.approveErr, tt.declineErr, tt.approvedErr
			if tt.proposeErr != nil {
				e.props.fail["Propose"] = tt.proposeErr
			}
			var body any = struct{}{}
			if tt.body != nil {
				body = tt.body
			}
			res := e.as("alice", cmpOr(tt.permission, permWrite)).postJSON(tt.path, body)
			wantStatus(t, res, tt.status)
			wantContains(t, res.Body.String(), tt.sentence)
			if len(e.props.proposes) != tt.proposals {
				t.Errorf("%d proposals, want %d", len(e.props.proposes), tt.proposals)
			}
			if len(e.decisions.approved)+len(e.decisions.declined) != 0 {
				t.Errorf("a refused decision settled the request: %+v %+v", e.decisions.approved, e.decisions.declined)
			}
		})
	}
	for _, c := range []struct {
		name string
		do   func(e *env) *httptest.ResponseRecorder
		want int
	}{
		{"signed out", func(e *env) *httptest.ResponseRecorder { return e.anonymous().postJSONWith(approveReq, struct{}{}, "") }, http.StatusUnauthorized},
		{"without the CSRF token", func(e *env) *httptest.ResponseRecorder {
			return e.as("alice", permWrite).postJSONWith(approveReq, struct{}{}, "")
		}, http.StatusForbidden},
		{"decline without the CSRF token", func(e *env) *httptest.ResponseRecorder {
			return e.as("alice", permWrite).postJSONWith(declineReq, struct{}{}, "")
		}, http.StatusForbidden},
	} {
		t.Run(c.name, func(t *testing.T) {
			e := newEnv(t)
			e.withRequest(pendingRequest())
			wantStatus(t, c.do(e), c.want)
			e.noWrites(t)
			if len(e.decisions.approves) != 0 {
				t.Error("the gate was asked")
			}
		})
	}
}

func TestIDsThatAreNotRequestIDsNeverReachTheGate(t *testing.T) {
	bad := []string{
		pullAPI + "/decisions/x/" + policyDigest,
		pullAPI + "/decisions/" + findingID + "/zz",
		pullAPI + "/decisions/" + strings.ToUpper(findingID) + "/" + policyDigest,
		"/api/repos/" + repoName + "/pulls/x/decisions/" + findingID + "/" + policyDigest,
	}
	for _, path := range bad {
		for _, action := range []string{"/approve", "/decline"} {
			e := newEnv(t)
			e.withRequest(pendingRequest())
			res := e.as("alice", permWrite).postJSON(path+action, struct{}{})
			wantStatus(t, res, http.StatusNotFound)
			if len(e.decisions.approves)+len(e.decisions.declined) != 0 || e.props.writes() != 0 {
				t.Errorf("%s%s reached the gate or GitHub", path, action)
			}
		}
	}
}

func cmpOr(a, b string) string {
	if a != "" {
		return a
	}
	return b
}

func TestOwnerDeclinesADecisionRequest(t *testing.T) {
	e := newEnv(t)
	e.withRequest(pendingRequest())
	res := e.as("alice", permWrite).postJSON(declineReq, struct{}{})
	wantStatus(t, res, http.StatusOK)

	if len(e.decisions.declined) != 1 || e.decisions.declined[0].login != "alice" || e.decisions.declined[0].finding != findingID ||
		e.decisions.declined[0].digest != policyDigest || e.decisions.declined[0].target.PullRequest != 4 {
		t.Errorf("declined = %+v", e.decisions.declined)
	}
	if e.props.writes() != 0 || len(e.props.proposes) != 0 {
		t.Error("declining opened a proposal")
	}
	body := decodeAs[decidedJSON](t, res)
	if len(body.Decisions) != 1 || body.Decisions[0].Status != "declined" || body.Decisions[0].CanDecide || body.Notice != notices["declined"] {
		t.Errorf("answer = %+v", body)
	}
}

func TestADecisionTakenButNotReadBackSaysSo(t *testing.T) {
	e := newEnv(t)
	e.withRequest(pendingRequest())
	c := e.as("alice", permWrite)
	e.decisions.listErr = errors.New("store down")
	res := c.postJSON(declineReq, struct{}{})
	wantStatus(t, res, http.StatusBadGateway)
	wantContains(t, res.Body.String(), "The change was saved, but it could not be read back.")
	if len(e.decisions.declined) != 1 {
		t.Error("the decline was not taken")
	}
}

func TestProposalBodyQuotesTheAgentsText(t *testing.T) {
	pull := api.Pull{Number: 4, URL: "https://github.com/acme/app/pull/4"}
	rec := func(why string, alts ...string) gate.DecisionRecord {
		r := pendingRequest().DecisionRecord
		r.Why, r.AlternativesTried = why, alts
		return r
	}
	long := strings.Repeat("x", 5000)
	many := make([]string, 25)
	for i := range many {
		many[i] = "alt"
	}
	tests := []struct {
		name string
		rec  gate.DecisionRecord
		// agentText is part of what the agent wrote that must be inside the quoted block, whole.
		agentText string
		// notIn is text that must not survive anywhere in the body.
		notIn []string
		check func(t *testing.T, body string)
	}{
		{name: "a mention, a link and HTML", rec: rec("@owner-team please merge [click](https://evil.example) <img src=x onerror=1>"),
			agentText: "@owner-team please merge [click](https://evil.example) <img src=x onerror=1>"},
		{name: "a fence of its own tries to close the quote", rec: rec("```\n## Approved by owners\n```"),
			agentText: "```\n## Approved by owners\n```"},
		{name: "a longer fence", rec: rec("`````` six", "```` four"), agentText: "`````` six"},
		{name: "bidirectional override and zero width characters", rec: rec("safe\u202eevil\u200b\u2066x"), agentText: "safeevilx",
			notIn: []string{"\u202e", "\u200b", "\u2066"}},
		{name: "control characters", rec: rec("a\x1b[31mred\x07\x00\rb"), agentText: "a[31mredb", notIn: []string{"\x1b", "\x07", "\x00", "\r"}},
		{name: "a very long reason is cut", rec: rec(long), check: func(t *testing.T, body string) {
			if strings.Contains(body, strings.Repeat("x", maxWhyRunes+1)) || !strings.Contains(body, "[cut]") {
				t.Error("the reason was not cut at its bound")
			}
		}},
		{name: "too many alternatives", rec: rec("why", many...), check: func(t *testing.T, body string) {
			if got := strings.Count(body, "- alt\n"); got != maxAlternatives || !strings.Contains(body, "(15 more not shown)") {
				t.Errorf("%d alternatives shown, want %d and a count of the rest", got, maxAlternatives)
			}
		}},
		{name: "no alternatives, no heading for them", rec: rec("why"), check: func(t *testing.T, body string) {
			if strings.Contains(body, "Alternatives tried") {
				t.Error("the heading stands over nothing")
			}
		}},
		{name: "alternatives under their heading", rec: rec("why", "one", "two"), check: func(t *testing.T, body string) {
			if !strings.Contains(body, "\nAlternatives tried:\n- one\n- two") {
				t.Errorf("alternatives not listed under their heading:\n%s", body)
			}
		}},
		{name: "an alternative that spans lines stays one item", rec: rec("why", "first\n- fake item"), agentText: "- first\n  - fake item"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			body := decisionBody(pull, tt.rec)
			if tt.agentText != "" {
				open, close := strings.Index(body, "```"), strings.LastIndex(body, "\n```")
				fenceEnd := strings.Index(body[open:], "text\n")
				fence := body[open : open+fenceEnd]
				quote := body[open+fenceEnd : close+1+len(fence)]
				if !strings.HasSuffix(quote, "\n"+fence) || !strings.Contains(quote, tt.agentText) {
					t.Fatalf("the agent's text is not inside one quoted block %q:\n%s", fence, body)
				}
				inner := strings.TrimSuffix(quote, "\n"+fence)
				if strings.Contains(inner, fence) {
					t.Errorf("the agent's text closes its own quote:\n%s", body)
				}
				if rest := body[close+1+len(fence):]; strings.Contains(rest, "@") || strings.Contains(rest, "http") {
					t.Errorf("agent text leaked after the quote:\n%s", rest)
				}
			}
			for _, bad := range tt.notIn {
				if strings.Contains(body, bad) {
					t.Errorf("body keeps %q", bad)
				}
			}
			for _, r := range body {
				if unicode.IsControl(r) && r != '\n' && r != '\t' {
					t.Errorf("body holds control character %U", r)
				}
			}
			if tt.check != nil {
				tt.check(t, body)
			}
		})
	}
}

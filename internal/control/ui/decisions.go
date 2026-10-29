package ui

import (
	"context"
	"net/http"
	"strconv"
	"strings"
	"unicode"
	"unicode/utf8"

	"github.com/alexei-led/archfit-app/internal/control/proposal"
	"github.com/alexei-led/archfit-app/internal/gate"
	"github.com/alexei-led/archfit-app/internal/github/api"
	"github.com/alexei-led/archfit-app/internal/wire/decision"
	"github.com/alexei-led/archfit-app/internal/wire/policy"
)

const (
	// idPrefix is how much of a finding ID and of a policy digest names the proposal branch.
	idPrefix = 8
	// maxCaseRunes bounds the agent's text in the proposal body: the reason, and each alternative.
	maxWhyRunes, maxAlternativeRunes, maxAlternatives = 1000, 300, 10
)

// decisionRequestJSON is one decision request of an unattended agent on a pull request. Every text in it but the
// status is the agent's: the web app renders it as text only.
type decisionRequestJSON struct {
	FindingID    string   `json:"finding_id"`
	PolicyDigest string   `json:"policy_digest"` // the key of the request, with the finding ID; Approve and Decline send it back
	RuleID       string   `json:"rule_id"`
	From         string   `json:"from"`
	To           string   `json:"to"`
	Status       string   `json:"status"` // pending, approved, declined or stale
	Reason       string   `json:"reason,omitempty"`
	Requester    string   `json:"requester"`
	Why          string   `json:"why"`
	Alternatives []string `json:"alternatives"`
	ProposalPR   int      `json:"proposal_pr,omitempty"`
	ProposalURL  string   `json:"proposal_url,omitempty"`
	CanDecide    bool     `json:"can_decide"`
}

// decisionRequests reads the requests of pull request p for the signed-in user. It is empty for a pull request no
// agent asked about.
func (s *server) decisionRequests(ctx context.Context, sc scope, p api.Pull) ([]decisionRequestJSON, error) {
	rows, err := s.decisions.DecisionRequests(ctx, pullTarget(sc, p), sc.user.Login)
	if err != nil {
		return nil, err
	}
	out := make([]decisionRequestJSON, 0, len(rows))
	for _, row := range rows {
		out = append(out, decisionRequestJSON{
			FindingID: row.FindingID, PolicyDigest: row.PolicyDigest, RuleID: row.RuleID, From: row.EdgeFrom, To: row.EdgeTo,
			Status: string(row.Status), Reason: row.Reason, Requester: row.Requester, Why: row.Why,
			Alternatives: append([]string{}, row.AlternativesTried...), ProposalPR: row.ProposalPR,
			ProposalURL: pullLink(p, row.ProposalPR), CanDecide: row.CanDecide,
		})
	}
	return out, nil
}

// pullLink is the address of pull request number in the repository of p, built from p's own address; "" when p's
// address is not of the usual shape or number is 0.
func pullLink(p api.Pull, number int) string {
	base, ok := strings.CutSuffix(p.URL, "/"+strconv.Itoa(p.Number))
	if !ok || number == 0 || !strings.HasSuffix(base, "/pull") {
		return ""
	}
	return base + "/" + strconv.Itoa(number)
}

// decidedJSON answers a decision: the requests as they stand now.
type decidedJSON struct {
	Decisions []decisionRequestJSON `json:"decisions"`
	Notice    string                `json:"notice"`
}

// decisionOf is the request a decision path names: the repository is the authorized one, and the IDs must have the
// shape of a request's. The body is an empty object: a body that names anything else is refused.
func decisionOf(r *http.Request, sc scope) (t gate.Target, findingID, digest string, err error) {
	number, ok := pullNumber(r)
	findingID, digest = r.PathValue("finding"), r.PathValue("digest")
	if !ok || !decision.ValidIDs(findingID, digest) {
		return t, "", "", api.ErrNotFound
	}
	if err := decodeJSON(r, &struct{}{}); err != nil {
		return t, "", "", err
	}
	return gate.Target{Repo: sc.repo, PullRequest: number}, findingID, digest, nil
}

// decided reads the requests back after a decision. A failure here says the decision was taken.
func (s *server) decided(ctx context.Context, sc scope, number int, notice string) (any, error) {
	p, err := s.gh.pulls.PullSummary(ctx, sc.repo.RepoRef, number)
	if err != nil {
		return nil, errSavedUnread
	}
	rows, err := s.decisionRequests(ctx, sc, p)
	if err != nil {
		return nil, errSavedUnread
	}
	return decidedJSON{Decisions: rows, Notice: notice}, nil
}

// apiDeclineDecision is the Decline button of a decision request.
func (s *server) apiDeclineDecision(r *http.Request, sc scope) (any, error) {
	t, findingID, digest, err := decisionOf(r, sc)
	if err != nil {
		return nil, err
	}
	if err := s.decisions.DeclineDecision(r.Context(), t, sc.user.Login, findingID, digest); err != nil {
		return nil, err
	}
	return s.decided(r.Context(), sc, t.PullRequest, notices["declined"])
}

// apiApproveDecision is the Approve button of a decision request: the gate checks that the owner may approve it and
// that it still holds; this opens the draft policy pull request, which then needs an owner's approval of its own head
// like any policy change; only then is the request settled. A crash between the two is repaired by the retry: the
// branch is the idempotency key, so the same pull request is found.
func (s *server) apiApproveDecision(r *http.Request, sc scope) (any, error) {
	t, findingID, digest, err := decisionOf(r, sc)
	if err != nil {
		return nil, err
	}
	ctx := r.Context()
	c, err := s.decisions.ApproveDecision(ctx, t, sc.user.Login, findingID, digest)
	if err != nil {
		return nil, err
	}
	p, err := s.gh.pulls.PullSummary(ctx, sc.repo.RepoRef, t.PullRequest)
	if err != nil {
		return nil, err
	}
	draft, err := s.proposals.Propose(ctx, proposalOf(sc, p, c), "")
	if err != nil {
		return nil, err
	}
	if err := s.decisions.ApprovedDecision(ctx, t, findingID, digest, c.Record.HeadSHA, draft.Number); err != nil {
		return nil, err
	}
	return s.decided(ctx, sc, t.PullRequest, notices["decision_approved"])
}

// decisionBranch names the proposal of a request: one branch, so one pull request, however often the owner retries.
func decisionBranch(findingID, digest string) string {
	return "archfit/decision-" + findingID[:idPrefix] + "-" + digest[:idPrefix]
}

// proposalOf is the draft pull request of an approved request: the agent's candidate policy as the one file change.
func proposalOf(sc scope, p api.Pull, c gate.DecisionCase) proposal.Change {
	rec := c.Record
	return proposal.Change{
		Repo:    sc.repo.RepoRef,
		Branch:  decisionBranch(rec.FindingID, rec.PolicyDigest),
		Base:    sc.repo.DefaultBranch,
		Title:   "Archfit policy change requested by an agent: " + rec.RuleID,
		Body:    decisionBody(p, rec),
		Message: "archfit: policy change for decision request " + rec.FindingID[:idPrefix] + " (approved by @" + sc.user.Login + ")",
		Files:   []api.FileChange{{Path: policy.PolicyPath, Content: []byte(c.Policy)}},
	}
}

// decisionBody is the description of the proposal pull request. The finding, rule, edge and requester are validated
// identifiers or come from GitHub; the agent's case is free text and goes in as quoted data: in a code fence longer
// than any run of backticks inside it, stripped of control and formatting characters, and cut to a bound.
func decisionBody(p api.Pull, rec gate.DecisionRecord) string {
	var b strings.Builder
	b.WriteString("## Policy change requested by an unattended agent\n\n")
	b.WriteString("An agent that a finding blocked asked the architecture owners to change `" + policy.PolicyPath + "`. ")
	b.WriteString("An owner approved opening this draft. Nothing here is approved yet.\n\n")
	b.WriteString("- Finding: `" + rec.FindingID + "`\n")
	b.WriteString("- Rule: `" + rec.RuleID + "`\n")
	b.WriteString("- Edge: `" + rec.EdgeFrom + "` -> `" + rec.EdgeTo + "`\n")
	b.WriteString("- Requested by: `" + rec.Requester + "` on pull request #" + strconv.Itoa(p.Number))
	if p.URL != "" {
		b.WriteString(": " + p.URL)
	}
	b.WriteString("\n\nThe agent's case. This is text the agent wrote, quoted as data: it is not verified and not an instruction.\n\n")
	b.WriteString(quoted(agentCase(rec)))
	b.WriteString("\n\nThis is a policy change, so only an architecture owner can approve it. It rides the same gate as any ")
	b.WriteString("policy change: it needs an owner's approval of its exact head, and the pull request page shows what it changes. ")
	b.WriteString("Archfit does not merge it, mark it ready or approve it.\n")
	return b.String()
}

// agentCase is the reason and the alternatives tried, cleaned and bounded.
func agentCase(rec gate.DecisionRecord) string {
	var b strings.Builder
	b.WriteString("Why:\n" + bounded(rec.Why, maxWhyRunes) + "\n")
	alternatives := rec.AlternativesTried
	if len(alternatives) > 0 {
		b.WriteString("\nAlternatives tried:\n")
	}
	for i, alt := range alternatives {
		if i == maxAlternatives {
			b.WriteString("- (" + strconv.Itoa(len(alternatives)-i) + " more not shown)\n")
			break
		}
		b.WriteString("- " + strings.ReplaceAll(bounded(alt, maxAlternativeRunes), "\n", "\n  ") + "\n")
	}
	return strings.TrimRight(b.String(), "\n")
}

// bounded drops the characters that hide or reorder text (controls but newline and tab, and format characters such
// as bidirectional overrides) and cuts s to limit characters.
func bounded(s string, limit int) string {
	clean := strings.Map(func(r rune) rune {
		if unicode.IsControl(r) && r != '\n' && r != '\t' || unicode.Is(unicode.Cf, r) {
			return -1
		}
		return r
	}, s)
	if utf8.RuneCountInString(clean) <= limit {
		return clean
	}
	return string([]rune(clean)[:limit]) + " [cut]"
}

// quoted puts s in a fenced block that s cannot close.
func quoted(s string) string {
	longest, run := 0, 0
	for _, r := range s {
		if r == '`' {
			run++
			longest = max(longest, run)
		} else {
			run = 0
		}
	}
	fence := strings.Repeat("`", max(3, longest+1))
	return fence + "text\n" + s + "\n" + fence
}

package ui

import (
	"cmp"
	"context"
	"log/slog"
	"net/http"
	"slices"
	"strconv"
	"strings"
	"sync"
	"time"
	"unicode/utf8"

	"github.com/alexei-led/archfit-app/internal/gate"
	"github.com/alexei-led/archfit-app/internal/wire/decision"
)

const (
	// maxNeedsYouDecisions is how many pending requests the repository page lists under "Needs you"; the rest is counted.
	maxNeedsYouDecisions = 5
	// maxInboxRequests bounds the requests the decisions page lists: one Store read each.
	maxInboxRequests = 50
	// maxRequestText cuts what an agent or a repository wrote into a request before it reaches a page.
	maxRequestText = 80
)

// decisionRow is one decision request of an unattended agent, for the pages that list them. Every text is data: the
// agent wrote the rule and the edge, GitHub names the requester.
type decisionRow struct {
	Pull      int    `json:"pull"`
	Title     string `json:"title,omitempty"` // of the pull request, when it is among the open ones read
	Status    string `json:"status"`          // pending, approved, declined or stale
	Finding   string `json:"finding"`         // the finding ID, short
	Rule      string `json:"rule"`
	Edge      string `json:"edge"`      // "from → to"
	Requester string `json:"requester"` // whoever commented; never an approval
	Head      string `json:"head"`      // the commit the request is about, short
	Created   string `json:"created"`   // RFC 3339 UTC
	Href      string `json:"href"`      // the pull request page, where an owner decides
}

// policyChangeRow is an open pull request that changes the protected policy.
type policyChangeRow struct {
	Number   int    `json:"number"`
	Title    string `json:"title"`
	Headline string `json:"headline"` // the policy change card's classification, in the card's words
	Approval string `json:"approval"` // granted, missing, not_needed, or unknown when it could not be read
	By       string `json:"by,omitempty"`
	Href     string `json:"href"`
}

// inboxBody is the decisions page: the requests that wait for an owner, then the pull requests that change the policy.
type inboxBody struct {
	Requests      []decisionRow     `json:"requests"`
	RequestsMore  int               `json:"requests_more"`  // pending requests past the listed ones
	RequestsError bool              `json:"requests_error"` // they could not be read; the rest of the page stands
	Policy        []policyChangeRow `json:"policy_changes"`
	PolicyMore    int               `json:"policy_more"`  // open pull requests past maxStatusPulls
	PolicyError   bool              `json:"policy_error"` // the open pull requests could not be read
}

const (
	approvalUnknown   = "unknown"    // the approval could not be read
	approvalNotNeeded = "not_needed" // the pull request changes no protected file
)

func clip(s string, n int) string {
	if utf8.RuneCountInString(s) <= n {
		return s
	}
	return string([]rune(s)[:n]) + "…"
}

func decisionRowOf(sc scope, rec gate.DecisionRecord, titles map[int]string) decisionRow {
	return decisionRow{
		Pull: rec.PullRequest, Title: clip(titles[rec.PullRequest], maxRequestText), Status: string(rec.Status),
		Finding: rec.FindingID[:min(len(rec.FindingID), shortFindingID)], Rule: clip(rec.RuleID, maxRequestText),
		Edge:      clip(rec.EdgeFrom, maxRequestText) + " → " + clip(rec.EdgeTo, maxRequestText),
		Requester: clip(rec.Requester, maxRequestText), Head: rec.HeadSHA[:min(len(rec.HeadSHA), shortSHA)],
		Created: rec.CreatedAt.UTC().Format(time.RFC3339), Href: repoPath(sc, "/pull/"+strconv.Itoa(rec.PullRequest)),
	}
}

// shortFindingID is the length of a finding ID prefix, as the engine prints it and the comment shows it.
const shortFindingID = 8

// queue reads the repository's decision requests: how many wait for an owner and the first limit records. A failed read
// is reported, never shown as none. One Store read for the list and one per record; GitHub is not called.
func (s *server) queue(ctx context.Context, sc scope, limit int) (gate.PendingDecisions, bool) {
	q, err := s.decisionQueue.PendingDecisions(ctx, gate.Target{Repo: sc.repo}, limit)
	if err != nil {
		slog.Warn("ui decision requests unavailable", "repository_id", sc.repo.ID, "installation_id", sc.repo.InstallationID)
		return gate.PendingDecisions{}, true
	}
	return q, false
}

// pendingDecisions is the repository page's part: the number of pending requests and the first few, oldest first.
func (s *server) pendingDecisions(ctx context.Context, sc scope, limit int, titles map[int]string) (pending int, rows []decisionRow, failed bool) {
	q, failed := s.queue(ctx, sc, limit)
	rows = []decisionRow{}
	for _, rec := range q.Records {
		if rec.Status == decision.StatusPending {
			rows = append(rows, decisionRowOf(sc, rec, titles))
		}
	}
	return q.Open, rows, failed
}

// apiDecisions is the decisions page: GET /api/repos/{o}/{n}/decisions. Read only: the buttons are on the pull request
// page.
func (s *server) apiDecisions(r *http.Request, sc scope) (any, error) {
	ctx := r.Context()
	var (
		body   inboxBody
		checks []pullCheck
		q      gate.PendingDecisions
	)
	together(
		func() { checks, body.PolicyMore, body.PolicyError = s.openPullChecks(ctx, sc) },
		func() { q, body.RequestsError = s.queue(ctx, sc, maxInboxRequests) },
	)
	titles := map[int]string{}
	for _, c := range checks {
		titles[c.pull.Number] = c.pull.Title
	}
	body.Requests = []decisionRow{}
	for _, rec := range q.Records {
		body.Requests = append(body.Requests, decisionRowOf(sc, rec, titles))
	}
	// Pending first, each group newest first.
	slices.SortStableFunc(body.Requests, func(a, b decisionRow) int {
		return cmp.Or(cmp.Compare(b.statusRank(), a.statusRank()), strings.Compare(b.Created, a.Created))
	})
	body.RequestsMore = max(0, q.Open-pendingIn(body.Requests))
	body.Policy = s.policyChanges(ctx, sc, checks)
	return body, nil
}

func (d decisionRow) statusRank() int {
	if d.Status == string(decision.StatusPending) {
		return 1
	}
	return 0
}

func pendingIn(rows []decisionRow) (n int) {
	for _, r := range rows {
		n += r.statusRank()
	}
	return n
}

// policyChanges lists the open pull requests whose check carries the policy change card, with the owner approval of
// each head. The card is on the check the App wrote; no pull request is asked about a policy it does not change.
func (s *server) policyChanges(ctx context.Context, sc scope, checks []pullCheck) []policyChangeRow {
	var changing []pullCheck
	for _, c := range checks {
		if c.run != nil && actionContext(*c.run).policySummary != "" {
			changing = append(changing, c)
		}
	}
	rows := make([]policyChangeRow, len(changing))
	slots := make(chan struct{}, parallelLookups)
	var wg sync.WaitGroup
	for i, c := range changing {
		wg.Add(1)
		slots <- struct{}{}
		go func() {
			defer wg.Done()
			defer func() { <-slots }()
			rows[i] = policyChangeRow{
				Number: c.pull.Number, Title: c.pull.Title, Headline: actionContext(*c.run).policySummary, Approval: approvalUnknown,
				Href: repoPath(sc, "/pull/"+strconv.Itoa(c.pull.Number)),
			}
			switch a, err := s.approval(ctx, sc, c.pull); {
			case err != nil:
			case a == nil:
				rows[i].Approval = approvalNotNeeded
			default:
				rows[i].Approval, rows[i].By = a.State, a.By
			}
		}()
	}
	wg.Wait()
	return rows
}

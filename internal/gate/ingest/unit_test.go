package ingest

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"reflect"
	"slices"
	"strings"
	"testing"

	"github.com/alexei-led/archfit-app/internal/github/api"
	"github.com/alexei-led/archfit-app/internal/github/oidc"
	"github.com/alexei-led/archfit-app/internal/wire/envelope"
	"github.com/alexei-led/archfit-app/internal/wire/report"
)

type rejectionRow struct {
	name   string
	base   func(*testing.T) *scenario // nil = prScenario
	mutate func(*scenario)
	status int
	code   string
	calls  []string
}

func (r rejectionRow) scenario(t *testing.T) *scenario {
	t.Helper()
	base := r.base
	if base == nil {
		base = prScenario
	}
	s := base(t)
	r.mutate(s)
	return s
}

func envelopeWithUnknownField(s *scenario) {
	raw, _ := json.Marshal(s.env)
	var m map[string]any
	_ = json.Unmarshal(raw, &m)
	m["installation_id"] = 99
	raw, _ = json.Marshal(m)
	s.rawEnvelope = string(raw)
}

func rejectionRows() []rejectionRow {
	rateLimited := fmt.Errorf("fake: %w", api.ErrRateLimited)
	return []rejectionRow{
		{name: "GET", mutate: func(s *scenario) { s.method = http.MethodGet },
			status: 400, code: "method_not_allowed"},
		{name: "body over the cap", mutate: func(s *scenario) { s.setBody(bytes.Repeat([]byte("x"), report.MaxBytes+1)) },
			status: 413, code: "payload_too_large"},
		{name: "no envelope header", mutate: func(s *scenario) { s.noEnvelope = true },
			status: 400, code: "envelope_missing"},
		{name: "envelope with an unknown field", mutate: envelopeWithUnknownField,
			status: 400, code: "envelope_invalid"},
		{name: "discovery envelope on the report route", mutate: func(s *scenario) { s.env.Kind = envelope.KindDiscovery },
			status: 400, code: "kind_mismatch"},
		{name: "no bearer", mutate: func(s *scenario) { s.auth = "" },
			status: 401, code: "token_missing"},
		{name: "not a bearer scheme", mutate: func(s *scenario) { s.auth = "Basic " + testToken },
			status: 401, code: "token_missing"},
		{name: "bad signature", mutate: func(s *scenario) { s.verifyErr = errors.New("oidc: bad signature") },
			status: 401, code: "token_invalid", calls: []string{"verify"}},
		{name: "wrong audience", mutate: func(s *scenario) { s.verifyErr = errors.New("oidc: wrong audience") },
			status: 401, code: "token_invalid", calls: []string{"verify"}},
		{name: "expired", mutate: func(s *scenario) { s.verifyErr = errors.New("oidc: token expired") },
			status: 401, code: "token_invalid", calls: []string{"verify"}},
		{name: "signing keys unavailable", mutate: func(s *scenario) { s.verifyErr = fmt.Errorf("fetch jwks: %w", oidc.ErrUnavailable) },
			status: 503, code: "oidc_unavailable", calls: []string{"verify"}},
		{name: "claim repository differs from envelope", mutate: func(s *scenario) { s.env.Repository = otherTenant },
			status: 403, code: "repository_mismatch", calls: []string{"verify"}},
		{name: "app not installed", mutate: func(s *scenario) { s.resolveErr = fmt.Errorf("fake: %w", api.ErrNotFound) },
			status: 403, code: "not_installed", calls: []string{"verify", "resolve"}},
		{name: "repository ID differs", mutate: func(s *scenario) { s.repo.ID = repoID + 1 },
			status: 403, code: "repository_mismatch", calls: []string{"verify", "resolve"}},
		{name: "installation not approved", mutate: func(s *scenario) { s.notAdmitted = true },
			status: 403, code: "installation_not_approved", calls: []string{"verify", "resolve"}},
		{name: "approval unreadable", mutate: func(s *scenario) { s.admitDown = true },
			status: 503, code: "admission_unavailable", calls: []string{"verify", "resolve"}},
		{name: "run not found", mutate: func(s *scenario) { s.runErr = fmt.Errorf("fake: %w", api.ErrNotFound) },
			status: 403, code: "run_not_found", calls: []string{"verify", "resolve", "run"}},
		{name: "claim event differs from run", mutate: func(s *scenario) { s.claims.EventName = "push" },
			status: 403, code: "run_mismatch", calls: []string{"verify", "resolve", "run"}},
		{name: "push claim SHA differs", base: pushScenario, mutate: func(s *scenario) { s.claims.SHA = movedSHA },
			status: 403, code: "run_mismatch", calls: []string{"verify", "resolve", "run"}},
		{name: "claim attempt 1 while the run is at attempt 2", mutate: func(s *scenario) { s.run.Attempt = 2 },
			status: 409, code: "stale_attempt", calls: []string{"verify", "resolve", "run"}},
		{name: "PR head moved", mutate: func(s *scenario) {
			p := s.pulls[prNumber]
			p.HeadSHA = movedSHA
			s.pulls[prNumber] = p
		}, status: 409, code: "stale_head", calls: []string{"verify", "resolve", "run", "pull"}},
		{name: "payload digest differs", mutate: func(s *scenario) { s.env.PayloadDigest = envelope.PayloadDigest([]byte("other")) },
			status: 400, code: "digest_mismatch", calls: []string{"verify", "resolve", "run", "pull"}},
		{name: "rate limit at ResolveRepo", mutate: func(s *scenario) { s.resolveErr = rateLimited },
			status: 429, code: "rate_limited", calls: []string{"verify", "resolve"}},
		{name: "rate limit at Run", mutate: func(s *scenario) { s.runErr = rateLimited },
			status: 429, code: "rate_limited", calls: []string{"verify", "resolve", "run"}},
		{name: "rate limit at Pull", mutate: func(s *scenario) { s.pullErr = rateLimited },
			status: 429, code: "rate_limited", calls: []string{"verify", "resolve", "run", "pull"}},
	}
}

func TestRejectionTable(t *testing.T) {
	for _, row := range rejectionRows() {
		t.Run(row.name, func(t *testing.T) {
			s := row.scenario(t)
			up, err := s.receive(t)
			wantRejection(t, up, err, row.status, row.code)
			wantCalls(t, s, row.calls...)
			if msg := err.Error(); !strings.Contains(msg, row.code) {
				t.Errorf("error text %q does not name the code", msg)
			}
		})
	}

	boom := errors.New("fake: connection reset")
	failures := map[string]func(*scenario){
		"ResolveRepo": func(s *scenario) { s.resolveErr = boom },
		"Run":         func(s *scenario) { s.runErr = boom },
		"Pull":        func(s *scenario) { s.pullErr = boom },
	}
	for name, mutate := range failures {
		t.Run("other API error at "+name+" is a plain error", func(t *testing.T) {
			s := prScenario(t)
			mutate(s)
			up, err := s.receive(t)
			var rej *Rejection
			if !errors.Is(err, boom) || errors.As(err, &rej) {
				t.Errorf("error = %v, want a plain error wrapping the API error", err)
			}
			if !reflect.DeepEqual(up, Upload{}) {
				t.Errorf("upload = %+v, want zero", up.Binding)
			}
		})
	}
}

func TestCheckOrder(t *testing.T) {
	rows := []struct {
		name   string
		mutate func(*scenario)
		status int
		code   string
	}{
		{"oversized and unauthenticated", func(s *scenario) {
			s.setBody(bytes.Repeat([]byte("x"), report.MaxBytes+1))
			s.auth = ""
		}, 413, "payload_too_large"},
		{"bad envelope and bad token", func(s *scenario) {
			s.rawEnvelope = "{"
			s.verifyErr = errors.New("oidc: bad signature")
		}, 400, "envelope_invalid"},
		{"stale head and digest mismatch", func(s *scenario) {
			p := s.pulls[prNumber]
			p.HeadSHA = movedSHA
			s.pulls[prNumber] = p
			s.env.PayloadDigest = envelope.PayloadDigest([]byte("other"))
		}, 409, "stale_head"},
	}
	for _, row := range rows {
		t.Run(row.name, func(t *testing.T) {
			s := prScenario(t)
			row.mutate(s)
			up, err := s.receive(t)
			wantRejection(t, up, err, row.status, row.code)
		})
	}
}

type resolutionRow struct {
	name     string
	mutate   func(*scenario)
	status   int // 0 = accepted
	pull     int
	problems []Problem
	calls    int // Pulls.Pull calls
}

func resolutionRows() []resolutionRow {
	moved := func(number int) api.Pull {
		p := openPull(number)
		p.HeadSHA = movedSHA
		return p
	}
	mismatch := []Problem{ProblemEnvelopeMismatch}
	return []resolutionRow{
		{name: "one associated PR with matching head", mutate: func(*scenario) {}, pull: prNumber, calls: 1},
		{name: "one with a moved head", mutate: func(s *scenario) { s.pulls[prNumber] = moved(prNumber) },
			status: 409, calls: 1},
		{name: "two PRs, one matching", mutate: func(s *scenario) {
			s.run.PullRequests = []int{8, prNumber}
			s.pulls[8] = moved(8)
		}, pull: prNumber, calls: 2},
		{name: "two PRs, both matching", mutate: func(s *scenario) {
			s.run.PullRequests = []int{prNumber, 8}
			s.pulls[8] = openPull(8)
		}, problems: mismatch, calls: 2},
		{name: "two PRs, none matching", mutate: func(s *scenario) {
			s.run.PullRequests = []int{prNumber, 8}
			s.pulls[prNumber] = moved(prNumber)
			s.pulls[8] = moved(8)
		}, problems: mismatch, calls: 2},
		{name: "empty list with a valid envelope hint", mutate: func(s *scenario) { s.run.PullRequests = nil },
			pull: prNumber, calls: 1},
		{name: "empty list with hint whose head differs", mutate: func(s *scenario) {
			s.run.PullRequests = nil
			s.pulls[prNumber] = moved(prNumber)
		}, status: 409, calls: 1},
		{name: "empty list with a hint GitHub does not know", mutate: func(s *scenario) {
			s.run.PullRequests = nil
			delete(s.pulls, prNumber)
		}, problems: mismatch, calls: 1},
		// envelope.Decode refuses pull_request 0 on a pull_request event, so an envelope
		// without a hint necessarily claims another event.
		{name: "empty list and no hint", mutate: func(s *scenario) {
			s.run.PullRequests = nil
			s.env.Event = eventPush
			s.env.PullRequest = 0
			s.env.BaseSHA = ""
			s.env.MergeBaseSHA = ""
			s.env.Integration = ""
		}, problems: mismatch},
		{name: "eleven candidates", mutate: func(s *scenario) {
			s.run.PullRequests = nil
			for n := 1; n <= 11; n++ {
				s.run.PullRequests = append(s.run.PullRequests, n)
				s.pulls[n] = moved(n)
			}
			s.pulls[prNumber] = openPull(prNumber)
		}, problems: mismatch},
		{name: "a closed PR", mutate: func(s *scenario) {
			p := s.pulls[prNumber]
			p.State = "closed"
			s.pulls[prNumber] = p
		}, pull: prNumber, problems: mismatch, calls: 1},
	}
}

func TestPullRequestResolution(t *testing.T) {
	for _, row := range resolutionRows() {
		t.Run(row.name, func(t *testing.T) {
			s := prScenario(t)
			row.mutate(s)
			up, err := s.receive(t)
			if n := strings.Count(strings.Join(s.calls, ","), "pull"); n != row.calls {
				t.Errorf("Pulls.Pull calls = %d, want %d", n, row.calls)
			}
			if row.status != 0 {
				wantRejection(t, up, err, row.status, "stale_head")
				return
			}
			wantProblems(t, up, err, row.problems...)
			if up.Binding.PullRequest != row.pull {
				t.Errorf("PullRequest = %d, want %d", up.Binding.PullRequest, row.pull)
			}
			if row.pull == 0 && (up.Binding.BaseSHA != "" || up.Binding.MergeBaseSHA != "" || up.Binding.BaseRef != defaultBranch) {
				t.Errorf("unbound binding = %+v, want zero base values and the default branch", up.Binding)
			}
			if up.Binding.HeadSHA != headSHA {
				t.Errorf("HeadSHA = %q, want the run head", up.Binding.HeadSHA)
			}
		})
	}
}

func TestEnvelopeComparison(t *testing.T) {
	flips := []struct {
		name   string
		mutate func(*envelope.Envelope)
	}{
		// envelope.Decode ties pull_request, base_sha and merge_base_sha to the event.
		{"event", func(e *envelope.Envelope) {
			e.Event, e.PullRequest, e.BaseSHA, e.MergeBaseSHA, e.Integration = eventPush, 0, "", "", ""
		}},
		{"analyzed commit", func(e *envelope.Envelope) { e.AnalyzedSHA = movedSHA }},
		{"integration", func(e *envelope.Envelope) { e.Integration = envelope.IntegrationConflicts }},
		{"PR number", func(e *envelope.Envelope) { e.PullRequest = 8 }},
		{"head", func(e *envelope.Envelope) { e.HeadSHA = movedSHA }},
		{"base", func(e *envelope.Envelope) { e.BaseSHA = movedSHA }},
		{"merge-base", func(e *envelope.Envelope) { e.MergeBaseSHA = movedSHA }},
		{"run ID", func(e *envelope.Envelope) { e.RunID = runID + 1 }},
		{"attempt", func(e *envelope.Envelope) { e.RunAttempt = 2 }},
	}
	for _, flip := range flips {
		t.Run(flip.name, func(t *testing.T) {
			s := prScenario(t)
			flip.mutate(&s.env)
			up, err := s.receive(t)
			wantProblems(t, up, err, ProblemEnvelopeMismatch)
			if want := healthyBinding(s); !reflect.DeepEqual(up.Binding, want) {
				t.Errorf("binding = %+v, want authoritative %+v", up.Binding, want)
			}
		})
	}

	t.Run("claimed digests are passed on", func(t *testing.T) {
		s := prScenario(t)
		s.env.BaselineDigest = strings.Repeat("1", 64)
		s.env.LabelsDigest = strings.Repeat("2", 64)
		up, err := s.receive(t)
		wantProblems(t, up, err)
		if up.BaselineDigest != s.env.BaselineDigest || up.LabelsDigest != s.env.LabelsDigest {
			t.Errorf("claimed digests = %q, %q, want the envelope's", up.BaselineDigest, up.LabelsDigest)
		}
	})
}

func TestWorkflowPathBinding(t *testing.T) {
	rows := []struct {
		name, ref, want string
	}{
		{"same repository", repoName + "/" + workflowPath + "@refs/pull/7/merge", workflowPath},
		{"reusable workflow elsewhere", "acme/shared/.github/workflows/archfit.yaml@refs/heads/main", ""},
	}
	for _, row := range rows {
		t.Run(row.name, func(t *testing.T) {
			s := prScenario(t)
			s.claims.JobWorkflowRef = row.ref
			up, err := s.receive(t)
			wantProblems(t, up, err)
			if up.Binding.WorkflowPath != row.want {
				t.Errorf("WorkflowPath = %q, want %q", up.Binding.WorkflowPath, row.want)
			}
		})
	}
}

// TestEligibleEventTable: a dispatch may upload a discovery, a baseline or a re-anchor; a report comes from a pull
// request; a re-anchor comes from a dispatch only.
func TestEligibleEventTable(t *testing.T) {
	tests := []struct {
		name            string
		kind            envelope.Kind
		event           string
		onDefaultBranch bool
		want            bool
	}{
		{"report on a pull request", envelope.KindReport, eventPullRequest, false, true},
		{"reanchor by dispatch on the default branch", envelope.KindReanchor, eventDispatch, true, true},
		{"reanchor on a pull request", envelope.KindReanchor, eventPullRequest, false, false},
		{"reanchor on a push to the default branch", envelope.KindReanchor, eventPush, true, false},
		{"baseline by dispatch on the default branch", envelope.KindBaseline, eventDispatch, true, true},
		{"report by dispatch", envelope.KindReport, eventDispatch, true, false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := eligible(tt.kind, tt.event, tt.onDefaultBranch); got != tt.want {
				t.Errorf("eligible = %v, want %v", got, tt.want)
			}
		})
	}
}

func TestAdmissionIsAskedForTheResolvedInstallationAndOwner(t *testing.T) {
	s := prScenario(t)
	if _, err := s.receive(t); err != nil {
		t.Fatal(err)
	}
	owner, _, _ := strings.Cut(repoName, "/")
	if want := []string{fmt.Sprintf("%d %s", installationID, owner)}; !slices.Equal(s.admitAsked, want) {
		t.Errorf("Admitted asked %v; want %v", s.admitAsked, want)
	}
}

// TestLandingComesFromGitHub: what the engine must have measured is GitHub's answer, never the envelope's claim. A
// pull request that GitHub says conflicts is measured at its head; one that merges, at GitHub's test merge commit; one
// whose merge GitHub has not computed matches no claim.
func TestLandingComesFromGitHub(t *testing.T) {
	no, yes := false, true
	tests := []struct {
		name        string
		github      func(*api.Pull)
		analyzed    string
		integration string
		mismatch    bool
	}{
		{"merges cleanly", func(p *api.Pull) { p.Mergeable = &yes }, mergeSHA, envelope.IntegrationMerged, false},
		{"mergeability not computed, merge commit known", func(p *api.Pull) { p.Mergeable = nil }, mergeSHA, envelope.IntegrationMerged, false},
		{"conflicts", func(p *api.Pull) { p.Mergeable, p.MergeCommitSHA = &no, "" }, headSHA, envelope.IntegrationConflicts, false},
		{"conflicts, a stale merge commit still named", func(p *api.Pull) { p.Mergeable = &no }, headSHA, envelope.IntegrationConflicts, false},
		{"envelope claims merged while GitHub says conflicts", func(p *api.Pull) { p.Mergeable, p.MergeCommitSHA = &no, "" }, mergeSHA, envelope.IntegrationMerged, true},
		{"envelope claims conflicts while GitHub says it merges", func(p *api.Pull) { p.Mergeable = &yes }, headSHA, envelope.IntegrationConflicts, true},
		{"envelope names a commit that is not GitHub's merge commit", func(*api.Pull) {}, movedSHA, envelope.IntegrationMerged, true},
		{"envelope analyses the head alone while GitHub merges", func(*api.Pull) {}, headSHA, envelope.IntegrationMerged, true},
		{"GitHub has no merge commit and no verdict", func(p *api.Pull) { p.Mergeable, p.MergeCommitSHA = nil, "" }, mergeSHA, envelope.IntegrationMerged, true},
		{"GitHub has no merge commit and the envelope claims conflicts", func(p *api.Pull) { p.Mergeable, p.MergeCommitSHA = nil, "" }, headSHA, envelope.IntegrationConflicts, true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			s := prScenario(t)
			pull := s.pulls[prNumber]
			tt.github(&pull)
			s.pulls[prNumber] = pull
			s.env.AnalyzedSHA, s.env.Integration = tt.analyzed, tt.integration
			up, err := s.receive(t)
			if tt.mismatch {
				wantProblems(t, up, err, ProblemEnvelopeMismatch)
			} else {
				wantProblems(t, up, err)
			}
		})
	}
}

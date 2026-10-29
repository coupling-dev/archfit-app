package ui

import (
	"errors"
	"maps"
	"net/http"
	"net/http/httptest"
	"slices"
	"strings"
	"testing"

	"github.com/alexei-led/archfit-app/internal/gate"
	"github.com/alexei-led/archfit-app/internal/github/api"
	"github.com/alexei-led/archfit-app/internal/wire/policy"
)

const (
	pullAPI    = "/api/repos/" + repoName + "/pulls/4"
	approveAPI = pullAPI + "/approve"
)

var (
	baselineDigest = strings.Repeat("ab", 32)
	shownBaseline  = gate.Candidate{
		Paths:   []string{policy.BaselinePath, policy.LabelsPath},
		Digests: map[string]string{policy.BaselinePath: baselineDigest, policy.LabelsPath: ""},
	}
)

// sameRepoHead makes pull request #4 a branch of the repository itself, not of a fork.
func (e *env) sameRepoHead() {
	e.gh.mu.Lock()
	defer e.gh.mu.Unlock()
	p := e.gh.pulls[repoName+"#4"]
	p.HeadRepo = "ACME/app" // GitHub's case may differ from the authorized name
	e.gh.pulls[repoName+"#4"] = p
}

func TestPullJSONCarriesTheApproval(t *testing.T) {
	tests := []struct {
		name     string
		approval gate.PullApproval
		want     *approvalJSON
	}{
		{name: "no protected file changed"},
		{
			name:     "missing, and the user may approve",
			approval: gate.PullApproval{Candidate: shownBaseline},
			want: &approvalJSON{
				HeadSHA: sha(4), Paths: shownBaseline.Paths, Digests: shownBaseline.Digests, State: "missing", CanApprove: true,
				ShortDigests: map[string]string{policy.BaselinePath: baselineDigest[:7], policy.LabelsPath: ""},
			},
		},
		{
			name:     "missing, and the user is not an owner",
			approval: gate.PullApproval{Candidate: shownBaseline, Refusal: gate.ErrNotOwner},
			want: &approvalJSON{
				HeadSHA: sha(4), Paths: shownBaseline.Paths, Digests: shownBaseline.Digests, State: "missing",
				ShortDigests: map[string]string{policy.BaselinePath: baselineDigest[:7], policy.LabelsPath: ""},
				Reason:       "Only an architecture owner named in .archfit-app.yaml on the base branch can approve.",
			},
		},
		{
			name:     "granted, opened by an agent identity",
			approval: gate.PullApproval{Candidate: shownBaseline, Granted: true, ApprovedBy: "alice", AuthorIsAgent: true},
			want: &approvalJSON{
				HeadSHA: sha(4), Paths: shownBaseline.Paths, Digests: shownBaseline.Digests, State: "granted", By: "alice",
				ShortDigests:  map[string]string{policy.BaselinePath: baselineDigest[:7], policy.LabelsPath: ""},
				AuthorIsAgent: true,
			},
		},
		{
			name:     "a fork: no digests",
			approval: gate.PullApproval{Candidate: gate.Candidate{Paths: []string{policy.PolicyPath}}, Refusal: gate.ErrForkPull},
			want: &approvalJSON{
				HeadSHA: sha(4), Paths: []string{policy.PolicyPath}, State: "missing",
				Reason: "A pull request from a fork cannot be approved. Push the branch to this repository.",
			},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			e := newEnv(t)
			e.sameRepoHead()
			e.approvals.approval = tt.approval
			res := e.as("alice", permRead).get(pullAPI)
			wantStatus(t, res, http.StatusOK)

			got := decodeAs[pullJSON](t, res).Approval
			if (got == nil) != (tt.want == nil) {
				t.Fatalf("approval = %+v, want %+v", got, tt.want)
			}
			if tt.want == nil {
				if strings.Contains(res.Body.String(), `"approval"`) {
					t.Errorf("the answer names an approval: %s", res.Body.String())
				}
				return
			}
			if got.HeadSHA != tt.want.HeadSHA || !slices.Equal(got.Paths, tt.want.Paths) || !maps.Equal(got.Digests, tt.want.Digests) ||
				(got.Digests == nil) != (tt.want.Digests == nil) || !maps.Equal(got.ShortDigests, tt.want.ShortDigests) ||
				got.State != tt.want.State || got.By != tt.want.By || got.CanApprove != tt.want.CanApprove ||
				got.Reason != tt.want.Reason || got.AuthorIsAgent != tt.want.AuthorIsAgent {
				t.Errorf("approval =\n%+v\nwant\n%+v", *got, *tt.want)
			}
			read := e.approvals.reads[0]
			want := gate.Target{Repo: e.gh.repos[repoName], PullRequest: 4, HeadSHA: sha(4), BaseRef: mainBranch}
			if read.target != want || read.login != "alice" || read.author != "alexei" {
				t.Errorf("PullApproval(%+v, %q, %q), want the authorized repository, alice and the author", read.target, read.login, read.author)
			}
		})
	}

	t.Run("a head in another repository is a fork", func(t *testing.T) {
		e := newEnv(t)
		e.as("alice", permRead).get(pullAPI)
		if !e.approvals.reads[0].target.Fork {
			t.Errorf("target = %+v, want a fork", e.approvals.reads[0].target)
		}
	})
	t.Run("an approval that cannot be read fails the page", func(t *testing.T) {
		e := newEnv(t)
		e.approvals.readErr = errors.New("github down")
		res := e.as("alice", permRead).get(pullAPI)
		wantStatus(t, res, http.StatusBadGateway)
	})
}

func TestApprovePull(t *testing.T) {
	seen := shownBaseline.Digests
	tests := []struct {
		name       string
		permission string
		body       any
		approveErr error
		reevalErr  error
		status     int
		sentence   string
		approved   bool // Approve reached the gate and recorded
		reran      bool
	}{
		{name: "an owner approves the head shown", permission: permWrite, body: approveJSON{HeadSHA: sha(4), Digests: seen},
			status: http.StatusOK, sentence: notices["approved"], approved: true, reran: true},
		{name: "a new commit since the page loaded", permission: permWrite, body: approveJSON{HeadSHA: sha(3), Digests: seen},
			status: http.StatusConflict, sentence: "The pull request has a new commit since this page was loaded."},
		{name: "the short head of the page", permission: permWrite, body: approveJSON{HeadSHA: sha(4)[:7], Digests: seen},
			status: http.StatusConflict, sentence: "Nothing was approved."},
		{name: "not an owner", permission: permWrite, body: approveJSON{HeadSHA: sha(4), Digests: seen}, approveErr: gate.ErrNotOwner,
			status: http.StatusForbidden, sentence: "Only an architecture owner named in .archfit-app.yaml on the base branch can approve."},
		{name: "an owner listed as an agent", permission: permAdmin, body: approveJSON{HeadSHA: sha(4), Digests: seen}, approveErr: gate.ErrAgentIdentity,
			status: http.StatusForbidden, sentence: "An agent identity cannot approve."},
		{name: "the files changed since the page loaded", permission: permWrite, body: approveJSON{HeadSHA: sha(4)}, approveErr: gate.ErrDigestsChanged,
			status: http.StatusConflict, sentence: "The protected files changed since this page was loaded. Nothing was approved."},
		{name: "nothing to approve", permission: permWrite, body: approveJSON{HeadSHA: sha(4)}, approveErr: gate.ErrNothingToApprove,
			status: http.StatusConflict, sentence: "nothing to approve"},
		{name: "a fork", permission: permWrite, body: approveJSON{HeadSHA: sha(4)}, approveErr: gate.ErrForkPull,
			status: http.StatusConflict, sentence: "A pull request from a fork cannot be approved."},
		{name: "recorded, but the re-run failed", permission: permWrite, body: approveJSON{HeadSHA: sha(4), Digests: seen}, reevalErr: api.ErrRateLimited,
			status: http.StatusBadGateway, sentence: "The approval was recorded, but the workflow could not be re-run now.", approved: true, reran: true},
		{name: "a read-only user", permission: permRead, body: approveJSON{HeadSHA: sha(4), Digests: seen},
			status: http.StatusNotFound, sentence: "Not found."},
		{name: "a body naming a repository", permission: permWrite, body: map[string]any{"head_sha": sha(4), "repository": otherRepo},
			status: http.StatusUnprocessableEntity, sentence: "The request could not be read."},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			e := newEnv(t)
			e.sameRepoHead()
			e.approvals.approval = gate.PullApproval{Candidate: shownBaseline}
			e.approvals.approveErr, e.approvals.reevaluateErr = tt.approveErr, tt.reevalErr
			res := e.as("alice", tt.permission).postJSON(approveAPI, tt.body)
			wantStatus(t, res, tt.status)
			wantContains(t, res.Body.String(), tt.sentence)

			if got := len(e.approvals.approved) == 1; got != tt.approved {
				t.Fatalf("approved = %v, want %v", e.approvals.approved, tt.approved)
			}
			if got := len(e.approvals.reevaluated) == 1; got != tt.reran {
				t.Errorf("re-decided = %v, want %v", e.approvals.reevaluated, tt.reran)
			}
			if !tt.approved {
				return
			}
			call := e.approvals.approved[0]
			want := gate.Target{Repo: e.gh.repos[repoName], PullRequest: 4, HeadSHA: sha(4), BaseRef: mainBranch}
			if call.target != want || call.login != "alice" || !maps.Equal(call.seen, seen) {
				t.Errorf("Approve(%+v, %q, %v), want the authorized pull request, the session's login and the digests shown", call.target, call.login, call.seen)
			}
			if e.approvals.reevaluated[0] != want {
				t.Errorf("Reevaluate(%+v), want %+v", e.approvals.reevaluated[0], want)
			}
			if tt.status != http.StatusOK {
				return
			}
			body := decodeAs[approvedJSON](t, res)
			if body.Approval == nil || body.Approval.State != "granted" || body.Approval.By != "alice" || body.Approval.CanApprove {
				t.Errorf("answer = %+v, want the approval read again: granted by alice", body.Approval)
			}
		})
	}

	t.Run("recorded, but not read back", func(t *testing.T) {
		e := newEnv(t)
		e.sameRepoHead()
		e.approvals.approval = gate.PullApproval{Candidate: shownBaseline}
		c := e.as("alice", permWrite)
		e.approvals.readErr = errors.New("github down")
		res := c.postJSON(approveAPI, approveJSON{HeadSHA: sha(4), Digests: seen})
		wantStatus(t, res, http.StatusBadGateway)
		wantContains(t, res.Body.String(), "The change was saved, but it could not be read back.")
		if len(e.approvals.approved) != 1 || len(e.approvals.reevaluated) != 1 {
			t.Errorf("approved %v, re-decided %v; want one each", e.approvals.approved, e.approvals.reevaluated)
		}
	})
	t.Run("signed out", func(t *testing.T) {
		e := newEnv(t)
		res := e.anonymous().postJSONWith(approveAPI, approveJSON{HeadSHA: sha(4), Digests: seen}, "")
		wantStatus(t, res, http.StatusUnauthorized)
		e.noWrites(t)
	})
	t.Run("an Actions OIDC token is no session", func(t *testing.T) {
		e := newEnv(t)
		c := e.as("alice", permWrite)
		req := httptest.NewRequest(http.MethodPost, baseURL+approveAPI, strings.NewReader(`{"head_sha":"`+sha(4)+`"}`))
		req.Header.Set("Content-Type", mediaJSON)
		req.Header.Set(csrfHeader, c.csrf())
		req.Header.Set("Authorization", "Bearer an-actions-oidc-token")
		wantStatus(t, e.anonymous().do(req), http.StatusUnauthorized)
		e.noWrites(t)
	})
	t.Run("without the CSRF token", func(t *testing.T) {
		e := newEnv(t)
		res := e.as("alice", permWrite).postJSONWith(approveAPI, approveJSON{HeadSHA: sha(4), Digests: seen}, "")
		wantStatus(t, res, http.StatusForbidden)
		e.noWrites(t)
	})
	t.Run("not a pull request number", func(t *testing.T) {
		e := newEnv(t)
		res := e.as("alice", permWrite).postJSON("/api/repos/"+repoName+"/pulls/x/approve", approveJSON{HeadSHA: sha(4)})
		wantStatus(t, res, http.StatusNotFound)
		e.noWrites(t)
	})
}

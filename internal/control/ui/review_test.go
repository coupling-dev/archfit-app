package ui

import (
	"encoding/json"
	"errors"
	"net/http"
	"strings"
	"testing"

	"github.com/alexei-led/archfit-app/internal/control/answers"
	"github.com/alexei-led/archfit-app/internal/control/onboarding"
	"github.com/alexei-led/archfit-app/internal/gate"
	"github.com/alexei-led/archfit-app/internal/wire/envelope"
	"github.com/alexei-led/archfit-app/internal/wire/result"
)

func TestPullReviewLinks(t *testing.T) {
	for _, tt := range []struct {
		name, kind, permission, status, href string
		refusal                              error
	}{
		{"eligible owner", gate.OwnerWorkflow, permRead, "unknown", "/r/acme/app/owner/workflow?pull=4", nil},
		{"eligible admin", gate.OwnerFirst, permAdmin, "unknown", "/r/acme/app/owner/first?pull=4", nil},
		{"ineligible viewer", gate.OwnerWorkflow, permRead, "unknown", "", gate.ErrOwnerNotOwner},
		{"unnamed administrator", gate.OwnerFirst, permAdmin, "unknown", "", gate.ErrOwnerNotAdmin},
		{"no review needed", gate.OwnerWorkflow, permRead, "pass", "", nil},
		{"review no longer due", gate.OwnerWorkflow, permRead, "unknown", "", gate.ErrOwnerNotDue},
		{"unreadable authority", gate.OwnerWorkflow, permRead, "unknown", "", errors.New("unavailable")},
	} {
		t.Run(tt.name, func(t *testing.T) {
			e := newEnv(t)
			calls := 0
			e.owners.review = func(a answers.OwnerAction, u gate.OwnerUser) error {
				calls++
				if u.Login != "alice" || u.Admin != (tt.permission == permAdmin) {
					t.Fatalf("wrong viewer: %+v", u)
				}
				if a.Kind != tt.kind || a.Pull != 4 {
					return gate.ErrOwnerNotDue
				}
				return tt.refusal
			}
			raw, err := json.Marshal(result.Result{Status: result.Status(tt.status), Summary: "Archfit: " + tt.status})
			if err != nil {
				t.Fatal(err)
			}
			homePut(t, e, "publication/4/"+sha(4), map[string]any{"schema": "archfit-app.publication.v1", "head_sha": sha(4), "run_id": 42, "run_attempt": 1, "result": raw, "digest": envelope.PayloadDigest(raw), "source": "sweep"})
			response := e.as("alice", tt.permission).get("/api/repos/acme/app/pulls/4")
			wantStatus(t, response, http.StatusOK)
			var body pullJSON
			if err := json.Unmarshal(response.Body.Bytes(), &body); err != nil {
				t.Fatal(err)
			}
			if tt.status == "pass" || errors.Is(tt.refusal, gate.ErrOwnerNotDue) {
				if body.Review != nil || tt.status == "pass" && calls != 0 {
					t.Fatalf("unexpected review: %+v (%d calls)", body.Review, calls)
				}
				return
			}
			if body.Review == nil || body.Review.Href != tt.href || body.Review.Label == "" {
				t.Fatalf("review = %+v, want href %q", body.Review, tt.href)
			}
			if tt.href != "" {
				want := "Review first policy"
				if tt.kind == gate.OwnerWorkflow {
					want = "Review workflow change"
				}
				if body.Review.Label != want {
					t.Fatalf("label = %q, want %q", body.Review.Label, want)
				}
			}
		})
	}
}

func TestHomeResolvesReviewForSetupAndApprovedRepositories(t *testing.T) {
	for _, state := range []string{"first_approval", "not_admitted", "approved"} {
		for _, allowed := range []bool{true, false} {
			t.Run(state+map[bool]string{true: "/owner", false: "/viewer"}[allowed], func(t *testing.T) {
				e := newHomeEnv(t, homePolicy)
				if state != "approved" {
					e.setups.out = onboarding.Setup{State: state, FirstPull: 4}
					if state == "not_admitted" {
						e.setups.out.FirstPull, e.setups.out.OpenPull = 0, 4
					}
				} else {
					e.gh.setFile(repoName, sha(4), workflow, "changed workflow")
					e.gh.setFile(repoName, sha(4), ".archfit.yaml", homePolicy)
					p := e.gh.pulls[repoName+"#4"]
					p.HeadRepo = repoName
					e.gh.pulls[repoName+"#4"] = p
				}
				kind := gate.OwnerWorkflow
				if state == "first_approval" {
					kind = gate.OwnerFirst
				}
				e.owners.review = func(a answers.OwnerAction, _ gate.OwnerUser) error {
					if a.Kind != kind || a.Pull != 4 {
						return gate.ErrOwnerNotDue
					}
					if !allowed {
						return gate.ErrOwnerNotOwner
					}
					return nil
				}
				response := e.as("alice", permAdmin).get(homeAPI)
				wantStatus(t, response, http.StatusOK)
				var body homeJSON
				if err := json.Unmarshal(response.Body.Bytes(), &body); err != nil {
					t.Fatal(err)
				}
				var href string
				if body.Setup != nil {
					if body.Setup.Review == nil {
						t.Fatal("missing setup handoff")
					}
					href = body.Setup.Review.Href
				} else {
					if len(body.NeedsYou) != 1 {
						t.Fatalf("needs you: %+v", body.NeedsYou)
					}
					href = body.NeedsYou[0].Href
				}
				if allowed && href != "/r/acme/app/owner/"+kind+"?pull=4" || !allowed && href != "" {
					t.Fatalf("href = %q, allowed %t", href, allowed)
				}
			})
		}
	}
}

// The P0 navigation copy explicitly calls the read-only destination a review,
// distinct from approval. Keep the engine's wording check for every other phrase.
func withoutOwnerReviewCopy(text string) string {
	for _, phrase := range []string{
		"Review first policy", "Review workflow change",
		"A repository administrator named as an architecture owner in the proposed policy can review this.",
		"An architecture owner named in the approved policy can review this.",
	} {
		text = strings.ReplaceAll(text, phrase, "")
	}
	return text
}

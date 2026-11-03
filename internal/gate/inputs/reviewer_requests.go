package inputs

import (
	"context"
	"crypto/rand"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"strconv"
	"strings"
	"time"

	"github.com/alexei-led/archfit-app/internal/github/api"
	"github.com/alexei-led/archfit-app/internal/store"
)

const (
	reviewerRequestPrefix = "review-request/"
	reviewerLeaseTTL      = time.Minute
	reviewerLeaseMargin   = 5 * time.Second
	reviewerAttempts      = 3
	reviewerCASAttempts   = 5
)

type reviewerRequestState struct {
	Version  int  `json:"version"`
	Attempts int  `json:"attempts"`
	Done     bool `json:"done"`
}

type reviewerRequestLease struct {
	Token   string    `json:"token"`
	Expires time.Time `json:"expires"`
}

// RequestEligibleReviewers best-effort asks missing protected owners/teams to review. It returns true for a
// notification problem; that result never changes authorization or gate state.
func (r *resolver) RequestEligibleReviewers(ctx context.Context, t Target, author string, now time.Time) bool {
	if t.PullRequest <= 0 || t.Fork {
		return false
	}
	client, ok := r.pulls.(api.ReviewerRequests)
	if !ok {
		return true
	}
	var err error
	t, err = r.pinProtectedRef(ctx, t)
	if err != nil {
		return true
	}
	settingsValue, found, err := r.baseSettings(ctx, t)
	if err != nil {
		return true
	}
	if !found {
		return false
	}
	changed, err := r.changedProtected(ctx, t, settingsValue)
	if err != nil || len(changed) == 0 {
		return err != nil
	}
	key := reviewerRequestKey(t)
	lease, held, err := r.acquireReviewerRequestLease(ctx, t, key, now)
	if err != nil {
		logOutcome(t, "reviewer_requests", "unavailable")
		return true
	}
	if !held {
		return false // another request for the same candidate is in progress
	}
	defer r.releaseReviewerRequestLease(ctx, t, key, lease)
	workCtx, cancel := context.WithTimeout(ctx, reviewerLeaseTTL-reviewerLeaseMargin)
	defer cancel()
	ctx = workCtx

	state, old, err := r.loadReviewerRequestState(ctx, t, key)
	if err != nil {
		logOutcome(t, "reviewer_requests", "unreadable")
		return true
	}
	if state.Done {
		return false
	}
	if state.Attempts >= reviewerAttempts {
		logOutcome(t, "reviewer_requests", "attempts_exhausted")
		return true
	}

	users, teams, err := client.RequestedReviewers(ctx, t.Repo.RepoRef, t.PullRequest)
	if err != nil {
		if r.bumpReviewerAttempt(ctx, t, key, old, state) != nil {
			logOutcome(t, "reviewer_requests", "state_write_failed")
		}
		logOutcome(t, "reviewer_requests", "unavailable")
		return true
	}
	requestedUsers, requestedTeams := lowerSet(users), lowerSet(teams)
	reviews, err := r.pulls.Reviews(ctx, t.Repo.RepoRef, t.PullRequest)
	if err != nil {
		if r.bumpReviewerAttempt(ctx, t, key, old, state) != nil {
			logOutcome(t, "reviewer_requests", "state_write_failed")
		}
		logOutcome(t, "reviewer_requests", "unavailable")
		return true
	}
	reviewed := make(map[string]bool)
	for _, review := range reviews {
		if review.CommitSHA == t.HeadSHA && review.Reviewer != "" {
			reviewed[strings.ToLower(review.Reviewer)] = true
		}
	}

	var missingUsers, missingTeams []string
	problem := false
	for _, principal := range settingsValue.Owners {
		if org, slug, isTeam := strings.Cut(principal, "/"); isTeam {
			if org == "" || slug == "" || requestedTeams[strings.ToLower(principal)] {
				continue
			}
			// GitHub accepts a team request, not a team minus one member. Skip the team if the PR author is in it.
			if author == "" {
				problem = true
				continue
			}
			member, err := r.pulls.TeamMember(ctx, t.Repo.RepoRef, principal, author)
			if err != nil {
				problem = true
				continue
			}
			if member {
				continue
			}
			if !strings.EqualFold(org, repoOwner(t.Repo.FullName)) {
				problem = true
				continue
			}
			missingTeams = append(missingTeams, org+"/"+slug)
			continue
		}
		if principal == "" || settingsValue.IsAgent(principal) || strings.EqualFold(principal, author) ||
			requestedUsers[strings.ToLower(principal)] || reviewed[strings.ToLower(principal)] {
			continue
		}
		missingUsers = append(missingUsers, principal)
	}
	missingUsers = uniqueFolded(missingUsers)
	missingTeams = uniqueFolded(missingTeams)
	if len(missingUsers) == 0 && len(missingTeams) == 0 && !problem {
		state.Done = true
		if err := r.saveReviewerRequestState(ctx, t, key, old, state); err != nil {
			logOutcome(t, "reviewer_requests", "state_write_failed")
			return true
		}
		return false
	}
	if state.Attempts >= reviewerAttempts {
		logOutcome(t, "reviewer_requests", "attempts_exhausted")
		return true
	}
	state.Attempts++
	attemptRaw, err := json.Marshal(state)
	if err != nil {
		logOutcome(t, "reviewer_requests", "state_write_failed")
		return true
	}
	if err := r.writeReviewerRequestState(ctx, t, key, old, attemptRaw); err != nil {
		logOutcome(t, "reviewer_requests", "state_write_failed")
		return true
	}
	if len(missingUsers) != 0 || len(missingTeams) != 0 {
		if err := client.RequestReviewers(ctx, t.Repo.RepoRef, t.PullRequest, missingUsers, missingTeams); err != nil {
			logOutcome(t, "reviewer_requests", "request_failed")
			return true
		}
	}
	if problem {
		logOutcome(t, "reviewer_requests", "partial")
		return true
	}
	state.Done = true
	next, err := json.Marshal(state)
	if err != nil || r.writeReviewerRequestState(ctx, t, key, attemptRaw, next) != nil {
		// If GitHub accepted the request but this write failed, the next attempt reads requested_reviewers before retrying.
		logOutcome(t, "reviewer_requests", "state_write_failed")
		return true
	}
	return false
}

func reviewerRequestKey(t Target) string {
	return reviewerRequestPrefix + strings.ToLower(t.Repo.FullName) + "/" + strconv.Itoa(t.PullRequest) + "/" + t.HeadSHA + "/" + t.BaseSHA
}

func reviewerLeaseKey(key string) string { return key + "/lease" }

func repoOwner(fullName string) string {
	owner, _, _ := strings.Cut(fullName, "/")
	return owner
}

func lowerSet(values []string) map[string]bool {
	set := make(map[string]bool, len(values))
	for _, value := range values {
		set[strings.ToLower(value)] = true
	}
	return set
}

func uniqueFolded(values []string) []string {
	seen := make(map[string]bool, len(values))
	out := make([]string, 0, len(values))
	for _, value := range values {
		key := strings.ToLower(value)
		if !seen[key] {
			seen[key] = true
			out = append(out, value)
		}
	}
	return out
}

func (r *resolver) loadReviewerRequestState(ctx context.Context, t Target, key string) (reviewerRequestState, []byte, error) {
	raw, err := r.store.Get(ctx, store.Tenant(t.Repo.InstallationID), key)
	if errors.Is(err, store.ErrNotFound) {
		return reviewerRequestState{Version: 1}, nil, nil
	}
	if err != nil {
		return reviewerRequestState{}, nil, err
	}
	var state reviewerRequestState
	if json.Unmarshal(raw, &state) != nil || state.Version != 1 || state.Attempts < 0 || state.Attempts > reviewerAttempts {
		return reviewerRequestState{}, nil, errors.New("inputs: invalid reviewer request state")
	}
	return state, raw, nil
}

func (r *resolver) saveReviewerRequestState(ctx context.Context, t Target, key string, old []byte, state reviewerRequestState) error {
	state.Version = 1
	next, err := json.Marshal(state)
	if err != nil {
		return fmt.Errorf("inputs: encode reviewer request state: %w", err)
	}
	return r.writeReviewerRequestState(ctx, t, key, old, next)
}

func (r *resolver) writeReviewerRequestState(ctx context.Context, t Target, key string, old, next []byte) error {
	ok, err := r.store.CompareAndSwap(ctx, store.Tenant(t.Repo.InstallationID), key, old, next)
	if err != nil {
		return fmt.Errorf("inputs: save reviewer request state: %w", err)
	}
	if !ok {
		return errors.New("inputs: reviewer request state changed")
	}
	return nil
}

func (r *resolver) bumpReviewerAttempt(ctx context.Context, t Target, key string, old []byte, state reviewerRequestState) error {
	if state.Attempts >= reviewerAttempts {
		return nil
	}
	state.Attempts++
	return r.saveReviewerRequestState(ctx, t, key, old, state)
}

func (r *resolver) acquireReviewerRequestLease(ctx context.Context, t Target, key string, now time.Time) ([]byte, bool, error) {
	tenant, leaseKey := store.Tenant(t.Repo.InstallationID), reviewerLeaseKey(key)
	for range reviewerCASAttempts {
		old, err := r.store.Get(ctx, tenant, leaseKey)
		if errors.Is(err, store.ErrNotFound) {
			old = nil
		} else if err != nil {
			return nil, false, err
		} else {
			var held reviewerRequestLease
			if json.Unmarshal(old, &held) == nil && now.Before(held.Expires) {
				return nil, false, nil
			}
		}
		mine, err := json.Marshal(reviewerRequestLease{Token: rand.Text(), Expires: now.Add(reviewerLeaseTTL)})
		if err != nil {
			return nil, false, err
		}
		ok, err := r.store.CompareAndSwap(ctx, tenant, leaseKey, old, mine)
		if err != nil {
			return nil, false, err
		}
		if ok {
			return mine, true, nil
		}
	}
	return nil, false, errors.New("inputs: reviewer request lease contention")
}

func (r *resolver) releaseReviewerRequestLease(ctx context.Context, t Target, key string, mine []byte) {
	if mine == nil {
		return
	}
	ctx, cancel := context.WithTimeout(context.WithoutCancel(ctx), 5*time.Second)
	defer cancel()
	ok, err := r.store.CompareAndSwap(ctx, store.Tenant(t.Repo.InstallationID), reviewerLeaseKey(key), mine, nil)
	if err != nil || !ok {
		slog.Default().WarnContext(ctx, "inputs: reviewer request lease not released", "installation_id", t.Repo.InstallationID,
			"repository_id", t.Repo.ID, "failed", err != nil)
	}
}

// ReviewerNotificationProblem reads a saved notification failure or pending attempt. It never requests reviewers.
func (r *resolver) ReviewerNotificationProblem(ctx context.Context, t Target) bool {
	if t.PullRequest <= 0 || t.Fork {
		return false
	}
	var err error
	t, err = r.pinProtectedRef(ctx, t)
	if err != nil {
		return true
	}
	state, _, err := r.loadReviewerRequestState(ctx, t, reviewerRequestKey(t))
	return err != nil || (!state.Done && state.Attempts > 0)
}

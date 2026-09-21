package inputs

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"slices"
	"strings"
	"time"

	"github.com/alexei-led/archfit-app/internal/github/api"
	"github.com/alexei-led/archfit-app/internal/store"
	"github.com/alexei-led/archfit-app/internal/wire/policy"
	"github.com/alexei-led/archfit-app/internal/wire/settings"
)

// entitlementTimeout bounds the Store read so that a Store outage never blocks a merge.
// A variable only so that tests need not wait for it.
var entitlementTimeout = 2 * time.Second

const (
	reviewApproved  = "APPROVED"
	reviewCommented = "COMMENTED"
)

type resolver struct {
	repos api.Repos
	pulls api.Pulls
	store store.Store
}

func New(repos api.Repos, pulls api.Pulls, s store.Store) Resolver {
	return &resolver{repos: repos, pulls: pulls, store: s}
}

func (r *resolver) Resolve(ctx context.Context, t Target, now time.Time) (Trusted, error) {
	ent := r.entitlement(ctx, t.Repo.InstallationID)
	out := Trusted{
		Approval:    ApprovalNotRequired,
		Suspended:   suspended(t.Repo.Private, ent, now),
		Entitlement: ent,
	}

	raw, err := r.file(ctx, t, t.BaseRef, settings.Path)
	if err != nil {
		return Trusted{}, err
	}
	if raw == nil {
		logOutcome(t, "settings", "missing")
		return out, nil
	}
	s, err := settings.Parse(raw)
	if err != nil {
		logOutcome(t, "settings", "unparsable") // a broken settings file is a missing one
		return out, nil
	}
	out.Settings, out.SettingsFound = s, true

	trusted := []struct {
		path    string
		digests *[]string
	}{
		{policy.PolicyPath, &out.PolicyDigests},
		{policy.BaselinePath, &out.BaselineDigests},
		{policy.LabelsPath, &out.LabelsDigests},
	}
	var waivers []policy.Waiver
	for _, f := range trusted {
		raw, err := r.file(ctx, t, t.BaseRef, f.path)
		if err != nil {
			return Trusted{}, err
		}
		if raw == nil {
			continue
		}
		*f.digests = append(*f.digests, policy.Digest(raw))
		if f.path == policy.PolicyPath {
			waivers = parseWaivers(t, raw)
		}
	}
	out.EarliestWaiverExpiry = earliestExpiry(waivers, now)
	if t.PullRequest == 0 {
		return out, nil
	}

	changed, truncated, err := r.pulls.ChangedFiles(ctx, t.Repo.RepoRef, t.PullRequest)
	if err != nil {
		return Trusted{}, fmt.Errorf("inputs: changed files: %w", err)
	}
	out.ProtectedPaths = protectedPaths(changed, truncated, s.Workflow)
	if len(out.ProtectedPaths) == 0 {
		return out, nil
	}
	out.Approval = ApprovalMissing
	if t.Fork {
		return out, nil
	}

	reviews, err := r.pulls.Reviews(ctx, t.Repo.RepoRef, t.PullRequest)
	if err != nil {
		return Trusted{}, fmt.Errorf("inputs: reviews: %w", err)
	}
	by, err := approver(reviews, s.Owners, t.HeadSHA, func(team, login string) (bool, error) {
		return r.pulls.TeamMember(ctx, t.Repo.RepoRef, team, login)
	})
	if err != nil {
		return Trusted{}, fmt.Errorf("inputs: team membership: %w", err)
	}
	if by == "" {
		return out, nil
	}
	out.Approval, out.ApprovedBy = ApprovalGranted, by

	// The single exception to "never trust the head tree": hash what a base-version owner approved.
	for _, f := range trusted {
		if !slices.Contains(out.ProtectedPaths, f.path) {
			continue
		}
		raw, err := r.file(ctx, t, t.HeadSHA, f.path)
		if err != nil {
			return Trusted{}, err
		}
		if raw == nil {
			continue
		}
		*f.digests = append(*f.digests, policy.Digest(raw))
		if f.path == policy.PolicyPath {
			waivers = append(waivers, parseWaivers(t, raw)...)
		}
	}
	out.EarliestWaiverExpiry = earliestExpiry(waivers, now)
	return out, nil
}

// file returns nil bytes for an absent file. Every other failure is an error: an unreadable file is never "absent".
func (r *resolver) file(ctx context.Context, t Target, ref, path string) ([]byte, error) {
	raw, _, err := r.repos.File(ctx, t.Repo.RepoRef, ref, path)
	if errors.Is(err, api.ErrNotFound) {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("inputs: read %s: %w", path, err)
	}
	if raw == nil {
		raw = []byte{}
	}
	return raw, nil
}

// entitlement never fails: every Store problem means "not suspended". Errors and payloads are not logged.
func (r *resolver) entitlement(ctx context.Context, installationID int64) Entitlement {
	ctx, cancel := context.WithTimeout(ctx, entitlementTimeout)
	defer cancel()

	var ent Entitlement
	outcome := "found"
	raw, err := r.store.Get(ctx, store.Tenant(installationID), EntitlementKey)
	switch {
	case errors.Is(err, store.ErrNotFound):
		outcome = "missing"
	case err != nil:
		outcome = "unreadable"
	case json.Unmarshal(raw, &ent) != nil:
		ent, outcome = Entitlement{}, "unreadable" // a failed decode may have filled some fields
	}
	slog.Info("inputs: entitlement", "installation_id", installationID, "outcome", outcome)
	return ent
}

func parseWaivers(t Target, rawPolicy []byte) []policy.Waiver {
	waivers, err := policy.Waivers(rawPolicy)
	if err != nil {
		logOutcome(t, "waivers", "unreadable")
		return nil
	}
	return waivers
}

func logOutcome(t Target, what, outcome string) {
	slog.Info("inputs: "+what, "installation_id", t.Repo.InstallationID, "repository_id", t.Repo.ID, "outcome", outcome)
}

func suspended(private bool, e Entitlement, now time.Time) bool {
	return private && !e.SuspendAfter.IsZero() && !now.Before(e.SuspendAfter)
}

// protectedPaths returns the protected paths among changed, in fixed order.
// A truncated list fails closed: any path may have changed.
func protectedPaths(changed []string, truncated bool, workflow string) []string {
	var out []string
	for _, p := range []string{policy.PolicyPath, policy.BaselinePath, policy.LabelsPath, settings.Path, workflow} {
		if truncated || slices.Contains(changed, p) {
			out = append(out, p)
		}
	}
	return out
}

// approver returns the first owner, in review order, whose latest non-comment review approves headSHA; "" if none.
func approver(reviews []api.Review, owners []string, headSHA string, member func(team, login string) (bool, error)) (string, error) {
	latest := map[string]int{}
	for i, rv := range reviews {
		if rv.State != reviewCommented {
			latest[strings.ToLower(rv.Reviewer)] = i
		}
	}
	for i, rv := range reviews {
		if rv.State != reviewApproved || rv.CommitSHA != headSHA || latest[strings.ToLower(rv.Reviewer)] != i {
			continue
		}
		for _, owner := range owners {
			ok, err := strings.EqualFold(owner, rv.Reviewer), error(nil)
			if strings.Contains(owner, "/") {
				ok, err = member(owner, rv.Reviewer)
			}
			if err != nil {
				return "", err
			}
			if ok {
				return rv.Reviewer, nil
			}
		}
	}
	return "", nil
}

// earliestExpiry skips expired waivers so that an old expiry does not hide the next one.
func earliestExpiry(waivers []policy.Waiver, now time.Time) time.Time {
	var earliest time.Time
	for _, w := range waivers {
		if w.Expires.Before(now) {
			continue
		}
		if earliest.IsZero() || w.Expires.Before(earliest) {
			earliest = w.Expires
		}
	}
	return earliest
}

package inputs

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"slices"
	"time"

	"github.com/alexei-led/archfit-app/internal/github/api"
	"github.com/alexei-led/archfit-app/internal/store"
	"github.com/alexei-led/archfit-app/internal/wire/policy"
	"github.com/alexei-led/archfit-app/internal/wire/settings"
)

// entitlementTimeout bounds the Store read so that a Store outage never blocks a merge.
// A variable only so that tests need not wait for it.
var entitlementTimeout = 2 * time.Second

type resolver struct {
	repos  api.Repos
	pulls  api.Pulls
	queues api.MergeQueues
	store  store.Store
}

// New returns the Resolver. queues is read only for a merge-group Target.
func New(repos api.Repos, pulls api.Pulls, queues api.MergeQueues, s store.Store) Resolver {
	return &resolver{repos: repos, pulls: pulls, queues: queues, store: s}
}

func (r *resolver) Resolve(ctx context.Context, t Target, now time.Time) (Trusted, error) {
	if t.MergeGroup {
		return r.resolveGroup(ctx, t, now)
	}
	ent := r.entitlement(ctx, t.Repo.InstallationID)
	out := Trusted{
		Suspended:   suspended(t.Repo.Private, ent, now),
		Entitlement: ent,
	}
	if t.PullRequest != 0 {
		var err error
		t, err = r.pinProtectedRef(ctx, t)
		if err != nil {
			return Trusted{}, err
		}
	}
	out.SettingsSHA = t.BaseSHA
	protectedRef := protectedRef(t)
	raw, err := r.file(ctx, t, protectedRef, settings.Path)
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
		raw, err := r.file(ctx, t, protectedRef, f.path)
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

	// A pull request that changes a protected path is never approved: not by a push, a review or the App. Its inputs
	// stay those of the protected ref, and the verdict asks for action (owner_approval_missing).
	out.ProtectedPaths, err = r.changedProtected(ctx, t, s)
	if err != nil {
		return Trusted{}, err
	}
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
func protectedPaths(changed []string, truncated bool, s settings.Settings) []string {
	var out []string
	for _, p := range ProtectedPaths(s) {
		if truncated || slices.Contains(changed, p) {
			out = append(out, p)
		}
	}
	return out
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

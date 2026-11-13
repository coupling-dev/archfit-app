package inputs

import (
	"context"
	"errors"
	"fmt"
	"slices"
	"strconv"
	"strings"
	"time"

	"github.com/alexei-led/archfit-app/internal/github/api"
	"github.com/alexei-led/archfit-app/internal/wire/policy"
	"github.com/alexei-led/archfit-app/internal/wire/settings"
)

// member is one queued pull request of a merge group, with the protected paths its own pull request changes.
type member struct {
	number int
	head   string
	paths  []string // protected paths its own pull request changes, by the settings of the snapshot
}

// resolveGroup resolves the trusted inputs of a merge group commit t.HeadSHA of t.BaseRef's queue.
//
// The members are the queue entries from the front up to the entry whose commit is t.HeadSHA: GitHub builds each
// entry's commit from the base and every entry ahead of it. Every protected file is read at two immutable commits,
// the default-branch head S, read once, and the group commit X. Nothing approves a protected change: a member that
// changes a protected path in its own pull request blocks the group (Group.Unapproved), and so does a protected file
// that differs between S and X while no member changes it (GroupConflict).
func (r *resolver) resolveGroup(ctx context.Context, t Target, now time.Time) (Trusted, error) {
	ent := r.entitlement(ctx, t.Repo.InstallationID)
	out := Trusted{
		Suspended:   suspended(t.Repo.Private, ent, now),
		Entitlement: ent,
		Group:       &Group{},
	}
	// The queue is read before the snapshot: a member that merges in between is then still a member, and its change
	// is in S, so nothing in S..X is left without a member.
	entries, err := r.queues.MergeQueue(ctx, t.Repo.RepoRef, t.BaseRef)
	if err != nil {
		return Trusted{}, fmt.Errorf("inputs: merge queue: %w", err)
	}
	snapshot, err := r.repos.BranchHead(ctx, t.Repo.RepoRef, t.BaseRef)
	if err != nil {
		return Trusted{}, fmt.Errorf("inputs: default branch head: %w", err)
	}
	t.BaseSHA = snapshot
	out.SettingsSHA = snapshot
	raw, err := r.file(ctx, t, snapshot, settings.Path)
	if err != nil {
		return Trusted{}, err
	}
	if raw == nil {
		logOutcome(t, "settings", "missing")
		return out, nil
	}
	s, err := settings.Parse(raw)
	if err != nil {
		logOutcome(t, "settings", "unparsable")
		return out, nil
	}
	out.Settings, out.SettingsFound = s, true

	paths := ProtectedPaths(s)
	atS, err := r.blobs(ctx, t, snapshot, paths)
	if err != nil {
		return Trusted{}, err
	}
	accept := func(at map[string][]byte) {
		out.PolicyDigests, out.BaselineDigests, out.LabelsDigests = digestList(at[policy.PolicyPath]),
			digestList(at[policy.BaselinePath]), digestList(at[policy.LabelsPath])
		out.EarliestWaiverExpiry = time.Time{}
		if at[policy.PolicyPath] != nil {
			out.EarliestWaiverExpiry = earliestExpiry(parseWaivers(t, at[policy.PolicyPath]), now)
		}
	}
	accept(atS)

	members, problem, detail, err := r.members(ctx, t, entries)
	if err != nil {
		return Trusted{}, err
	}
	if problem != "" {
		out.Group.Problem, out.Group.Detail = problem, detail
		return out, nil
	}
	atX, err := r.blobs(ctx, t, t.HeadSHA, paths)
	if err != nil {
		return Trusted{}, err
	}
	var unapproved []string
	for i := range members {
		m := &members[i]
		mt := Target{Repo: t.Repo, PullRequest: m.number, HeadSHA: m.head, BaseRef: t.BaseRef, BaseSHA: t.BaseSHA}
		if m.paths, err = r.changedProtected(ctx, mt, s); err != nil {
			return Trusted{}, err
		}
		if len(m.paths) > 0 {
			unapproved = append(unapproved, "#"+strconv.Itoa(m.number)+" "+strings.Join(m.paths, ", "))
		}
	}

	var conflicts []string
	for _, p := range paths {
		claimed := slices.ContainsFunc(members, func(m member) bool { return slices.Contains(m.paths, p) })
		differs := !sameBlob(atS[p], atX[p])
		if differs || claimed {
			out.ProtectedPaths = append(out.ProtectedPaths, p)
		}
		if differs && !claimed {
			conflicts = append(conflicts, p+" changed by no queued pull request")
		}
	}
	if len(unapproved) > 0 {
		out.Group.Unapproved = strings.Join(unapproved, "; ")
		return out, nil
	}
	if len(conflicts) > 0 {
		out.Group.Problem, out.Group.Detail = GroupConflict, strings.Join(conflicts, "; ")
	}
	return out, nil
}

// members returns the group's members in queue order, or the problem that stops the group being trusted. Every
// property of a member comes from GitHub: the queue entry, then the pull request read again by number.
func (r *resolver) members(ctx context.Context, t Target, entries []api.QueueEntry) ([]member, GroupProblem, string, error) {
	last := slices.IndexFunc(entries, func(e api.QueueEntry) bool { return e.HeadSHA == t.HeadSHA })
	if last < 0 {
		return nil, GroupUnverified, "the merge queue holds no entry with this commit", nil
	}
	var out []member
	for _, e := range entries[:last+1] {
		n := "#" + strconv.Itoa(e.PullRequest)
		if e.CrossRepo {
			return nil, GroupFork, n, nil
		}
		pull, err := r.pulls.Pull(ctx, t.Repo.RepoRef, e.PullRequest)
		if errors.Is(err, api.ErrNotFound) {
			return nil, GroupUnverified, n + " not found", nil
		}
		if err != nil {
			return nil, "", "", fmt.Errorf("inputs: pull request %d: %w", e.PullRequest, err)
		}
		switch {
		case pull.HeadRepo != t.Repo.FullName:
			return nil, GroupFork, n, nil
		// A member that merged since the queue read is still the queued pull request: its change is in the group, and
		// its approval is still checked. Any other closed pull request is not.
		case (pull.State != "open" && !pull.Merged) || pull.BaseRef != t.BaseRef || pull.HeadSHA != e.PullHeadSHA || e.PullHeadSHA == "":
			return nil, GroupUnverified, n + " is not the queued pull request", nil
		}
		out = append(out, member{number: e.PullRequest, head: pull.HeadSHA})
	}
	return out, "", "", nil
}

// blobs reads each path at the commit sha; an absent path is nil.
func (r *resolver) blobs(ctx context.Context, t Target, sha string, paths []string) (map[string][]byte, error) {
	out := make(map[string][]byte, len(paths))
	for _, p := range paths {
		raw, err := r.file(ctx, t, sha, p)
		if err != nil {
			return nil, err
		}
		out[p] = raw
	}
	return out, nil
}

// sameBlob: both absent, or both present with the same bytes. An empty file is present.
func sameBlob(a, b []byte) bool {
	return (a == nil) == (b == nil) && string(a) == string(b)
}

func digestList(raw []byte) []string {
	if raw == nil {
		return nil
	}
	return []string{policy.Digest(raw)}
}

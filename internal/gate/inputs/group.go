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

// member is one queued pull request of a merge group, with what its own pull request changes and who approved it.
type member struct {
	number   int
	head     string
	paths    []string          // protected paths its own pull request changes, by the settings of the snapshot
	digests  map[string]string // each of paths at its head; set only when approver is
	approver string
}

// resolveGroup resolves the trusted inputs of a merge group commit t.HeadSHA of t.BaseRef's queue.
//
// The members are the queue entries from the front up to the entry whose commit is t.HeadSHA: GitHub builds each
// entry's commit from the base and every entry ahead of it. Every protected file is read at two immutable commits,
// the default-branch head S, read once, and the group commit X. A file that differs between them is accepted only
// when exactly one member changes it in its own pull request, that member holds a current owner approval of its own
// head, and its head holds exactly X's blob. Every member that changes a protected path needs its own approval, even
// when the group's file equals S's: an unauthorized change in any member is refused. The group commit itself is never
// an approval subject.
func (r *resolver) resolveGroup(ctx context.Context, t Target, now time.Time) (Trusted, error) {
	ent := r.entitlement(ctx, t.Repo.InstallationID)
	out := Trusted{
		Approval:    ApprovalNotRequired,
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
	var unapproved, approvedBy []string
	for i := range members {
		m := &members[i]
		if err := r.approveMember(ctx, t, s, m); err != nil {
			return Trusted{}, err
		}
		switch {
		case len(m.paths) == 0:
		case m.approver == "":
			unapproved = append(unapproved, "#"+strconv.Itoa(m.number)+" "+strings.Join(m.paths, ", "))
		default:
			approvedBy = append(approvedBy, m.approver+" (#"+strconv.Itoa(m.number)+")")
		}
	}

	var changed, conflicts []string
	for _, p := range paths {
		claimed := slices.ContainsFunc(members, func(m member) bool { return slices.Contains(m.paths, p) })
		differs := !sameBlob(atS[p], atX[p])
		if differs || claimed {
			out.ProtectedPaths = append(out.ProtectedPaths, p)
		}
		if differs {
			changed = append(changed, p)
		}
	}
	if len(unapproved) > 0 {
		out.Approval, out.Group.Unapproved = ApprovalMissing, strings.Join(unapproved, "; ")
		return out, nil
	}
	for _, p := range changed {
		if why := attribute(members, p, atX[p]); why != "" {
			conflicts = append(conflicts, p+" "+why)
		}
	}
	if len(conflicts) > 0 {
		out.Approval, out.Group.Problem, out.Group.Detail = ApprovalMissing, GroupConflict, strings.Join(conflicts, "; ")
		return out, nil
	}
	if len(approvedBy) > 0 {
		out.Approval, out.ApprovedBy = ApprovalGranted, strings.Join(approvedBy, ", ")
	}
	accept(atX) // equal to S's blob for every path not in changed
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

// approveMember fills in the protected paths m's own pull request changes and, when there are any, the owner who
// approved m's head under the existing pull request rules (a GitHub review of the head, else the App record) and the
// digests of those paths at its head.
func (r *resolver) approveMember(ctx context.Context, t Target, s settings.Settings, m *member) error {
	mt := Target{Repo: t.Repo, PullRequest: m.number, HeadSHA: m.head, BaseRef: t.BaseRef, BaseSHA: t.BaseSHA}
	paths, err := r.changedProtected(ctx, mt, s)
	if err != nil || len(paths) == 0 {
		return err
	}
	m.paths = paths
	reviews, err := r.pulls.Reviews(ctx, t.Repo.RepoRef, m.number)
	if err != nil {
		return fmt.Errorf("inputs: reviews: %w", err)
	}
	by, _, _, err := r.approvalDecision(ctx, mt, s, paths, reviews)
	if err != nil {
		return err
	}
	if by == "" {
		return nil
	}
	if m.digests, err = r.headDigests(ctx, mt, paths); err != nil {
		return err
	}
	m.approver = by
	return nil
}

// attribute returns "" when exactly one member changes p and its approved head holds exactly blob, else why not.
func attribute(members []member, p string, blob []byte) string {
	var claimants []member
	for _, m := range members {
		if slices.Contains(m.paths, p) {
			claimants = append(claimants, m)
		}
	}
	want := ""
	if blob != nil {
		want = policy.Digest(blob)
	}
	switch {
	case len(claimants) == 0:
		return "changed by no queued pull request"
	case len(claimants) > 1:
		return "changed by " + numbers(claimants)
	case claimants[0].digests[p] != want:
		return "is not the file an owner approved on " + numbers(claimants)
	}
	return ""
}

func numbers(ms []member) string {
	out := make([]string, len(ms))
	for i, m := range ms {
		out[i] = "#" + strconv.Itoa(m.number)
	}
	return strings.Join(out, " and ")
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

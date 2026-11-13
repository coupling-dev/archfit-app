// Package records holds the App's Archfit 4 state: the v4/ keys under an installation tenant, their JSON values
// with a schema, the indexes that stand in for the Store's missing list, and the one-use consume of a proposal.
// Archfit 3 keys are neither read nor written here.
package records

import (
	"crypto/rand"
	"strconv"
	"strings"
)

// Prefix starts every key of this package. Archfit 3 keys never start with it, so the previous App revision and
// this one never share a key (M2 D7).
const Prefix = "v4/"

// ReposKey is the index of the repositories of an installation, for the sweep.
const ReposKey = "v4/repos"

// repo is the canonical form of a repository full name in a key: lower case, as GitHub compares names.
func repo(fullName string) string { return strings.ToLower(fullName) }

// RepoKey holds the repository record (Repo).
func RepoKey(fullName string) string { return "v4/repo/" + repo(fullName) }

// RequestKey holds one request (Request). The issue number is the request id.
func RequestKey(fullName string, issue int) string {
	return "v4/request/" + repo(fullName) + "/" + strconv.Itoa(issue)
}

// RequestIndexKey holds the requests of a repository that cover their key, by request key: the open ones, and the
// answered ones whose proposal is not consumed yet.
func RequestIndexKey(fullName string) string { return "v4/request-index/" + repo(fullName) }

// ApprovalKey holds one approval (Approval).
func ApprovalKey(fullName, id string) string { return "v4/approval/" + repo(fullName) + "/" + id }

// ProposalKey holds one proposal (Proposal).
func ProposalKey(fullName, id string) string { return "v4/proposal/" + repo(fullName) + "/" + id }

// ProposalIndexKey holds the open proposals of a repository by pull request.
func ProposalIndexKey(fullName string) string { return "v4/proposal-index/" + repo(fullName) }

// ProposalLeaseKey is held while an answer writes the repository's proposal: its branch, its pull request and its
// records change together, and two answers must not interleave (gate/proposals).
func ProposalLeaseKey(fullName string) string { return "v4/proposal-lease/" + repo(fullName) }

// Push is one push of the default branch: the commit it replaced and the commit it pushed. A push is judged once, under
// both: a push that moves the branch back to a commit judged before is another push, and is judged again.
type Push struct{ Before, After string }

// noCommit stands for the commit before a push that created the branch, as GitHub writes it.
const noCommit = "0000000000000000000000000000000000000000"

// PolicyPushKey holds how the App judged a default-branch push that changed the policy (PolicyPush).
func PolicyPushKey(fullName string, p Push) string {
	before := p.Before
	if before == "" {
		before = noCommit
	}
	return "v4/policy-push/" + repo(fullName) + "/" + before + ".." + p.After
}

// RecoveredPushKey holds the immutable PolicyPush evidence for a merge recovered by the sweep.
func RecoveredPushKey(fullName, commit string) string {
	return "v4/recovered-push/" + repo(fullName) + "/" + commit
}

// PullKey holds the pull record of a pull request (Pull).
func PullKey(fullName string, pr int) string {
	return "v4/pull/" + repo(fullName) + "/" + strconv.Itoa(pr)
}

// PullIndexKey holds the open pull requests of a repository.
func PullIndexKey(fullName string) string { return "v4/pull-index/" + repo(fullName) }

// PullEntry is the entry key of a pull request in the proposal and pull indexes: its decimal number.
func PullEntry(pr int) string { return strconv.Itoa(pr) }

// NewID returns a fresh approval or proposal id: 128 random bits in lower-case base32, never containing "/".
func NewID() string { return strings.ToLower(rand.Text()) }

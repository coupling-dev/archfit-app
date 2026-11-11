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

// RequestIndexKey holds the open requests of a repository by request key.
func RequestIndexKey(fullName string) string { return "v4/request-index/" + repo(fullName) }

// ApprovalKey holds one approval (Approval).
func ApprovalKey(fullName, id string) string { return "v4/approval/" + repo(fullName) + "/" + id }

// ProposalKey holds one proposal (Proposal).
func ProposalKey(fullName, id string) string { return "v4/proposal/" + repo(fullName) + "/" + id }

// ProposalIndexKey holds the open proposals of a repository by pull request.
func ProposalIndexKey(fullName string) string { return "v4/proposal-index/" + repo(fullName) }

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

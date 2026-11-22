package approvals

import (
	"context"
	"fmt"
	"regexp"
	"slices"
	"strconv"
	"strings"

	"github.com/coupling-dev/archfit-app/internal/gate/records"
)

// requestMarker is the "# request #N" comment an answer writes on its policy line.
var requestMarker = regexp.MustCompile(`#\s*request\s+#(\d+)`)

// policyChange describes push p, which brought the policy after, for the history shown before the first approval: the
// digests before and after, the pull request GitHub says merged its head commit and who merged it (merged_by, never
// the commit author), or a direct push, and the requests whose markers the change added. Deliberate simplification:
// one entry per push, for its head commit; the commits inside a push of several are not listed one by one. Upgrade
// when direct pushes of several policy commits need telling apart.
func (s *Service) policyChange(ctx context.Context, p Push, after []byte) (records.PolicyChange, error) {
	var before []byte
	if p.Before != "" {
		var err error
		if before, err = s.policy(ctx, p.Repo.RepoRef, p.Before); err != nil {
			return records.PolicyChange{}, err
		}
	}
	c := records.PolicyChange{OldDigest: historyDigest(before), NewDigest: historyDigest(after), Commit: p.After,
		Requests: addedRequests(before, after), Direct: true}
	pulls, err := s.gh.CommitPulls(ctx, p.Repo.RepoRef, p.After)
	if err != nil {
		return records.PolicyChange{}, fmt.Errorf("approvals: list the pull requests of %s: %w", p.After, err)
	}
	for _, pl := range pulls {
		if !pl.Merged || pl.MergeCommitSHA != p.After {
			continue
		}
		// The list leaves merged_by out; the pull request itself reports it.
		full, err := s.gh.Pull(ctx, p.Repo.RepoRef, pl.Number)
		if err != nil {
			return records.PolicyChange{}, fmt.Errorf("approvals: read pull request %d: %w", pl.Number, err)
		}
		c.PullRequest, c.Merger, c.Direct = pl.Number, full.MergedBy, false
		break
	}
	return c, nil
}

func historyDigest(raw []byte) string {
	if raw == nil {
		return records.NoPolicy
	}
	return digestOf(raw)
}

// addedRequests lists, in order and once each, the request numbers of the marker lines after has and before had not.
func addedRequests(before, after []byte) []int {
	had := strings.Split(string(before), "\n")
	var out []int
	for _, line := range strings.Split(string(after), "\n") {
		if slices.Contains(had, line) {
			continue
		}
		for _, m := range requestMarker.FindAllStringSubmatch(line, -1) {
			if n, err := strconv.Atoi(m[1]); err == nil && n > 0 && !slices.Contains(out, n) {
				out = append(out, n)
			}
		}
	}
	return out
}

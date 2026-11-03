package api

import (
	"context"
	"errors"
	"net/http"
	"sort"
	"strings"
)

// mergeQueueQuery reads one page of a branch's merge queue. GitHub answers mergeQueue null for a branch without one.
const mergeQueueQuery = `query($owner: String!, $name: String!, $branch: String!, $after: String) {
  repository(owner: $owner, name: $name) {
    mergeQueue(branch: $branch) {
      entries(first: 100, after: $after) {
        pageInfo { hasNextPage endCursor }
        nodes {
          position
          headCommit { oid }
          baseCommit { oid }
          pullRequest { number headRefOid isCrossRepository }
        }
      }
    }
  }
}`

// maxQueuePages bounds one read at 1,000 entries. A longer queue is an error, never a truncated membership.
const maxQueuePages = 10

// QueueEntry is one entry of a merge queue as GitHub's GraphQL API states it.
type QueueEntry struct {
	Position    int    // 1 is the front of the queue
	HeadSHA     string // the entry's merge group commit: the base plus every entry ahead of it plus this pull request
	BaseSHA     string
	PullRequest int
	PullHeadSHA string // the pull request head the entry was built from
	CrossRepo   bool   // the pull request comes from a fork
}

// ErrQueueTooLong is returned when a merge queue holds more entries than one read takes.
var ErrQueueTooLong = errors.New("github: merge queue too long to read")

// ErrQueueForbidden is GraphQL's FORBIDDEN on the merge queue: the installation lacks the Merge queues permission.
var ErrQueueForbidden = errors.New("github: merge queue not readable: Merge queues permission missing")

type mergeQueueAnswer struct {
	Data struct {
		Repository *struct {
			MergeQueue *struct {
				Entries struct {
					PageInfo struct {
						HasNextPage bool   `json:"hasNextPage"`
						EndCursor   string `json:"endCursor"`
					} `json:"pageInfo"`
					Nodes []struct {
						Position   int `json:"position"`
						HeadCommit *struct {
							OID string `json:"oid"`
						} `json:"headCommit"`
						BaseCommit *struct {
							OID string `json:"oid"`
						} `json:"baseCommit"`
						PullRequest *struct {
							Number            int    `json:"number"`
							HeadRefOID        string `json:"headRefOid"`
							IsCrossRepository bool   `json:"isCrossRepository"`
						} `json:"pullRequest"`
					} `json:"nodes"`
				} `json:"entries"`
			} `json:"mergeQueue"`
		} `json:"repository"`
	} `json:"data"`
	Errors []struct {
		Type string `json:"type"`
	} `json:"errors"`
}

// MergeQueue returns the entries of branch's merge queue in position order; none for a branch without a queue. Any
// GraphQL error is an error (a missing Merge queues permission is FORBIDDEN): membership is never guessed.
func (c *Client) MergeQueue(ctx context.Context, r RepoRef, branch string) ([]QueueEntry, error) {
	cr, _, err := r.target()
	if err != nil {
		return nil, err
	}
	owner, name, _ := strings.Cut(r.FullName, "/")
	var out []QueueEntry
	var after *string
	for range maxQueuePages {
		k := call{cred: cr, method: http.MethodPost, route: graphqlRoute, url: apiURL(graphqlRoute, nil),
			body: map[string]any{"query": mergeQueueQuery, "variables": map[string]any{
				"owner": owner, "name": name, "branch": branch, "after": after,
			}}}
		var answer mergeQueueAnswer
		if err := c.send(ctx, k, &answer); err != nil {
			return nil, err
		}
		for _, e := range answer.Errors {
			switch e.Type {
			case "RATE_LIMITED":
				return nil, k.errorf("%w", ErrRateLimited)
			case "FORBIDDEN":
				return nil, k.errorf("%w", ErrQueueForbidden)
			}
		}
		if len(answer.Errors) > 0 {
			// GraphQL messages stay out of the error: they can quote content.
			return nil, k.errorf("merge queue query failed")
		}
		repo := answer.Data.Repository
		if repo == nil {
			return nil, k.errorf("%w", ErrNotFound)
		}
		if repo.MergeQueue == nil {
			return nil, nil
		}
		page := repo.MergeQueue.Entries
		for _, n := range page.Nodes {
			// GitHub's schema makes both commits nullable: an entry beyond the build concurrency has no group commit
			// yet. It stays an entry, with no HeadSHA, so it binds no upload. An entry without a pull request cannot be
			// attributed to anyone, so the read fails rather than drop a member.
			if n.PullRequest == nil {
				return nil, k.errorf("merge queue entry without a pull request")
			}
			var head, base string
			if n.HeadCommit != nil {
				head = n.HeadCommit.OID
			}
			if n.BaseCommit != nil {
				base = n.BaseCommit.OID
			}
			out = append(out, QueueEntry{
				Position: n.Position, HeadSHA: head, BaseSHA: base,
				PullRequest: n.PullRequest.Number, PullHeadSHA: n.PullRequest.HeadRefOID, CrossRepo: n.PullRequest.IsCrossRepository,
			})
		}
		if !page.PageInfo.HasNextPage {
			sort.SliceStable(out, func(i, j int) bool { return out[i].Position < out[j].Position })
			return out, nil
		}
		cursor := page.PageInfo.EndCursor
		after = &cursor
	}
	return nil, call{method: http.MethodPost, route: graphqlRoute}.errorf("%w", ErrQueueTooLong)
}

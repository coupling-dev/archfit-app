package webhook

import (
	"bytes"
	"encoding/json"
	"errors"
	"slices"
	"strings"
)

const (
	typePullRequest       = "pull_request"
	typePullRequestReview = "pull_request_review"
	typeIssueComment      = "issue_comment"
	typePush              = "push"
	typeInstallation      = "installation"
	typeInstallationRepos = "installation_repositories"

	branchRefPrefix = "refs/heads/"
	shaLen          = 40

	// GitHub truncates the commits of a push payload at this count.
	commitsTruncatedAt = 2048
	// Our cap: above it a full rescan is cheaper to reason about than a huge path list.
	maxChangedPaths = 3000
)

var (
	errNoDeliveryID   = errors.New("webhook: empty delivery ID")
	errMalformedBody  = errors.New("webhook: malformed payload")
	errNoInstallation = errors.New("webhook: payload has no installation ID")
	errNoRepository   = errors.New("webhook: payload has no repository name")
	errNoPullRequest  = errors.New("webhook: payload has no pull request number")
	errBadHeadSHA     = errors.New("webhook: payload has no valid head SHA")
	errNoComment      = errors.New("webhook: payload has no comment ID or author")
)

// actionKinds is the Mapping table for every known event type except push, which has no action.
var actionKinds = map[string]map[string]EventKind{
	typePullRequest: {
		"opened":           PullOpened,
		"reopened":         PullOpened,
		"ready_for_review": PullOpened,
		"synchronize":      PullSynchronized,
		"closed":           PullClosed,
	},
	typePullRequestReview: {
		"submitted": ReviewChanged,
		"edited":    ReviewChanged,
		"dismissed": ReviewChanged,
	},
	typeIssueComment: {
		"created": PullCommented, // only on a pull request: payload.kind checks issue.pull_request
	},
	typeInstallation: {
		"created": InstallationAdded,
		"deleted": InstallationRemoved,
	},
	typeInstallationRepos: {
		"added":   InstallationAdded,
		"removed": ReposRemoved,
	},
}

// payload holds the only fields read from any delivery. Unknown fields are ignored on purpose.
type payload struct {
	Action       string `json:"action"`
	Installation struct {
		ID      int64 `json:"id"`
		Account struct {
			Login string `json:"login"`
		} `json:"account"`
	} `json:"installation"`
	Repository struct {
		FullName      string `json:"full_name"`
		Private       bool   `json:"private"`
		DefaultBranch string `json:"default_branch"`
	} `json:"repository"`
	PullRequest struct {
		Number int `json:"number"`
		Head   struct {
			SHA  string `json:"sha"`
			Repo *struct {
				FullName string `json:"full_name"`
			} `json:"repo"`
		} `json:"head"`
		Base struct {
			Ref string `json:"ref"`
		} `json:"base"`
	} `json:"pull_request"`
	Issue struct {
		Number      int       `json:"number"`
		PullRequest *struct{} `json:"pull_request"` // present only when the issue is a pull request
	} `json:"issue"`
	Comment struct {
		ID   int64  `json:"id"`
		Body string `json:"body"`
		User struct {
			Login string `json:"login"`
			Type  string `json:"type"`
		} `json:"user"`
	} `json:"comment"`
	Sender struct {
		Login string `json:"login"`
	} `json:"sender"`
	PusherType string `json:"pusher_type"`
	Ref        string `json:"ref"`
	After      string `json:"after"`
	Deleted    bool   `json:"deleted"`
	Forced     bool   `json:"forced"`
	Commits    []struct {
		Added    []string `json:"added"`
		Modified []string `json:"modified"`
		Removed  []string `json:"removed"`
	} `json:"commits"`
}

// Parse normalizes a verified delivery. Unknown event types yield Kind == Ignored, not an error.
func Parse(eventType, deliveryID string, body []byte) (Event, error) {
	if deliveryID == "" {
		return Event{}, errNoDeliveryID
	}
	ev := Event{Kind: Ignored, DeliveryID: deliveryID}
	if _, known := actionKinds[eventType]; !known && eventType != typePush {
		return ev, nil
	}

	// json.Unmarshal accepts "null" into a struct; only an object is a payload.
	if !bytes.HasPrefix(bytes.TrimSpace(body), []byte("{")) {
		return Event{}, errMalformedBody
	}
	var p payload
	if err := json.Unmarshal(body, &p); err != nil {
		// The json error quotes payload bytes; it is dropped on purpose.
		return Event{}, errMalformedBody
	}

	// The default branch is named by the repository object: without one a push cannot be classified.
	if eventType == typePush && !strings.Contains(p.Repository.FullName, "/") {
		return Event{}, errNoRepository
	}
	kind := p.kind(eventType)
	if kind == Ignored {
		ev.InstallationID = max(p.Installation.ID, 0)
		return ev, nil
	}
	if p.Installation.ID <= 0 {
		return Event{}, errNoInstallation
	}
	ev.Kind = kind
	ev.InstallationID = p.Installation.ID
	if kind == InstallationAdded || kind == InstallationRemoved || kind == ReposRemoved {
		ev.Account = p.Installation.Account.Login
		return ev, nil
	}

	if !strings.Contains(p.Repository.FullName, "/") {
		return Event{}, errNoRepository
	}
	ev.Repo = p.Repository.FullName
	ev.RepoPrivate = p.Repository.Private
	ev.DefaultBranch = p.Repository.DefaultBranch

	if kind == PullCommented {
		if p.Issue.Number <= 0 {
			return Event{}, errNoPullRequest
		}
		if p.Comment.ID <= 0 || p.Comment.User.Login == "" {
			return Event{}, errNoComment
		}
		ev.PullRequest = p.Issue.Number
		ev.CommentID, ev.Author, ev.AuthorType, ev.Body = p.Comment.ID, p.Comment.User.Login, p.Comment.User.Type, p.Comment.Body
		return ev, nil
	}
	if kind == DefaultBranchPush || kind == BranchPush {
		ev.HeadSHA = p.After
		ev.HeadRepo = ev.Repo
		ev.Branch = strings.TrimPrefix(p.Ref, branchRefPrefix)
		ev.Pusher = p.Sender.Login
		ev.PusherType = p.PusherType
		if kind == DefaultBranchPush {
			ev.BaseRef = p.Repository.DefaultBranch
			ev.ChangedPaths = p.changedPaths()
		}
	} else {
		if p.PullRequest.Number <= 0 {
			return Event{}, errNoPullRequest
		}
		ev.PullRequest = p.PullRequest.Number
		ev.HeadSHA = p.PullRequest.Head.SHA
		if p.PullRequest.Head.Repo != nil {
			ev.HeadRepo = p.PullRequest.Head.Repo.FullName
		}
		ev.BaseRef = p.PullRequest.Base.Ref
	}
	if !isSHA(ev.HeadSHA) {
		return Event{}, errBadHeadSHA
	}
	return ev, nil
}

func (p *payload) kind(eventType string) EventKind {
	if eventType == typePush {
		branch, isBranch := strings.CutPrefix(p.Ref, branchRefPrefix)
		switch {
		case p.Deleted || !isBranch || p.Repository.DefaultBranch == "":
			return Ignored
		case branch == p.Repository.DefaultBranch:
			return DefaultBranchPush
		default:
			return BranchPush
		}
	}
	if kind, ok := actionKinds[eventType][p.Action]; ok {
		if kind == PullCommented && p.Issue.PullRequest == nil {
			return Ignored // a comment on a plain issue
		}
		return kind
	}
	return Ignored
}

// changedPaths returns nil for "unknown, treat as everything changed".
func (p *payload) changedPaths() []string {
	if p.Forced || len(p.Commits) == 0 || len(p.Commits) >= commitsTruncatedAt {
		return nil
	}
	seen := map[string]struct{}{}
	for _, c := range p.Commits {
		for _, files := range [][]string{c.Added, c.Modified, c.Removed} {
			for _, f := range files {
				seen[f] = struct{}{}
			}
		}
	}
	if len(seen) > maxChangedPaths {
		return nil
	}
	paths := make([]string, 0, len(seen))
	for f := range seen {
		paths = append(paths, f)
	}
	slices.Sort(paths)
	return paths
}

func isSHA(s string) bool {
	if len(s) != shaLen {
		return false
	}
	for i := range len(s) {
		if c := s[i]; (c < '0' || c > '9') && (c < 'a' || c > 'f') {
			return false
		}
	}
	return true
}

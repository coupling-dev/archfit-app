package api

import (
	"context"
	"errors"
	"net/http"
	"net/url"
	"strconv"
)

// Rulesets reads effective branch rules and the repository's rulesets, including inherited rulesets.
type Rulesets interface {
	BranchPolicy(context.Context, RepoRef, string) ([]RepositoryRule, error)
	ListRulesets(context.Context, RepoRef) ([]Ruleset, error)
	GetRuleset(context.Context, RepoRef, int64) (Ruleset, error)
	AppIntegrationID(context.Context) (int64, error)
}

// RequiredCheck binds a check context to its producer; nil IntegrationID accepts any producer.
type RequiredCheck struct {
	Context       string `json:"context"`
	IntegrationID *int64 `json:"integration_id"`
}

// RepositoryRule is a GitHub rule. Unknown types are preserved and ignored by consumers.
type RepositoryRule struct {
	Type       string `json:"type"`
	RulesetID  int64  `json:"ruleset_id"`
	Parameters struct {
		Approvals int             `json:"required_approving_review_count"`
		Checks    []RequiredCheck `json:"required_status_checks"`
		Paths     []string        `json:"restricted_file_paths"`
	} `json:"parameters"`
}

// BypassActor is a ruleset bypass, not proof that the current caller can bypass.
type BypassActor struct {
	ID   int64  `json:"actor_id"`
	Type string `json:"actor_type"`
	Mode string `json:"bypass_mode"`
}

// Ruleset is GitHub's read-back. A nil BypassActors means unreadable, not an empty list.
type Ruleset struct {
	ID           int64            `json:"id"`
	Name         string           `json:"name"`
	Target       string           `json:"target"`
	Enforcement  string           `json:"enforcement"`
	CanBypass    string           `json:"current_user_can_bypass"`
	BypassActors *[]BypassActor   `json:"bypass_actors"`
	Rules        []RepositoryRule `json:"rules"`
}

func (c *Client) AppIntegrationID(ctx context.Context) (int64, error) {
	a, err := c.appIdentity(ctx)
	return a.id, err
}

func (c *Client) BranchPolicy(ctx context.Context, r RepoRef, branch string) ([]RepositoryRule, error) {
	cr, base, err := r.target()
	if err != nil {
		return nil, err
	}
	escaped, err := escapeSegments(branch)
	if err != nil {
		return nil, err
	}
	return listAll[RepositoryRule](ctx, c, call{cred: cr, method: http.MethodGet, route: "/repos/{r}/rules/branches/{branch}", url: pagedURL(base+"/rules/branches/"+escaped, nil)}, "")
}

func (c *Client) ListRulesets(ctx context.Context, r RepoRef) ([]Ruleset, error) {
	cr, base, err := r.target()
	if err != nil {
		return nil, err
	}
	return listAll[Ruleset](ctx, c, call{cred: cr, method: http.MethodGet, route: "/repos/{r}/rulesets", url: pagedURL(base+"/rulesets", url.Values{"includes_parents": {"true"}})}, "")
}

func (c *Client) GetRuleset(ctx context.Context, r RepoRef, id int64) (Ruleset, error) {
	cr, base, err := r.target()
	if err != nil {
		return Ruleset{}, err
	}
	if id <= 0 {
		return Ruleset{}, errors.New("github: invalid ruleset ID")
	}
	var out Ruleset
	err = c.send(ctx, call{cred: cr, method: http.MethodGet, route: "/repos/{r}/rulesets/{id}", url: apiURL(base+"/rulesets/"+strconv.FormatInt(id, 10), url.Values{"includes_parents": {"true"}})}, &out)
	return out, err
}

// PublicTagCommit resolves a public repository tag without sending installation credentials.
// Branch names are never resolved. Annotated tags are peeled with a bounded number of reads.
func (c *Client) PublicTagCommit(ctx context.Context, fullName, tag string) (string, error) {
	base, err := repoPath(fullName)
	if err != nil {
		return "", err
	}
	escaped, err := escapeSegments(tag)
	if err != nil {
		return "", err
	}
	k := call{cred: cred{kind: credNone}, method: http.MethodGet, route: "/repos/{r}/git/ref/tags/{tag}", url: apiURL(base+"/git/ref/tags/"+escaped, nil)}
	for range 5 {
		var out struct {
			Object struct {
				Type string `json:"type"`
				SHA  string `json:"sha"`
			} `json:"object"`
		}
		if err := c.send(ctx, k, &out); err != nil {
			return "", err
		}
		if !commitSHARE.MatchString(out.Object.SHA) {
			return "", k.errorf("invalid response")
		}
		switch out.Object.Type {
		case "commit":
			return out.Object.SHA, nil
		case "tag":
			k.route = "/repos/{r}/git/tags/{sha}"
			k.url = apiURL(base+"/git/tags/"+out.Object.SHA, nil)
		default:
			return "", k.errorf("tag does not name a commit")
		}
	}
	return "", errors.New("github: too many annotated tags")
}

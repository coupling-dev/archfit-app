package api

import (
	"context"
	"net/http"
)

const ruleRequiredChecks = "required_status_checks"

// RequiredChecks reads the status checks that active rulesets require on branch, and whether GitHub marks the branch
// protected. Both reads need only the Metadata permission.
func (c *Client) RequiredChecks(ctx context.Context, r RepoRef, branch string) (BranchRules, error) {
	cr, base, err := r.target()
	if err != nil {
		return BranchRules{}, err
	}
	escaped, err := escapeSegments(branch)
	if err != nil {
		return BranchRules{}, err
	}
	type ruleJSON struct {
		Type       string `json:"type"`
		Parameters struct {
			Checks []struct {
				Context string `json:"context"`
			} `json:"required_status_checks"`
		} `json:"parameters"`
	}
	k := call{cred: cr, method: http.MethodGet, route: "/repos/{r}/rules/branches/{branch}",
		url: pagedURL(base+"/rules/branches/"+escaped, nil)}
	rules, err := listAll[ruleJSON](ctx, c, k, "")
	if err != nil {
		return BranchRules{}, err
	}
	out := BranchRules{}
	for _, rule := range rules {
		if rule.Type != ruleRequiredChecks {
			continue
		}
		for _, check := range rule.Parameters.Checks {
			out.Contexts = append(out.Contexts, check.Context)
		}
	}
	k = call{cred: cr, method: http.MethodGet, route: "/repos/{r}/branches/{branch}",
		url: apiURL(base+"/branches/"+escaped, nil)}
	var branchJSON struct {
		Protected bool `json:"protected"`
	}
	if err := c.send(ctx, k, &branchJSON); err != nil {
		return BranchRules{}, err
	}
	out.Protected = branchJSON.Protected
	return out, nil
}

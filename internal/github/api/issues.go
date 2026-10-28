package api

import (
	"context"
	"net/http"
	"net/url"
	"strconv"
)

type issueJSON struct {
	Number int    `json:"number"`
	Body   string `json:"body"` // null for an issue without a body
	User   *struct {
		Login string `json:"login"`
	} `json:"user"`
	PullRequest *struct{} `json:"pull_request"` // set when the "issue" is a pull request
}

// AppIssues lists the open issues and keeps those the bot login opened. The list is not narrowed with GitHub's
// creator parameter: whether it matches an App's "slug[bot]" login is not documented, and a filter that silently
// matched nothing would make the caller open a second issue.
func (c *Client) AppIssues(ctx context.Context, r RepoRef) ([]Issue, error) {
	cr, base, err := r.target()
	if err != nil {
		return nil, err
	}
	app, err := c.appIdentity(ctx)
	if err != nil {
		return nil, err
	}
	k := call{cred: cr, method: http.MethodGet, route: "/repos/{r}/issues",
		url: pagedURL(base+"/issues", url.Values{"state": {"open"}})}
	all, err := listAll[issueJSON](ctx, c, k, "")
	if err != nil {
		return nil, err
	}
	out := []Issue{}
	for _, j := range all {
		if j.PullRequest == nil && j.User != nil && j.User.Login == app.botLogin {
			out = append(out, Issue{Number: j.Number, Body: j.Body})
		}
	}
	return out, nil
}

func (c *Client) CreateIssue(ctx context.Context, r RepoRef, i NewIssue) (Issue, error) {
	if err := checkBody(i.Body); err != nil {
		return Issue{}, err
	}
	cr, base, err := r.target()
	if err != nil {
		return Issue{}, err
	}
	body := map[string]any{"title": i.Title, "body": i.Body}
	if i.Assignee != "" {
		body["assignees"] = []string{i.Assignee}
	}
	var j issueJSON
	k := call{cred: cr, method: http.MethodPost, route: "/repos/{r}/issues", url: apiURL(base+"/issues", nil), body: body}
	if err := c.send(ctx, k, &j); err != nil {
		return Issue{}, err
	}
	if j.Number <= 0 {
		return Issue{}, k.errorf("invalid response")
	}
	return Issue{Number: j.Number, Body: i.Body}, nil
}

func (c *Client) UpdateIssueBody(ctx context.Context, r RepoRef, number int, body string) error {
	if err := checkBody(body); err != nil {
		return err
	}
	cr, base, err := r.target()
	if err != nil {
		return err
	}
	k := call{cred: cr, method: http.MethodPatch, route: "/repos/{r}/issues/{n}",
		url: apiURL(base+"/issues/"+strconv.Itoa(number), nil), body: map[string]string{"body": body}}
	return c.send(ctx, k, nil)
}

// CloseIssue reads the issue first: the issues endpoint closes a pull request as well, and api never closes one.
func (c *Client) CloseIssue(ctx context.Context, r RepoRef, number int) error {
	cr, base, err := r.target()
	if err != nil {
		return err
	}
	path := base + "/issues/" + strconv.Itoa(number)
	var j issueJSON
	k := call{cred: cr, method: http.MethodGet, route: "/repos/{r}/issues/{n}", url: apiURL(path, nil)}
	if err := c.send(ctx, k, &j); err != nil {
		return err
	}
	if j.PullRequest != nil {
		return k.errorf("a pull request, not an issue")
	}
	k = call{cred: cr, method: http.MethodPatch, route: "/repos/{r}/issues/{n}",
		url: apiURL(path, nil), body: map[string]string{"state": "closed"}}
	return c.send(ctx, k, nil)
}

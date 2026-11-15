package api

import (
	"context"
	"fmt"
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
		if i.Assignee != "" && isStatus(err, http.StatusUnprocessableEntity) {
			return Issue{}, fmt.Errorf("%w: %w", ErrUnprocessable, err)
		}
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

// SetAssigneesAndLabels reads the issue first, as CloseIssue does: the issues endpoint edits a pull request as well.
func (c *Client) SetAssigneesAndLabels(ctx context.Context, r RepoRef, number int, assignees, labels []string) (gotAssignees, gotLabels []string, err error) {
	if number <= 0 {
		return nil, nil, errPullNumber
	}
	cr, base, err := r.target()
	if err != nil {
		return nil, nil, err
	}
	path := base + "/issues/" + strconv.Itoa(number)
	var j issueJSON
	k := call{cred: cr, method: http.MethodGet, route: "/repos/{r}/issues/{n}", url: apiURL(path, nil)}
	if err := c.send(ctx, k, &j); err != nil {
		return nil, nil, err
	}
	if j.PullRequest != nil {
		return nil, nil, k.errorf("a pull request, not an issue")
	}
	// nil is sent as [], which clears the set: a missing key would keep it.
	if assignees == nil {
		assignees = []string{}
	}
	if labels == nil {
		labels = []string{}
	}
	var got struct {
		Assignees []struct {
			Login string `json:"login"`
		} `json:"assignees"`
		Labels []struct {
			Name string `json:"name"`
		} `json:"labels"`
	}
	k = call{cred: cr, method: http.MethodPatch, route: "/repos/{r}/issues/{n}", url: apiURL(path, nil),
		body: map[string][]string{"assignees": assignees, "labels": labels}}
	if err := c.send(ctx, k, &got); err != nil {
		if isStatus(err, http.StatusUnprocessableEntity) {
			return nil, nil, fmt.Errorf("%w: %w", ErrUnprocessable, err)
		}
		return nil, nil, err
	}
	gotAssignees, gotLabels = []string{}, []string{}
	for _, a := range got.Assignees {
		gotAssignees = append(gotAssignees, a.Login)
	}
	for _, l := range got.Labels {
		gotLabels = append(gotLabels, l.Name)
	}
	return gotAssignees, gotLabels, nil
}

// CloseIssue reads the issue first: the issues endpoint closes a pull request as well, and api never closes one.
func (c *Client) CloseIssue(ctx context.Context, r RepoRef, number int) error {
	return c.setIssueState(ctx, r, number, "closed")
}

func (c *Client) ReopenIssue(ctx context.Context, r RepoRef, number int) error {
	return c.setIssueState(ctx, r, number, "open")
}

// setIssueState closes or reopens the issue number. A number that names a pull request is an error, and nothing changes.
func (c *Client) setIssueState(ctx context.Context, r RepoRef, number int, state string) error {
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
		url: apiURL(path, nil), body: map[string]string{"state": state}}
	return c.send(ctx, k, nil)
}

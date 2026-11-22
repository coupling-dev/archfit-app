package api

import (
	"context"
	"errors"
	"net/http"
	"net/url"
	"strings"
)

// ActionsSuite finds the GitHub Actions suite for the head commit.
func (c *Client) ActionsSuite(ctx context.Context, repo Repo, head string) (int64, bool, error) {
	cr, base, err := repo.target()
	if err != nil {
		return 0, false, err
	}
	if !commitSHARE.MatchString(head) {
		return 0, false, errNotCommitSHA
	}
	var data struct {
		Suites []struct {
			ID int64 `json:"id"`
		} `json:"check_suites"`
	}
	k := call{cred: cr, method: http.MethodGet, route: "/repos/{r}/commits/{sha}/check-suites", url: apiURL(base+"/commits/"+head+"/check-suites", url.Values{"app_id": {"15368"}, "per_page": {"1"}})}
	if err := c.send(ctx, k, &data); err != nil {
		return 0, false, err
	}
	if len(data.Suites) == 0 {
		return 0, false, nil
	}
	return data.Suites[0].ID, data.Suites[0].ID > 0, nil
}

// RerequestSuite requests a suite rerun and reports GitHub's status.
func (c *Client) RerequestSuite(ctx context.Context, repo Repo, id int64) (int, error) {
	cr, base, err := repo.target()
	if err != nil {
		return 0, err
	}
	if id <= 0 {
		return 0, errors.New("github: invalid suite ID")
	}
	k := call{cred: cr, method: http.MethodPost, route: "/repos/{r}/check-suites/{id}/rerequest", url: apiURL(base+"/check-suites/"+itoa(id)+"/rerequest", nil)}
	res, err := c.do(ctx, k)
	if err != nil {
		var status *statusError
		if errors.As(err, &status) {
			return status.status, err
		}
		return 0, err
	}
	return res.status, nil
}

// RunURL reads the first check run's URL in the Actions suite.
func (c *Client) RunURL(ctx context.Context, repo Repo, id int64) (string, error) {
	cr, base, err := repo.target()
	if err != nil {
		return "", err
	}
	if id <= 0 {
		return "", errors.New("github: invalid suite ID")
	}
	var data struct {
		Runs []struct {
			URL string `json:"html_url"`
		} `json:"check_runs"`
	}
	k := call{cred: cr, method: http.MethodGet, route: "/repos/{r}/check-suites/{id}/check-runs", url: apiURL(base+"/check-suites/"+itoa(id)+"/check-runs", url.Values{"per_page": {"1"}})}
	if err := c.send(ctx, k, &data); err != nil {
		return "", err
	}
	if len(data.Runs) == 0 || data.Runs[0].URL == "" {
		return "", ErrNotFound
	}
	return data.Runs[0].URL, nil
}

// HasMarkerComment checks comments authored by this App.
func (c *Client) HasMarkerComment(ctx context.Context, repo Repo, pr int) (bool, error) {
	comments, err := c.AppComments(ctx, repo.RepoRef, pr)
	if err != nil {
		return false, err
	}
	for _, v := range comments {
		if strings.Contains(v.Body, "<!-- archfit:setup-recheck -->") {
			return true, nil
		}
	}
	return false, nil
}

// Comment creates a setup recheck comment.
func (c *Client) Comment(ctx context.Context, repo Repo, pr int, body string) error {
	_, err := c.CreateComment(ctx, repo.RepoRef, pr, body)
	return err
}

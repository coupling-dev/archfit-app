package api

import (
	"context"
	"errors"
	"net/http"
	"strconv"
	"strings"
)

// RequestedReviewers returns only the users and teams GitHub currently lists as requested. Existing CODEOWNERS
// requests appear in this list too, so callers can add only missing configured principals.
func (c *Client) RequestedReviewers(ctx context.Context, r RepoRef, number int) (users, teams []string, err error) {
	cr, base, err := r.target()
	if err != nil {
		return nil, nil, err
	}
	if number <= 0 {
		return nil, nil, errors.New("github: invalid pull request number")
	}
	var result struct {
		Users []struct {
			Login string `json:"login"`
		} `json:"users"`
		Teams []struct {
			Slug         string `json:"slug"`
			Organization *struct {
				Login string `json:"login"`
			} `json:"organization"`
		} `json:"teams"`
	}
	k := call{cred: cr, method: http.MethodGet, route: "/repos/{r}/pulls/{n}/requested_reviewers",
		url: apiURL(base+"/pulls/"+strconv.Itoa(number)+"/requested_reviewers", nil)}
	if err := c.send(ctx, k, &result); err != nil {
		return nil, nil, err
	}
	for _, user := range result.Users {
		if user.Login != "" {
			users = append(users, user.Login)
		}
	}
	for _, team := range result.Teams {
		if team.Slug == "" {
			continue
		}
		if team.Organization != nil && team.Organization.Login != "" {
			teams = append(teams, team.Organization.Login+"/"+team.Slug)
		} else {
			teams = append(teams, team.Slug)
		}
	}
	return users, teams, nil
}

// RequestReviewers adds users or teams to GitHub's requested reviewers. An empty list is a no-op; callers must not
// pass principals already requested, unrelated reviewers, or every member of a configured team.
func (c *Client) RequestReviewers(ctx context.Context, r RepoRef, number int, users, teams []string) error {
	cr, base, err := r.target()
	if err != nil {
		return err
	}
	if number <= 0 {
		return errors.New("github: invalid pull request number")
	}
	if len(users) == 0 && len(teams) == 0 {
		return nil
	}
	for _, user := range users {
		if _, err := escapeSegment(user); err != nil || strings.Contains(user, "/") {
			return errors.New("github: invalid requested reviewer")
		}
	}
	teamSlugs := make([]string, 0, len(teams))
	for _, team := range teams {
		org, slug, ok := strings.Cut(team, "/")
		if !ok || org == "" || slug == "" || strings.Contains(slug, "/") || !strings.EqualFold(org, r.owner()) {
			return errors.New("github: invalid requested reviewer team")
		}
		if _, err := escapeSegment(slug); err != nil {
			return errors.New("github: invalid requested reviewer team")
		}
		teamSlugs = append(teamSlugs, slug)
	}
	k := call{cred: cr, method: http.MethodPost, route: "/repos/{r}/pulls/{n}/requested_reviewers",
		url:  apiURL(base+"/pulls/"+strconv.Itoa(number)+"/requested_reviewers", nil),
		body: map[string][]string{"reviewers": users, "team_reviewers": teamSlugs}}
	return c.send(ctx, k, nil)
}

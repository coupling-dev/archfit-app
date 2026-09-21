package api

import (
	"context"
	"errors"
	"net/http"
)

func (c *Client) Permission(ctx context.Context, userToken, fullName string) (string, error) {
	cr, err := userCred(userToken)
	if err != nil {
		return "", err
	}
	base, err := repoPath(fullName)
	if err != nil {
		return "", err
	}
	var j struct {
		Permissions struct {
			Admin bool `json:"admin"`
			Push  bool `json:"push"`
			Pull  bool `json:"pull"`
		} `json:"permissions"`
	}
	k := call{cred: cr, method: http.MethodGet, route: "/repos/{r}", url: apiURL(base, nil)}
	if err := c.send(ctx, k, &j); err != nil {
		if errors.Is(err, ErrNotFound) {
			return "none", nil
		}
		return "", err
	}
	switch {
	case j.Permissions.Admin:
		return "admin", nil
	case j.Permissions.Push:
		return "write", nil
	case j.Permissions.Pull:
		return "read", nil
	}
	return "none", nil
}

func (c *Client) UserRepos(ctx context.Context, userToken string) ([]Repo, error) {
	const route = "/user/installations"
	cr, err := userCred(userToken)
	if err != nil {
		return nil, err
	}
	type installationJSON struct {
		ID int64 `json:"id"`
	}
	k := call{cred: cr, method: http.MethodGet, route: route, url: pagedURL(route, nil)}
	installations, err := listAll[installationJSON](ctx, c, k, "installations")
	if err != nil {
		return nil, err
	}
	out := []Repo{}
	for _, in := range installations {
		k = call{cred: cr, method: http.MethodGet, route: "/user/installations/{id}/repositories",
			url: pagedURL(route+"/"+itoa(in.ID)+"/repositories", nil)}
		// The cap applies to the total over all installations.
		repos, more, err := listPages[repoJSON](ctx, c, k, "repositories", maxItems-len(out))
		if err != nil {
			return nil, err
		}
		if more {
			return nil, k.errorf("more than %d items", maxItems)
		}
		for _, j := range repos {
			out = append(out, j.repo(in.ID))
		}
	}
	return out, nil
}

package api

import (
	"context"
	"errors"
	"net/http"
	"sync"
)

func (c *Client) Permission(ctx context.Context, userToken, fullName string) (string, error) {
	perm, _, err := c.Access(ctx, userToken, fullName)
	return perm, err
}

// Access returns the user's permission on the repository and the repository as the user sees it, in one GitHub call.
// The Repo has no installation ID: that comes from InstallationID, with the App JWT.
func (c *Client) Access(ctx context.Context, userToken, fullName string) (string, Repo, error) {
	cr, err := userCred(userToken)
	if err != nil {
		return "", Repo{}, err
	}
	base, err := repoPath(fullName)
	if err != nil {
		return "", Repo{}, err
	}
	var j struct {
		repoJSON
		Permissions struct {
			Admin bool `json:"admin"`
			Push  bool `json:"push"`
			Pull  bool `json:"pull"`
		} `json:"permissions"`
	}
	k := call{cred: cr, method: http.MethodGet, route: "/repos/{r}", url: apiURL(base, nil)}
	if err := c.send(ctx, k, &j); err != nil {
		if errors.Is(err, ErrNotFound) {
			return "none", Repo{}, nil
		}
		return "", Repo{}, err
	}
	repo := j.repo(0)
	switch {
	case j.Permissions.Admin:
		return "admin", repo, nil
	case j.Permissions.Push:
		return "write", repo, nil
	case j.Permissions.Pull:
		return "read", repo, nil
	}
	return "none", repo, nil
}

// maxInstallationPages bounds how many installations' repository lists are read at once: GitHub asks clients not to
// flood it with concurrent requests, and an account has a handful of installations.
const maxInstallationPages = 8

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
	// Every installation's pages are read together, each into its own slot, so the merge below keeps GitHub's order and
	// reports the first failure in that order: a failed page fails the whole list, never a partial one.
	calls := make([]call, len(installations))
	pages := make([][]repoJSON, len(installations))
	more := make([]bool, len(installations))
	errs := make([]error, len(installations))
	slots := make(chan struct{}, maxInstallationPages)
	var wg sync.WaitGroup
	for i, in := range installations {
		calls[i] = call{cred: cr, method: http.MethodGet, route: "/user/installations/{id}/repositories",
			url: pagedURL(route+"/"+itoa(in.ID)+"/repositories", nil)}
		wg.Add(1)
		slots <- struct{}{}
		go func() {
			defer wg.Done()
			defer func() { <-slots }()
			pages[i], more[i], errs[i] = listPages[repoJSON](ctx, c, calls[i], "repositories", maxItems)
		}()
	}
	wg.Wait()
	// The cap applies to the total over all installations.
	out := []Repo{}
	for i, in := range installations {
		if errs[i] != nil {
			return nil, errs[i]
		}
		if more[i] || len(out)+len(pages[i]) > maxItems {
			return nil, calls[i].errorf("more than %d items", maxItems)
		}
		for _, j := range pages[i] {
			out = append(out, j.repo(in.ID))
		}
	}
	return out, nil
}

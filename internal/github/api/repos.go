package api

import (
	"context"
	"encoding/base64"
	"net/http"
	"net/url"
	"time"
)

const maxFileBytes = 1 << 20

type repoJSON struct {
	ID            int64  `json:"id"`
	FullName      string `json:"full_name"`
	Private       bool   `json:"private"`
	DefaultBranch string `json:"default_branch"`
}

func (j repoJSON) repo(installationID int64) Repo {
	return Repo{
		RepoRef: RepoRef{InstallationID: installationID, FullName: j.FullName},
		ID:      j.ID, Private: j.Private, DefaultBranch: j.DefaultBranch,
	}
}

func (c *Client) ListInstallations(ctx context.Context) ([]Installation, error) {
	const route = "/app/installations"
	type installationJSON struct {
		ID      int64 `json:"id"`
		Account struct {
			Login string `json:"login"`
			Type  string `json:"type"`
		} `json:"account"`
		CreatedAt   time.Time `json:"created_at"`
		SuspendedAt *string   `json:"suspended_at"`
	}
	k := call{cred: appCred, method: http.MethodGet, route: route, url: pagedURL(route, nil)}
	all, err := listAll[installationJSON](ctx, c, k, "")
	if err != nil {
		return nil, err
	}
	out := []Installation{}
	for _, in := range all {
		if in.SuspendedAt == nil {
			out = append(out, Installation{ID: in.ID, Account: in.Account.Login, AccountType: in.Account.Type, CreatedAt: in.CreatedAt})
		}
	}
	return out, nil
}

func (c *Client) ListRepos(ctx context.Context, installationID int64) ([]Repo, error) {
	const route = "/installation/repositories"
	cr, err := instCred(installationID)
	if err != nil {
		return nil, err
	}
	k := call{cred: cr, method: http.MethodGet, route: route, url: pagedURL(route, nil)}
	all, err := listAll[repoJSON](ctx, c, k, "repositories")
	if err != nil {
		return nil, err
	}
	out := make([]Repo, len(all))
	for i, j := range all {
		out[i] = j.repo(installationID)
	}
	return out, nil
}

func (c *Client) ResolveRepo(ctx context.Context, fullName string) (Repo, error) {
	base, err := repoPath(fullName)
	if err != nil {
		return Repo{}, err
	}
	var inst struct {
		ID int64 `json:"id"`
	}
	k := call{cred: appCred, method: http.MethodGet, route: "/repos/{r}/installation", url: apiURL(base+"/installation", nil)}
	if err := c.send(ctx, k, &inst); err != nil {
		return Repo{}, err
	}
	cr, err := instCred(inst.ID)
	if err != nil {
		return Repo{}, k.errorf("invalid response")
	}
	var j repoJSON
	k = call{cred: cr, method: http.MethodGet, route: "/repos/{r}", url: apiURL(base, nil)}
	if err := c.send(ctx, k, &j); err != nil {
		return Repo{}, err
	}
	return j.repo(inst.ID), nil
}

func (c *Client) File(ctx context.Context, r RepoRef, ref, path string) (content []byte, blobSHA string, err error) {
	cr, base, err := r.target()
	if err != nil {
		return nil, "", err
	}
	escaped, err := escapeSegments(path)
	if err != nil {
		return nil, "", err
	}
	q := url.Values{}
	if ref != "" {
		q.Set("ref", ref)
	}
	k := call{cred: cr, method: http.MethodGet, route: "/repos/{r}/contents/{path}", url: apiURL(base+"/contents/"+escaped, q)}
	// A directory answers with an array, which fails to decode into this object.
	var j struct {
		Type     string `json:"type"`
		Encoding string `json:"encoding"`
		Size     int64  `json:"size"`
		Content  string `json:"content"`
		SHA      string `json:"sha"`
	}
	if err := c.send(ctx, k, &j); err != nil {
		return nil, "", err
	}
	switch {
	case j.Type != "file":
		return nil, "", k.errorf("not a regular file")
	case j.Size > maxFileBytes:
		return nil, "", k.errorf("file over %d bytes", maxFileBytes)
	case j.Encoding != "base64":
		return nil, "", k.errorf("invalid response")
	}
	// GitHub wraps the base64 text in newlines, which the decoder skips.
	data, decErr := base64.StdEncoding.DecodeString(j.Content)
	if decErr != nil {
		return nil, "", k.errorf("invalid response")
	}
	if len(data) > maxFileBytes {
		return nil, "", k.errorf("file over %d bytes", maxFileBytes)
	}
	return data, j.SHA, nil
}

func (c *Client) BranchHead(ctx context.Context, r RepoRef, branch string) (string, error) {
	cr, base, err := r.target()
	if err != nil {
		return "", err
	}
	escaped, err := escapeSegments(branch)
	if err != nil {
		return "", err
	}
	k := call{cred: cr, method: http.MethodGet, route: "/repos/{r}/git/ref/heads/{branch}",
		url: apiURL(base+"/git/ref/heads/"+escaped, nil)}
	var j struct {
		Object struct {
			SHA string `json:"sha"`
		} `json:"object"`
	}
	if err := c.send(ctx, k, &j); err != nil {
		return "", err
	}
	if j.Object.SHA == "" {
		return "", k.errorf("invalid response")
	}
	return j.Object.SHA, nil
}

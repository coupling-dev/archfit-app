package api

import (
	"context"
	"encoding/base64"
	"errors"
	"net/http"
	"net/url"
	"regexp"
	"strconv"
)

const graphqlRoute = "/graphql"

const markReadyMutation = `mutation($id: ID!) { markPullRequestReadyForReview(input: {pullRequestId: $id}) { pullRequest { isDraft } } }`

var commitSHARE = regexp.MustCompile(`^[0-9a-f]{40}$`)

type shaJSON struct {
	SHA string `json:"sha"`
}

func (c *Client) EnsureBranch(ctx context.Context, r RepoRef, branch, fromRef string) error {
	cr, base, err := r.target()
	if err != nil {
		return err
	}
	_, err = c.BranchHead(ctx, r, branch)
	if !errors.Is(err, ErrNotFound) {
		return err
	}
	// fromRef is a commit SHA or a branch name.
	sha := fromRef
	if !commitSHARE.MatchString(fromRef) {
		if sha, err = c.BranchHead(ctx, r, fromRef); err != nil {
			return err
		}
	}
	k := call{cred: cr, method: http.MethodPost, route: "/repos/{r}/git/refs", url: apiURL(base+"/git/refs", nil),
		body: map[string]string{"ref": "refs/heads/" + branch, "sha": sha}}
	err = c.send(ctx, k, nil)
	if isStatus(err, http.StatusUnprocessableEntity) {
		// Lost a creation race if the branch is there now; any other 422 stands.
		if _, headErr := c.BranchHead(ctx, r, branch); headErr == nil {
			return nil
		}
	}
	return err
}

func (c *Client) Commit(ctx context.Context, r RepoRef, branch, message string, files []FileChange) (sha string, err error) {
	if len(files) == 0 {
		return "", errors.New("github: commit without files")
	}
	cr, base, err := r.target()
	if err != nil {
		return "", err
	}
	escapedBranch, err := escapeSegments(branch)
	if err != nil {
		return "", err
	}
	head, err := c.BranchHead(ctx, r, branch)
	if err != nil {
		return "", err
	}
	escapedHead, err := escapeSegment(head)
	if err != nil {
		return "", err
	}
	var parent struct {
		Tree shaJSON `json:"tree"`
	}
	k := call{cred: cr, method: http.MethodGet, route: "/repos/{r}/git/commits/{sha}",
		url: apiURL(base+"/git/commits/"+escapedHead, nil)}
	if err := c.send(ctx, k, &parent); err != nil {
		return "", err
	}

	type treeEntry struct {
		Path string `json:"path"`
		Mode string `json:"mode"`
		Type string `json:"type"`
		SHA  string `json:"sha"`
	}
	entries := make([]treeEntry, len(files))
	for i, f := range files {
		var blob shaJSON
		k = call{cred: cr, method: http.MethodPost, route: "/repos/{r}/git/blobs", url: apiURL(base+"/git/blobs", nil),
			body: map[string]string{"content": base64.StdEncoding.EncodeToString(f.Content), "encoding": "base64"}}
		if err := c.send(ctx, k, &blob); err != nil {
			return "", err
		}
		entries[i] = treeEntry{Path: f.Path, Mode: "100644", Type: "blob", SHA: blob.SHA}
	}

	var tree shaJSON
	k = call{cred: cr, method: http.MethodPost, route: "/repos/{r}/git/trees", url: apiURL(base+"/git/trees", nil),
		body: map[string]any{"base_tree": parent.Tree.SHA, "tree": entries}}
	if err := c.send(ctx, k, &tree); err != nil {
		return "", err
	}
	if tree.SHA == "" || parent.Tree.SHA == "" {
		return "", k.errorf("invalid response")
	}
	if tree.SHA == parent.Tree.SHA {
		return head, nil
	}

	var commit shaJSON
	k = call{cred: cr, method: http.MethodPost, route: "/repos/{r}/git/commits", url: apiURL(base+"/git/commits", nil),
		body: map[string]any{"message": message, "tree": tree.SHA, "parents": []string{head}}}
	if err := c.send(ctx, k, &commit); err != nil {
		return "", err
	}
	if commit.SHA == "" {
		return "", k.errorf("invalid response")
	}
	// force stays false: a non-fast-forward is GitHub's 422 and the caller reloads.
	k = call{cred: cr, method: http.MethodPatch, route: "/repos/{r}/git/refs/heads/{branch}",
		url:  apiURL(base+"/git/refs/heads/"+escapedBranch, nil),
		body: map[string]any{"sha": commit.SHA, "force": false}}
	if err := c.send(ctx, k, nil); err != nil {
		return "", err
	}
	return commit.SHA, nil
}

func (c *Client) EnsurePull(ctx context.Context, r RepoRef, p NewPull) (Pull, error) {
	cr, base, err := r.target()
	if err != nil {
		return Pull{}, err
	}
	find := func() ([]Pull, error) {
		return c.pullsByHead(ctx, r, p.Head, url.Values{"state": {"open"}, "base": {p.Base}})
	}
	open, err := find()
	if err != nil {
		return Pull{}, err
	}
	if len(open) > 0 {
		return open[0], nil
	}
	var j pullJSON
	k := call{cred: cr, method: http.MethodPost, route: "/repos/{r}/pulls", url: apiURL(base+"/pulls", nil),
		body: map[string]any{"title": p.Title, "body": p.Body, "head": p.Head, "base": p.Base, "draft": p.Draft}}
	createErr := c.send(ctx, k, &j)
	if createErr == nil {
		return j.pull(), nil
	}
	if isStatus(createErr, http.StatusUnprocessableEntity) {
		// Lost a creation race if the pull is there now; any other 422 stands.
		if open, err = find(); err == nil && len(open) > 0 {
			return open[0], nil
		}
	}
	return Pull{}, createErr
}

func (c *Client) UpdatePullBody(ctx context.Context, r RepoRef, number int, body string) error {
	cr, base, err := r.target()
	if err != nil {
		return err
	}
	k := call{cred: cr, method: http.MethodPatch, route: "/repos/{r}/pulls/{n}",
		url: apiURL(base+"/pulls/"+strconv.Itoa(number), nil), body: map[string]string{"body": body}}
	return c.send(ctx, k, nil)
}

// MarkReady uses GraphQL: REST has no way out of draft state.
func (c *Client) MarkReady(ctx context.Context, r RepoRef, number int) error {
	pull, err := c.getPull(ctx, r, number)
	if err != nil {
		return err
	}
	if !pull.Draft {
		return nil
	}
	cr, _, err := r.target()
	if err != nil {
		return err
	}
	k := call{cred: cr, method: http.MethodPost, route: graphqlRoute, url: apiURL(graphqlRoute, nil),
		body: map[string]any{"query": markReadyMutation, "variables": map[string]string{"id": pull.NodeID}}}
	var answer struct {
		Errors []struct {
			Type string `json:"type"`
		} `json:"errors"`
	}
	if err := c.send(ctx, k, &answer); err != nil {
		return err
	}
	for _, e := range answer.Errors {
		if e.Type == "RATE_LIMITED" {
			return k.errorf("%w", ErrRateLimited)
		}
	}
	if len(answer.Errors) > 0 {
		// GraphQL messages stay out of the error: they can quote content.
		return k.errorf("mutation failed")
	}
	return nil
}

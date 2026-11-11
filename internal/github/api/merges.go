package api

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"net/url"
	"strconv"
)

// maxCompareFiles is the most files GitHub's compare lists.
const maxCompareFiles = 300

var (
	errNotCommitSHA = errors.New("github: not a 40-hex commit SHA")
	errPullNumber   = errors.New("github: invalid pull request or issue number")

	// compareStatuses are the values GitHub documents; any other is not read as one of them.
	compareStatuses = map[string]bool{"ahead": true, "behind": true, "identical": true, "diverged": true}
)

// pullTarget guards a write on pull request number at the expected head SHA, so no empty SHA lets GitHub act on
// whatever head it has.
func pullTarget(r RepoRef, number int, headSHA string) (cred, string, error) {
	if number <= 0 {
		return cred{}, "", errPullNumber
	}
	if !commitSHARE.MatchString(headSHA) {
		return cred{}, "", errNotCommitSHA
	}
	cr, base, err := r.target()
	if err != nil {
		return cred{}, "", err
	}
	return cr, base + "/pulls/" + strconv.Itoa(number), nil
}

// refusal maps GitHub's refusal of a merge or a branch update to its sentinel; the status stays readable by Status.
func refusal(err error) error {
	switch {
	case isStatus(err, http.StatusMethodNotAllowed):
		return fmt.Errorf("%w: %w", ErrNotMergeable, err)
	case isStatus(err, http.StatusConflict):
		return fmt.Errorf("%w: %w", ErrHeadChanged, err)
	case isStatus(err, http.StatusUnprocessableEntity):
		return fmt.Errorf("%w: %w", ErrUnprocessable, err)
	}
	return err
}

func (c *Client) MergePull(ctx context.Context, r RepoRef, number int, headSHA string) (string, error) {
	cr, pull, err := pullTarget(r, number, headSHA)
	if err != nil {
		return "", err
	}
	var j struct {
		SHA    string `json:"sha"`
		Merged bool   `json:"merged"`
	}
	k := call{cred: cr, method: http.MethodPut, route: "/repos/{r}/pulls/{n}/merge", url: apiURL(pull+"/merge", nil),
		body: map[string]string{"merge_method": "squash", "sha": headSHA}}
	if err := c.send(ctx, k, &j); err != nil {
		return "", refusal(err)
	}
	if !j.Merged || !commitSHARE.MatchString(j.SHA) {
		return "", k.errorf("invalid response")
	}
	return j.SHA, nil
}

func (c *Client) UpdateBranch(ctx context.Context, r RepoRef, number int, headSHA string) error {
	cr, pull, err := pullTarget(r, number, headSHA)
	if err != nil {
		return err
	}
	k := call{cred: cr, method: http.MethodPut, route: "/repos/{r}/pulls/{n}/update-branch",
		url: apiURL(pull+"/update-branch", nil), body: map[string]string{"expected_head_sha": headSHA}}
	return refusal(c.send(ctx, k, nil))
}

func (c *Client) Compare(ctx context.Context, r RepoRef, base, head string) (Comparison, error) {
	if !commitSHARE.MatchString(base) || !commitSHARE.MatchString(head) {
		return Comparison{}, errNotCommitSHA
	}
	cr, repo, err := r.target()
	if err != nil {
		return Comparison{}, err
	}
	var j struct {
		Status string      `json:"status"`
		Files  *[]fileJSON `json:"files"`
	}
	// per_page=1 keeps the commit list short; the files are on the first page whatever its size.
	k := call{cred: cr, method: http.MethodGet, route: "/repos/{r}/compare/{base}...{head}",
		url: apiURL(repo+"/compare/"+base+"..."+head, url.Values{"per_page": {"1"}})}
	if err := c.send(ctx, k, &j); err != nil {
		return Comparison{}, err
	}
	// Without a file list nothing says what changed: an empty list would read as "nothing changed". An unknown status
	// would read as a known one to a caller that tests for one value.
	if !compareStatuses[j.Status] || j.Files == nil {
		return Comparison{}, k.errorf("invalid response")
	}
	return Comparison{Status: j.Status, Paths: uniquePaths(*j.Files), Truncated: len(*j.Files) >= maxCompareFiles}, nil
}

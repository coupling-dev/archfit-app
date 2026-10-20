package api

import (
	"context"
	"errors"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"
)

type pullJSON struct {
	Number int    `json:"number"`
	Title  string `json:"title"`
	User   *struct {
		Login string `json:"login"`
	} `json:"user"`
	NodeID       string    `json:"node_id"`
	State        string    `json:"state"`
	Draft        bool      `json:"draft"`
	Body         string    `json:"body"` // null stays ""
	HTMLURL      string    `json:"html_url"`
	MergedAt     *string   `json:"merged_at"`
	UpdatedAt    time.Time `json:"updated_at"`
	ChangedFiles int       `json:"changed_files"`
	Head         struct {
		SHA  string `json:"sha"`
		Ref  string `json:"ref"`
		Repo *struct {
			FullName string `json:"full_name"`
		} `json:"repo"`
	} `json:"head"`
	Base struct {
		SHA string `json:"sha"`
		Ref string `json:"ref"`
	} `json:"base"`
}

func (j pullJSON) pull() Pull {
	p := Pull{
		Number: j.Number, State: j.State, Draft: j.Draft, Body: j.Body, URL: j.HTMLURL,
		// List responses carry no "merged" flag, so merged_at decides everywhere.
		Merged:  j.MergedAt != nil && *j.MergedAt != "",
		HeadSHA: j.Head.SHA, HeadRef: j.Head.Ref, BaseRef: j.Base.Ref, BaseSHA: j.Base.SHA,
		UpdatedAt: j.UpdatedAt,
	}
	if j.Head.Repo != nil {
		p.HeadRepo = j.Head.Repo.FullName
	}
	p.Title = j.Title
	if j.User != nil {
		p.Author = j.User.Login
	}
	return p
}

func (c *Client) getPull(ctx context.Context, r RepoRef, number int) (pullJSON, error) {
	cr, base, err := r.target()
	if err != nil {
		return pullJSON{}, err
	}
	var j pullJSON
	k := call{cred: cr, method: http.MethodGet, route: "/repos/{r}/pulls/{n}", url: apiURL(base+"/pulls/"+strconv.Itoa(number), nil)}
	return j, c.send(ctx, k, &j)
}

// PullSummary is Pull without the merge base: one call fewer, for a reader that does not use MergeBaseSHA.
func (c *Client) PullSummary(ctx context.Context, r RepoRef, number int) (Pull, error) {
	j, err := c.getPull(ctx, r, number)
	if err != nil {
		return Pull{}, err
	}
	return j.pull(), nil
}

func (c *Client) Pull(ctx context.Context, r RepoRef, number int) (Pull, error) {
	j, err := c.getPull(ctx, r, number)
	if err != nil {
		return Pull{}, err
	}
	p := j.pull()
	baseSHA, err := escapeSegment(p.BaseSHA)
	if err != nil {
		return Pull{}, err
	}
	headSHA, err := escapeSegment(p.HeadSHA)
	if err != nil {
		return Pull{}, err
	}
	cr, base, err := r.target()
	if err != nil {
		return Pull{}, err
	}
	var cmp struct {
		MergeBase struct {
			SHA string `json:"sha"`
		} `json:"merge_base_commit"`
	}
	// per_page=1 keeps the commit list out of the answer; only the merge base is read.
	k := call{cred: cr, method: http.MethodGet, route: "/repos/{r}/compare/{base}...{head}",
		url: apiURL(base+"/compare/"+baseSHA+"..."+headSHA, url.Values{"per_page": {"1"}})}
	if err := c.send(ctx, k, &cmp); err != nil {
		return Pull{}, err
	}
	if cmp.MergeBase.SHA == "" {
		return Pull{}, k.errorf("invalid response")
	}
	p.MergeBaseSHA = cmp.MergeBase.SHA
	return p, nil
}

func (c *Client) OpenPulls(ctx context.Context, r RepoRef) ([]Pull, error) {
	cr, base, err := r.target()
	if err != nil {
		return nil, err
	}
	k := call{cred: cr, method: http.MethodGet, route: "/repos/{r}/pulls",
		url: pagedURL(base+"/pulls", url.Values{"state": {"open"}})}
	all, err := listAll[pullJSON](ctx, c, k, "")
	if err != nil {
		return nil, err
	}
	out := make([]Pull, len(all))
	for i, j := range all {
		out[i] = j.pull()
	}
	return out, nil
}

// pullsByHead reads one page of pulls whose head is owner:branch, owner being the owner of r, and keeps
// those whose head repository is r itself: a fork's branch never counts.
func (c *Client) pullsByHead(ctx context.Context, r RepoRef, branch string, q url.Values) ([]Pull, error) {
	cr, base, err := r.target()
	if err != nil {
		return nil, err
	}
	if _, err := escapeSegments(branch); err != nil {
		return nil, err
	}
	q.Set("head", r.owner()+":"+branch)
	var page []pullJSON
	k := call{cred: cr, method: http.MethodGet, route: "/repos/{r}/pulls", url: apiURL(base+"/pulls", q)}
	if err := c.send(ctx, k, &page); err != nil {
		return nil, err
	}
	out := []Pull{}
	for _, j := range page {
		if p := j.pull(); strings.EqualFold(p.HeadRepo, r.FullName) && p.HeadRef == branch {
			out = append(out, p)
		}
	}
	return out, nil
}

func (c *Client) PullByHead(ctx context.Context, r RepoRef, branch string) (Pull, error) {
	pulls, err := c.pullsByHead(ctx, r, branch, url.Values{
		"state": {"all"}, "sort": {"created"}, "direction": {"desc"}, "per_page": {"1"},
	})
	if err != nil {
		return Pull{}, err
	}
	if len(pulls) == 0 {
		return Pull{}, ErrNotFound
	}
	return pulls[0], nil
}

func (c *Client) ChangedFiles(ctx context.Context, r RepoRef, number int) (paths []string, truncated bool, err error) {
	cr, base, err := r.target()
	if err != nil {
		return nil, false, err
	}
	type fileJSON struct {
		Filename         string `json:"filename"`
		PreviousFilename string `json:"previous_filename"`
	}
	k := call{cred: cr, method: http.MethodGet, route: "/repos/{r}/pulls/{n}/files",
		url: pagedURL(base+"/pulls/"+strconv.Itoa(number)+"/files", nil)}
	files, more, err := listPages[fileJSON](ctx, c, k, "", maxItems)
	if err != nil {
		return nil, false, err
	}
	// GitHub silently stops this list at 3 000 files; the pull's own count tells.
	pull, err := c.getPull(ctx, r, number)
	if err != nil {
		return nil, false, err
	}
	seen := map[string]bool{}
	paths = []string{}
	for _, f := range files {
		for _, p := range []string{f.Filename, f.PreviousFilename} {
			if p != "" && !seen[p] {
				seen[p] = true
				paths = append(paths, p)
			}
		}
	}
	return paths, more || pull.ChangedFiles > len(files), nil
}

func (c *Client) Reviews(ctx context.Context, r RepoRef, number int) ([]Review, error) {
	cr, base, err := r.target()
	if err != nil {
		return nil, err
	}
	type reviewJSON struct {
		User *struct {
			Login string `json:"login"`
		} `json:"user"`
		State    string `json:"state"`
		CommitID string `json:"commit_id"`
	}
	k := call{cred: cr, method: http.MethodGet, route: "/repos/{r}/pulls/{n}/reviews",
		url: pagedURL(base+"/pulls/"+strconv.Itoa(number)+"/reviews", nil)}
	all, err := listAll[reviewJSON](ctx, c, k, "")
	if err != nil {
		return nil, err
	}
	out := []Review{}
	for _, j := range all {
		if j.State == "PENDING" {
			continue
		}
		rv := Review{State: j.State, CommitSHA: j.CommitID}
		if j.User != nil {
			rv.Reviewer = j.User.Login
		}
		out = append(out, rv)
	}
	return out, nil
}

func (c *Client) TeamMember(ctx context.Context, r RepoRef, team, login string) (bool, error) {
	cr, _, err := r.target()
	if err != nil {
		return false, err
	}
	org, slug, ok := strings.Cut(team, "/")
	if !ok {
		return false, errors.New("github: team is not org/team-slug")
	}
	var parts [3]string
	for i, s := range []string{org, slug, login} {
		if parts[i], err = escapeSegment(s); err != nil {
			return false, errors.New("github: team is not org/team-slug or the login is invalid")
		}
	}
	var j struct {
		State string `json:"state"`
	}
	k := call{cred: cr, method: http.MethodGet, route: "/orgs/{org}/teams/{slug}/memberships/{login}",
		url: apiURL("/orgs/"+parts[0]+"/teams/"+parts[1]+"/memberships/"+parts[2], nil)}
	if err := c.send(ctx, k, &j); err != nil {
		if errors.Is(err, ErrNotFound) {
			return false, nil
		}
		return false, err
	}
	return j.State == "active", nil
}

package api

import (
	"archive/zip"
	"bytes"
	"context"
	"errors"
	"io"
	"net/http"
	"net/url"
	"path"
	"time"
)

// zipOverhead is the room a zip with one file may take above the file's own size cap.
const zipOverhead = 1 << 20

type workflowRunJSON struct {
	ID             int64  `json:"id"`
	RunAttempt     int    `json:"run_attempt"`
	Event          string `json:"event"`
	HeadSHA        string `json:"head_sha"`
	Path           string `json:"path"`
	Status         string `json:"status"`
	Conclusion     string `json:"conclusion"` // null stays ""
	HeadRepository *struct {
		FullName string `json:"full_name"`
	} `json:"head_repository"`
	PullRequests []struct {
		Number int `json:"number"`
	} `json:"pull_requests"`
}

func (j workflowRunJSON) run() WorkflowRun {
	run := WorkflowRun{
		ID: j.ID, Attempt: j.RunAttempt, Event: j.Event, HeadSHA: j.HeadSHA, WorkflowPath: j.Path,
		Status: j.Status, Conclusion: j.Conclusion,
	}
	if j.HeadRepository != nil {
		run.HeadRepo = j.HeadRepository.FullName
	}
	for _, p := range j.PullRequests {
		run.PullRequests = append(run.PullRequests, p.Number)
	}
	return run
}

// workflowFile returns the escaped file name GitHub addresses a workflow by.
func workflowFile(workflowPath string) (string, error) {
	return escapeSegment(path.Base(workflowPath))
}

func (c *Client) Run(ctx context.Context, r RepoRef, runID int64) (WorkflowRun, error) {
	cr, base, err := r.target()
	if err != nil {
		return WorkflowRun{}, err
	}
	var j workflowRunJSON
	k := call{cred: cr, method: http.MethodGet, route: "/repos/{r}/actions/runs/{id}",
		url: apiURL(base+"/actions/runs/"+itoa(runID), nil)}
	if err := c.send(ctx, k, &j); err != nil {
		return WorkflowRun{}, err
	}
	return j.run(), nil
}

func (c *Client) Rerun(ctx context.Context, r RepoRef, runID int64) error {
	cr, base, err := r.target()
	if err != nil {
		return err
	}
	k := call{cred: cr, method: http.MethodPost, route: "/repos/{r}/actions/runs/{id}/rerun",
		url: apiURL(base+"/actions/runs/"+itoa(runID)+"/rerun", nil)}
	return c.send(ctx, k, nil)
}

func (c *Client) Dispatch(ctx context.Context, r RepoRef, workflowPath, ref string, inputs map[string]string) error {
	cr, base, err := r.target()
	if err != nil {
		return err
	}
	file, err := workflowFile(workflowPath)
	if err != nil {
		return err
	}
	if ref == "" {
		return errors.New("github: workflow dispatch without a ref")
	}
	body := struct {
		Ref    string            `json:"ref"`
		Inputs map[string]string `json:"inputs,omitempty"`
	}{Ref: ref, Inputs: inputs}
	k := call{cred: cr, method: http.MethodPost, route: "/repos/{r}/actions/workflows/{file}/dispatches",
		url: apiURL(base+"/actions/workflows/"+file+"/dispatches", nil), body: body}
	return c.send(ctx, k, nil)
}

func (c *Client) LatestRun(ctx context.Context, r RepoRef, workflowPath string, f RunFilter) (WorkflowRun, error) {
	cr, base, err := r.target()
	if err != nil {
		return WorkflowRun{}, err
	}
	file, err := workflowFile(workflowPath)
	if err != nil {
		return WorkflowRun{}, err
	}
	q := url.Values{"per_page": {"1"}}
	for name, v := range map[string]string{"branch": f.Branch, "event": f.Event, "status": f.Status} {
		if v != "" {
			q.Set(name, v)
		}
	}
	var j struct {
		Runs []workflowRunJSON `json:"workflow_runs"`
	}
	k := call{cred: cr, method: http.MethodGet, route: "/repos/{r}/actions/workflows/{file}/runs",
		url: apiURL(base+"/actions/workflows/"+file+"/runs", q)}
	if err := c.send(ctx, k, &j); err != nil {
		return WorkflowRun{}, err
	}
	if len(j.Runs) == 0 {
		return WorkflowRun{}, k.errorf("%w", ErrNotFound)
	}
	return j.Runs[0].run(), nil
}

func (c *Client) Artifact(ctx context.Context, r RepoRef, runID int64, name string, maxBytes int64) ([]byte, error) {
	if maxBytes <= 0 {
		return nil, errors.New("github: artifact size cap must be positive")
	}
	id, err := c.ArtifactID(ctx, r, runID, name)
	if err != nil {
		return nil, err
	}
	return c.ArtifactByID(ctx, r, id, maxBytes)
}

// ArtifactID is the newest unexpired artifact of the run with that name. An artifact never changes, so its ID names
// its content: a reader may keep what it downloaded under the ID.
func (c *Client) ArtifactID(ctx context.Context, r RepoRef, runID int64, name string) (int64, error) {
	if name == "" {
		return 0, errors.New("github: empty artifact name")
	}
	cr, base, err := r.target()
	if err != nil {
		return 0, err
	}
	var list struct {
		Artifacts []struct {
			ID        int64     `json:"id"`
			Name      string    `json:"name"`
			Expired   bool      `json:"expired"`
			CreatedAt time.Time `json:"created_at"`
		} `json:"artifacts"`
	}
	k := call{cred: cr, method: http.MethodGet, route: "/repos/{r}/actions/runs/{id}/artifacts",
		url: apiURL(base+"/actions/runs/"+itoa(runID)+"/artifacts", url.Values{"name": {name}, "per_page": {perPage}})}
	if err := c.send(ctx, k, &list); err != nil {
		return 0, err
	}
	var newest int64
	var newestAt time.Time
	for _, a := range list.Artifacts {
		if a.Name == name && !a.Expired && (newest == 0 || a.CreatedAt.After(newestAt)) {
			newest, newestAt = a.ID, a.CreatedAt
		}
	}
	if newest == 0 {
		return 0, k.errorf("%w", ErrNotFound)
	}
	return newest, nil
}

// ArtifactByID downloads an artifact and returns the one file in it, at most maxBytes.
func (c *Client) ArtifactByID(ctx context.Context, r RepoRef, id int64, maxBytes int64) ([]byte, error) {
	if maxBytes <= 0 {
		return nil, errors.New("github: artifact size cap must be positive")
	}
	cr, base, err := r.target()
	if err != nil {
		return nil, err
	}
	k := call{cred: cr, method: http.MethodGet, route: "/repos/{r}/actions/artifacts/{id}/zip",
		url: apiURL(base+"/actions/artifacts/"+itoa(id)+"/zip", nil), redirect: true, maxBody: maxBytes + zipOverhead}
	res, err := c.do(ctx, k)
	if err != nil {
		return nil, artifactDefect(k, err)
	}
	if res.location != "" {
		// The blob store is another host: it gets the signed URL and no GitHub credential.
		blob, err := url.Parse(res.location)
		if err != nil || blob.Scheme != "https" || blob.Host == "" {
			return nil, k.errorf("invalid redirect")
		}
		k = call{cred: cred{kind: credNone}, method: http.MethodGet, route: "artifact download", url: res.location,
			maxBody: maxBytes + zipOverhead}
		if res, err = c.do(ctx, k); err != nil {
			return nil, artifactDefect(k, err)
		}
	}
	data, err := extractSingleFile(res.body, maxBytes)
	if err != nil {
		return nil, k.errorf("%w: %w", ErrArtifactInvalid, err)
	}
	return data, nil
}

// artifactDefect marks a zip over its size limit as ErrArtifactInvalid: the artifact is what it is on every download.
// Any other failure of the download is returned as it is.
func artifactDefect(k call, err error) error {
	if errors.Is(err, errResponseTooLarge) {
		return k.errorf("%w: %w", ErrArtifactInvalid, err)
	}
	return err
}

// extractSingleFile returns the content of a zip that holds exactly one regular file of at most maxBytes.
func extractSingleFile(data []byte, maxBytes int64) ([]byte, error) {
	zr, err := zip.NewReader(bytes.NewReader(data), int64(len(data)))
	if err != nil {
		return nil, errors.New("artifact is not a zip")
	}
	if len(zr.File) != 1 || !zr.File[0].Mode().IsRegular() {
		return nil, errors.New("artifact must hold exactly one regular file")
	}
	f := zr.File[0]
	if f.UncompressedSize64 > uint64(maxBytes) {
		return nil, errors.New("artifact file over the size cap")
	}
	rc, err := f.Open()
	if err != nil {
		return nil, errors.New("artifact file is unreadable")
	}
	defer func() { _ = rc.Close() }()
	// The declared size is not trusted: the bytes actually read are capped too.
	content, err := io.ReadAll(io.LimitReader(rc, maxBytes+1))
	if err != nil {
		return nil, errors.New("artifact file is unreadable")
	}
	if int64(len(content)) > maxBytes {
		return nil, errors.New("artifact file over the size cap")
	}
	return content, nil
}

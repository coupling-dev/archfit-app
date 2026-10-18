package api

import (
	"context"
	"errors"
	"net/http"
	"net/url"
	"time"
)

const (
	maxSummaryBytes = 65535
	maxAnnotations  = 50
	statusCompleted = "completed"
)

type annotationJSON struct {
	Path      string `json:"path"`
	StartLine int    `json:"start_line"`
	EndLine   int    `json:"end_line"`
	Level     string `json:"annotation_level"`
	Message   string `json:"message"`
}

type checkOutputJSON struct {
	Title       string           `json:"title"`
	Summary     string           `json:"summary"`
	Annotations []annotationJSON `json:"annotations,omitempty"`
	// AnnotationsCount is response-only: checkRunBody never sets it, so no request carries it.
	AnnotationsCount int `json:"annotations_count,omitempty"`
}

type checkRunJSON struct {
	ID         int64            `json:"id,omitempty"`
	Name       string           `json:"name,omitempty"`
	HeadSHA    string           `json:"head_sha,omitempty"`
	ExternalID string           `json:"external_id,omitempty"`
	DetailsURL string           `json:"details_url,omitempty"`
	Status     string           `json:"status,omitempty"`
	Conclusion string           `json:"conclusion,omitempty"`
	StartedAt  *time.Time       `json:"started_at,omitempty"`
	Output     *checkOutputJSON `json:"output,omitempty"`
	App        *struct {
		ID int64 `json:"id"`
	} `json:"app,omitempty"`
}

func (j checkRunJSON) checkRun() CheckRun {
	run := CheckRun{
		ID: j.ID, Name: j.Name, HeadSHA: j.HeadSHA, ExternalID: j.ExternalID, DetailsURL: j.DetailsURL, Status: j.Status, Conclusion: j.Conclusion,
	}
	if j.StartedAt != nil {
		run.StartedAt = *j.StartedAt
	}
	if j.Output != nil {
		run.Title, run.Summary, run.AnnotationCount = j.Output.Title, j.Output.Summary, j.Output.AnnotationsCount
	}
	return run
}

// checkRunBody validates the caller contract and builds the request body. Nothing is ever truncated.
func checkRunBody(c CheckRun) (checkRunJSON, error) {
	switch {
	case len(c.Summary) > maxSummaryBytes:
		return checkRunJSON{}, errors.New("github: check run summary over 65535 bytes")
	case len(c.Annotations) > maxAnnotations:
		return checkRunJSON{}, errors.New("github: check run with more than 50 annotations")
	case c.Status == statusCompleted && c.Conclusion == "":
		return checkRunJSON{}, errors.New("github: completed check run without a conclusion")
	case c.Status != statusCompleted && c.Conclusion != "":
		return checkRunJSON{}, errors.New("github: conclusion on a check run that is not completed")
	}
	body := checkRunJSON{
		Name: c.Name, HeadSHA: c.HeadSHA, ExternalID: c.ExternalID, DetailsURL: c.DetailsURL, Status: c.Status, Conclusion: c.Conclusion,
	}
	if !c.StartedAt.IsZero() {
		started := c.StartedAt.UTC()
		body.StartedAt = &started
	}
	if c.Title != "" || c.Summary != "" || len(c.Annotations) > 0 {
		body.Output = &checkOutputJSON{Title: c.Title, Summary: c.Summary}
		for _, a := range c.Annotations {
			line := max(a.Line, 1)
			body.Output.Annotations = append(body.Output.Annotations,
				annotationJSON{Path: a.Path, StartLine: line, EndLine: line, Level: a.Level, Message: a.Message})
		}
	}
	return body, nil
}

func (c *Client) CheckRuns(ctx context.Context, r RepoRef, headSHA, name string) ([]CheckRun, error) {
	cr, base, err := r.target()
	if err != nil {
		return nil, err
	}
	sha, err := escapeSegments(headSHA)
	if err != nil {
		return nil, err
	}
	app, err := c.appIdentity(ctx)
	if err != nil {
		return nil, err
	}
	q := url.Values{"app_id": {itoa(app.id)}, "filter": {"latest"}}
	if name != "" {
		q.Set("check_name", name)
	}
	k := call{cred: cr, method: http.MethodGet, route: "/repos/{r}/commits/{sha}/check-runs",
		url: pagedURL(base+"/commits/"+sha+"/check-runs", q)}
	all, err := listAll[checkRunJSON](ctx, c, k, "check_runs")
	if err != nil {
		return nil, err
	}
	out := []CheckRun{}
	for _, j := range all {
		// app_id already filters; this keeps another app's run out even if GitHub ignored the parameter.
		if j.App != nil && j.App.ID == app.id {
			out = append(out, j.checkRun())
		}
	}
	return out, nil
}

func (c *Client) CreateCheckRun(ctx context.Context, r RepoRef, run CheckRun) (CheckRun, error) {
	body, err := checkRunBody(run)
	if err != nil {
		return CheckRun{}, err
	}
	cr, base, err := r.target()
	if err != nil {
		return CheckRun{}, err
	}
	var j checkRunJSON
	k := call{cred: cr, method: http.MethodPost, route: "/repos/{r}/check-runs", url: apiURL(base+"/check-runs", nil), body: body}
	if err := c.send(ctx, k, &j); err != nil {
		return CheckRun{}, err
	}
	if j.ID == 0 {
		return CheckRun{}, k.errorf("invalid response")
	}
	created := run
	created.ID = j.ID
	return created, nil
}

func (c *Client) UpdateCheckRun(ctx context.Context, r RepoRef, run CheckRun) error {
	if run.ID <= 0 {
		return errors.New("github: check run update without an ID")
	}
	body, err := checkRunBody(run)
	if err != nil {
		return err
	}
	body.HeadSHA = "" // not updatable
	cr, base, err := r.target()
	if err != nil {
		return err
	}
	k := call{cred: cr, method: http.MethodPatch, route: "/repos/{r}/check-runs/{id}",
		url: apiURL(base+"/check-runs/"+itoa(run.ID), nil), body: body}
	return c.send(ctx, k, nil)
}

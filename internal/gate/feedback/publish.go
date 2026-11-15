package feedback

import (
	"context"
	"net/url"
	"strconv"
	"strings"
	"time"

	"github.com/alexei-led/archfit-app/internal/gate/inputs"
	"github.com/alexei-led/archfit-app/internal/github/api"
)

// Actions, as logged.
const (
	actionNone           = "none"
	actionCreated        = "created"
	actionUpdated        = "updated"
	actionUnchanged      = "unchanged"
	actionSkippedNewer   = "skipped_newer"
	actionSkippedOldHead = "skipped_old_head"
)

type publisher struct {
	checks   api.Checks
	comments api.Comments
	pulls    api.Pulls
	appURL   string
	now      func() time.Time
}

// pullLink is the App's page of the target's pull request; "" without an App URL.
func (s *publisher) pullLink(t inputs.Target) string {
	if s.appURL == "" || t.PullRequest == 0 {
		return ""
	}
	owner, name, _ := strings.Cut(t.Repo.FullName, "/")
	return s.appURL + "/r/" + url.PathEscape(owner) + "/" + url.PathEscape(name) + "/pull/" + strconv.Itoa(t.PullRequest)
}

// latestRun returns the run with the highest ID. Two concurrent first publications may create two
// runs; every later call settles on the same one.
func (s *publisher) latestRun(ctx context.Context, r api.RepoRef, headSHA, name string) (api.CheckRun, bool, error) {
	runs, err := s.checks.CheckRuns(ctx, r, headSHA, name)
	if err != nil || len(runs) == 0 {
		return api.CheckRun{}, false, err
	}
	latest := runs[0]
	for _, run := range runs[1:] {
		if run.ID > latest.ID {
			latest = run
		}
	}
	return latest, true, nil
}

// createRun creates the run with the first annotationsPerRequest annotations and sends the rest in updates of the
// same run: GitHub takes 50 per request and appends each batch. Before each further batch it reads the latest run
// again and stops when another run took its place or superseded says a newer publication holds it, so an older
// publication never overwrites a newer summary.
func (s *publisher) createRun(ctx context.Context, r api.RepoRef, run api.CheckRun, superseded func(api.CheckRun) bool) (int64, error) {
	var rest []api.Annotation
	run.Annotations, rest = batch(run.Annotations)
	created, err := s.checks.CreateCheckRun(ctx, r, run)
	if err != nil {
		return 0, err
	}
	run.ID = created.ID
	for len(rest) > 0 {
		latest, found, err := s.latestRun(ctx, r, run.HeadSHA, run.Name)
		if err != nil {
			return 0, err
		}
		if !found || latest.ID != run.ID || superseded(latest) {
			return 0, nil
		}
		run.Annotations, rest = batch(rest)
		if err := s.checks.UpdateCheckRun(ctx, r, run); err != nil {
			return 0, err
		}
	}
	return run.ID, nil
}

// batch splits off the annotations of one request.
func batch(all []api.Annotation) (now, later []api.Annotation) {
	n := min(len(all), annotationsPerRequest)
	return all[:n], all[n:]
}

// findComment returns the App comments and the lowest-ID feedback comment that starts with the comment marker.
func (s *publisher) findComment(ctx context.Context, t inputs.Target) (api.Comment, bool, error) {
	all, err := s.comments.AppComments(ctx, t.Repo.RepoRef, t.PullRequest)
	if err != nil {
		return api.Comment{}, false, err
	}
	var found api.Comment
	for _, c := range all {
		if strings.HasPrefix(c.Body, commentMarker) && (found.ID == 0 || c.ID < found.ID) {
			found = c
		}
	}
	return found, found.ID != 0, nil
}

package sweep

import (
	"context"
	"fmt"
	"time"

	"github.com/alexei-led/archfit-app/internal/github/api"
)

// t0 has an hour index divisible by 3, so a three-installation pass starts at index 0.
var t0 = time.Unix(1782000000, 0).UTC()

const (
	day          = 24 * time.Hour
	marker       = "MARKER"
	workflowPath = ".github/workflows/archfit.yaml"
)

type fakeActions struct {
	api.Actions
	runs          map[int64]*api.WorkflowRun
	runErrs       map[int64]error
	rerunErrs     map[int64]error
	runCalls      []int64
	rerunCalls    []int64 // every request, failed ones included
	headRunsErr   error
	headRunsCalls []string                  // heads, in call order
	compares      map[string]api.Comparison // by "<base>..<head>"; an unscripted compare is an error
	compareErrs   map[string]error
	compareCalls  []string
}

func (f *fakeActions) Run(_ context.Context, _ api.RepoRef, runID int64) (api.WorkflowRun, error) {
	f.runCalls = append(f.runCalls, runID)
	if err := f.runErrs[runID]; err != nil {
		return api.WorkflowRun{}, err
	}
	run, ok := f.runs[runID]
	if !ok {
		return api.WorkflowRun{}, fmt.Errorf("run %d: %w", runID, api.ErrNotFound)
	}
	return *run, nil
}

// HeadRuns lists the runs of the map whose head and workflow are these, as GitHub lists a head's runs.
func (f *fakeActions) HeadRuns(_ context.Context, _ api.RepoRef, workflowPath, headSHA string) ([]api.WorkflowRun, error) {
	f.headRunsCalls = append(f.headRunsCalls, headSHA)
	if f.headRunsErr != nil {
		return nil, f.headRunsErr
	}
	var out []api.WorkflowRun
	for _, run := range f.runs {
		if run.HeadSHA == headSHA && run.WorkflowPath == workflowPath {
			out = append(out, *run)
		}
	}
	return out, nil
}

func (f *fakeActions) Compare(_ context.Context, _ api.RepoRef, base, head string) (api.Comparison, error) {
	key := base + ".." + head
	f.compareCalls = append(f.compareCalls, key)
	if err := f.compareErrs[key]; err != nil {
		return api.Comparison{}, err
	}
	c, ok := f.compares[key]
	if !ok {
		return api.Comparison{}, fmt.Errorf("fake: unscripted compare %s", key)
	}
	return c, nil
}

// Rerun behaves like GitHub: a successful request queues the next attempt.
func (f *fakeActions) Rerun(_ context.Context, _ api.RepoRef, runID int64) error {
	f.rerunCalls = append(f.rerunCalls, runID)
	if err := f.rerunErrs[runID]; err != nil {
		return err
	}
	run := f.runs[runID]
	run.Attempt++
	run.Status = "queued"
	return nil
}

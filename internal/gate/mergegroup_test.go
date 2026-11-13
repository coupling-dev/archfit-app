package gate

import (
	"context"
	"slices"

	"github.com/alexei-led/archfit-app/internal/github/api"
)

func (f *fakeGitHub) MergeQueue(_ context.Context, r api.RepoRef, branch string) ([]api.QueueEntry, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if err := f.enter("MergeQueue", r); err != nil {
		return nil, err
	}
	return slices.Clone(f.queue[branchKey(r.FullName, branch)]), nil
}

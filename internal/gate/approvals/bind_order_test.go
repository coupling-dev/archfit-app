package approvals

import (
	"context"
	"errors"
	"testing"

	"github.com/coupling-dev/archfit-app/internal/gate/records"
	"github.com/coupling-dev/archfit-app/internal/github/api"
)

// scripted fails the GitHub reads a test names, and answers the rest from the hub.
type scripted struct {
	*hub
	pull, tip error
	file      func(ref, path string) error
}

func (s scripted) Pull(ctx context.Context, r api.RepoRef, number int) (api.Pull, error) {
	if s.pull != nil {
		return api.Pull{}, s.pull
	}
	return s.hub.Pull(ctx, r, number)
}

// PullSummary fails the same way Pull does: the owner's reads are summaries, and the script must reach them.
func (s scripted) PullSummary(ctx context.Context, r api.RepoRef, number int) (api.Pull, error) {
	return s.Pull(ctx, r, number)
}

func (s scripted) BranchHead(ctx context.Context, r api.RepoRef, branch string) (string, error) {
	if s.tip != nil {
		return "", s.tip
	}
	return s.hub.BranchHead(ctx, r, branch)
}

func (s scripted) File(ctx context.Context, r api.RepoRef, ref, path string) ([]byte, string, error) {
	if s.file != nil {
		if err := s.file(ref, path); err != nil {
			return nil, "", err
		}
	}
	return s.hub.File(ctx, r, ref, path)
}

// The binding reads a pull request and the default branch together, then the files; a refusal or a failed read is
// the one the sequential reads gave first, whichever read finished first.
func TestBindingRefusesWithTheFirstFailureInReadOrder(t *testing.T) {
	errPull := errors.New("pull read failed")
	errTip := errors.New("branch read failed")
	errPolicy := errors.New("head policy read failed")
	errWorkflow := errors.New("head workflow read failed")
	errCurrent := errors.New("default branch policy read failed")
	errTipWorkflow := errors.New("default branch workflow read failed")
	onHead := func(path string, err error) func(string, string) error {
		return func(ref, p string) error {
			if ref == "h1" && p == path {
				return err
			}
			return nil
		}
	}
	onTip := func(path string, err error) func(string, string) error {
		return func(ref, p string) error {
			if ref == "c0" && p == path {
				return err
			}
			return nil
		}
	}
	const first, workflow = records.KindFirst, records.KindWorkflow
	for _, tt := range []struct {
		name   string
		kind   string
		pull   int
		want   error
		broken func(w *world)
		read   scripted
	}{
		{"pull read fails before the branch read", first, 12, errPull, nil, scripted{pull: errPull, tip: errTip}},
		{"a closed pull request is refused before the branch read", first, 12, ErrNotApprovable,
			func(w *world) {
				w.gh.pulls[12] = api.Pull{Number: 12, State: "closed", HeadSHA: "h1", HeadRepo: testRepo.FullName, BaseRef: "main"}
			},
			scripted{tip: errTip}},
		{"branch read fails before the head files", first, 12, errTip, nil, scripted{tip: errTip, file: onHead(PolicyPath, errPolicy)}},
		{"head policy read fails before the head workflow", first, 12, errPolicy, nil,
			scripted{file: func(ref, p string) error {
				if ref == "h1" && p == PolicyPath {
					return errPolicy
				}
				return onHead(WorkflowPath, errWorkflow)(ref, p)
			}}},
		{"a missing head workflow is refused before the default branch policy is read", first, 12, ErrNotApprovable,
			func(w *world) { delete(w.gh.workflows, "h1") }, scripted{file: onTip(PolicyPath, errCurrent)}},
		{"default branch policy read fails", first, 12, errCurrent, nil, scripted{file: onTip(PolicyPath, errCurrent)}},
		{"default branch workflow read fails", workflow, 12, errTipWorkflow, nil, scripted{file: onTip(WorkflowPath, errTipWorkflow)}},
		{"a missing head workflow is refused before the default branch workflow is read", workflow, 12, ErrNotApprovable,
			func(w *world) { delete(w.gh.workflows, "h1") }, scripted{file: onTip(WorkflowPath, errTipWorkflow)}},
	} {
		t.Run(tt.name, func(t *testing.T) {
			w := newWorld(t)
			w.gh.bare("c0")
			w.openPull(12, "h1", policyV(1), "wf-1")
			if tt.kind == workflow {
				w.approvedAt(digestV(0), "c0")
			}
			if tt.broken != nil {
				tt.broken(w)
			}
			read := tt.read
			read.hub = w.gh
			svc := New(read, w.mem)
			_, err := svc.Bind(context.Background(), testRepo, Action{Kind: tt.kind, Pull: tt.pull})
			if !errors.Is(err, tt.want) {
				t.Fatalf("bind = %v, want %v", err, tt.want)
			}
		})
	}
}

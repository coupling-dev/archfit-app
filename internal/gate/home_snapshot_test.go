package gate

import (
	"context"
	"errors"
	"fmt"
	"testing"

	"github.com/coupling-dev/archfit-app/internal/github/api"
)

type snapshotFiles struct {
	tips, reads int
	readErr     error
	raw         string
}

func (g *snapshotFiles) BranchHead(context.Context, api.RepoRef, string) (string, error) {
	g.tips++
	return fmt.Sprintf("tip-%d", g.tips), nil
}
func (g *snapshotFiles) File(context.Context, api.RepoRef, string, string) ([]byte, string, error) {
	g.reads++
	return []byte(g.raw), "", g.readErr
}

func TestHomeSnapshotIsRequestAndRepositoryScoped(t *testing.T) {
	gh := &snapshotFiles{raw: "version: 3\nowners: [alice]\nmodules:\n  app:\n    paths: [cmd/**]\n"}
	r := api.Repo{RepoRef: api.RepoRef{InstallationID: 1, FullName: "acme/app"}, DefaultBranch: "main"}
	ctx, _ := WithHomeSnapshot(t.Context(), r)
	first, err := ReadHomeSnapshot(ctx, gh, r)
	if err != nil {
		t.Fatal(err)
	}
	one := first.Policy(ctx, gh)
	again, err := ReadHomeSnapshot(ctx, gh, r)
	if err != nil {
		t.Fatal(err)
	}
	two := again.Policy(ctx, gh)
	if gh.tips != 1 || gh.reads != 1 || one.Digest != two.Digest || first.Tip() != again.Tip() {
		t.Fatal("protected inputs were reread")
	}
	nextCtx, _ := WithHomeSnapshot(t.Context(), r)
	next, err := ReadHomeSnapshot(nextCtx, gh, r)
	if err != nil || next.Tip() == first.Tip() {
		t.Fatal("a new request reused a previous tip")
	}
	other := r
	other.InstallationID = 2
	isolated, err := ReadHomeSnapshot(ctx, gh, other)
	if err != nil || isolated.Tip() == first.Tip() {
		t.Fatal("another tenant reused the snapshot")
	}
}

func TestHomeSnapshotPreservesReadAndDecodeFailures(t *testing.T) {
	for _, failure := range []error{api.ErrNotFound, api.ErrRateLimited, context.DeadlineExceeded} {
		t.Run(failure.Error(), func(t *testing.T) {
			gh := &snapshotFiles{readErr: failure}
			snapshot, err := ReadHomeSnapshot(t.Context(), gh, api.Repo{})
			if err != nil {
				t.Fatal(err)
			}
			got := snapshot.Policy(t.Context(), gh)
			if !errors.Is(got.ReadErr, failure) {
				t.Fatalf("read error = %v", got.ReadErr)
			}
			if got.DecodeErr == nil {
				t.Fatal("missing bytes became valid policy")
			}
		})
	}
}

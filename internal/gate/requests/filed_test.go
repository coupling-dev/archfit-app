package requests

import (
	"context"
	"testing"

	"github.com/alexei-led/archfit-app/internal/gate/records"
	"github.com/alexei-led/archfit-app/internal/github/api"
)

type filedGitHub struct {
	*previewGH
	comments []string
}

func (g *filedGitHub) CollaboratorPermission(context.Context, api.RepoRef, string) (string, error) {
	return "write", nil
}
func (g *filedGitHub) CreateComment(_ context.Context, _ api.RepoRef, _ int, body string) (api.Comment, error) {
	g.comments = append(g.comments, body)
	return api.Comment{}, nil
}
func (g *filedGitHub) SetAssigneesAndLabels(context.Context, api.RepoRef, int, []string, []string) ([]string, []string, error) {
	return nil, nil, nil
}

func TestFiledUnprotectedCan(t *testing.T) {
	w := newPreviewWorld(t)
	gh := &filedGitHub{previewGH: w.gh}
	svc := New(gh, w.mem, nil)
	record := "pkg_tc -> pkg_util: graph: not an allowed dependency\n  balance: 9/10 (min 7)\n  next: request\npolicy: not protected\n"
	reaction, err := svc.Filed(context.Background(), Issue{Repo: previewRepo, Number: 77, Author: "writer", Today: testToday,
		Body: "/archfit request why\n\n```text\n" + record + "```\n"})
	if err != nil || reaction != ReactionFiled {
		t.Fatalf("Filed = %q, %v; comments %q", reaction, err, gh.comments)
	}
	var req records.Request
	if err := records.Load(context.Background(), w.mem, tenant(previewRepo), records.RequestKey(previewRepo.FullName, 77), &req); err != nil {
		t.Fatal(err)
	}
	if req.Key != graphKey || req.Record != record || req.Numbers.Verified || req.Numbers.Balance == nil || req.Numbers.Balance.Score != 9 {
		t.Errorf("stored request = %+v", req)
	}
}

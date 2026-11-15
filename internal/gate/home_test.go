package gate

import (
	"context"
	"errors"
	"testing"

	"github.com/alexei-led/archfit-app/internal/gate/records"
	"github.com/alexei-led/archfit-app/internal/github/api"
	"github.com/alexei-led/archfit-app/internal/store"
	"github.com/alexei-led/archfit-app/internal/wire/policy3"
)

type homeFiles struct {
	reads []string
}

func (g *homeFiles) BranchHead(context.Context, api.RepoRef, string) (string, error) {
	g.reads = append(g.reads, "tip")
	return "immutable-tip", nil
}
func (g *homeFiles) File(_ context.Context, _ api.RepoRef, ref, path string) ([]byte, string, error) {
	g.reads = append(g.reads, ref+":"+path)
	if path == homePolicyPath {
		return []byte("version: 3\nowners: [alice]\nmodules:\n  app:\n    paths: [cmd/**]\n"), "", nil
	}
	return nil, "", api.ErrNotFound
}
func (*homeFiles) OpenPulls(context.Context, api.RepoRef) ([]api.Pull, error) { return nil, nil }

func TestHomeFacadeRefusesUnadmittedRepositoriesBeforeReading(t *testing.T) {
	s := store.NewMemory()
	gh := &homeFiles{}
	h := NewHome(gh, s, NewAdmissions(s, []string{"acme"}))
	_, err := h.Home(t.Context(), api.Repo{RepoRef: api.RepoRef{InstallationID: 1, FullName: "outsider/app"}, DefaultBranch: "main"})
	if !errors.Is(err, api.ErrNotFound) {
		t.Fatalf("Home = %v", err)
	}
	if len(gh.reads) != 0 {
		t.Fatalf("read unadmitted repository: %v", gh.reads)
	}
}

func TestHomeFacadeReadsThePolicyAtTheProtectedTip(t *testing.T) {
	s := store.NewMemory()
	gh := &homeFiles{}
	raw, _, _ := gh.File(t.Context(), api.RepoRef{}, "immutable-tip", homePolicyPath)
	value, err := records.Encode(&records.Repo{ApprovedDigest: policy3.Digest(raw)})
	if err != nil {
		t.Fatal(err)
	}
	if err := s.Put(t.Context(), 1, records.RepoKey("acme/app"), value); err != nil {
		t.Fatal(err)
	}
	gh.reads = nil
	h := NewHome(gh, s, NewAdmissions(s, []string{"acme"}))
	out, err := h.Home(t.Context(), api.Repo{RepoRef: api.RepoRef{InstallationID: 1, FullName: "acme/app"}, DefaultBranch: "main"})
	if err != nil {
		t.Fatal(err)
	}
	if out.Tip != "immutable-tip" || out.OutsideFlow || out.DriftKnown || len(out.Policy.Modules) != 1 {
		t.Fatalf("Home = %+v", out)
	}
	for _, ref := range gh.reads {
		if ref == "main:"+homePolicyPath {
			t.Fatal("read a moving policy ref")
		}
	}
}

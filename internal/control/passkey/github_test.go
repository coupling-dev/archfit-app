package passkey

import (
	"context"
	"errors"
	"slices"
	"testing"

	"github.com/alexei-led/archfit-app/internal/github/api"
)

const (
	schema3Policy  = "version: 3\nowners: [alice, Carol]\nmodules:\n  app:\n    paths: [cmd/**, pkg/**]\n"
	archfit3Policy = "version: 2\nmodules:\n  a:\n    layer: core\n    paths: [\"pkg/a/**\"]\n"
)

type fakeFiles struct {
	content []byte
	err     error
	got     []string // "repo@ref:path" of every read
}

func (f *fakeFiles) File(_ context.Context, r api.RepoRef, ref, path string) ([]byte, string, error) {
	f.got = append(f.got, r.FullName+"@"+ref+":"+path)
	return f.content, "blob", f.err
}

func TestOwnersComeFromThePolicyOnTheDefaultBranch(t *testing.T) {
	githubDown := errors.New("github down")
	for _, tc := range []struct {
		name          string
		files         fakeFiles
		wantOwners    []string
		wantHasPolicy bool
		wantErr       error
	}{
		{"a schema 3 policy", fakeFiles{content: []byte(schema3Policy)}, []string{"alice", "Carol"}, true, nil},
		{"no policy", fakeFiles{err: api.ErrNotFound}, nil, false, nil},
		{"an Archfit 3 policy", fakeFiles{content: []byte(archfit3Policy)}, nil, false, nil},
		{"a schema 3 policy that does not decode", fakeFiles{content: []byte("version: 3\nowners: []\n")}, nil, false, ErrPolicy},
		{"a schema 3 policy with Archfit 3 keys only", fakeFiles{content: []byte("version: 3\nmodules:\n  app:\n    paths: [x]\n")}, nil, false, ErrPolicy},
		{"an Archfit 3 policy with a wrong key", fakeFiles{content: []byte(archfit3Policy + "owners: [mallory]\n")}, nil, false, ErrPolicy},
		{"not YAML", fakeFiles{content: []byte(":\n\t- [")}, nil, false, ErrPolicy},
		{"GitHub fails", fakeFiles{err: githubDown}, nil, false, githubDown},
	} {
		t.Run(tc.name, func(t *testing.T) {
			files := tc.files
			owners, hasPolicy, err := NewOwners(&files).Owners(ctx, repo)
			if !errors.Is(err, tc.wantErr) || hasPolicy != tc.wantHasPolicy || !slices.Equal(owners, tc.wantOwners) {
				t.Fatalf("Owners = %v, %v, %v; want %v, %v, %v", owners, hasPolicy, err, tc.wantOwners, tc.wantHasPolicy, tc.wantErr)
			}
			if want := []string{"Acme/App@main:.archfit.yaml"}; !slices.Equal(files.got, want) {
				t.Errorf("read %v, want %v", files.got, want)
			}
		})
	}

	noBranch := repo
	noBranch.DefaultBranch = ""
	if _, _, err := NewOwners(&fakeFiles{}).Owners(ctx, noBranch); err == nil {
		t.Error("Owners read a repository without a default branch")
	}
}

type fakeTracker struct {
	createErr, assignErr error
	created              []api.NewIssue
	assigned             map[int][]string
	labels               map[int][]string
}

func (f *fakeTracker) CreateIssue(_ context.Context, _ api.RepoRef, i api.NewIssue) (api.Issue, error) {
	if f.createErr != nil {
		return api.Issue{}, f.createErr
	}
	f.created = append(f.created, i)
	return api.Issue{Number: 40 + len(f.created), Body: i.Body}, nil
}

func (f *fakeTracker) SetAssigneesAndLabels(_ context.Context, _ api.RepoRef, n int, assignees, labels []string) ([]string, []string, error) {
	if f.assignErr != nil {
		return nil, nil, f.assignErr
	}
	f.assigned[n], f.labels[n] = assignees, labels
	return assignees, labels, nil
}

func TestTheIssueIsOpenedThenAssigned(t *testing.T) {
	f := &fakeTracker{assigned: map[int][]string{}, labels: map[int][]string{}}
	if err := NewIssues(f, f).Open(ctx, repo.RepoRef, "title", "body", []string{"alice", "carol"}); err != nil {
		t.Fatal(err)
	}
	if len(f.created) != 1 || f.created[0].Assignee != "" || f.created[0].Title != "title" {
		t.Errorf("created %+v, want one issue with no assignee at creation", f.created)
	}
	if got := f.assigned[41]; !slices.Equal(got, []string{"alice", "carol"}) || f.labels[41] != nil {
		t.Errorf("issue 41: assignees %v, labels %v; want alice and carol, no label", got, f.labels[41])
	}

	for name, f := range map[string]*fakeTracker{
		"create fails": {createErr: errors.New("down"), assigned: map[int][]string{}, labels: map[int][]string{}},
		"assign fails": {assignErr: errors.New("down"), assigned: map[int][]string{}, labels: map[int][]string{}},
	} {
		if err := NewIssues(f, f).Open(ctx, repo.RepoRef, "t", "b", []string{"alice"}); err == nil {
			t.Errorf("%s: Open succeeded", name)
		}
	}
}

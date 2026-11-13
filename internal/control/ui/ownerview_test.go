package ui

import (
	"bytes"
	"encoding/json"
	"net/http"
	"os"
	"path/filepath"
	"strconv"
	"testing"

	"github.com/alexei-led/archfit-app/internal/gate"
	"github.com/alexei-led/archfit-app/internal/wire/policy3"
	"github.com/alexei-led/archfit-app/internal/wire/wording"
)

const ownerPolicy = "version: 3\nowners: [alice]\nmodules:\n  app:\n    paths: [cmd/**]\n"
const firstDiff = "--- a/.archfit.yaml\n+++ b/.archfit.yaml\n@@ -0,0 +1,5 @@\n+version: 3\n+owners: [alice]\n+modules:\n+  app:\n+    paths: [cmd/**]\n"
const changedDiff = "--- a/.archfit.yaml\n+++ b/.archfit.yaml\n@@ -3,3 +3,3 @@\n modules:\n   app:\n-    paths: [cmd/**]\n+    paths: [cmd/**, pkg/**]\n"

func ownerFixture(kind string, pull int, open bool) gate.OwnerView {
	v := gate.OwnerView{Kind: kind, Pull: pull, Digest: policy3.Digest([]byte("fixture-90")), ActionDigest: policy3.Digest([]byte("fixture-91")), Tip: sha(10),
		Files: []gate.OwnerFile{{Role: "current", Ref: sha(10), Path: ".archfit.yaml", Digest: policy3.Digest([]byte(ownerPolicy)), Text: ownerPolicy}},
		Diffs: []gate.OwnerDiff{{Title: "The policy change", Path: ".archfit.yaml", Text: changedDiff}}, Proposals: []gate.OwnerProposal{}}
	switch kind {
	case gate.OwnerFirst:
		v.Status = "unknown: the first policy needs approval."
		v.MainAction = "Approve the first policy"
		v.Note = "There is no approved policy yet. Approving makes every line below the approved policy."
		v.Diffs[0] = gate.OwnerDiff{Title: "The first policy", Path: ".archfit.yaml", Text: firstDiff}
	case gate.OwnerWorkflow:
		v.Status = "unknown: the workflow changed."
		v.MainAction = "Approve the workflow change"
		v.Note = "This approves the policy and workflow at this pull request's head."
		v.Diffs = append([]gate.OwnerDiff{{Title: "The workflow change", Path: ".github/workflows/archfit.yaml",
			Text: "--- a/.github/workflows/archfit.yaml\n+++ b/.github/workflows/archfit.yaml\n@@ -1,1 +1,1 @@\n-name: old\n+name: archfit\n"}}, v.Diffs...)
	case gate.OwnerConfirm:
		v.Status = "unknown: the policy changed outside the flow."
		v.MainAction = "Confirm the policy"
		v.Note = "This makes the current policy the approved policy."
	case gate.OwnerRevert:
		v.Status = "unknown: the policy changed outside the flow."
		v.MainAction = "Revert the policy"
		v.Note = "This restores the approved policy."
		v.Diffs[0].Title = "The policy to restore"
		if open {
			v.Note += " It keeps the approved answers in #12."
			v.Proposals = append(v.Proposals, gate.OwnerProposal{ID: "p1", Pull: 12, Head: sha(13), Digest: policy3.Digest([]byte("fixture-92")),
				Approvals: []gate.OwnerApproval{{ID: "a1", Request: 431, Digest: policy3.Digest([]byte("fixture-93"))}}})
			v.Diffs = append(v.Diffs, gate.OwnerDiff{Title: "Approved answers kept on the approved policy", Path: ".archfit.yaml",
				Text: "--- a/.archfit.yaml\n+++ b/.archfit.yaml\n@@ -3,3 +3,5 @@\n modules:\n   app:\n     paths: [cmd/**]\n+    depends_on:\n+      - util  # request #431\n"})
		}
	}
	if pull > 0 {
		v.Head, v.Base = sha(11), sha(9)
	}
	if kind != gate.OwnerFirst {
		v.Approved = sha(8)
	}
	return v
}

func TestOwnerViewAPIGoldens(t *testing.T) {
	for _, tt := range []struct {
		name, kind string
		pull       int
		open       bool
	}{
		{"first", "first", 0, false}, {"first-pull", "first", 7, false}, {"workflow", "workflow", 7, false},
		{"confirm", "confirm", 0, false}, {"revert", "revert", 0, false}, {"revert-answers", "revert", 0, true},
	} {
		t.Run(tt.name, func(t *testing.T) {
			e := newEnv(t)
			e.owners.view = ownerFixture(tt.kind, tt.pull, tt.open)
			url := ownerAPI + tt.kind + "/view"
			if tt.pull > 0 {
				url += "?pull=" + strconv.Itoa(tt.pull)
			}
			res := e.as("alice", permRead).get(url)
			wantStatus(t, res, http.StatusOK)
			if words := wording.NotUsedIn(res.Body.String()); len(words) > 0 {
				t.Fatalf("view uses %v", words)
			}
			var pretty bytes.Buffer
			if err := json.Indent(&pretty, res.Body.Bytes(), "", "  "); err != nil {
				t.Fatal(err)
			}
			pretty.WriteByte('\n')
			path := filepath.Join("testdata", "owner", tt.name+".json")
			if os.Getenv("UPDATE_GOLDEN") == "1" {
				if err := os.MkdirAll(filepath.Dir(path), 0755); err != nil {
					t.Fatal(err)
				}
				if err := os.WriteFile(path, pretty.Bytes(), 0644); err != nil {
					t.Fatal(err)
				}
			}
			want, err := os.ReadFile(path)
			if err != nil {
				t.Fatal(err)
			}
			if !bytes.Equal(want, pretty.Bytes()) {
				t.Fatalf("differs from %s:\n%s", path, pretty.String())
			}
			if len(e.owners.actions) != 1 || e.owners.actions[0].Kind != tt.kind || e.owners.actions[0].Pull != tt.pull || e.owners.repos[0].FullName != repoName {
				t.Fatalf("wrong scope: %+v, %+v", e.owners.actions, e.owners.repos)
			}
		})
	}
}

func TestOwnerViewAPIHidesRepositoryDataAndRejectsMalformedLinks(t *testing.T) {
	for _, suffix := range []string{"nope/view", "workflow/view", "first/view?pull=0", "first/view?pull=01", "first/view?pull=-1",
		"first/view?pull=1&pull=2", "first/view?pull=x", "first/view?pull=", "confirm/view?pull=7", "revert/view?pull=7"} {
		t.Run(suffix, func(t *testing.T) {
			e := newEnv(t)
			wantStatus(t, e.as("alice", permRead).get(ownerAPI+suffix), http.StatusNotFound)
			if len(e.owners.actions) > 0 {
				t.Fatal("invalid link read owner data")
			}
		})
	}
	e := newEnv(t)
	wantStatus(t, e.anonymous().get(ownerAPI+"first/view"), http.StatusUnauthorized)
	wantStatus(t, e.signIn("mallory").get(ownerAPI+"first/view"), http.StatusNotFound)
	if len(e.owners.actions) > 0 {
		t.Fatal("unauthorized view read")
	}
}

func TestOwnerViewAPIExplainsUnreadableFiles(t *testing.T) {
	e := newEnv(t)
	e.owners.err = gate.ErrOwnerFileUnreadable
	res := e.as("alice", permRead).get(ownerAPI + "revert/view")
	wantStatus(t, res, http.StatusConflict)
	var body errorJSON
	if err := json.Unmarshal(res.Body.Bytes(), &body); err != nil {
		t.Fatal(err)
	}
	if body.Error != "This file cannot be displayed, so it cannot be approved here. Use the existing owner flow on the GitHub issue." {
		t.Fatalf("error = %q", body.Error)
	}
	if words := wording.NotUsedIn(body.Error); len(words) > 0 {
		t.Fatalf("error uses %v", words)
	}
}

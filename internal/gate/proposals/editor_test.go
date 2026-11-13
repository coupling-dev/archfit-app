package proposals

import (
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/alexei-led/archfit-app/internal/gate/records"
	"github.com/alexei-led/archfit-app/internal/gate/requests"
	"github.com/alexei-led/archfit-app/internal/github/api"
	"github.com/alexei-led/archfit-app/internal/wire/policy3"
)

func TestRealEditPreviewValidatesBeforeDigestOrWrites(t *testing.T) {
	w := newWorld(t)
	in := EditInput{File: []byte("version: 3\nowners: []\nmodules: {}\n"), Base: policy3.Digest([]byte(testPolicy)), Owner: olivia}
	got, err := w.svc.EditPreview(context.Background(), testRepo, in)
	if err != nil || len(got.Errors) != 2 || got.Digest != "" || got.Target != "" || len(w.hub.writes()) > 0 || len(w.redeemed) > 0 {
		t.Fatalf("invalid preview %+v %v writes=%v", got, err, w.hub.writes())
	}
	for _, line := range got.Errors {
		if !strings.HasPrefix(line, ".archfit.yaml:") {
			t.Fatal(line)
		}
	}
}

func TestEditorIgnoresAnUnrecordedPolicyPull(t *testing.T) {
	w := newWorld(t)
	head := w.hub.push(fakeBase, "README.md", "code")
	w.hub.branches[Branch] = head
	w.hub.push(Branch, policyPath, testPolicy+"# unrecorded\n")
	w.hub.pulls[12] = &api.Pull{Number: 12, State: "open", HeadRef: Branch, HeadRepo: testRepo.FullName, BaseRef: fakeBase, HeadSHA: w.hub.branches[Branch]}
	got, err := w.svc.Editor(context.Background(), testRepo)
	if err != nil || string(got.File) != testPolicy || got.Recovery {
		t.Fatalf("editor %+v %v", got, err)
	}
}

func TestExplicitReloadRepairsAnUnrecordedWriteWithoutReusingApproval(t *testing.T) {
	w := newWorld(t)
	w.request(431, "graph", "pkg_tc", "pkg_util")
	w.mustAnswer(olivia, 431, requests.Answer{Kind: policy3.Approve}, true)
	recorded := w.hub.file(Branch, policyPath)
	w.hub.push(Branch, policyPath, recorded+"# failed write\n")
	got, err := w.svc.Editor(context.Background(), testRepo)
	if err != nil || string(got.File) != recorded || !got.Recovery {
		t.Fatalf("editor %+v %v", got, err)
	}
	writes := len(w.hub.writes())
	_, err = w.svc.EditPreview(context.Background(), testRepo, EditInput{File: []byte(recorded + "# edit\n"), Base: got.Digest, Owner: olivia})
	if !errors.Is(err, ErrEditChanged) || len(w.hub.writes()) != writes {
		t.Fatal("stale preview repaired implicitly", err)
	}
	got, err = w.svc.ReloadEditor(context.Background(), testRepo, olivia)
	if err != nil || got.Recovery || string(got.File) != recorded || len(w.hub.merges) > 0 || len(w.redeemed) != 1 {
		t.Fatalf("reload %+v %v", got, err)
	}
	out, err := w.svc.Edit(context.Background(), testRepo, w.editInput(recorded, recorded+"# fresh edit\n", false))
	if err != nil || out.State != requests.PullMerged {
		t.Fatalf("fresh edit %+v %v", out, err)
	}
}

func TestOnlyAnOwnerCanRecoverTheEditor(t *testing.T) {
	w := newWorld(t)
	_, err := w.svc.ReloadEditor(context.Background(), testRepo, Owner{ID: 900, Login: "mallory"})
	if !errors.Is(err, ErrNotOwner) || len(w.hub.writes()) > 0 {
		t.Fatal("unauthorized recovery", err)
	}
}

func TestEditCleansClosedProposalsAfterValidRedemption(t *testing.T) {
	w := newWorld(t)
	w.request(431, "graph", "pkg_tc", "pkg_util")
	first := w.mustAnswer(olivia, 431, requests.Answer{Kind: policy3.Approve}, true)
	w.hub.pulls[first.PullRequest].State = "closed"
	_, err := w.svc.Edit(context.Background(), testRepo, w.editInput(testPolicy, testPolicy+"# new edit\n", true))
	if err != nil {
		t.Fatal(err)
	}
	if w.requestState(431) != records.StateOpen || !w.hub.issues[431] {
		t.Fatal("closed proposal stranded its request")
	}
	a := load[records.Approval](t, w.store, records.ApprovalKey(testRepo.FullName, first.Approval))
	if a.State != records.StateVoid {
		t.Fatal(a)
	}
}

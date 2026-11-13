package proposals

import (
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/alexei-led/archfit-app/internal/gate/records"
	"github.com/alexei-led/archfit-app/internal/gate/requests"
	"github.com/alexei-led/archfit-app/internal/wire/policy3"
)

func (w *world) editInput(base, file string, later bool) EditInput {
	w.t.Helper()
	in := EditInput{File: []byte(file), Base: policy3.Digest([]byte(base)), Owner: olivia, Later: later}
	v, err := w.svc.EditPreview(context.Background(), testRepo, in)
	if err != nil {
		w.t.Fatal(err)
	}
	in.Target = v.Target
	token := "edit-token"
	w.tokens[token] = v.Digest
	in.Redeem = w.redeem(token)
	return in
}

func TestEditApprovedAndMerged(t *testing.T) {
	w := newWorld(t)
	file := testPolicy + "# owner edit\n"
	in := w.editInput(testPolicy, file, false)
	out, err := w.svc.Edit(context.Background(), testRepo, in)
	if err != nil || out.State != requests.PullMerged {
		t.Fatalf("edit = %+v, %v", out, err)
	}
	if got := w.hub.file(fakeBase, policyPath); got != file {
		t.Fatalf("merged file = %s", got)
	}
	a := load[records.Approval](t, w.store, records.ApprovalKey(testRepo.FullName, out.Approval))
	if a.Kind != records.KindEdit || a.Request != 0 || a.Login != "olivia" || a.Credential != "cred-edit-token" {
		t.Fatalf("approval %+v", a)
	}
	note := w.hub.lastComment(out.PullRequest)
	if !strings.Contains(note, "olivia") || !strings.Contains(note, "passkey") {
		t.Errorf("comment %q", note)
	}
	if w.proposalOf(out).ExpectedDigest != policy3.Digest([]byte(file)) {
		t.Error("wrong pinned digest")
	}
}

func TestEditJoinsLastAfterMergeLater(t *testing.T) {
	w := newWorld(t)
	w.request(431, "graph", "pkg_tc", "pkg_util")
	first := w.mustAnswer(olivia, 431, requests.Answer{Kind: policy3.Approve}, true)
	base := w.hub.file(Branch, policyPath)
	file := base + "# edit last\n"
	out, err := w.svc.Edit(context.Background(), testRepo, w.editInput(base, file, false))
	if err != nil || out.PullRequest != first.PullRequest || out.State != requests.PullMerged {
		t.Fatalf("edit %+v, %v", out, err)
	}
	if w.hub.file(fakeBase, policyPath) != file {
		t.Error("earlier answer or edit lost")
	}
	p := w.proposalOf(out)
	if len(p.Approvals) != 2 || p.Approvals[1] != out.Approval {
		t.Errorf("order %+v", p)
	}
}

func TestEditRefusesBeforeRedemptionOrWrites(t *testing.T) {
	for _, name := range []string{"decode", "no passkey", "another digest", "used token", "old target", "proposal base", "moved preview", "not owner"} {
		t.Run(name, func(t *testing.T) {
			w := newWorld(t)
			in := w.editInput(testPolicy, testPolicy+"# edit\n", false)
			want := ErrEditChanged
			switch name {
			case "decode":
				in.File = []byte("version: 3\nowners: []\nmodules: {}\n")
				want = ErrEditPolicy
			case "no passkey":
				in.Redeem = nil
				want = ErrNeedsPasskey
			case "another digest":
				in.File = []byte(testPolicy + "# different\n")
				want = errToken
			case "used token":
				_, _ = in.Redeem(context.Background(), policy3.Digest(in.File))
				want = errToken
			case "old target":
				w.hub.push(fakeBase, policyPath, testPolicy+"# new\n")
				w.approveTip()
			case "proposal base":
				w.request(431, "graph", "pkg_tc", "pkg_util")
				w.mustAnswer(olivia, 431, requests.Answer{Kind: policy3.Approve}, true)
			case "moved preview":
				w.hub.push(fakeBase, "README.md", "new readme")
			case "not owner":
				in.Owner = Owner{ID: 900, Login: "mallory"}
				want = ErrNotOwner
			}
			before := len(w.hub.writes())
			redeemed := len(w.redeemed)
			_, err := w.svc.Edit(context.Background(), testRepo, in)
			if !errors.Is(err, want) {
				t.Fatalf("err = %v, want %v", err, want)
			}
			if len(w.hub.writes()) != before || len(w.redeemed) != redeemed {
				t.Errorf("refusal wrote or redeemed: %v", w.hub.writes()[before:])
			}
		})
	}
}

func TestTargetMovesWhileWritingEdit(t *testing.T) {
	w := newWorld(t)
	in := w.editInput(testPolicy, testPolicy+"# edit\n", false)
	w.hub.onCommit = func() {
		h := w.hub
		old := h.branches[fakeBase]
		c := h.commits[old]
		h.branches[fakeBase] = h.put(old, "code-only target move", c.files)
	}
	out, err := w.svc.Edit(context.Background(), testRepo, in)
	if err != nil || out.State != records.StateVoid || w.hub.count("MergePull") != 0 {
		t.Fatalf("edit %+v %v", out, err)
	}
	if a := load[records.Approval](t, w.store, records.ApprovalKey(testRepo.FullName, out.Approval)); a.State != records.StateVoid {
		t.Fatal(a)
	}
	if !strings.Contains(w.hub.lastComment(out.PullRequest), "the policy changed; reload") {
		t.Fatal("no reload notice")
	}
}

func TestConcurrentEditVoidCannotBeRepinnedByAnAnswer(t *testing.T) {
	w := newWorld(t)
	out, err := w.svc.Edit(context.Background(), testRepo, w.editInput(testPolicy, testPolicy+"# old edit\n", true))
	if err != nil {
		t.Fatal(err)
	}
	w.request(432, "graph", "pkg_tc", "pkg_util")
	w.hub.onCommit = func() {
		if _, _, err := records.Void(context.Background(), w.store, tenant(testRepo), testRepo.FullName, out.Approval); err != nil {
			t.Fatal(err)
		}
	}
	_, err = w.answer(olivia, 432, requests.Answer{Kind: policy3.Approve}, false)
	if !errors.Is(err, records.ErrMoved) {
		t.Fatalf("stale writer = %v", err)
	}
	p := w.proposalOf(out)
	if !p.EditVoided || p.EditEpoch != 1 || w.requestState(432) != records.StateOpen || len(w.hub.merges) > 0 {
		t.Fatalf("stale writer repinned: %+v", p)
	}
}

func TestEditSizeLimitPrecedesDecoding(t *testing.T) {
	w := newWorld(t)
	_, err := w.svc.EditPreview(context.Background(), testRepo, EditInput{File: []byte(strings.Repeat("x", MaxEditBytes+1))})
	if !errors.Is(err, ErrEditSize) {
		t.Fatal(err)
	}
	if len(w.redeemed) > 0 || len(w.hub.writes()) > 0 {
		t.Fatal("oversize edit acted")
	}
}

func TestMovedTargetVoidsEditNeverReplaysIt(t *testing.T) {
	w := newWorld(t)
	out, err := w.svc.Edit(context.Background(), testRepo, w.editInput(testPolicy, testPolicy+"# do not replay\n", true))
	if err != nil {
		t.Fatal(err)
	}
	w.hub.push(fakeBase, "README.md", "target moved")
	w.request(432, "graph", "pkg_tc", "pkg_util")
	w.mustAnswer(olivia, 432, requests.Answer{Kind: policy3.Approve}, true)
	a := load[records.Approval](t, w.store, records.ApprovalKey(testRepo.FullName, out.Approval))
	if a.State != records.StateVoid {
		t.Errorf("edit state %s", a.State)
	}
	if strings.Contains(w.hub.file(Branch, policyPath), "do not replay") {
		t.Error("stale edit replayed")
	}
	if w.hub.issues[0] {
		t.Error("request 0 reopened")
	}
}

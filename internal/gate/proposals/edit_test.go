package proposals

import (
	"context"
	"errors"
	"maps"
	"strings"
	"testing"

	"github.com/coupling-dev/archfit-app/internal/gate/records"
	"github.com/coupling-dev/archfit-app/internal/gate/requests"
	"github.com/coupling-dev/archfit-app/internal/wire/policy3"
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
				in.File = []byte("version: 1\nowners: []\nmodules: {}\n")
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

func TestTargetPolicyMovesWhileWritingEdit(t *testing.T) {
	w := newWorld(t)
	in := w.editInput(testPolicy, testPolicy+"# edit\n", false)
	w.hub.onCommit = func() {
		h := w.hub
		old := h.branches[fakeBase]
		files := maps.Clone(h.commits[old].files)
		files[policyPath] = []byte(testPolicy + "# main\n")
		h.branches[fakeBase] = h.put(old, "policy target move", files)
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
	w.hub.push(fakeBase, policyPath, testPolicy+"# main\n")
	w.approveTip()
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

func TestEditUnchangedAgainstOpenProposalIsRefused(t *testing.T) {
	w := newWorld(t)
	first, err := w.svc.Edit(context.Background(), testRepo, w.editInput(testPolicy, testPolicy+"# first\n", true))
	if err != nil {
		t.Fatal(err)
	}
	head := w.hub.file(Branch, policyPath)
	in := w.editInput(head, head, false)
	before, redeemed, comments := len(w.hub.writes()), len(w.redeemed), len(w.hub.comments[first.PullRequest])
	_, err = w.svc.Edit(context.Background(), testRepo, in)
	if !errors.Is(err, ErrEditUnchanged) || err.Error() != "Nothing changed against the open policy pull request." {
		t.Fatalf("err = %v", err)
	}
	if len(w.hub.writes()) != before || len(w.redeemed) != redeemed || len(w.hub.comments[first.PullRequest]) != comments {
		t.Error("an unchanged edit wrote, redeemed or commented")
	}
	if p := w.proposalOf(first); len(p.Approvals) != 1 {
		t.Errorf("approvals %v", p.Approvals)
	}
}

func TestEditPreviewDiffsAgainstOpenProposal(t *testing.T) {
	w := newWorld(t)
	v, err := w.svc.EditPreview(context.Background(), testRepo, EditInput{File: []byte(testPolicy + "# a\n"),
		Base: policy3.Digest([]byte(testPolicy)), Owner: olivia})
	if err != nil || v.Against != AgainstDefaultBranch || !strings.Contains(v.Diff, "+# a") {
		t.Fatalf("no proposal: %+v, %v", v, err)
	}
	if _, err := w.svc.Edit(context.Background(), testRepo, w.editInput(testPolicy, testPolicy+"# first\n", true)); err != nil {
		t.Fatal(err)
	}
	head := w.hub.file(Branch, policyPath)
	v, err = w.svc.EditPreview(context.Background(), testRepo, EditInput{File: []byte(head), Base: policy3.Digest([]byte(head)), Owner: olivia})
	if err != nil || v.Against != AgainstProposal || v.Diff != "" {
		t.Fatalf("unchanged against the proposal: %+v, %v", v, err)
	}
	v, err = w.svc.EditPreview(context.Background(), testRepo, EditInput{File: []byte(head + "# second\n"),
		Base: policy3.Digest([]byte(head)), Owner: olivia})
	if err != nil || strings.Contains(v.Diff, "+# first") || !strings.Contains(v.Diff, "+# second") {
		t.Fatalf("diff against the proposal: %+v, %v", v, err)
	}
}

func TestOpenProposalApprovedAsItIsAfterMainMoved(t *testing.T) {
	w := newWorld(t)
	w.hub.strict = true
	first, err := w.svc.Edit(context.Background(), testRepo, w.editInput(testPolicy, testPolicy+"# first\n", true))
	if err != nil {
		t.Fatal(err)
	}
	w.hub.push(fakeBase, policyPath, testPolicy+"# main\n")
	w.approveTip()
	view, err := w.svc.Editor(context.Background(), testRepo)
	if err != nil {
		t.Fatal(err)
	}
	if view.PullRequest != first.PullRequest || !view.Voided || view.Foreign || !view.Recovery ||
		!strings.Contains(view.Diff, "+# first") || !strings.Contains(view.Lost, "-# first") {
		t.Fatalf("view %+v", view)
	}
	head := string(view.File)
	in := w.editInput(head, head, false)
	in.AsIs = true
	out, err := w.svc.Edit(context.Background(), testRepo, in)
	if err != nil || out.State != requests.PullMerged || out.PullRequest != first.PullRequest {
		t.Fatalf("approve as it is: %+v, %v", out, err)
	}
	if got := w.hub.file(fakeBase, policyPath); got != head {
		t.Errorf("merged %q", got)
	}
	if a := load[records.Approval](t, w.store, records.ApprovalKey(testRepo.FullName, first.Approval)); a.State != records.StateVoid {
		t.Errorf("the stale edit stays %s", a.State)
	}
	if note := w.hub.lastComment(out.PullRequest); !strings.Contains(note, "approved again as it is") {
		t.Errorf("note %q", note)
	}
}

func TestApproveAsItIsNeedsTheOpenProposalsFile(t *testing.T) {
	w := newWorld(t)
	in := w.editInput(testPolicy, testPolicy, false)
	in.AsIs = true
	if _, err := w.svc.Edit(context.Background(), testRepo, in); !errors.Is(err, ErrEditChanged) {
		t.Fatalf("no proposal: %v", err)
	}
	if _, err := w.svc.Edit(context.Background(), testRepo, w.editInput(testPolicy, testPolicy+"# first\n", true)); err != nil {
		t.Fatal(err)
	}
	head := w.hub.file(Branch, policyPath)
	in = w.editInput(head, head+"# more\n", false)
	in.AsIs = true
	if _, err := w.svc.Edit(context.Background(), testRepo, in); !errors.Is(err, ErrEditChanged) || len(w.redeemed) != 1 {
		t.Fatalf("another file: %v, redeemed %d", err, len(w.redeemed))
	}
}

func TestAnEditIsRefusedWhileTheOpenPullRequestNeedsADecision(t *testing.T) {
	w := newWorld(t)
	if _, err := w.svc.Edit(context.Background(), testRepo, w.editInput(testPolicy, testPolicy+"# first\n", true)); err != nil {
		t.Fatal(err)
	}
	w.hub.push(fakeBase, policyPath, testPolicy+"# main\n")
	w.approveTip() // the first edit's bytes no longer apply: it is stale
	head := w.hub.file(Branch, policyPath)
	in := w.editInput(head, head+"# second\n", false)
	redeemed := len(w.redeemed)
	if _, err := w.svc.Edit(context.Background(), testRepo, in); !errors.Is(err, ErrEditChanged) || len(w.redeemed) != redeemed {
		t.Fatalf("an edit over a stale one: %v", err)
	}
}

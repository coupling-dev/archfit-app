package approvals

import (
	"context"
	"testing"

	"github.com/alexei-led/archfit-app/internal/gate/records"
	"github.com/alexei-led/archfit-app/internal/github/api"
)

func (w *world) edited() {
	w.t.Helper()
	w.gh.commit("target", policyV(0))
	w.approved(digestV(0))
	edit := records.Edit{File: []byte(policyV(1)), Base: digestV(0), Target: "target"}
	pin := records.Pin{Proposal: "edit-proposal", PullRequest: 12, HeadSHA: "edit-head", Digest: digestV(1)}
	if err := records.EditPolicy(context.Background(), w.mem, w.tenant(), testRepo.FullName, "edit", records.Approval{
		Owner: 1, Login: "olivia", Credential: "cred-1"}, edit, pin); err != nil {
		w.t.Fatal(err)
	}
	w.gh.pulls[12] = api.Pull{Number: 12, State: "open", HeadSHA: "edit-head"}
}

func TestInvalidationDoesNotVoidTheEditsOwnMerge(t *testing.T) {
	w := newWorld(t)
	w.edited()
	w.gh.onHead = func() {
		w.gh.onHead = nil
		w.gh.files["merge"] = policyV(1)
		w.gh.history = append(w.gh.history, "merge")
		w.gh.pulls[12] = api.Pull{Number: 12, State: "closed", Merged: true, MergeCommitSHA: "merge"}
	}
	if err := w.svc.retireMovedEdits(context.Background(), testRepo); err != nil {
		t.Fatal(err)
	}
	out := w.push("target", "merge", PolicyPath)
	if out.Judged != JudgedConsumed {
		t.Fatalf("legitimate merge voided %+v", out)
	}
}

func TestEditMergePushConsumesAndCannotReplay(t *testing.T) {
	w := newWorld(t)
	w.edited()
	w.merge(12, "merge", 1)
	out := w.push("target", "merge", PolicyPath)
	if out.Judged != JudgedConsumed || w.proposal("edit-proposal").State != records.StateMerged {
		t.Fatalf("out %+v", out)
	}
	if again := w.push("target", "merge", PolicyPath); again.Judged != JudgedConsumed {
		t.Fatal(again)
	}
	if w.gh.issues[0] {
		t.Fatal("request zero reopened")
	}
}

func TestCodeOnlyPushVoidsEditAndLateMergeIsNotConsumed(t *testing.T) {
	w := newWorld(t)
	w.edited()
	w.gh.commit("code", policyV(0))
	if out := w.push("target", "code", "README.md"); out.Judged != JudgedNone {
		t.Fatal(out)
	}
	var a records.Approval
	if err := records.Load(context.Background(), w.mem, w.tenant(), records.ApprovalKey(testRepo.FullName, "edit"), &a); err != nil {
		t.Fatal(err)
	}
	if a.State != records.StateVoid || !w.proposal("edit-proposal").EditVoided {
		t.Fatal("edit not void", a)
	}
	if len(w.gh.checks) == 0 || w.gh.checks[len(w.gh.checks)-1].Conclusion == "success" {
		t.Fatal("success not retired")
	}
	w.merge(12, "late", 1)
	out := w.push("code", "late", PolicyPath)
	if out.Judged != JudgedOutside || w.repo().ApprovedDigest != digestV(0) {
		t.Fatalf("late edit was approved %+v", out)
	}
	if w.gh.issues[0] {
		t.Fatal("request zero reopened")
	}
}

func TestUndeliveredCodeOnlyMoveCannotBlessEditMerge(t *testing.T) {
	w := newWorld(t)
	w.edited()
	w.gh.commit("code", policyV(0))
	w.merge(12, "late", 1)
	out := w.push("target", "late", PolicyPath)
	if out.Judged != JudgedOutside || w.repo().ApprovedDigest != digestV(0) {
		t.Fatalf("unseen movement approved %+v", out)
	}
}

func TestRecoveryRefusesEditWhoseTargetMoved(t *testing.T) {
	w := newWorld(t)
	w.edited()
	r := w.repo()
	r.ApprovedCommit = "target"
	raw, err := records.Encode(&r)
	if err != nil {
		t.Fatal(err)
	}
	if err := w.mem.Put(context.Background(), w.tenant(), records.RepoKey(testRepo.FullName), raw); err != nil {
		t.Fatal(err)
	}
	w.gh.commit("code", policyV(0))
	w.merge(12, "late", 1)
	out, err := w.svc.Reconcile(context.Background(), testRepo, "late")
	if err != nil || out.Judged != JudgedAmbiguous || w.repo().ApprovedDigest != digestV(0) {
		t.Fatalf("recovery %+v, %v", out, err)
	}
}

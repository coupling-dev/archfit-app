package proposals

import (
	"context"
	"encoding/json"
	"strings"
	"testing"

	"github.com/coupling-dev/archfit-app/internal/gate/records"
	"github.com/coupling-dev/archfit-app/internal/gate/requests"
	"github.com/coupling-dev/archfit-app/internal/wire/policy3"
)

// ciPasses marks the other required checks of the policy branch's head as passed, as CI does.
func (w *world) ciPasses() string {
	w.hub.mu.Lock()
	defer w.hub.mu.Unlock()
	head := w.hub.branches[Branch]
	w.hub.ciDone[head] = true
	return head
}

func (w *world) advance() {
	w.t.Helper()
	if err := w.svc.Advance(context.Background(), testRepo); err != nil {
		w.t.Fatal(err)
	}
}

func TestMainMovesBetweenApprovalAndMergeAndTheOwnerDoesNothing(t *testing.T) {
	for _, edit := range []bool{false, true} {
		name := map[bool]string{false: "answer", true: "edit"}[edit]
		t.Run(name, func(t *testing.T) {
			w := newWorld(t)
			w.hub.strict, w.hub.requireCI = true, true
			var out Outcome
			if edit {
				var err error
				if out, err = w.svc.Edit(context.Background(), testRepo, w.editInput(testPolicy, testPolicy+"# owner edit\n", false)); err != nil {
					t.Fatal(err)
				}
			} else {
				w.request(431, "graph", "pkg_tc", "pkg_util")
				out = w.mustAnswer(olivia, 431, requests.Answer{Kind: policy3.Approve}, false)
			}
			if out.State != requests.PullRefused || out.Reason != "required checks or reviews have not passed yet" {
				t.Fatalf("the first merge: %+v", out)
			}
			approved := w.hub.file(Branch, policyPath)
			tip := w.hub.push(fakeBase, "README.md", "main moved")
			w.advance() // the default-branch push
			if w.hub.commits[w.hub.branches[Branch]].parent != tip {
				t.Fatal("the pull request was not brought up to date")
			}
			w.advance() // a sweep while CI runs: nothing to do
			if len(w.hub.merges) != 1 {
				t.Fatalf("merged before the checks passed: %v", w.hub.merges)
			}
			w.ciPasses()
			w.advance() // the sweep after CI
			if got := w.hub.file(fakeBase, policyPath); got != approved || w.hub.file(fakeBase, "README.md") != "main moved" {
				t.Fatalf("merged policy %q", got)
			}
			a := load[records.Approval](t, w.store, records.ApprovalKey(testRepo.FullName, out.Approval))
			if a.State != records.StateOpen {
				t.Errorf("the approval is %s; it waits for its consume", a.State)
			}
			if edit {
				var e records.Edit
				if err := json.Unmarshal(a.Change, &e); err != nil || e.Target != tip {
					t.Errorf("edit target %q, want the tip %q", e.Target, tip)
				}
			}
			if strings.Contains(strings.Join(w.hub.comments[out.PullRequest], ""), "no longer") {
				t.Error("an approval was reported void")
			}
		})
	}
}

func TestAdvanceLeavesAMergeLaterPullRequest(t *testing.T) {
	w := newWorld(t)
	w.request(431, "graph", "pkg_tc", "pkg_util")
	w.mustAnswer(olivia, 431, requests.Answer{Kind: policy3.Approve}, true)
	w.hub.push(fakeBase, "README.md", "main moved")
	w.advance()
	if len(w.hub.merges) != 0 || w.hub.count("CommitOn") != 1 {
		t.Fatalf("merge later was merged or rewritten: merges %v", w.hub.merges)
	}
}

func TestAdvanceLeavesABranchSomeoneElseChanged(t *testing.T) {
	w := newWorld(t)
	w.hub.requireCI = true
	w.request(431, "graph", "pkg_tc", "pkg_util")
	w.mustAnswer(olivia, 431, requests.Answer{Kind: policy3.Approve}, false)
	w.hub.push(Branch, "README.md", "someone else")
	w.ciPasses()
	w.advance()
	if len(w.hub.merges) != 1 {
		t.Fatalf("a foreign head was merged: %v", w.hub.merges)
	}
}

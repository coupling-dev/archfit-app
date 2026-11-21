package gate

import (
	"strings"
	"testing"

	"github.com/coupling-dev/archfit-app/internal/gate/ingest"
	"github.com/coupling-dev/archfit-app/internal/wire/policy3"
)

// policyChange makes the shop an onboarded repository whose default branch holds target, approved. It opens pull
// request 7, which changes the policy to policy1Required and the Action's pin, and returns the upload of its run. The
// engine gives unknown for a policy that differs from the target's, naming the head's policy digest: only owners
// change the policy (the policy_differs golden).
func (w *world4) policyChange(target string) (*pull, *spec4) {
	w.t.Helper()
	w.setFile(w.shop, testBranch, policyPath, []byte(target))
	w.approvedAt(target, w.tip)

	p := w.openPull(w.shop, 7)
	w.setFile(w.shop, w.gh.pulls[pullKey(shopName, 7)].MergeBaseSHA, policyPath, []byte(target))
	moved := workflow4(actionBumped, " # v0.9.1")
	w.setFile(w.shop, p.head, policyPath, []byte(policy1Required))
	w.setFile(w.shop, p.head, ingest.WorkflowPath, moved)
	s := w.spec4(p, "policy_differs")
	w.setFile(w.shop, s.claims.SHA, ingest.WorkflowPath, moved)

	head := policy3.Digest([]byte(policy1Required))
	s.env.PolicyDigest = head
	s.result["policy"].(map[string]any)["digest"] = head
	return p, s
}

// TestAPolicyChangeGoesGreenByTheOwnersApproval: the pull request that changes the policy and the workflow gets no
// verdict from the engine, which gives unknown for a policy that differs from the target's. An owner approves the head
// with a passkey, and that approval is the check, through fresh uploads and reruns, until the head moves.
func TestAPolicyChangeGoesGreenByTheOwnersApproval(t *testing.T) {
	for _, target := range []struct{ name, policy string }{
		{"an advisory policy", policy4},
		{"a required policy", policy4Required},
	} {
		for _, rerun := range []bool{false, true} {
			t.Run(target.name+map[bool]string{false: ", fresh upload", true: ", rerun"}[rerun], func(t *testing.T) {
				w := newWorld4(t)
				p, s := w.policyChange(target.policy)
				w.wantStatus(w.send4(s), "unknown", reasonPolicyNotTarget)
				if run, ok := w.latestGate(p.head); !ok || run.Conclusion == "success" {
					t.Fatalf("archfit/gate before the approval = %+v, want no success", run)
				}

				out, err := w.act(OwnerUser{ID: 501, Login: "olivia"}, OwnerAction{Kind: OwnerWorkflow, Pull: 7})
				if err != nil {
					t.Fatal(err)
				}
				// The approval alone carries the head, before any run uploads again.
				w.wantOwnerCheck(p, out.Approval)
				if rerun {
					w.rerunUpload(p, s)
				} else {
					w.send4(s)
				}
				w.wantOwnerCheck(p, out.Approval)

				w.push(p)
				w.setFile(w.shop, p.head, policyPath, []byte(policy1Required))
				w.setFile(w.shop, p.head, ingest.WorkflowPath, w.gh.files[fileKey(shopName, s.claims.SHA, ingest.WorkflowPath)])
				later := w.spec4(p, "policy_differs")
				later.env, later.result = s.env, s.result
				later.env.HeadSHA, later.env.RunID, later.env.RunAttempt = p.head, p.runID, 1
				later.result["head"] = p.head
				w.setFile(w.shop, later.claims.SHA, ingest.WorkflowPath, w.gh.files[fileKey(shopName, p.head, ingest.WorkflowPath)])
				w.send4(later)
				if run, ok := w.latestGate(p.head); !ok || run.Conclusion == "success" || strings.HasPrefix(run.ExternalID, "approval:") {
					t.Errorf("a later head's check = %+v, want no owner approval", run)
				}
			})
		}
	}
}

// TestAWorkflowApprovalOfAPullRequestThatKeepsThePolicyLeavesTheVerdictToTheEngine: only a pull request that changes
// the policy from its base takes the owner's approval as its check. A change of the Action's pin alone is judged by
// the engine, also when its head sits on an older policy than the approved one, or when the approved policy moves
// after the approval; the approval writes no success of its own either.
func TestAWorkflowApprovalOfAPullRequestThatKeepsThePolicyLeavesTheVerdictToTheEngine(t *testing.T) {
	for _, tc := range []struct {
		name string
		// head is the policy at the pull request's base and head; the default branch holds policy4Required, approved.
		head string
		// after runs once the owner approved.
		after func(w *world4)
	}{
		{"the head keeps the approved policy", policy4Required, func(*world4) {}},
		{"the head sits on an older policy", policy4, func(*world4) {}},
		{"the approved policy moves after the approval", policy4Required, func(w *world4) {
			w.setFile(w.shop, testBranch, policyPath, []byte(policy1Required))
			w.approvedAt(policy1Required, w.tip)
		}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			w := newWorld4(t)
			w.setFile(w.shop, testBranch, policyPath, []byte(policy4Required))
			w.approvedAt(policy4Required, w.tip)
			p := w.openPull(w.shop, 7)
			// GitHub's base SHA is the default branch's tip; the branch started at the merge base, with the head's policy.
			w.setFile(w.shop, w.gh.pulls[pullKey(shopName, 7)].MergeBaseSHA, policyPath, []byte(tc.head))
			moved := workflow4(actionBumped, " # v0.9.1", "      - run: echo extra\n")
			w.setFile(w.shop, p.head, policyPath, []byte(tc.head))
			w.setFile(w.shop, p.head, ingest.WorkflowPath, moved)
			s := w.spec4(p, "deny")
			s.env.PolicyDigest = policy3.Digest([]byte(policy4Required))
			s.result["policy"].(map[string]any)["digest"] = s.env.PolicyDigest
			w.setFile(w.shop, s.claims.SHA, ingest.WorkflowPath, moved)
			w.wantStatus(w.send4(s), "unknown", reasonWorkflowChanged)

			if _, err := w.act(OwnerUser{ID: 501, Login: "olivia"}, OwnerAction{Kind: OwnerWorkflow, Pull: 7}); err != nil {
				t.Fatal(err)
			}
			if run, _ := w.latestGate(p.head); strings.HasPrefix(run.ExternalID, "approval:") {
				t.Fatalf("archfit/gate after the approval = %+v, want no owner verdict", run)
			}
			tc.after(w)
			w.rerunUpload(p, s)
			if run, ok := w.latestGate(p.head); !ok || run.Conclusion == "success" || strings.HasPrefix(run.ExternalID, "approval:") {
				t.Errorf("archfit/gate = %+v, want the engine's verdict", run)
			}
		})
	}
}

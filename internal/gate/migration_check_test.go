package gate

import (
	"strings"
	"testing"

	"github.com/coupling-dev/archfit-app/internal/gate/ingest"
	"github.com/coupling-dev/archfit-app/internal/wire/manifest"
	"github.com/coupling-dev/archfit-app/internal/wire/policy3"
)

// engine090 is the engine of the Action's new home (beta plan task 5.3).
const engine090 = "v0.9.0"

// policy2 is a policy of an engine before Archfit 4: the App reads no policy of version 2.
const policy2 = "version: 2\nowners: [olivia]\nmodules:\n  app:\n    paths: [cmd/**, pkg/**]\n"

// actionWorkflow is the workflow file that runs the Action of repo at commit.
func actionWorkflow(repo, commit, comment string) []byte {
	return []byte("name: archfit\non: [pull_request, push, merge_group]\npermissions:\n  id-token: write\njobs:\n  check:\n" +
		"    runs-on: ubuntu-latest\n    steps:\n      - uses: " + repo + "@" + commit + comment + "\n")
}

// migration makes the shop an onboarded repository of engine v4.0.0: target on its default branch and the Action of its
// old home, with manifest 4 approving engine 0.9.0 too. It opens pull request 7, which moves it to policy version 1
// and the Action of the new home, and returns the upload of its run by the 0.9.0 Action: engine 0.9.0 reads only
// version 1, so it judges the head's policy against the target's and gives unknown, naming the head's policy digest
// (engine main 8f04ffe4, checked on a fixture of a version 3 base and a version 1 head).
func (w *world4) migration(target string) (*pull, *spec4) {
	w.t.Helper()
	w.setFile(w.shop, testBranch, policyPath, []byte(target))
	w.setFile(w.shop, testBranch, ingest.WorkflowPath, actionWorkflow(manifest.LegacyActionRepository, actionPinned, " # v4.0.0"))
	w.gate.v4.engineApproved = func(id manifest.Identity) bool {
		return id.ImageDigest == image4 && id.Platform == "linux/amd64" && (id.EngineVersion == engine4 || id.EngineVersion == engine090)
	}

	p := w.openPull(w.shop, 7)
	moved := actionWorkflow(manifest.ActionRepository, actionBumped, " # v0.9.0")
	w.setFile(w.shop, p.head, policyPath, []byte(policy1Required))
	w.setFile(w.shop, p.head, ingest.WorkflowPath, moved)
	s := w.spec4(p, "policy_differs")
	w.setFile(w.shop, s.claims.SHA, ingest.WorkflowPath, moved)

	head := policy3.Digest([]byte(policy1Required))
	s.env.EngineVersion, s.env.PolicyDigest = engine090, head
	s.result["engine"] = strings.TrimPrefix(engine090, "v")
	s.result["policy"].(map[string]any)["digest"] = head
	s.result["unknown"] = []any{map[string]any{
		"path": policyPath, "cause": "policy",
		"what": `version 3 is not supported: archfit reads policy version 1; remove the file and run "archfit init" to write a fresh policy`,
		"why":  "the policy on refs/remotes/origin/main is not valid",
		"next": map[string]any{"kind": "restore", "text": "ask an owner to fix .archfit.yaml on refs/remotes/origin/main"},
	}}
	return p, s
}

// TestAMigrationPullRequestGoesGreenByTheOwnersApproval: the pull request that moves an onboarded repository to engine
// 0.9.0 changes its policy to version 1 and its workflow to the Action's new home. No engine run can judge it: 0.9.0
// gives unknown for a version 3 or 2 policy on the target branch. An owner approves the head with a passkey, and that
// approval is the check, through fresh uploads and reruns, until the head moves.
func TestAMigrationPullRequestGoesGreenByTheOwnersApproval(t *testing.T) {
	for _, tc := range []struct {
		name, target string
		guarded      bool
		action       string
		user         OwnerUser
		before       string // the reason of the upload before the approval
	}{
		{"version 3, advisory", policy4, true, OwnerWorkflow, OwnerUser{ID: 501, Login: "olivia"}, reasonPolicyNotTarget},
		{"version 3, required", policy4Required, true, OwnerWorkflow, OwnerUser{ID: 501, Login: "olivia"}, reasonPolicyNotTarget},
		// The App reads no version 2 policy, so a repository without a record onboards by the first approval.
		{"version 2, no approved policy", policy2, false, OwnerFirst, oliviaAdmin, reasonFirstPolicy},
	} {
		for _, rerun := range []bool{false, true} {
			t.Run(tc.name+map[bool]string{false: ", fresh upload", true: ", rerun"}[rerun], func(t *testing.T) {
				w := newWorld4(t)
				p, s := w.migration(tc.target)
				if tc.guarded {
					w.approvedAt(tc.target, w.tip)
				} else {
					w.unrecorded()
				}
				w.wantStatus(w.send4(s), "unknown", tc.before)
				if run, ok := w.latestGate(p.head); !ok || run.Conclusion == "success" {
					t.Fatalf("archfit/gate before the approval = %+v, want no success", run)
				}

				out, err := w.act(tc.user, OwnerAction{Kind: tc.action, Pull: 7})
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

// TestAWorkflowApprovalOfAHeadThatKeepsThePolicyLeavesTheVerdictToTheEngine: only a head that changes the approved
// policy takes the owner's approval as its check; a move to the Action's new home alone is judged by the engine.
func TestAWorkflowApprovalOfAHeadThatKeepsThePolicyLeavesTheVerdictToTheEngine(t *testing.T) {
	w := newWorld4(t)
	w.setFile(w.shop, testBranch, policyPath, []byte(policy4Required))
	w.setFile(w.shop, testBranch, ingest.WorkflowPath, actionWorkflow(manifest.LegacyActionRepository, actionPinned, " # v4.0.0"))
	w.approvedAt(policy4Required, w.tip)
	p := w.openPull(w.shop, 7)
	moved := actionWorkflow(manifest.ActionRepository, actionBumped, " # v0.9.0")
	w.setFile(w.shop, p.head, policyPath, []byte(policy4Required))
	w.setFile(w.shop, p.head, ingest.WorkflowPath, moved)
	s := w.spec4(p, "deny")
	s.env.PolicyDigest = policy3.Digest([]byte(policy4Required))
	s.result["policy"].(map[string]any)["digest"] = s.env.PolicyDigest
	w.setFile(w.shop, s.claims.SHA, ingest.WorkflowPath, moved)
	w.wantStatus(w.send4(s), "unknown", reasonWorkflowChanged)

	if _, err := w.act(OwnerUser{ID: 501, Login: "olivia"}, OwnerAction{Kind: OwnerWorkflow, Pull: 7}); err != nil {
		t.Fatal(err)
	}
	w.wantStatus(w.rerunUpload(p, s), "fail", "")
	if run, ok := w.latestGate(p.head); !ok || run.Conclusion != "failure" || strings.HasPrefix(run.ExternalID, "approval:") {
		t.Errorf("archfit/gate = %+v, want the engine's failure", run)
	}
}

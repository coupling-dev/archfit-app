package gate

import (
	"context"
	"strings"
	"testing"

	"github.com/coupling-dev/archfit-app/internal/gate/ingest"
	"github.com/coupling-dev/archfit-app/internal/gate/records"
	"github.com/coupling-dev/archfit-app/internal/github/api"
	"github.com/coupling-dev/archfit-app/internal/store"
	"github.com/coupling-dev/archfit-app/internal/wire/manifest"
	"github.com/coupling-dev/archfit-app/internal/wire/policy3"
)

const (
	policy4Advisory = "version: 1\ngate: advisory\nowners: [olivia]\nmodules:\n  app:\n    paths: [cmd/**, pkg/**]\n"
	setupNoVerdict  = "No verdict yet: merge this setup pull request to start advisory checks."
	advisoryLine    = "Advisory: this check does not block the merge."
)

// advisoryWorld is a repository with an advisory policy on its default branch and no approval: the state after the
// setup pull request merged, before an owner approved anything.
func advisoryWorld(t *testing.T, policy string) (*world4, *pull, *spec4) {
	t.Helper()
	w := newWorld4(t)
	p := w.openPull(w.shop, 7)
	s := w.spec4(p, "pass")
	w.usePolicy(s, policy)
	w.unrecorded()
	return w, p, s
}

// unapprovedRequired is a run of a required policy on the default branch that no owner approved yet.
func (w *world4) unapprovedRequired(p *pull, golden string) *spec4 {
	w.t.Helper()
	s := w.spec4(p, golden)
	w.usePolicy(s, policy4Required)
	w.unrecorded()
	return s
}

func TestAdvisoryPolicyWithoutApprovalGivesTheVerdict(t *testing.T) {
	for _, tc := range []struct {
		name, policy, golden, status, conclusion string
	}{
		{"pass, no gate", policy4, "pass", "pass", "success"},
		{"fail, no gate", policy4, "deny", "fail", "neutral"},
		{"pass, explicit gate", policy4Advisory, "pass", "pass", "success"},
		{"fail, explicit gate", policy4Advisory, "deny", "fail", "neutral"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			w, p, s := advisoryWorld(t, tc.policy)
			s.result = w.result(tc.golden, p.head)
			w.usePolicy(s, tc.policy)
			w.unrecorded()
			w.wantStatus(w.send4(s), tc.status, "")
			c := w.wantGate(p, "completed", tc.conclusion)
			if !strings.Contains(c.Title, ", advisory") || !strings.Contains(c.Summary, advisoryLine) {
				t.Errorf("check %q lacks the advisory words:\n%s", c.Title, c.Summary)
			}
		})
	}
}

func TestAdvisoryPushWithoutApprovalGivesTheVerdict(t *testing.T) {
	for golden, conclusion := range map[string]string{"pass": "success", "deny": "neutral"} {
		t.Run(golden, func(t *testing.T) {
			w := newWorld4(t)
			p := w.openPull(w.shop, 7)
			s := w.pushSpec(p, golden)
			w.unrecorded()
			if a := w.send4(s); a.Reason != "" {
				t.Fatalf("answer %s, want no reason", a.raw)
			}
			c, ok := w.latestGate(w.tip)
			if !ok || c.Conclusion != conclusion {
				t.Fatalf("push check = %+v, ok %v, want %s", c, ok, conclusion)
			}
			if len(w.gh.comments) != 0 {
				t.Errorf("a push wrote a comment: %v", w.gh.comments)
			}
		})
	}
}

func TestRequiredPolicyWithoutApprovalStillWaitsForTheFirstApproval(t *testing.T) {
	w, p, s := advisoryWorld(t, policy4Required)
	w.wantStatus(w.send4(s), "unknown", reasonFirstApproval)
	w.wantGate(p, "completed", "action_required")
}

func TestAdvisoryHistoryIsNeverAnApproval(t *testing.T) {
	w, _, s := advisoryWorld(t, policy4)
	w.wantStatus(w.send4(s), "pass", "")
	// The policy becomes required on the default branch: the advisory checks that ran are no approval.
	p2 := w.openPull(w.shop, 8)
	s2 := w.spec4(p2, "pass")
	w.usePolicy(s2, policy4Required)
	w.unrecorded()
	w.wantStatus(w.send4(s2), "unknown", reasonFirstApproval)
}

func TestUnguardedSetupPullRequestGetsNoVerdictYet(t *testing.T) {
	for name, head := range map[string]string{"head policy advisory": policy4, "head policy required": policy4Required} {
		t.Run(name, func(t *testing.T) {
			w := newWorld4(t)
			w.unrecorded()
			w.removeFile(w.shop, testBranch, policyPath)
			p := w.openPull(w.shop, 7)
			s := w.spec4(p, "deny")
			w.setFile(w.shop, p.head, policyPath, []byte(head))
			w.wantStatus(w.send4(s), "unknown", reasonFirstPolicy)
			c := w.wantGate(p, "completed", "neutral")
			if !strings.Contains(w.feedbackText(p), setupNoVerdict) {
				t.Errorf("check %q lacks the setup words:\n%s", c.Title, w.feedbackText(p))
			}
			if strings.Contains(w.feedbackText(p), "no-chaos-to-runtime") {
				t.Errorf("the App adjudicated the head's result:\n%s", w.feedbackText(p))
			}
		})
	}
}

func TestBeforeAndAfterTheSetupPullRequestMerges(t *testing.T) {
	w := newWorld4(t)
	w.unrecorded()
	w.removeFile(w.shop, testBranch, policyPath)
	before := w.openPull(w.shop, 7)
	w.wantStatus(w.send4(w.spec4(before, "pass")), "unknown", reasonFirstPolicy)
	w.wantGate(before, "completed", "neutral")

	w.setFile(w.shop, testBranch, policyPath, []byte(policy4))
	after := w.openPull(w.shop, 8)
	w.wantStatus(w.send4(w.spec4(after, "pass")), "pass", "")
	w.wantGate(after, "completed", "success")
}

func TestAVerifiedUnknownIsNeutralInAdvisoryAndBlocksInRequiredMode(t *testing.T) {
	headDigest := policy3.Digest([]byte(policy4 + "  extra:\n    paths: [x/**]\n"))
	for _, tc := range []struct {
		name, golden string
		build        func(*world4, *spec4)
		reason       string
	}{
		{"an engine unknown", "policy_differs", func(*world4, *spec4) {}, ""},
		{"a policy that changed since the run", "pass", func(w *world4, s *spec4) {
			old := w.sha()
			w.setFile(w.shop, old, policyPath, []byte(policy4+"  extra:\n    paths: [x/**]\n"))
			w.gh.compares[shopName+"|"+old+"|"+w.tip] = api.Comparison{Status: "ahead", Paths: []string{policyPath}}
			s.env.TargetSHA, s.env.PolicyDigest = old, headDigest
			s.result["policy"].(map[string]any)["digest"] = headDigest
		}, reasonPolicyChanged},
	} {
		for _, mode := range []struct{ name, policy, conclusion string }{
			{"advisory", policy4, "neutral"},
			{"required", policy4Required, "action_required"},
		} {
			t.Run(tc.name+", "+mode.name, func(t *testing.T) {
				w := newWorld4(t)
				p := w.openPull(w.shop, 7)
				s := w.spec4(p, tc.golden)
				w.usePolicy(s, mode.policy)
				tc.build(w, s)
				w.wantStatus(w.send4(s), "unknown", tc.reason)
				c := w.wantGate(p, "completed", mode.conclusion)
				text := w.feedbackText(p)
				if !strings.Contains(c.Title, "unknown, no verdict") {
					t.Errorf("check %q does not say there is no verdict:\n%s", c.Title, text)
				}
				if tc.reason != "" && !strings.Contains(text, "next:") {
					t.Errorf("the check does not give the next step:\n%s", text)
				}
				if got := strings.Contains(text, advisoryLine); got != (mode.name == "advisory") {
					t.Errorf("advisory line shown = %v in %s mode:\n%s", got, mode.name, text)
				}
			})
		}
	}
}

func TestAnUnverifiableUploadBlocksInAdvisoryToo(t *testing.T) {
	for _, tc := range []struct {
		name   string
		build  func(*world4, *pull, *spec4)
		reason string
	}{
		{"a run that did not use the policy", func(_ *world4, _ *pull, s *spec4) {
			s.env.PolicyDigest = "sha256:" + strings.Repeat("ab", 32)
			s.result["policy"].(map[string]any)["digest"] = s.env.PolicyDigest
		}, reasonPolicyNotTarget},
		{"a target commit GitHub does not know", func(_ *world4, _ *pull, s *spec4) {
			s.env.TargetSHA = strings.Repeat("0", 39) + "1"
		}, reasonTargetOffBranch},
		{"a changed workflow", func(w *world4, _ *pull, s *spec4) {
			w.setFile(w.shop, s.claims.SHA, ingest.WorkflowPath, workflow4(actionUnknown, ""))
		}, reasonWorkflowChanged},
		{"an engine the App does not list", func(w *world4, _ *pull, _ *spec4) {
			w.gate.v4.engineApproved = func(manifest.Identity) bool { return false }
		}, reasonUnknownEngine},
	} {
		t.Run(tc.name, func(t *testing.T) {
			w := newWorld4(t)
			p := w.openPull(w.shop, 7)
			s := w.spec4(p, "pass")
			tc.build(w, p, s)
			w.wantStatus(w.send4(s), "unknown", tc.reason)
			w.wantGate(p, "completed", "action_required")
		})
	}
}

func TestAChangeOutsideTheFlowBlocksInAdvisoryToo(t *testing.T) {
	w := newWorld4(t)
	p := w.openPull(w.shop, 7)
	s := w.spec4(p, "pass")
	raw, err := records.Encode(&records.Repo{OutsideFlow: &records.OutsideFlow{}})
	if err != nil {
		t.Fatal(err)
	}
	if err := w.mem.Put(context.Background(), store.Tenant(w.shop.InstallationID), records.RepoKey(w.shop.FullName), raw); err != nil {
		t.Fatal(err)
	}
	w.wantStatus(w.send4(s), "unknown", reasonOutsideFlow)
	w.wantGate(p, "completed", "action_required")
}

func TestAnInvalidPolicyOnAGuardedDefaultBranchStillBlocks(t *testing.T) {
	// No valid policy means the mode is unknown: only an advisory policy on the tip makes an unknown neutral.
	w := newWorld4(t)
	w.removeFile(w.shop, testBranch, policyPath)
	p := w.openPull(w.shop, 7)
	w.wantStatus(w.send4(w.spec4(p, "pass")), "unknown", reasonPolicyUnapproved)
	w.wantGate(p, "completed", "action_required")
}

func TestSweepKeepsTheAdvisoryVerdictOfAPolicyNoOwnerApproved(t *testing.T) {
	for golden, conclusion := range map[string]string{"pass": "success", "deny": "neutral"} {
		t.Run(golden, func(t *testing.T) {
			w := newWorld4(t)
			p := w.openPull(w.shop, 7)
			s := w.spec4(p, golden)
			w.unrecorded()
			w.send4(s)
			w.sweep4()
			w.wantSweepGate(p, "completed", conclusion)
		})
	}
}

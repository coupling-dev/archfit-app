package gate

import (
	"context"
	"encoding/json"
	"errors"
	"slices"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/alexei-led/archfit-app/internal/gate/records"
	"github.com/alexei-led/archfit-app/internal/gate/sweep"
	"github.com/alexei-led/archfit-app/internal/github/api"
	"github.com/alexei-led/archfit-app/internal/store"
	"github.com/alexei-led/archfit-app/internal/wire/policy3"
)

func (w *world4) wantSweepGate(p *pull, status, conclusion string) api.CheckRun {
	w.t.Helper()
	c, ok := w.latestGate(p.head)
	if !ok || c.Status != status || c.Conclusion != conclusion {
		w.t.Fatalf("latest gate = %+v, want %s/%s", c, status, conclusion)
	}
	return c
}

func (w *world4) sweep4() Summary {
	w.t.Helper()
	sum, err := w.gate.sweep4(context.Background(), Summary{})
	if err != nil || sum.Errors != 0 {
		w.t.Fatalf("sweep4 = %+v, %v", sum, err)
	}
	return sum
}

func (w *world4) put4(key string, value records.Value) {
	w.t.Helper()
	raw, err := records.Encode(value)
	if err != nil {
		w.t.Fatal(err)
	}
	if err := w.mem.Put(context.Background(), store.Tenant(shopInstallation), key, raw); err != nil {
		w.t.Fatal(err)
	}
}

func (w *world4) repo4() records.Repo {
	w.t.Helper()
	var r records.Repo
	if err := records.Load(context.Background(), w.mem, store.Tenant(shopInstallation), records.RepoKey(shopName), &r); err != nil {
		w.t.Fatal(err)
	}
	return r
}

func (w *world4) guarded4(policy string) {
	w.t.Helper()
	if _, err := policy3.Decode([]byte(policy)); err != nil {
		w.t.Fatal(err)
	}
	w.setFile(w.shop, w.tip, policyPath, []byte(policy))
	w.setFile(w.shop, testBranch, policyPath, []byte(policy))
	w.approved(w.shop, policy)
	r := w.repo4()
	r.ApprovedCommit = w.tip
	w.put4(records.RepoKey(shopName), &r)
}

func (w *world4) tip4(policy string, history ...api.Commit) string {
	w.t.Helper()
	before := w.tip
	tip := history[len(history)-1].SHA
	w.setFile(w.shop, tip, policyPath, []byte(policy))
	w.setFile(w.shop, testBranch, policyPath, []byte(policy))
	w.gh.heads[branchKey(shopName, testBranch)] = tip
	w.gh.compares[shopName+"|"+before+"|"+tip] = api.Comparison{Status: "ahead", Paths: []string{policyPath}, Commits: history, TotalCommits: len(history)}
	w.tip = tip
	return tip
}

func TestSweepReplacesTenDayWaitingFromTheVerifiedArtifact(t *testing.T) {
	for _, days := range []int{9, 10, 11} {
		t.Run(strconv.Itoa(days)+" days", func(t *testing.T) {
			w := newAskWorld(t)
			p := w.pullBy(7)
			w.uploadAsk(p, "graph", "fail")
			w.ask(p, workerLogin, "/archfit request allow this dependency")
			old := w.wantSweepGate(p, "in_progress", "")
			for i, c := range w.gh.checks[shopName] {
				if c.ID == old.ID {
					w.gh.checks[shopName][i].StartedAt = w.now.Add(-time.Duration(days) * 24 * time.Hour)
				}
			}
			w.sweep4()
			got := w.wantSweepGate(p, "in_progress", "")
			if (got.ID != old.ID) != (days >= 10) {
				t.Fatalf("old check %d, new check %d at %d days", old.ID, got.ID, days)
			}
			if !strings.Contains(w.comment(p), "waiting, request #1000") {
				t.Error("fresh check lost the covered result")
			}
			if days >= 10 {
				w.sweep4()
				if c := w.wantSweepGate(p, "in_progress", ""); c.ID != got.ID {
					t.Error("the next sweep created another fresh check")
				}
			}
		})
	}
}

func TestSweepNeverRefreshesAnUnverifiableWaitingResult(t *testing.T) {
	for _, mode := range []string{"missing", "digest", "invalid", "transient"} {
		t.Run(mode, func(t *testing.T) {
			w := newAskWorld(t)
			p := w.pullBy(7)
			w.uploadAsk(p, "graph", "fail")
			w.ask(p, workerLogin, "/archfit request allow this dependency")
			for i := range w.gh.checks[shopName] {
				w.gh.checks[shopName][i].StartedAt = w.now.Add(-11 * 24 * time.Hour)
			}
			switch mode {
			case "missing":
				delete(w.gh.artifacts, p.runID)
			case "digest":
				w.gh.artifacts[p.runID] = []byte("different")
			case "invalid":
				w.gh.fail = map[string]error{"Artifact": api.ErrArtifactInvalid}
			case "transient":
				w.gh.fail = map[string]error{"Artifact": errors.New("network")}
			}
			sum, err := w.gate.sweep4(t.Context(), Summary{})
			if err != nil {
				t.Fatal(err)
			}
			if mode == "transient" {
				if sum.Errors != 1 {
					t.Errorf("summary = %+v", sum)
				}
				w.wantSweepGate(p, "in_progress", "")
			} else {
				w.wantSweepGate(p, "completed", "action_required")
				if strings.Contains(w.comment(p), "## Archfit: pass") {
					t.Error("unverifiable artifact passed")
				}
			}
		})
	}
}

func TestSweepMissingUploadUsesTheCompletedRunUpdateTime(t *testing.T) {
	for _, tc := range []struct {
		name           string
		age            time.Duration
		running, noRun bool
		want           bool
	}{
		{"59 minutes", 59 * time.Minute, false, false, false},
		{"60 minutes", 60 * time.Minute, false, false, false},
		{"61 minutes", 61 * time.Minute, false, false, true},
		{"still running", 2 * time.Hour, true, false, false},
		{"no workflow run", 61 * time.Minute, false, true, true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			w := newWorld4(t)
			p := w.openPull(w.shop, 7)
			w.editPull(p, func(pl *api.Pull) { pl.UpdatedAt = w.now.Add(-tc.age) })
			w.editRun(p, func(run *api.WorkflowRun) {
				run.CreatedAt = w.now.Add(-10 * 24 * time.Hour)
				run.UpdatedAt = w.now.Add(-tc.age)
				if tc.running {
					run.Status = runInProgress
				}
			})
			if tc.noRun {
				delete(w.gh.runs, p.runID)
			}
			w.sweep4()
			check, found := w.check(p, p.head, gateCheckName)
			if found != tc.want {
				t.Fatalf("check exists=%v, want %v: %+v", found, tc.want, check)
			}
			if tc.want && check.Conclusion != "action_required" {
				t.Errorf("missing upload check = %+v", check)
			}
		})
	}
}

func TestSweepDropsAClosedPullFromTheIndexButKeepsItsRecord(t *testing.T) {
	w := newWorld4(t)
	p := w.openPull(w.shop, 7)
	w.wantStatus(w.send4(w.spec4(p, "pass")), "pass", "")
	w.editPull(p, func(pl *api.Pull) { pl.State = "closed" })
	w.sweep4()
	rec, head := w.pullRecord(p)
	if head != "" || rec.RunID != p.runID {
		t.Errorf("index head %q, record %+v", head, rec)
	}
}

func TestSweepWithoutApprovalStaysFirstApproval(t *testing.T) {
	w := newWorld4(t)
	p := w.openPull(w.shop, 7)
	w.wantStatus(w.send4(w.spec4(p, "pass")), "pass", "")
	w.unrecorded()
	w.sweep4()
	w.wantSweepGate(p, "completed", "action_required")
	if !strings.Contains(w.comment(p), "has no recorded approval") || len(w.gh.reruns) != 0 {
		t.Fatalf("comment %s, reruns %v", w.comment(p), w.gh.reruns)
	}
}

func TestSweepStalePolicyRerunsOnlyUnderTheGuard(t *testing.T) {
	for _, allowed := range []bool{true, false} {
		t.Run(map[bool]string{true: "policy only", false: "code moved"}[allowed], func(t *testing.T) {
			w := newWorld4(t)
			p := w.openPull(w.shop, 7)
			w.wantStatus(w.send4(w.spec4(p, "pass")), "pass", "")
			old := w.tip
			next := w.sha()
			policy := policy4 + "# new approved policy\n"
			w.setFile(w.shop, next, policyPath, []byte(policy))
			w.gh.heads[branchKey(shopName, testBranch)] = next
			w.approved(w.shop, policy)
			w.tip = next
			paths := []string{policyPath}
			if !allowed {
				paths = append(paths, "pkg/code.go")
			}
			w.gh.compares[shopName+"|"+old+"|"+next] = api.Comparison{Status: "ahead", Paths: paths}
			w.sweep4()
			if (len(w.gh.reruns) == 1) != allowed {
				t.Fatalf("reruns = %v", w.gh.reruns)
			}
			w.wantSweepGate(p, "completed", "action_required")
			if !strings.Contains(w.comment(p), "the policy changed since this run") {
				t.Error(w.comment(p))
			}
		})
	}
}

const datedPolicy4 = policy4 + "gate: required\nexceptions:\n  - rule: duplication\n    from: app\n    to: app\n    expires: 2026-11-19\n    why: migration\n"

func TestSweepExpiryRerunsOrFailsAfterTheLastUTCDate(t *testing.T) {
	for _, tc := range []struct {
		name        string
		day, runAge int
		rerun, fail bool
	}{
		{"last day", 19, 1, false, false},
		{"day after rerun", 20, 1, true, false},
		{"day after refused", 20, 31, false, true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			w := newWorld4(t)
			w.now = time.Date(2026, 11, tc.day, 1, 0, 0, 0, time.UTC)
			w.guarded4(datedPolicy4)
			p := w.openPull(w.shop, 7)
			s := w.spec4(p, "pass")
			s.env.PolicyDigest = policy3.Digest([]byte(datedPolicy4))
			s.result["policy"].(map[string]any)["digest"] = s.env.PolicyDigest
			s.result["date"] = "2026-11-18"
			w.wantStatus(w.send4(s), "pass", "")
			w.editRun(p, func(run *api.WorkflowRun) { run.CreatedAt = w.now.Add(-time.Duration(tc.runAge) * 24 * time.Hour) })
			w.sweep4()
			if (len(w.gh.reruns) == 1) != tc.rerun {
				t.Errorf("reruns %v", w.gh.reruns)
			}
			conclusion := "success"
			if tc.fail {
				conclusion = "failure"
			} else if tc.rerun {
				conclusion = "action_required"
			}
			w.wantSweepGate(p, "completed", conclusion)
			if tc.fail && !strings.Contains(w.comment(p), "duplication app -> app expired on 2026-11-19") {
				t.Error(w.comment(p))
			}
			if len(w.gh.issues[shopName]) != 1 {
				t.Errorf("expiry issues %+v", w.gh.issues[shopName])
			}
		})
	}
}

func TestExpiryWarningFailureDoesNotStopEnforcement(t *testing.T) {
	w := newWorld4(t)
	w.now = time.Date(2026, 11, 20, 0, 0, 0, 0, time.UTC)
	w.guarded4(datedPolicy4)
	p := w.openPull(w.shop, 7)
	s := w.spec4(p, "pass")
	s.env.PolicyDigest = policy3.Digest([]byte(datedPolicy4))
	s.result["policy"].(map[string]any)["digest"] = s.env.PolicyDigest
	s.result["date"] = "2026-11-18"
	w.wantStatus(w.send4(s), "pass", "")
	w.editRun(p, func(run *api.WorkflowRun) { run.CreatedAt = w.now.Add(-31 * 24 * time.Hour) })
	w.gh.fail = map[string]error{"CreateIssue": errors.New("notifications down")}
	sum, err := w.gate.sweep4(t.Context(), Summary{})
	if err != nil || sum.Errors != 1 {
		t.Fatalf("sweep = %+v,%v", sum, err)
	}
	w.wantSweepGate(p, "completed", "failure")
}

func TestCompletedRunningRecheckWithPolicyChangedIsRerunOnce(t *testing.T) {
	w := newWorld4(t)
	w.guarded4(policy4)
	p := w.openPull(w.shop, 7)
	s := w.spec4(p, "pass")
	if _, err := records.Rechecked(t.Context(), w.mem, store.Tenant(shopInstallation), shopName, 7, records.Recheck{Proposal: "p1", Path: sweep.PathRunning, HeadSHA: p.head}); err != nil {
		t.Fatal(err)
	}
	// The first upload creates the record; task 21 then marked the run as in flight.
	w.wantStatus(w.send4(s), "pass", "")
	if _, err := records.Rechecked(t.Context(), w.mem, store.Tenant(shopInstallation), shopName, 7, records.Recheck{Proposal: "p1", Path: sweep.PathRunning, HeadSHA: p.head}); err != nil {
		t.Fatal(err)
	}
	old := w.tip
	newTip := w.sha()
	newPolicy := policy4 + "# answered\n"
	w.setFile(w.shop, newTip, policyPath, []byte(newPolicy))
	w.setFile(w.shop, testBranch, policyPath, []byte(newPolicy))
	w.gh.heads[branchKey(shopName, testBranch)] = newTip
	w.approved(w.shop, newPolicy)
	w.gh.compares[shopName+"|"+old+"|"+newTip] = api.Comparison{Status: "ahead", Paths: []string{policyPath}}
	w.tip = newTip
	w.editRun(p, func(run *api.WorkflowRun) { run.Status = runInProgress })
	w.wantStatus(w.send4(s), "unknown", reasonPolicyChanged)
	w.sweep4()
	if len(w.gh.reruns) != 0 {
		t.Fatal("an in-flight run was rerun")
	}
	w.editRun(p, func(run *api.WorkflowRun) { run.Status = runCompleted })
	w.sweep4()
	w.sweep4()
	if !slices.Equal(w.gh.reruns, []int64{p.runID}) {
		t.Errorf("reruns = %v", w.gh.reruns)
	}
}

func TestSweepProvesASingleLostProposalMergeAndHandsOff(t *testing.T) {
	w := newAskWorld(t)
	w.guarded4(policyAsk)
	p := w.pullBy(7)
	w.uploadAsk(p, "graph", "fail")
	w.ask(p, workerLogin, "/archfit request allow this dependency")
	nextPolicy := policyAsk + "# answered\n"
	merge := w.sha()
	old := w.tip
	pin := &records.Pin{Proposal: "p1", PullRequest: 30, HeadSHA: w.sha(), Digest: policy3.Digest([]byte(nextPolicy))}
	if err := records.Answer(t.Context(), w.mem, store.Tenant(shopInstallation), shopName, "a1",
		records.Approval{Kind: records.KindApprove, Request: firstRequest, Change: json.RawMessage("{}")}, pin); err != nil {
		t.Fatal(err)
	}
	w.gh.pulls[pullKey(shopName, 30)] = api.Pull{Number: 30, State: "closed", Merged: true, MergeCommitSHA: merge}
	w.setFile(w.shop, merge, policyPath, []byte(nextPolicy))
	tip := w.sha()
	before := w.sha()
	w.setFile(w.shop, before, policyPath, []byte(policyAsk))
	w.tip4(nextPolicy, api.Commit{SHA: before, Parents: []string{old}}, api.Commit{SHA: merge, Parents: []string{before}}, api.Commit{SHA: tip, Parents: []string{merge}})
	// Proof follows parents, not the order returned by Compare.
	c := w.gh.compares[shopName+"|"+old+"|"+tip]
	slices.Reverse(c.Commits)
	w.gh.compares[shopName+"|"+old+"|"+tip] = c
	w.gh.fail = map[string]error{"Run": errors.New("handoff unavailable")}
	sum, err := w.gate.sweep4(t.Context(), Summary{})
	if err != nil || sum.Errors != 1 || w.repo4().SweepRecheck != "p1" {
		t.Fatalf("pending handoff: %+v,%v repo %+v", sum, err, w.repo4())
	}
	w.gh.fail = nil
	w.sweep4()
	w.sweep4()
	var proposal records.Proposal
	if err := records.Load(t.Context(), w.mem, store.Tenant(shopInstallation), records.ProposalKey(shopName, "p1"), &proposal); err != nil {
		t.Fatal(err)
	}
	if proposal.State != records.StateMerged || w.repo4().ApprovedDigest != pin.Digest || w.repo4().SweepRecheck != "" || !slices.Equal(w.gh.reruns, []int64{p.runID}) {
		t.Fatalf("proposal %+v, repo %+v, reruns %v", proposal, w.repo4(), w.gh.reruns)
	}
	rec, _ := w.pullRecord(p)
	if len(rec.Rechecks) != 1 || rec.Rechecks[0].Path != sweep.PathRerun {
		t.Errorf("rechecks = %+v", rec.Rechecks)
	}
	for _, after := range []string{merge, tip} {
		history := []api.Commit{{SHA: merge, Parents: []string{before}}}
		if after == tip {
			history = append(history, api.Commit{SHA: tip, Parents: []string{merge}})
		}
		w.gh.compares[shopName+"|"+before+"|"+after] = api.Comparison{Status: "ahead", Paths: []string{policyPath}, Commits: history, TotalCommits: len(history)}
		for range 2 {
			if err := w.gate.policyPushed(t.Context(), w.policyPush(before, after)); err != nil {
				t.Fatal(err)
			}
		}
		if r := w.repo4(); r.OutsideFlow != nil || r.ApprovedDigest != pin.Digest {
			t.Fatalf("delayed actual delivery undid recovery: %+v", r)
		}
	}
	if len(w.gh.reruns) != 1 {
		t.Errorf("delayed deliveries repeated the handoff: %v", w.gh.reruns)
	}
}

func TestSweepAmbiguousRangeNeverVoidsOrApproves(t *testing.T) {
	for _, mode := range []string{"two changes", "nonlinear", "unreadable", "index mismatch", "incomplete", "second index", "earlier policy change"} {
		t.Run(mode, func(t *testing.T) {
			w := newAskWorld(t)
			w.guarded4(policyAsk)
			p := w.pullBy(7)
			w.uploadAsk(p, "graph", "fail")
			w.ask(p, workerLogin, "/archfit request allow this dependency")
			old := w.tip
			merge := w.sha()
			last := w.sha()
			expected := policyAsk + "# approved\n"
			actual := expected
			pin := &records.Pin{Proposal: "p1", PullRequest: 30, HeadSHA: w.sha(), Digest: policy3.Digest([]byte(expected))}
			if err := records.Answer(t.Context(), w.mem, store.Tenant(shopInstallation), shopName, "a1",
				records.Approval{Kind: records.KindApprove, Request: firstRequest, Change: json.RawMessage("{}")}, pin); err != nil {
				t.Fatal(err)
			}
			w.gh.pulls[pullKey(shopName, 30)] = api.Pull{Number: 30, State: "closed", Merged: true, MergeCommitSHA: merge}
			nodes := []api.Commit{{SHA: merge, Parents: []string{old}}}
			switch mode {
			case "two changes":
				w.setFile(w.shop, merge, policyPath, []byte(expected))
				actual = expected + "# direct push\n"
				nodes = append(nodes, api.Commit{SHA: last, Parents: []string{merge}})
			case "earlier policy change":
				w.setFile(w.shop, last, policyPath, []byte(policyAsk+"# unapproved earlier\n"))
				nodes = []api.Commit{{SHA: last, Parents: []string{old}}, {SHA: merge, Parents: []string{last}}}
			case "nonlinear":
				nodes[0].Parents = append(nodes[0].Parents, w.sha())
			case "index mismatch":
				actual = expected + "# unapproved\n"
			}
			tip := w.tip4(actual, nodes...)
			if mode == "second index" {
				index := records.Index{Schema: records.SchemaProposalIndex}
				if err := records.Load(t.Context(), w.mem, store.Tenant(shopInstallation), records.ProposalIndexKey(shopName), &index); err != nil {
					t.Fatal(err)
				}
				if err := index.Add(records.PullEntry(31), "p2"); err != nil {
					t.Fatal(err)
				}
				w.put4(records.ProposalIndexKey(shopName), &index)
				w.put4(records.ProposalKey(shopName, "p2"), &records.Proposal{State: records.StateOpen, PullRequest: 31, ExpectedDigest: pin.Digest})
				w.gh.pulls[pullKey(shopName, 31)] = api.Pull{Number: 31, State: "open", BaseRef: "other"}
			}
			if mode == "unreadable" {
				w.gh.fail = map[string]error{"Compare": errors.New("cannot compare")}
			}
			if mode == "incomplete" {
				c := w.gh.compares[shopName+"|"+old+"|"+tip]
				c.TotalCommits = 2
				w.gh.compares[shopName+"|"+old+"|"+tip] = c
			}
			w.sweep4()
			w.sweep4()
			var proposal records.Proposal
			if err := records.Load(t.Context(), w.mem, store.Tenant(shopInstallation), records.ProposalKey(shopName, "p1"), &proposal); err != nil {
				t.Fatal(err)
			}
			if proposal.State != records.StateOpen || w.repo4().ApprovedDigest != policy3.Digest([]byte(policyAsk)) || w.repo4().OutsideFlow != nil {
				t.Fatalf("destructive ambiguous recovery: proposal %+v, repo %+v", proposal, w.repo4())
			}
			rec, _ := w.pullRecord(p)
			if rec.SweepNote != old+".."+tip {
				t.Errorf("note = %q", rec.SweepNote)
			}
			w.wantSweepGate(p, "completed", "action_required")
			if len(w.gh.issues[shopName]) != 1 || len(w.gh.reruns) != 0 {
				t.Fatalf("issues %+v, reruns %v", w.gh.issues[shopName], w.gh.reruns)
			}
		})
	}
}

func TestSweepRepairsAProvenDirectPushOutsideFlow(t *testing.T) {
	w := newWorld4(t)
	w.guarded4(policy4)
	p := w.openPull(w.shop, 7)
	w.wantStatus(w.send4(w.spec4(p, "pass")), "pass", "")
	before := w.tip
	tip := w.sha()
	w.tip4(policy4+"# direct\n", api.Commit{SHA: tip, Parents: []string{before}})
	w.sweep4()
	w.sweep4()
	if w.repo4().OutsideFlow == nil || len(w.gh.issues[shopName]) != 1 {
		t.Fatalf("repo %+v, issues %+v", w.repo4(), w.gh.issues[shopName])
	}
	w.wantSweepGate(p, "completed", "action_required")
}

func TestGateSweepRunsV4AndStopsOnARateLimit(t *testing.T) {
	w := newWorld4(t)
	p := w.openPull(w.shop, 7)
	w.editRun(p, func(run *api.WorkflowRun) { run.UpdatedAt = w.now.Add(-2 * time.Hour) })
	sum, err := w.gate.Sweep(t.Context())
	if err != nil || sum.Errors != 0 {
		t.Fatalf("Sweep = %+v,%v", sum, err)
	}
	w.wantSweepGate(p, "completed", "action_required")
	w.gh.fail = map[string]error{"ListRepos": api.ErrRateLimited}
	sum, err = w.gate.sweep4(t.Context(), Summary{})
	if err != nil || !sum.RateLimited {
		t.Fatalf("rate limited Sweep = %+v,%v", sum, err)
	}
}

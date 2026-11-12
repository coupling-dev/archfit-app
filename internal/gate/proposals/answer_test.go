package proposals

import (
	"context"
	"errors"
	"slices"
	"strconv"
	"strings"
	"sync"
	"testing"

	"github.com/alexei-led/archfit-app/internal/gate/records"
	"github.com/alexei-led/archfit-app/internal/gate/requests"
	"github.com/alexei-led/archfit-app/internal/github/api"
	"github.com/alexei-led/archfit-app/internal/wire/policy3"
)

func TestEachAnswerIsRecordedWrittenCheckedMergedAndNoted(t *testing.T) {
	tests := []struct {
		name     string
		rule     string
		from, to string
		answer   requests.Answer
		lines    string // the lines the merged policy gains
		passkey  bool
	}{
		{name: "approve a dependency", rule: "graph", from: "pkg_tc", to: "pkg_util",
			answer:  requests.Answer{Kind: policy3.Approve},
			lines:   "    depends_on:   # reviewed 2026-10-01\n      - pkg_net\n      - pkg_util  # request #431\n",
			passkey: true},
		{name: "approve another rule", rule: "coupling", from: "pkg_tc", to: "pkg_db",
			answer:  requests.Answer{Kind: policy3.Approve},
			lines:   "exceptions:\n  - rule: coupling  # request #431\n    from: pkg_tc\n    to: pkg_db\n    why: 'Approved in request #431.'\n",
			passkey: true},
		{name: "approve until a date", rule: "graph", from: "pkg_tc", to: "pkg_util",
			answer:  requests.Answer{Kind: policy3.ApproveUntil, Until: "2026-12-31"},
			lines:   "  - rule: graph  # request #431\n    from: pkg_tc\n    to: pkg_util\n    expires: 2026-12-31\n",
			passkey: true},
		{name: "reject with instead", rule: "graph", from: "pkg_db", to: "pkg_tc",
			answer: requests.Answer{Kind: policy3.Reject, Instead: "Go through pkg_net."},
			lines:  "  - id: request-431  # request #431\n    deny:\n      from: pkg_db\n      to: pkg_tc\n    why: 'Rejected in request #431.'\n    instead: Go through pkg_net.\n"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			w := newWorld(t)
			w.request(431, tt.rule, tt.from, tt.to)
			out := w.mustAnswer(olivia, 431, tt.answer, false)

			if out.State != requests.PullMerged || out.PullRequest == 0 || out.Merge == "" {
				t.Fatalf("outcome = %+v, want merged", out)
			}
			if got := w.hub.file(fakeBase, policyPath); !strings.Contains(got, tt.lines) {
				t.Errorf("the policy on main lacks\n%s\nit is\n%s", tt.lines, got)
			}
			a := load[records.Approval](t, w.store, records.ApprovalKey(testRepo.FullName, out.Approval))
			if a.Kind != string(tt.answer.Kind) || a.Owner != olivia.ID || a.Login != "olivia" || a.Request != 431 ||
				(a.Credential != "") != tt.passkey {
				t.Errorf("approval = %+v", a)
			}
			if got := w.requestState(431); got != records.StateAnswered {
				t.Errorf("request state = %q", got)
			}
			p := w.proposalOf(out)
			if p.PullRequest != out.PullRequest || p.State != records.StateOpen || len(p.Approvals) != 1 ||
				p.ExpectedDigest != policy3.Digest([]byte(w.hub.file(Branch, policyPath))) {
				t.Errorf("proposal = %+v; it stays open until the merge push consumes it (task 20)", p)
			}
			if !slices.Equal(w.hub.merges, []string{strconv.Itoa(out.PullRequest) + "@" + p.HeadSHA}) {
				t.Errorf("merges = %v, want one at the pinned head %s", w.hub.merges, p.HeadSHA)
			}
			if c := w.hub.checks; len(c) != 1 || c[0].Name != "archfit/gate" || c[0].HeadSHA != p.HeadSHA ||
				c[0].Conclusion != "success" || !strings.Contains(c[0].Summary, p.ExpectedDigest) {
				t.Errorf("checks = %+v", c)
			}
			note := w.hub.lastComment(431)
			if w.hub.issues[431] || !strings.Contains(note, "by `olivia`") || !strings.Contains(note, "merged in #"+strconv.Itoa(out.PullRequest)) ||
				strings.Contains(note, "passkey") != tt.passkey {
				t.Errorf("issue open = %v, note:\n%s", w.hub.issues[431], note)
			}
			body := w.hub.pulls[out.PullRequest].Body
			if !strings.Contains(body, "| #431 | ") || !strings.Contains(body, "`olivia`") {
				t.Errorf("pull request body:\n%s", body)
			}
		})
	}
}

func TestARejectWithNoPolicyLineAnswersWithoutAPullRequest(t *testing.T) {
	w := newWorld(t)
	w.request(431, "duplication", "pkg_tc", "pkg_util")
	out := w.mustAnswer(oscar, 431, requests.Answer{Kind: policy3.Reject, Instead: "Call pkg_util."}, false)
	if out.PullRequest != 0 || out.State != "" || w.hub.count("CommitOn") != 0 || w.hub.count("EnsurePull") != 0 {
		t.Errorf("outcome %+v, writes %v", out, w.hub.writes())
	}
	if got := w.requestState(431); got != records.StateAnswered {
		t.Errorf("request state = %q", got)
	}
	if note := w.hub.lastComment(431); w.hub.issues[431] || !strings.Contains(note, "this issue keeps it") ||
		!strings.Contains(note, "`Oscar`") {
		t.Errorf("issue open = %v, note:\n%s", w.hub.issues[431], note)
	}
}

func TestAnAnswerIsRefusedAndWritesNothing(t *testing.T) {
	approve := requests.Answer{Kind: policy3.Approve}
	tests := []struct {
		name string
		do   func(w *world) (Outcome, error)
		want error
	}{
		{"no passkey step-up", func(w *world) (Outcome, error) {
			return w.svc.Answer(context.Background(), testRepo, Input{Request: 431, Answer: approve, Owner: olivia})
		}, ErrNeedsPasskey},
		{"a token for another change", func(w *world) (Outcome, error) {
			token := w.stepUp(431, requests.Answer{Kind: policy3.ApproveUntil, Until: "2026-12-31"})
			return w.svc.Answer(context.Background(), testRepo, Input{Request: 431, Answer: approve, Owner: olivia,
				Redeem: w.redeem(token)})
		}, errToken},
		{"a reused token", func(w *world) (Outcome, error) {
			token := w.stepUp(431, approve)
			if _, err := w.redeem(token)(context.Background(), w.tokens[token]); err != nil {
				return Outcome{}, err
			}
			return w.svc.Answer(context.Background(), testRepo, Input{Request: 431, Answer: approve, Owner: olivia,
				Redeem: w.redeem(token)})
		}, errToken},
		{"an answer by a login the policy does not name", func(w *world) (Outcome, error) {
			return w.answer(Owner{ID: 600, Login: "mallory"}, 431, approve, false)
		}, ErrNotOwner},
		{"a request already answered", func(w *world) (Outcome, error) {
			return w.svc.Answer(context.Background(), testRepo, Input{Request: 433, Owner: oscar,
				Answer: requests.Answer{Kind: policy3.Reject, Instead: "No."}})
		}, requests.ErrClosedRequest},
		{"no request", func(w *world) (Outcome, error) {
			return w.svc.Answer(context.Background(), testRepo, Input{Request: 999, Answer: approve, Owner: olivia})
		}, requests.ErrNoRequest},
		{"a date in the past", func(w *world) (Outcome, error) {
			return w.svc.Answer(context.Background(), testRepo, Input{Request: 431, Owner: olivia,
				Answer: requests.Answer{Kind: policy3.ApproveUntil, Until: "2026-11-01"}})
		}, requests.ErrAnswer},
		{"a module the policy no longer has", func(w *world) (Outcome, error) {
			return w.answer(olivia, 432, approve, false)
		}, requests.ErrByHand},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			w := newWorld(t)
			w.request(431, "graph", "pkg_tc", "pkg_util")
			w.request(432, "graph", "pkg_tc", "pkg_gone")
			w.request(433, "graph", "pkg_db", "pkg_tc")
			w.mustAnswer(olivia, 433, requests.Answer{Kind: policy3.Reject, Instead: "No."}, true)
			before := w.snapshot()
			writes := len(w.hub.writes())
			_, err := tt.do(w)
			if !errors.Is(err, tt.want) {
				t.Fatalf("Answer = %v, want %v", err, tt.want)
			}
			if after := w.snapshot(); !mapsEqual(before, after) {
				t.Error("a refused answer changed a record")
			}
			if got := w.hub.writes()[writes:]; len(got) != 0 {
				t.Errorf("a refused answer wrote to GitHub: %v", got)
			}
		})
	}
}

func TestTheStepUpIsBoundToTheChangeDigest(t *testing.T) {
	w := newWorld(t)
	w.request(431, "graph", "pkg_tc", "pkg_util")
	a := requests.Answer{Kind: policy3.ApproveUntil, Until: "2026-12-31"}
	digest, err := w.svc.Digest(context.Background(), testRepo, 431, a)
	if err != nil {
		t.Fatal(err)
	}
	w.mustAnswer(olivia, 431, a, true)
	if !slices.Equal(w.redeemed, []string{digest}) || !strings.HasPrefix(digest, "sha256:") {
		t.Errorf("redeemed %v, want the digest %s", w.redeemed, digest)
	}
	if other, _ := w.svc.Digest(context.Background(), testRepo, 431, requests.Answer{Kind: policy3.Approve}); other == digest {
		t.Error("two answers share a digest")
	}
}

func TestARefusedMergeKeepsTheProposalOpen(t *testing.T) {
	w := newWorld(t)
	w.request(431, "graph", "pkg_tc", "pkg_util")
	w.hub.mergeErr = api.ErrNotMergeable
	out := w.mustAnswer(olivia, 431, requests.Answer{Kind: policy3.Approve}, false)
	if out.State != requests.PullRefused || out.PullRequest == 0 {
		t.Fatalf("outcome = %+v", out)
	}
	p := w.proposalOf(out)
	if p.State != records.StateOpen || w.hub.pulls[out.PullRequest].State != "open" || len(w.hub.checks) != 1 {
		t.Errorf("proposal %+v, pull %+v, checks %d", p, w.hub.pulls[out.PullRequest], len(w.hub.checks))
	}
	if !strings.Contains(w.hub.lastComment(431), "GitHub did not merge it") {
		t.Errorf("note:\n%s", w.hub.lastComment(431))
	}
	if w.hub.count("Pull") == 0 {
		t.Error("the pull request was not read after the refusal")
	}
}

func TestAMergeRefusedButDoneIsMerged(t *testing.T) {
	w := newWorld(t)
	w.request(431, "graph", "pkg_tc", "pkg_util")
	w.hub.mergeErr, w.hub.mergedBy = api.ErrNotMergeable, "c0ffee"
	out := w.mustAnswer(olivia, 431, requests.Answer{Kind: policy3.Approve}, false)
	if out.State != requests.PullMerged || out.Merge != "c0ffee" {
		t.Errorf("outcome = %+v, want merged by c0ffee", out)
	}
}

func TestTheMergeWaitsForThePullRequestToShowTheHead(t *testing.T) {
	w := newWorld(t)
	w.request(431, "graph", "pkg_tc", "pkg_util")
	w.hub.headLag = 3
	out := w.mustAnswer(olivia, 431, requests.Answer{Kind: policy3.Approve}, false)
	if out.State != requests.PullMerged {
		t.Errorf("outcome = %+v, want merged once the pull request shows the head", out)
	}
}

func TestTwoAnswersJoinOnePullRequest(t *testing.T) {
	w := newWorld(t)
	w.request(431, "graph", "pkg_tc", "pkg_util")
	w.request(432, "graph", "pkg_tc", "pkg_db")
	first := w.mustAnswer(olivia, 431, requests.Answer{Kind: policy3.Approve}, true)
	if first.State != requests.PullLater || len(w.hub.merges) != 0 {
		t.Fatalf("merge later: outcome %+v, merges %v", first, w.hub.merges)
	}
	firstHead := w.proposalOf(first).HeadSHA
	if !strings.Contains(w.hub.lastComment(431), "takes effect when #"+strconv.Itoa(first.PullRequest)+" merges") {
		t.Errorf("note:\n%s", w.hub.lastComment(431))
	}

	second := w.mustAnswer(oscar, 432, requests.Answer{Kind: policy3.Approve}, false)
	if second.PullRequest != first.PullRequest || second.State != requests.PullMerged {
		t.Fatalf("second outcome = %+v, want the same pull request, merged", second)
	}
	p := w.proposalOf(second)
	if !slices.Equal(p.Approvals, []string{first.Approval, second.Approval}) {
		t.Errorf("approvals = %v", p.Approvals)
	}
	if parent := w.hub.commits[p.HeadSHA].parent; parent != firstHead {
		t.Errorf("the second commit's parent = %s, want the first head %s: a fast-forward", parent, firstHead)
	}
	want := "      - pkg_util  # request #431\n      - pkg_db  # request #432\n"
	if got := w.hub.file(fakeBase, policyPath); !strings.Contains(got, want) {
		t.Errorf("merged policy:\n%s", got)
	}
	if body := w.hub.pulls[second.PullRequest].Body; !strings.Contains(body, "| #431 | approve | `olivia` |") ||
		!strings.Contains(body, "| #432 | approve | `Oscar` |") {
		t.Errorf("body names every answer:\n%s", body)
	}
}

func TestATargetThatMovesBetweenTwoAnswers(t *testing.T) {
	t.Run("another file: a fast-forward on the head", func(t *testing.T) {
		w := newWorld(t)
		w.request(431, "graph", "pkg_tc", "pkg_util")
		w.request(432, "graph", "pkg_tc", "pkg_db")
		first := w.mustAnswer(olivia, 431, requests.Answer{Kind: policy3.Approve}, true)
		head := w.proposalOf(first).HeadSHA
		w.hub.push(fakeBase, "README.md", "moved")
		second := w.mustAnswer(olivia, 432, requests.Answer{Kind: policy3.Approve}, true)
		if p := w.proposalOf(second); w.hub.commits[p.HeadSHA].parent != head {
			t.Errorf("the head moved off the branch: parent %s, want %s", w.hub.commits[p.HeadSHA].parent, head)
		}
	})
	t.Run("the policy: a rewrite from the new tip with both answers", func(t *testing.T) {
		w := newWorld(t)
		w.request(431, "graph", "pkg_tc", "pkg_util")
		w.request(432, "graph", "pkg_tc", "pkg_db")
		first := w.mustAnswer(olivia, 431, requests.Answer{Kind: policy3.Approve}, true)
		oldDigest := w.proposalOf(first).ExpectedDigest
		tip := w.hub.push(fakeBase, policyPath, testPolicy+"# moved\n")
		second := w.mustAnswer(olivia, 432, requests.Answer{Kind: policy3.Approve}, false)
		if second.State != requests.PullMerged {
			t.Fatalf("outcome = %+v", second)
		}
		p := w.proposalOf(second)
		merged := w.hub.file(fakeBase, policyPath)
		if w.hub.commits[p.HeadSHA].parent != tip || p.ExpectedDigest == oldDigest ||
			!strings.Contains(merged, "      - pkg_util  # request #431\n      - pkg_db  # request #432\n") ||
			!strings.HasSuffix(merged, "# moved\n") {
			t.Errorf("head parent %s (tip %s), digest %s (old %s), merged:\n%s", w.hub.commits[p.HeadSHA].parent, tip,
				p.ExpectedDigest, oldDigest, merged)
		}
	})
	t.Run("someone pushed to the branch: a rewrite that drops the push", func(t *testing.T) {
		w := newWorld(t)
		w.request(431, "graph", "pkg_tc", "pkg_util")
		w.request(432, "graph", "pkg_tc", "pkg_db")
		w.mustAnswer(olivia, 431, requests.Answer{Kind: policy3.Approve}, true)
		w.hub.push(Branch, "evil.go", "package evil")
		second := w.mustAnswer(olivia, 432, requests.Answer{Kind: policy3.Approve}, false)
		if second.State != requests.PullMerged || w.hub.file(fakeBase, "evil.go") != "" {
			t.Errorf("outcome %+v; evil.go on main = %q", second, w.hub.file(fakeBase, "evil.go"))
		}
	})
}

func TestAChangeThatNoLongerAppliesIsVoidAndItsRequestOpensAgain(t *testing.T) {
	w := newWorld(t)
	w.request(431, "graph", "pkg_tc", "pkg_util")
	w.request(432, "graph", "pkg_tc", "pkg_db")
	first := w.mustAnswer(olivia, 431, requests.Answer{Kind: policy3.Approve}, true)
	without := strings.Replace(testPolicy, "  pkg_util:\n    paths: [pkg/util/**]\n", "", 1)
	w.hub.push(fakeBase, policyPath, without)

	second := w.mustAnswer(olivia, 432, requests.Answer{Kind: policy3.Approve}, false)
	if second.State != requests.PullMerged || second.PullRequest != first.PullRequest {
		t.Fatalf("outcome = %+v", second)
	}
	if a := load[records.Approval](t, w.store, records.ApprovalKey(testRepo.FullName, first.Approval)); a.State != records.StateVoid {
		t.Errorf("the first approval is %q, want void", a.State)
	}
	if got := w.requestState(431); got != records.StateOpen || !w.hub.issues[431] ||
		!strings.Contains(w.hub.lastComment(431), "open again") {
		t.Errorf("request 431: state %q, issue open %v, note:\n%s", got, w.hub.issues[431], w.hub.lastComment(431))
	}
	merged := w.hub.file(fakeBase, policyPath)
	if strings.Contains(merged, "- pkg_util") || !strings.Contains(merged, "- pkg_db  # request #432") {
		t.Errorf("merged policy:\n%s", merged)
	}
}

func TestAPullRequestClosedWithoutAMergeVoidsItsProposal(t *testing.T) {
	w := newWorld(t)
	w.request(431, "graph", "pkg_tc", "pkg_util")
	w.request(432, "graph", "pkg_tc", "pkg_db")
	first := w.mustAnswer(olivia, 431, requests.Answer{Kind: policy3.Approve}, true)
	w.hub.pulls[first.PullRequest].State = "closed"

	second := w.mustAnswer(olivia, 432, requests.Answer{Kind: policy3.Approve}, true)
	if second.PullRequest == first.PullRequest {
		t.Errorf("the answer joined a closed pull request")
	}
	if p := load[records.Proposal](t, w.store, records.ProposalKey(testRepo.FullName,
		load[records.Approval](t, w.store, records.ApprovalKey(testRepo.FullName, first.Approval)).Proposal)); p.State != records.StateVoid {
		t.Errorf("the first proposal is %q, want void", p.State)
	}
	if got := w.requestState(431); got != records.StateOpen || !strings.Contains(w.hub.lastComment(431), "closed without a merge") {
		t.Errorf("request 431: %q, note:\n%s", got, w.hub.lastComment(431))
	}
	if got := w.hub.file(Branch, policyPath); strings.Contains(got, "pkg_util  #") {
		t.Errorf("the new proposal carries the void answer:\n%s", got)
	}
}

func TestAMergedProposalWaitsForItsPushAndANewOneOpens(t *testing.T) {
	w := newWorld(t)
	w.request(431, "graph", "pkg_tc", "pkg_util")
	w.request(432, "graph", "pkg_tc", "pkg_db")
	first := w.mustAnswer(olivia, 431, requests.Answer{Kind: policy3.Approve}, false)
	second := w.mustAnswer(olivia, 432, requests.Answer{Kind: policy3.Approve}, false)
	if second.PullRequest == first.PullRequest || second.State != requests.PullMerged {
		t.Fatalf("second = %+v, want a new merged pull request", second)
	}
	if p := w.proposalOf(first); p.State != records.StateOpen {
		t.Errorf("the merged proposal is %q; only its push consumes it (task 20)", p.State)
	}
	if got := w.hub.file(fakeBase, policyPath); !strings.Contains(got, "- pkg_util  # request #431") ||
		!strings.Contains(got, "- pkg_db  # request #432") {
		t.Errorf("policy on main:\n%s", got)
	}
}

func TestTwoAnswersAtOnceGoIntoOnePullRequest(t *testing.T) {
	w := newWorld(t)
	w.request(431, "graph", "pkg_tc", "pkg_util")
	w.request(432, "graph", "pkg_tc", "pkg_db")
	ins := []Input{
		{Request: 431, Answer: requests.Answer{Kind: policy3.Approve}, Owner: olivia, Later: true},
		{Request: 432, Answer: requests.Answer{Kind: policy3.Approve}, Owner: oscar, Later: true},
	}
	for i := range ins {
		ins[i].Redeem = w.redeem(w.stepUp(ins[i].Request, ins[i].Answer))
	}
	w.svc.sleep = sleepCtx // the second answer really waits for the lease
	outs := make([]Outcome, 2)
	errs := make([]error, 2)
	var wg sync.WaitGroup
	for i := range ins {
		wg.Go(func() { outs[i], errs[i] = w.svc.Answer(context.Background(), testRepo, ins[i]) })
	}
	wg.Wait()
	if errs[0] != nil || errs[1] != nil || outs[0].PullRequest != outs[1].PullRequest {
		t.Fatalf("outcomes %+v, errors %v", outs, errs)
	}
	p := w.proposalOf(outs[0])
	branch := w.hub.file(Branch, policyPath)
	if len(p.Approvals) != 2 || p.HeadSHA != w.hub.branches[Branch] || p.ExpectedDigest != policy3.Digest([]byte(branch)) ||
		!strings.Contains(branch, "request #431") || !strings.Contains(branch, "request #432") {
		t.Errorf("proposal %+v, branch head %s:\n%s", p, w.hub.branches[Branch], branch)
	}
}

func TestNoPolicyOnTheDefaultBranch(t *testing.T) {
	w := newWorld(t)
	w.hub.branches[fakeBase] = w.hub.put("", "empty", map[string][]byte{"README.md": []byte("x")})
	w.request(431, "graph", "pkg_tc", "pkg_util")
	if _, err := w.answer(olivia, 431, requests.Answer{Kind: policy3.Reject, Instead: "No."}, false); !errors.Is(err, requests.ErrNoPolicy) {
		t.Errorf("err = %v, want ErrNoPolicy", err)
	}
}

func mapsEqual(a, b map[string]string) bool {
	if len(a) != len(b) {
		return false
	}
	for k, v := range a {
		if b[k] != v {
			return false
		}
	}
	return true
}

func TestAFullProposalTakesNoAnswerUntilItMerges(t *testing.T) {
	w := newWorld(t)
	mods := []string{"pkg_util", "pkg_db"}
	for i := range records.MaxApprovals {
		n := 500 + i
		w.request(n, "coupling", "pkg_tc", mods[i%2]+"-"+strconv.Itoa(i))
		w.mustAnswer(olivia, n, requests.Answer{Kind: policy3.Reject, Instead: "No " + strconv.Itoa(i) + "."}, true)
	}
	w.request(431, "graph", "pkg_tc", "pkg_util")
	writes := len(w.hub.writes())
	if _, err := w.answer(olivia, 431, requests.Answer{Kind: policy3.Approve}, false); !errors.Is(err, ErrFull) {
		t.Fatalf("err = %v, want ErrFull", err)
	}
	if got := w.hub.writes()[writes:]; len(got) != 0 || len(w.redeemed) != 0 {
		t.Errorf("a refused answer wrote %v or used the token %v", got, w.redeemed)
	}
}

func TestAnExceptionOfAnOnlyRuleHasNoTo(t *testing.T) {
	w := newWorld(t)
	w.hub.push(fakeBase, policyPath, testPolicy+"  - id: no-docker\n    only:\n      use: github.com/docker/docker\n      in: [pkg_net]\n"+
		"    why: One adapter owns the Docker API.\n")
	w.request(431, "no-docker", "pkg_tc", "github.com/docker/docker")
	out := w.mustAnswer(olivia, 431, requests.Answer{Kind: policy3.ApproveUntil, Until: "2026-12-31"}, false)
	want := "exceptions:\n  - rule: no-docker  # request #431\n    from: pkg_tc\n    expires: 2026-12-31\n"
	if got := w.hub.file(fakeBase, policyPath); out.State != requests.PullMerged || !strings.Contains(got, want) {
		t.Errorf("outcome %+v, policy on main:\n%s", out, got)
	}
}

// TestARefusedAnswerStillRecordsWhatItFound: an answer refused after it voided a proposal whose pull request was closed
// keeps that cleanup, and records nothing of itself.
func TestARefusedAnswerStillRecordsWhatItFound(t *testing.T) {
	w := newWorld(t)
	w.request(431, "graph", "pkg_tc", "pkg_util")
	w.request(432, "graph", "pkg_tc", "pkg_db")
	first := w.mustAnswer(olivia, 431, requests.Answer{Kind: policy3.Approve}, true)
	w.hub.pulls[first.PullRequest].State = "closed"

	approve := requests.Answer{Kind: policy3.Approve}
	_, err := w.svc.Answer(context.Background(), testRepo, Input{Request: 432, Answer: approve, Owner: olivia,
		Redeem: w.redeem("an expired token")})
	if !errors.Is(err, errToken) {
		t.Fatalf("err = %v, want the token's refusal", err)
	}
	if got := w.requestState(431); got != records.StateOpen || !w.hub.issues[431] {
		t.Errorf("the closed proposal's request is %q, issue open %v; its cleanup must stand", got, w.hub.issues[431])
	}
	if got := w.requestState(432); got != records.StateOpen {
		t.Errorf("the refused answer's request is %q, want still open", got)
	}
	if ok, _ := IsProposalPull(context.Background(), w.store, testRepo, first.PullRequest); ok {
		t.Error("the closed proposal is still in the index")
	}
}

// TestAnAnswerKeepsGoingAfterItsCallerLeaves: once recorded, the check, the merge and the issue are done although the
// caller's context ended (a closed tab, the UI deadline).
func TestAnAnswerKeepsGoingAfterItsCallerLeaves(t *testing.T) {
	w := newWorld(t)
	w.request(431, "graph", "pkg_tc", "pkg_util")
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	w.hub.hooks["UpdatePullBody"] = cancel // the first call after the record
	a := requests.Answer{Kind: policy3.Approve}
	out, err := w.svc.Answer(ctx, testRepo, Input{Request: 431, Answer: a, Owner: olivia, Redeem: w.redeem(w.stepUp(431, a))})
	if err != nil || out.State != requests.PullMerged {
		t.Fatalf("outcome %+v, %v; want merged", out, err)
	}
	if w.hub.issues[431] || !strings.Contains(w.hub.lastComment(431), "merged in #") {
		t.Errorf("issue open %v, note:\n%s", w.hub.issues[431], w.hub.lastComment(431))
	}
}

// TestAnAnswerJoinsWhenOnlyTheOpenProposalSaysIt: the default branch does not allow the dependency yet, an earlier
// answer waiting to merge does. The new answer travels with that proposal, and is voided with it.
func TestAnAnswerJoinsWhenOnlyTheOpenProposalSaysIt(t *testing.T) {
	w := newWorld(t)
	w.request(431, "graph", "pkg_tc", "pkg_util")
	first := w.mustAnswer(olivia, 431, requests.Answer{Kind: policy3.Approve}, true)
	w.request(433, "graph", "pkg_tc", "pkg_util") // the same key, asked again while the first waits to merge
	second := w.mustAnswer(oscar, 433, requests.Answer{Kind: policy3.Approve}, true)
	if second.PullRequest != first.PullRequest || second.State != requests.PullLater {
		t.Fatalf("second = %+v, want it in #%d", second, first.PullRequest)
	}
	if p := w.proposalOf(second); !slices.Equal(p.Approvals, []string{first.Approval, second.Approval}) {
		t.Errorf("approvals = %v", p.Approvals)
	}

	w.hub.pulls[first.PullRequest].State = "closed"
	w.request(434, "graph", "pkg_tc", "pkg_db")
	w.mustAnswer(olivia, 434, requests.Answer{Kind: policy3.Approve}, true)
	for _, n := range []int{431, 433} {
		if got := w.requestState(n); got != records.StateAnswered && got != records.StateOpen {
			t.Errorf("request %d = %q", n, got)
		}
		if a := load[records.Approval](t, w.store, records.ApprovalKey(testRepo.FullName,
			map[int]string{431: first.Approval, 433: second.Approval}[n])); a.State != records.StateVoid {
			t.Errorf("the approval of request %d is %q, want void with its closed proposal", n, a.State)
		}
	}
}

// TestAVoidedApprovalFreesItsPlace: the capacity counts the answers the branch holds, not the ones voided.
func TestAVoidedApprovalFreesItsPlace(t *testing.T) {
	w := newWorld(t)
	extra := ""
	for i := range records.MaxApprovals {
		extra += "  m" + strconv.Itoa(i) + ":\n    paths: [m" + strconv.Itoa(i) + "/**]\n"
	}
	full := strings.Replace(testPolicy, "rules:\n", extra+"rules:\n", 1)
	w.hub.push(fakeBase, policyPath, full)
	for i := range records.MaxApprovals {
		w.request(500+i, "graph", "pkg_tc", "m"+strconv.Itoa(i))
		w.mustAnswer(olivia, 500+i, requests.Answer{Kind: policy3.Approve}, true)
	}
	w.request(431, "graph", "pkg_tc", "pkg_util")
	if _, err := w.answer(olivia, 431, requests.Answer{Kind: policy3.Approve}, true); !errors.Is(err, ErrFull) {
		t.Fatalf("a tenth answer = %v, want ErrFull", err)
	}
	w.hub.push(fakeBase, policyPath, strings.Replace(full, "  m0:\n    paths: [m0/**]\n", "", 1))
	out, err := w.answer(olivia, 431, requests.Answer{Kind: policy3.Approve}, true)
	if err != nil {
		t.Fatalf("an answer after one approval was voided = %v", err)
	}
	if p := w.proposalOf(out); len(p.Approvals) != records.MaxApprovals {
		t.Errorf("approvals = %d, want %d: the voided one left, the new one joined", len(p.Approvals), records.MaxApprovals)
	}
}

// TestAnAnswerThatWritesNoLineResyncsWithoutMerging: earlier answers that no longer apply are voided and the branch
// rewritten and pinned again, but an answer that joins nothing never merges the proposal.
func TestAnAnswerThatWritesNoLineResyncsWithoutMerging(t *testing.T) {
	t.Run("answers kept: re-pinned and checked, not merged", func(t *testing.T) {
		w := newWorld(t)
		w.request(431, "graph", "pkg_tc", "pkg_util")
		w.request(432, "graph", "pkg_tc", "pkg_db")
		w.request(433, "duplication", "pkg_tc", "pkg_net")
		w.mustAnswer(olivia, 431, requests.Answer{Kind: policy3.Approve}, true)
		kept := w.mustAnswer(olivia, 432, requests.Answer{Kind: policy3.Approve}, true)
		w.hub.push(fakeBase, policyPath, strings.Replace(testPolicy, "  pkg_util:\n    paths: [pkg/util/**]\n", "", 1))
		checks := len(w.hub.checks)

		out := w.mustAnswer(oscar, 433, requests.Answer{Kind: policy3.Reject, Instead: "Call pkg_net."}, false)
		if out.PullRequest != 0 || len(w.hub.merges) != 0 {
			t.Errorf("outcome %+v, merges %v: an answer that joins nothing must not merge", out, w.hub.merges)
		}
		p := w.proposalOf(kept)
		branch := w.hub.file(Branch, policyPath)
		if p.HeadSHA != w.hub.branches[Branch] || p.ExpectedDigest != policy3.Digest([]byte(branch)) ||
			strings.Contains(branch, "request #431") || !strings.Contains(branch, "request #432") {
			t.Errorf("proposal %+v, branch:\n%s", p, branch)
		}
		if len(w.hub.checks) != checks+1 || w.hub.checks[len(w.hub.checks)-1].HeadSHA != p.HeadSHA {
			t.Errorf("checks %+v; want one on the new head", w.hub.checks[checks:])
		}
		if got := w.requestState(431); got != records.StateOpen {
			t.Errorf("request 431 = %q, want open again", got)
		}
	})
	t.Run("no answer kept: the proposal is void and leaves the index", func(t *testing.T) {
		w := newWorld(t)
		w.request(431, "graph", "pkg_tc", "pkg_util")
		w.request(433, "duplication", "pkg_tc", "pkg_net")
		first := w.mustAnswer(olivia, 431, requests.Answer{Kind: policy3.Approve}, true)
		w.hub.push(fakeBase, policyPath, strings.Replace(testPolicy, "  pkg_util:\n    paths: [pkg/util/**]\n", "", 1))

		w.mustAnswer(oscar, 433, requests.Answer{Kind: policy3.Reject, Instead: "Call pkg_net."}, false)
		if p := w.proposalOf(first); p.State != records.StateVoid {
			t.Errorf("proposal = %q, want void", p.State)
		}
		if ok, _ := IsProposalPull(context.Background(), w.store, testRepo, first.PullRequest); ok {
			t.Error("the void proposal's pull request is still in the index")
		}
		if w.hub.branches[Branch] != w.hub.branches[fakeBase] || len(w.hub.merges) != 0 {
			t.Errorf("branch %s, main %s, merges %v", w.hub.branches[Branch], w.hub.branches[fakeBase], w.hub.merges)
		}
	})
}

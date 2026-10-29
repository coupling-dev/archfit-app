package gate

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/alexei-led/archfit-app/internal/gate/decisions"
	"github.com/alexei-led/archfit-app/internal/github/api"
	"github.com/alexei-led/archfit-app/internal/github/webhook"
	"github.com/alexei-led/archfit-app/internal/store"
	"github.com/alexei-led/archfit-app/internal/wire/decision"
	"github.com/alexei-led/archfit-app/internal/wire/policy"
	"github.com/alexei-led/archfit-app/internal/wire/settings"
)

const (
	agentLogin = "agent-7"
	findingA   = "0a0a0a0a0a0a0a0a0a0a0a0a0a0a0a0a"
	findingB   = "0b0b0b0b0b0b0b0b0b0b0b0b0b0b0b0b"
	candidate  = "version: 2\nmodules:\n  a:\n    paths: [\"pkg/a/**\"]\n  b:\n    paths: [\"pkg/b/**\"]\n"
)

// requestDoc is a request about the pull's current head under the policy on the default branch, field by field.
func (w *world) requestDoc(p *pull) map[string]any {
	return map[string]any{
		"schema_version": decision.SchemaV1, "finding_id": findingA, "rule_id": "boundary.no-import",
		"edge": map[string]any{"from": "a", "to": "b"}, "head_sha": p.head,
		"policy_digest":      w.digestOf(p.repo, testBranch, policy.PolicyPath),
		"why":                "a needs b for the read model",
		"alternatives_tried": []any{"moved the type"},
		"proposal":           map[string]any{"kind": "policy_patch", "policy": candidate},
	}
}

// requestComment is the comment an agent posts with gh pr comment --body-file.
func requestComment(t *testing.T, doc map[string]any) string {
	t.Helper()
	raw, err := json.Marshal(doc)
	if err != nil {
		t.Fatal(err)
	}
	return decision.OpenMarker + "\n```json\n" + string(raw) + "\n```\n" + decision.CloseMarker
}

func (w *world) commentEvent(p *pull, body string) webhook.Event {
	e := w.event(webhook.PullCommented, p)
	e.HeadSHA, e.HeadRepo, e.BaseRef = "", "", ""
	e.CommentID, e.Author, e.AuthorType, e.Body = 9000+int64(w.nextSHA), agentLogin, "User", body
	w.nextSHA++
	return e
}

func (w *world) requests() *decisions.Requests { return w.gate.requests.(*decisions.Requests) }

func (w *world) askFor(p *pull, mutate ...func(map[string]any)) webhook.Event {
	doc := w.requestDoc(p)
	for _, m := range mutate {
		m(doc)
	}
	return w.commentEvent(p, requestComment(w.t, doc))
}

func (w *world) record(p *pull, finding string) decisions.Record {
	w.t.Helper()
	rec, err := w.requests().Get(context.Background(), w.target(p), finding, w.digestOf(p.repo, testBranch, policy.PolicyPath))
	if err != nil {
		w.t.Fatalf("record: %v", err)
	}
	return rec
}

func (w *world) noRecords() {
	w.t.Helper()
	if _, err := w.mem.Get(context.Background(), store.Tenant(shopInstallation), "decision/acme/shop/pending"); !errors.Is(err, store.ErrNotFound) {
		w.t.Fatalf("a decision record exists (%v)", err)
	}
}

// statusComments are the App's decision status comments on the pull.
func (w *world) statusComments(p *pull) []string {
	var out []string
	for _, c := range w.comments(p) {
		if strings.HasPrefix(c.Body, decision.StatusMarkerPrefix) {
			out = append(out, c.Body)
		}
	}
	return out
}

func (w *world) wantStatus(p *pull, finding, line string) {
	w.t.Helper()
	marker := decision.StatusMarker(finding, w.digestOf(p.repo, testBranch, policy.PolicyPath))
	want := marker + "\n" + line + "\nOnly an architecture owner can approve this request."
	got := w.statusComments(p)
	if len(got) != 1 || got[0] != want {
		w.t.Fatalf("status comments = %q, want exactly %q", got, want)
	}
}

func TestDecisionRequestIsRecordedAndAnswered(t *testing.T) {
	w := newWorld(t)
	p := w.openPull(w.shop, 7)
	w.handle(w.askFor(p))

	rec := w.record(p, findingA)
	if rec.Status != decision.StatusPending || rec.Requester != agentLogin || rec.HeadSHA != p.head || rec.EdgeFrom != "a" || rec.CommentID == 0 {
		t.Errorf("record = %+v", rec)
	}
	if pol, err := w.requests().Policy(context.Background(), w.target(p), findingA, rec.PolicyDigest); err != nil || pol != candidate {
		t.Errorf("stored policy = %q, %v", pol, err)
	}
	w.wantStatus(p, findingA, "pending")
	if rec.StatusCommentID != w.comments(p)[0].ID {
		t.Errorf("record names status comment %d, the thread has %d", rec.StatusCommentID, w.comments(p)[0].ID)
	}
	// The record grants nothing: no approval, no check.
	if len(w.gh.checkWrites) != 0 {
		t.Errorf("a request wrote a check: %+v", w.gh.checkWrites)
	}
	if _, err := w.mem.Get(context.Background(), store.Tenant(shopInstallation), "approval/acme/shop/7/"+p.head); !errors.Is(err, store.ErrNotFound) {
		t.Errorf("a request left an approval (%v)", err)
	}
}

func TestDecisionRequestSameHeadWritesNothing(t *testing.T) {
	w := newWorld(t)
	p := w.openPull(w.shop, 7)
	w.handle(w.askFor(p))
	before, ops := w.record(p, findingA), w.gh.commentOps

	w.now = w.now.Add(time.Hour)
	w.handle(w.askFor(p))
	after := w.record(p, findingA)
	if !after.UpdatedAt.Equal(before.UpdatedAt) || after.CommentID != before.CommentID || after.StatusCommentID != before.StatusCommentID {
		t.Errorf("record changed: %+v -> %+v", before, after)
	}
	if w.gh.commentOps != ops {
		t.Errorf("comment writes %d -> %d", ops, w.gh.commentOps)
	}
	w.wantStatus(p, findingA, "pending")
}

func TestDecisionRefileOnANewHeadReplacesThePendingRecord(t *testing.T) {
	w := newWorld(t)
	p := w.openPull(w.shop, 7)
	w.handle(w.askFor(p))
	first := w.record(p, findingA)

	w.push(p)
	w.handle(w.event(webhook.PullSynchronized, p))
	w.wantStatus(p, findingA, "stale (head moved)")
	w.now = w.now.Add(time.Hour)
	w.handle(w.askFor(p))

	rec := w.record(p, findingA)
	if rec.Status != decision.StatusPending || rec.HeadSHA != p.head || rec.HeadSHA == first.HeadSHA || rec.Reason != "" {
		t.Errorf("record = %+v", rec)
	}
	if rec.StatusCommentID != first.StatusCommentID {
		t.Errorf("status comment %d -> %d: it is edited in place", first.StatusCommentID, rec.StatusCommentID)
	}
	w.wantStatus(p, findingA, "pending")
}

func TestDecisionSynchronizeKeepsARequestOnTheCurrentHead(t *testing.T) {
	w := newWorld(t)
	p := w.openPull(w.shop, 7)
	w.push(p)
	w.handle(w.askFor(p))
	w.handle(w.event(webhook.PullSynchronized, p)) // a redelivery for the head the request names
	if rec := w.record(p, findingA); rec.Status != decision.StatusPending {
		t.Errorf("record = %+v", rec)
	}
	w.wantStatus(p, findingA, "pending")
}

func TestDecisionDeclineSticksUntilThePolicyDigestChanges(t *testing.T) {
	w := newWorld(t)
	p := w.openPull(w.shop, 7)
	w.handle(w.askFor(p))
	digest := w.digestOf(p.repo, testBranch, policy.PolicyPath)
	if err := w.requests().Settle(context.Background(), w.target(p), findingA, digest, decision.StatusDeclined, "", 0, w.now); err != nil {
		t.Fatal(err)
	}
	w.wantStatus(p, findingA, "declined")
	ops := w.gh.commentOps

	w.push(p)
	w.handle(w.askFor(p))
	rec := w.record(p, findingA)
	if rec.Status != decision.StatusDeclined || rec.HeadSHA == p.head {
		t.Errorf("a refile on a new head moved a declined record: %+v", rec)
	}
	if w.gh.commentOps != ops {
		t.Errorf("a declined request got a comment write")
	}

	// A new policy digest is a new key: the same finding is asked again.
	w.setFile(w.shop, testBranch, policy.PolicyPath, []byte(basePolicy+"# changed\n"))
	w.handle(w.askFor(p))
	if got := w.record(p, findingA); got.Status != decision.StatusPending {
		t.Errorf("record under the new digest = %+v", got)
	}
}

func TestDecisionRequestThatFailsACheckGetsOneAnswerAndNoRecord(t *testing.T) {
	tests := []struct {
		name   string
		mutate func(map[string]any)
		body   func(string) string
		want   string // start of the status line
	}{
		{"unknown key", func(d map[string]any) { d["blast_radius"] = 3 }, nil, "declined (unknown key)"},
		{"wrong schema version", func(d map[string]any) { d["schema_version"] = "v2" }, nil, "declined (schema_version: must equal archfit.decision-request.v1)"},
		{"bad why", func(d map[string]any) { d["why"] = "" }, nil, "declined (why: must be 1 to 2000 characters)"},
		{"block not closed", nil, func(b string) string { return strings.Replace(b, decision.CloseMarker, "", 1) }, "declined (request block is not closed)"},
		{"head moved", func(d map[string]any) { d["head_sha"] = strings.Repeat("e", 40) }, nil, "stale (head moved)"},
		{"policy changed", func(d map[string]any) { d["policy_digest"] = strings.Repeat("c", 64) }, nil, "stale (policy changed)"},
		{"policy not valid", func(d map[string]any) {
			d["proposal"] = map[string]any{"kind": "policy_patch", "policy": "not: [valid"}
		}, nil, "declined (proposal.policy: not a valid policy file"},
		{"policy with an unknown key", func(d map[string]any) {
			d["proposal"] = map[string]any{"kind": "policy_patch", "policy": "version: 2\nwhat: 1\n"}
		}, nil, "declined (proposal.policy: not a valid policy file"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			w := newWorld(t)
			p := w.openPull(w.shop, 7)
			doc := w.requestDoc(p)
			if tt.mutate != nil {
				tt.mutate(doc)
			}
			body := requestComment(t, doc)
			if tt.body != nil {
				body = tt.body(body)
			}
			e := w.commentEvent(p, body)
			w.handle(e)
			w.handle(e) // a redelivery

			w.noRecords()
			got := w.statusComments(p)
			if len(got) != 1 {
				t.Fatalf("status comments = %q, want one", got)
			}
			lines := strings.Split(got[0], "\n")
			if len(lines) != 3 || !strings.HasPrefix(lines[0], decision.StatusMarkerPrefix) || !strings.HasPrefix(lines[1], tt.want) {
				t.Errorf("status comment = %q, want line 2 to start %q", got[0], tt.want)
			}
			if w.gh.commentOps != 1 {
				t.Errorf("comment writes = %d: a redelivery must write nothing", w.gh.commentOps)
			}
		})
	}
}

func TestDecisionAnswerNeverOverwritesAGoodRequest(t *testing.T) {
	w := newWorld(t)
	p := w.openPull(w.shop, 7)
	w.handle(w.askFor(p))
	w.handle(w.askFor(p, func(d map[string]any) { d["why"] = "" })) // same IDs, now invalid
	w.wantStatus(p, findingA, "pending")
}

func TestDecisionRequestOnAPullTheGateDoesNotDecideIsIgnored(t *testing.T) {
	tests := []struct {
		name  string
		setup func(w *world, p *pull)
		event func(w *world, p *pull) webhook.Event
	}{
		{"fork", func(w *world, p *pull) { w.editPull(p, func(pl *api.Pull) { pl.HeadRepo = "mallory/shop" }) }, nil},
		{"closed pull request", func(w *world, p *pull) { w.editPull(p, func(pl *api.Pull) { pl.State = "closed" }) }, nil},
		{"other base", func(w *world, p *pull) { w.editPull(p, func(pl *api.Pull) { pl.BaseRef = "release" }) }, nil},
		{"not onboarded", func(w *world, p *pull) { w.removeFile(w.shop, testBranch, settings.Path) }, nil},
		{"advisory mode", func(w *world, p *pull) { w.setSettings(w.shop, settings.GateAdvisory) }, nil},
		{"no block", nil, func(w *world, p *pull) webhook.Event { return w.commentEvent(p, "LGTM") }},
		{"the App's own comment", nil, func(w *world, p *pull) webhook.Event {
			own, err := w.gh.CreateComment(context.Background(), p.repo.RepoRef, p.number, requestComment(t, w.requestDoc(p)))
			if err != nil {
				t.Fatal(err)
			}
			e := w.askFor(p)
			e.CommentID, e.AuthorType = own.ID, "Bot"
			w.gh.commentOps = 0
			return e
		}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			w := newWorld(t)
			p := w.openPull(w.shop, 7)
			if tt.setup != nil {
				tt.setup(w, p)
			}
			e := w.askFor(p)
			if tt.event != nil {
				e = tt.event(w, p)
			}
			before := len(w.comments(p))
			w.handle(e)
			w.noRecords()
			if len(w.comments(p)) != before || w.gh.commentOps != 0 {
				t.Errorf("the App wrote a comment: %+v", w.comments(p))
			}
		})
	}
}

func TestDecisionPendingCaps(t *testing.T) {
	file := func(w *world, p *pull, n int) {
		w.handle(w.askFor(p, func(d map[string]any) { d["finding_id"] = fmt.Sprintf("%032x", n) }))
	}
	t.Run("per pull request", func(t *testing.T) {
		w := newWorld(t)
		p := w.openPull(w.shop, 7)
		for n := 1; n <= decisions.MaxPendingPerPull; n++ {
			file(w, p, n)
		}
		ops := w.gh.commentOps
		file(w, p, 99)
		if _, err := w.requests().Get(context.Background(), w.target(p), fmt.Sprintf("%032x", 99), w.digestOf(p.repo, testBranch, policy.PolicyPath)); !errors.Is(err, decisions.ErrNoRecord) {
			t.Errorf("the request beyond the cap has a record (%v)", err)
		}
		if w.gh.commentOps != ops || len(w.statusComments(p)) != decisions.MaxPendingPerPull {
			t.Errorf("the request beyond the cap was answered")
		}
		// A refile of a pending request is not a new one.
		w.push(p)
		file(w, p, 1)
		if rec := w.record(p, fmt.Sprintf("%032x", 1)); rec.HeadSHA != p.head {
			t.Errorf("a pending request could not be refiled at the cap")
		}
	})
	t.Run("per repository", func(t *testing.T) {
		w := newWorld(t)
		for pr := 1; pr <= decisions.MaxPendingPerRepo/decisions.MaxPendingPerPull; pr++ {
			p := w.openPull(w.shop, pr)
			for n := 1; n <= decisions.MaxPendingPerPull; n++ {
				file(w, p, n)
			}
		}
		last := w.openPull(w.shop, 99)
		file(w, last, 1)
		if len(w.statusComments(last)) != 0 {
			t.Errorf("the request beyond the repository cap was answered")
		}
	})
}

func TestDecisionPendingRequestsGoStale(t *testing.T) {
	tests := []struct {
		name string
		act  func(w *world, p *pull)
		want string
	}{
		{"new head", func(w *world, p *pull) {
			w.push(p)
			w.handle(w.event(webhook.PullSynchronized, p))
		}, "stale (head moved)"},
		{"pull request closed", func(w *world, p *pull) {
			w.editPull(p, func(pl *api.Pull) { pl.State = "closed" })
			w.handle(w.event(webhook.PullClosed, p))
		}, "stale (pull request closed)"},
		{"sweep after the policy moved", func(w *world, p *pull) {
			w.setFile(w.shop, testBranch, policy.PolicyPath, []byte(basePolicy+"# moved\n"))
			if _, err := w.gate.Sweep(context.Background()); err != nil {
				t.Fatal(err)
			}
		}, "stale (policy changed)"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			w := newWorld(t)
			p := w.openPull(w.shop, 7)
			other := w.openPull(w.shop, 8)
			digest := w.digestOf(p.repo, testBranch, policy.PolicyPath)
			w.handle(w.askFor(p))
			w.handle(w.askFor(other))
			tt.act(w, p)

			rec, err := w.requests().Get(context.Background(), w.target(p), findingA, digest)
			if err != nil {
				t.Fatal(err)
			}
			if rec.Status != decision.StatusStale || !strings.HasSuffix(tt.want, "("+rec.Reason+")") {
				t.Errorf("record = %+v", rec)
			}
			marker := decision.StatusMarker(findingA, digest)
			var line string
			for _, c := range w.comments(p) {
				if strings.HasPrefix(c.Body, marker+"\n") {
					line = strings.Split(c.Body, "\n")[1]
				}
			}
			if line != tt.want {
				t.Errorf("status line = %q, want %q", line, tt.want)
			}
			if o, err := w.requests().Get(context.Background(), w.target(other), findingA, digest); err != nil ||
				(tt.name != "sweep after the policy moved") != (o.Status == decision.StatusPending) {
				t.Errorf("another pull request's request went stale")
			}
		})
	}
}

func TestDecisionSweepLeavesAcceptedRequestsPending(t *testing.T) {
	w := newWorld(t)
	p := w.openPull(w.shop, 7)
	w.handle(w.askFor(p))
	if _, err := w.gate.Sweep(context.Background()); err != nil {
		t.Fatal(err)
	}
	if rec := w.record(p, findingA); rec.Status != decision.StatusPending {
		t.Errorf("record = %+v", rec)
	}
}

func TestDecisionStatusCommentFailureKeepsTheRecord(t *testing.T) {
	w := newWorld(t)
	p := w.openPull(w.shop, 7)
	w.gh.fail["CreateComment"] = api.ErrRateLimited
	e := w.askFor(p)
	if err := w.gate.HandleEvent(context.Background(), e); !errors.Is(err, api.ErrRateLimited) {
		t.Fatalf("HandleEvent = %v, want the rate limit, so the delivery is retried", err)
	}
	if rec := w.record(p, findingA); rec.Status != decision.StatusPending || rec.StatusCommentID != 0 {
		t.Fatalf("record = %+v", rec)
	}

	delete(w.gh.fail, "CreateComment")
	w.handle(e) // the retry
	w.wantStatus(p, findingA, "pending")
	if rec := w.record(p, findingA); rec.StatusCommentID == 0 {
		t.Errorf("the retry did not repair the status comment")
	}
}

func TestDecisionSweepRepairsAMissingStatusComment(t *testing.T) {
	w := newWorld(t)
	p := w.openPull(w.shop, 7)
	w.gh.fail["CreateComment"] = api.ErrRateLimited
	_ = w.gate.HandleEvent(context.Background(), w.askFor(p))
	delete(w.gh.fail, "CreateComment")
	if _, err := w.gate.Sweep(context.Background()); err != nil {
		t.Fatal(err)
	}
	w.wantStatus(p, findingA, "pending")
}

func TestDecisionLateSynchronizeForAnOldHeadDoesNotStaleTheNewHeadsRequest(t *testing.T) {
	w := newWorld(t)
	p := w.openPull(w.shop, 7)
	late := w.event(webhook.PullSynchronized, p) // the delivery for head A, retried late
	w.push(p)
	w.handle(w.askFor(p)) // a request for head B
	w.handle(late)
	if rec := w.record(p, findingA); rec.Status != decision.StatusPending || rec.HeadSHA != p.head {
		t.Errorf("record = %+v", rec)
	}
	w.wantStatus(p, findingA, "pending")
}

func TestDecisionLateCloseAfterAReopenDoesNotStaleANewRequest(t *testing.T) {
	w := newWorld(t)
	p := w.openPull(w.shop, 7)
	closed := w.event(webhook.PullClosed, p)
	w.handle(w.askFor(p)) // the pull request was reopened; this request is new
	w.handle(closed)
	if rec := w.record(p, findingA); rec.Status != decision.StatusPending {
		t.Errorf("record = %+v", rec)
	}
	w.wantStatus(p, findingA, "pending")
}

func TestDecisionSettledRequestIsRepairedAfterAFailedCommentEdit(t *testing.T) {
	repairs := map[string]func(w *world, p *pull, e webhook.Event){
		"the event is redelivered": func(w *world, p *pull, e webhook.Event) { w.handle(e) },
		"the sweep runs": func(w *world, p *pull, e webhook.Event) {
			if _, err := w.gate.Sweep(context.Background()); err != nil {
				t.Fatal(err)
			}
		},
	}
	for name, repair := range repairs {
		t.Run(name, func(t *testing.T) {
			w := newWorld(t)
			p := w.openPull(w.shop, 7)
			w.handle(w.askFor(p))
			w.push(p)
			e := w.event(webhook.PullSynchronized, p)
			w.gh.fail["UpdateComment"] = api.ErrRateLimited
			if err := w.gate.HandleEvent(context.Background(), e); !errors.Is(err, api.ErrRateLimited) {
				t.Fatalf("HandleEvent = %v, want the failed edit", err)
			}
			w.wantStatus(p, findingA, "pending") // the thread is out of date; the record is stale
			delete(w.gh.fail, "UpdateComment")

			repair(w, p, e)
			w.wantStatus(p, findingA, "stale (head moved)")
			raw, err := w.mem.Get(context.Background(), store.Tenant(shopInstallation), "decision/acme/shop/pending")
			if err != nil || strings.Contains(string(raw), findingA) {
				t.Errorf("pending list = %s (%v), want the request dropped once published", raw, err)
			}
		})
	}
}

func TestDecisionInvalidRepostAfterAStaleRequestIsAnswered(t *testing.T) {
	for name, stale := range map[string]func(w *world, p *pull){
		"head moved": func(w *world, p *pull) {
			w.push(p)
			w.handle(w.event(webhook.PullSynchronized, p))
		},
		"closed and reopened": func(w *world, p *pull) {
			w.editPull(p, func(pl *api.Pull) { pl.State = "closed" })
			w.handle(w.event(webhook.PullClosed, p))
			w.editPull(p, func(pl *api.Pull) { pl.State = "open" })
		},
	} {
		t.Run(name, func(t *testing.T) {
			w := newWorld(t)
			p := w.openPull(w.shop, 7)
			w.handle(w.askFor(p))
			stale(w, p)
			if line := strings.Split(w.statusComments(p)[0], "\n")[1]; !strings.HasPrefix(line, "stale") {
				t.Fatalf("setup: status line %q", line)
			}
			w.handle(w.askFor(p, func(d map[string]any) { d["why"] = "" }))
			if line := strings.Split(w.statusComments(p)[0], "\n")[1]; line != "declined (why: must be 1 to 2000 characters)" {
				t.Errorf("status line = %q: the agent must see why its repost was refused", line)
			}
			w.handle(w.askFor(p)) // and a valid repost still goes pending
			w.wantStatus(p, findingA, "pending")
		})
	}
}

func TestDecisionSweepStalesARequestForAnOldHeadWithoutAnEvent(t *testing.T) {
	w := newWorld(t)
	p := w.openPull(w.shop, 7)
	w.handle(w.askFor(p))
	w.push(p) // the synchronize delivery never arrived
	if _, err := w.gate.Sweep(context.Background()); err != nil {
		t.Fatal(err)
	}
	digest := w.digestOf(p.repo, testBranch, policy.PolicyPath)
	rec, err := w.requests().Get(context.Background(), w.target(p), findingA, digest)
	if err != nil || rec.Status != decision.StatusStale || rec.Reason != decision.ReasonHeadMoved {
		t.Fatalf("record = %+v, %v", rec, err)
	}
	w.wantStatus(p, findingA, "stale (head moved)")
}

package gate

import (
	"context"
	"encoding/json"
	"errors"
	"slices"
	"strings"
	"testing"

	"github.com/alexei-led/archfit-app/internal/gate/records"
	"github.com/alexei-led/archfit-app/internal/store"
	"github.com/alexei-led/archfit-app/internal/wire/policy3"
)

func TestTheRequestPageReadsARequestAndPreviewsAnAnswer(t *testing.T) {
	ctx := context.Background()
	w := newAskWorld(t)
	asked, other := w.pullBy(7), w.pullBy(8)
	w.uploadAsk(asked, "graph", "fail")
	w.ask(asked, workerLogin, "/archfit request tc needs the retry helper --expires 2026-12-01")
	w.uploadAsk(other, "graph", "fail", "pkg_tc>pkg_net") // the same error and one the request does not cover

	v, err := w.gate.RequestView(ctx, w.target(asked), firstRequest)
	if err != nil {
		t.Fatal(err)
	}
	if v.Reason != "tc needs the retry helper" || !v.Numbers.Verified || v.Numbers.Balance.Score != 9 ||
		!strings.Contains(v.Record, "balance: 9/10 (min 7)") || !v.Policy {
		t.Errorf("view = %+v", v)
	}

	p, err := w.gate.RequestPreview(ctx, w.target(asked), firstRequest, RequestAnswer{Kind: policy3.ApproveUntil})
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(p.Diff, "+  - rule: graph  # request #1000\n") || !p.Loosens {
		t.Errorf("diff:\n%s", p.Diff)
	}
	want := []RequestPullEffect{
		{Number: 7, Status: "waiting", Waits: true, Clears: 1},
		{Number: 8, Status: "fail", Clears: 1, Left: 1},
	}
	if !slices.Equal(p.Pulls, want) {
		t.Errorf("pulls = %+v, want %+v", p.Pulls, want)
	}
	clean(t, p.Diff)

	edited, err := PolicyEdit([]byte(policyAsk), p.Change, RequestComment(firstRequest))
	if err != nil || !strings.Contains(string(edited), "  - rule: graph  # request #1000\n    from: pkg_tc\n") {
		t.Errorf("PolicyEdit = %v:\n%s", err, edited)
	}
}

func TestTheRequestPageIsReadForAnAdmittedInstallationOnly(t *testing.T) {
	ctx := context.Background()
	w := newAskWorld(t)
	p := w.pullBy(7)
	w.admit.refused[shopInstallation] = true
	calls := len(w.gh.calls)
	if _, err := w.gate.RequestView(ctx, w.target(p), firstRequest); !errors.Is(err, errNotAdmitted) {
		t.Errorf("RequestView = %v, want errNotAdmitted", err)
	}
	if _, err := w.gate.RequestPreview(ctx, w.target(p), firstRequest, RequestAnswer{Kind: policy3.Approve}); !errors.Is(err, errNotAdmitted) {
		t.Errorf("RequestPreview = %v, want errNotAdmitted", err)
	}
	if len(w.gh.calls) != calls {
		t.Errorf("GitHub calls for an installation no operator approved: %v", w.gh.calls[calls:])
	}
}

func TestRequestViewReadsTheRecordedAnswer(t *testing.T) {
	for _, tc := range []struct {
		name           string
		kind           policy3.Answer
		until, instead string
		pin            *records.Pin
		void           bool
	}{
		{name: "approve without proposal", kind: policy3.Approve},
		{name: "joined until", kind: policy3.ApproveUntil, until: "2026-12-10", pin: &records.Pin{Proposal: "p1", PullRequest: 77, HeadSHA: "head", Digest: "digest"}},
		{name: "reject", kind: policy3.Reject, instead: "Use a port."},
		{name: "void answer", kind: policy3.Approve, void: true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			w := newAskWorld(t)
			p := w.pullBy(7)
			w.uploadAsk(p, "graph", "fail")
			w.ask(p, workerLogin, "/archfit request need the helper")
			change := AnswerChange{Request: firstRequest, Kind: tc.kind, Rule: "graph", From: "pkg_tc", To: "pkg_util", Until: tc.until, Instead: tc.instead}
			raw, err := json.Marshal(change)
			if err != nil {
				t.Fatal(err)
			}
			a := records.Approval{Kind: string(tc.kind), Request: firstRequest, Change: raw}
			if err := records.Answer(t.Context(), w.mem, store.Tenant(shopInstallation), shopName, "a1", a, tc.pin); err != nil {
				t.Fatal(err)
			}
			if tc.void {
				a.State = records.StateVoid
				w.put4(records.ApprovalKey(shopName, "a1"), &a)
			}
			got, err := w.gate.RequestView(t.Context(), w.target(p), firstRequest)
			if err != nil {
				t.Fatal(err)
			}
			if tc.void {
				if got.Answer != nil {
					t.Fatalf("void answer = %+v", got.Answer)
				}
				return
			}
			if got.Answer == nil || *got.Answer != (RequestAnswer{Kind: tc.kind, Until: tc.until, Instead: tc.instead}) {
				t.Fatalf("answer = %+v", got.Answer)
			}
		})
	}
}

func TestRequestViewOldAnsweredRecordHasNoAnswer(t *testing.T) {
	w := newAskWorld(t)
	p := w.pullBy(7)
	w.uploadAsk(p, "graph", "fail")
	w.ask(p, workerLogin, "/archfit request need the helper")
	req := w.storedRequest(firstRequest)
	req.State = records.StateAnswered
	w.put4(records.RequestKey(shopName, firstRequest), &req)
	got, err := w.gate.RequestView(t.Context(), w.target(p), firstRequest)
	if err != nil {
		t.Fatal(err)
	}
	if got.State != records.StateAnswered || got.Answer != nil {
		t.Fatalf("view = %+v", got)
	}
}

func TestHomeAndRequestLayerOrderAgree(t *testing.T) {
	w := newAskWorld(t)
	p := w.pullBy(7)
	w.uploadAsk(p, "graph", "fail")
	w.ask(p, workerLogin, "/archfit request need the helper")
	pol := "version: 3\nowners: [olivia]\nlayers: [core, adapters, outer]\nmodules:\n  pkg_tc:\n    paths: [pkg/tc/**]\n    layer: outer\n    volatility: high\n  pkg_util:\n    paths: [pkg/util/**]\n    layer: core\n    volatility: low\n"
	w.setFile(w.shop, testBranch, policyPath, []byte(pol))
	w.approved(w.shop, pol)
	home, err := NewHome(w.gh, w.mem, NewAdmissions(w.mem, []string{strings.SplitN(shopName, "/", 2)[0]})).Home(t.Context(), w.shop)
	if err != nil {
		t.Fatal(err)
	}
	request, err := w.gate.RequestView(t.Context(), w.target(p), firstRequest)
	if err != nil {
		t.Fatal(err)
	}
	layers := slices.Clone(home.Policy.Layers)
	slices.Reverse(layers)
	if !slices.Equal(request.Layers, layers) || !slices.Equal(layers, []string{"outer", "adapters", "core"}) {
		t.Fatalf("Home %v, request %v", layers, request.Layers)
	}
	if request.Modules[0].Volatility != "high" {
		t.Fatalf("modules: %+v", request.Modules)
	}
}

package gate

import (
	"context"
	"errors"
	"slices"
	"strings"
	"testing"

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
	if !strings.Contains(p.Diff, "+    expires: 2026-12-01 # request #1000\n") || !p.Loosens {
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
	if err != nil || !strings.Contains(string(edited), "    expires: 2026-12-01 # request #1000\n") {
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

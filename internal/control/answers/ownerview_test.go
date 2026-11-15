package answers_test

import (
	"errors"
	"testing"

	"github.com/alexei-led/archfit-app/internal/control/answers"
	"github.com/alexei-led/archfit-app/internal/control/passkey"
	"github.com/alexei-led/archfit-app/internal/gate"
)

func TestOwnerPagesRedeemTheViewDigestWhilePreservingTheExactM2Binding(t *testing.T) {
	for _, kind := range []string{gate.OwnerFirst, gate.OwnerWorkflow, gate.OwnerConfirm, gate.OwnerRevert} {
		for _, change := range []string{"unchanged", "view moved before submit", "proposal set moved inside action", "underlying rebound", "unreadable inside action"} {
			t.Run(kind+"/"+change, func(t *testing.T) {
				g := &ownerGate{view: gate.OwnerView{Digest: "sha256:view", ActionDigest: "sha256:original"}, redeemDigest: "sha256:original"}
				p := &fakePasskeys{}
				a := answers.OwnerAction{Kind: kind, Digest: "sha256:original", ViewDigest: "sha256:view", Token: "tok"}
				switch change {
				case "view moved before submit":
					g.view.Digest = "sha256:new-view"
				case "proposal set moved inside action":
					g.beforeRedeem = func() { g.view.Digest = "sha256:new-proposals" }
				case "underlying rebound":
					g.redeemDigest = "sha256:fresh"
				case "unreadable inside action":
					g.beforeRedeem = func() { g.err = errors.New("file unavailable") }
				}
				out, err := answers.NewOwners(g, p).OwnerAct(t.Context(), repo, gate.OwnerUser{ID: 501, Login: "olivia"}, a)
				if change != "unchanged" {
					if err == nil || p.token != "" || out.Approval != "" {
						t.Fatalf("changed view consumed passkey or approved: %+v, %v, %+v", out, err, p)
					}
					if change != "unreadable inside action" && !errors.Is(err, gate.ErrOwnerMoved) {
						t.Fatalf("refusal = %v", err)
					}
					return
				}
				if err != nil || out.Approval == "" {
					t.Fatalf("unchanged view refused: %+v, %v", out, err)
				}
				if p.purpose != (passkey.Purpose{Action: kind, Digest: a.ViewDigest}) || p.token != "tok" {
					t.Fatalf("passkey = %+v", p)
				}
				if g.in.Digest != a.Digest || g.viewCalls != 2 {
					t.Fatalf("underlying binding or read count changed: %+v, %d", g.in, g.viewCalls)
				}
			})
		}
	}
}

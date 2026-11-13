package gate

import (
	"errors"
	"strings"
	"testing"

	"github.com/alexei-led/archfit-app/internal/gate/approvals"
	"github.com/alexei-led/archfit-app/internal/gate/records"
	"github.com/alexei-led/archfit-app/internal/github/api"
	"github.com/alexei-led/archfit-app/internal/wire/policy3"
)

func ownerViewWorld(t *testing.T, kind string, pull int) *world4 {
	t.Helper()
	w := newWorld4(t)
	w.guarded4(policy4)
	switch kind {
	case OwnerFirst:
		w.unrecorded()
		if pull > 0 {
			w.onboarding()
			w.removeFile(w.shop, w.tip, policyPath)
			w.onboardingPull(pull)
		}
	case OwnerWorkflow:
		p := w.openPull(w.shop, pull)
		w.setFile(w.shop, p.head, policyPath, []byte(policy4))
		w.setFile(w.shop, p.head, approvals.WorkflowPath, workflow4(actionBumped, " # v4.0.1"))
	case OwnerConfirm, OwnerRevert:
		next := policy4 + "# pushed by hand\n"
		w.tip4(next, api.Commit{SHA: w.sha()})
		r := w.repo4()
		r.OutsideFlow = &records.OutsideFlow{Digest: policy3.Digest([]byte(next))}
		w.put4(records.RepoKey(shopName), &r)
	}
	return w
}

func TestOwnerViewsBindExactInputsAndRendering(t *testing.T) {
	for _, tt := range []struct {
		kind string
		pull int
	}{{OwnerFirst, 0}, {OwnerFirst, 7}, {OwnerWorkflow, 7}, {OwnerConfirm, 0}, {OwnerRevert, 0}} {
		t.Run(tt.kind+string(rune('0'+tt.pull)), func(t *testing.T) {
			w := ownerViewWorld(t, tt.kind, tt.pull)
			a := OwnerAction{Kind: tt.kind, Pull: tt.pull}
			v, err := w.gate.OwnerView(t.Context(), Target{Repo: w.shop}, a)
			if err != nil {
				t.Fatal(err)
			}
			if v.Status == "" || v.MainAction == "" || len(v.Files) == 0 || len(v.Diffs) == 0 || v.Digest == v.ActionDigest {
				t.Fatalf("view = %+v", v)
			}
			d, err := w.gate.OwnerDigest(t.Context(), Target{Repo: w.shop}, a)
			if err != nil || d != v.ActionDigest {
				t.Fatalf("binding = %s, %v; view = %s", d, err, v.ActionDigest)
			}
			same, err := w.gate.OwnerView(t.Context(), Target{Repo: w.shop}, a)
			if err != nil || same.Digest != v.Digest {
				t.Fatalf("unstable view = %s, %v", same.Digest, err)
			}
			if tt.kind == OwnerFirst && !strings.Contains(v.Diffs[0].Text, "+version: 3") {
				t.Fatalf("not all additions: %s", v.Diffs[0].Text)
			}
			v.Digest = ""
			v.Note += "changed"
			if finishOwnerView(w.shop, v).Digest == same.Digest {
				t.Fatal("rendering omitted from digest")
			}
		})
	}
}

func TestOwnerViewsFailClosed(t *testing.T) {
	for _, name := range []string{"unreadable", "mismatched approved", "moved head", "not admitted", "deleted outside policy"} {
		t.Run(name, func(t *testing.T) {
			w := ownerViewWorld(t, OwnerConfirm, 0)
			want := true
			switch name {
			case "unreadable":
				w.gh.fail["File"] = errors.New("down")
			case "mismatched approved":
				w.setFile(w.shop, w.repo4().ApprovedCommit, policyPath, []byte(policy4+"# changed"))
			case "moved head":
				w.setFile(w.shop, w.tip, policyPath, []byte(policy4+"# moved"))
			case "not admitted":
				w.admit.refused[w.shop.InstallationID] = true
			case "deleted outside policy":
				w.removeFile(w.shop, w.tip, policyPath)
				w.removeFile(w.shop, testBranch, policyPath)
				r := w.repo4()
				r.OutsideFlow.Digest = ""
				w.put4(records.RepoKey(shopName), &r)
				v, err := w.gate.OwnerView(t.Context(), Target{Repo: w.shop}, OwnerAction{Kind: OwnerRevert})
				if err != nil || !strings.Contains(v.Diffs[0].Text, "+version: 3") {
					t.Fatalf("deleted revert: %+v, %v", v, err)
				}
				want = false
			}
			if want {
				if _, err := w.gate.OwnerView(t.Context(), Target{Repo: w.shop}, OwnerAction{Kind: OwnerConfirm}); err == nil {
					t.Fatal("unverifiable view accepted")
				}
			}
		})
	}
}

func TestOwnerViewDigestIncludesAnUnchangedActionWithAMovedBase(t *testing.T) {
	w := ownerViewWorld(t, OwnerWorkflow, 7)
	a := OwnerAction{Kind: OwnerWorkflow, Pull: 7}
	v, err := w.gate.OwnerView(t.Context(), Target{Repo: w.shop}, a)
	if err != nil {
		t.Fatal(err)
	}
	pl := w.gh.pulls[pullKey(shopName, 7)]
	next := w.sha()
	w.setFile(w.shop, next, policyPath, []byte(policy4+"# base moved"))
	pl.BaseSHA = next
	w.gh.pulls[pullKey(shopName, 7)] = pl
	moved, err := w.gate.OwnerView(t.Context(), Target{Repo: w.shop}, a)
	if err != nil {
		t.Fatal(err)
	}
	if moved.ActionDigest != v.ActionDigest || moved.Digest == v.Digest {
		t.Fatal("base movement was not separately bound")
	}
}

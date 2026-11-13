package proposals

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"maps"
	"testing"

	"github.com/alexei-led/archfit-app/internal/gate/records"
	"github.com/alexei-led/archfit-app/internal/gate/requests"
	"github.com/alexei-led/archfit-app/internal/wire/policy3"
)

func TestRevertPreviewIsReadOnlyAndMatchesTheWrittenBytes(t *testing.T) {
	for _, n := range []int{0, 1, 3, 4} {
		t.Run(fmt.Sprint(n), func(t *testing.T) {
			w := newWorld(t)
			pairs := [][2]string{{"pkg_tc", "pkg_util"}, {"pkg_tc", "pkg_db"}, {"pkg_net", "pkg_util"}, {"pkg_db", "pkg_util"}}
			for i := range n {
				w.request(431+i, "graph", pairs[i][0], pairs[i][1])
				w.mustAnswer(olivia, 431+i, requests.Answer{Kind: policy3.Approve}, true)
			}
			w.outsideTheFlow()
			before, writes := w.snapshot(), len(w.hub.writes())
			v, err := w.svc.RevertPreview(context.Background(), testRepo)
			if err != nil {
				t.Fatal(err)
			}
			if !maps.Equal(before, w.snapshot()) || len(w.hub.writes()) != writes {
				t.Fatal("preview wrote state")
			}
			if len(v.Proposals) != min(n, 1) {
				t.Fatalf("proposals = %+v", v.Proposals)
			}
			if n == 0 && !bytes.Equal(v.Result, v.Approved) {
				t.Fatal("plain revert is not the approved file")
			}
			if n > 0 && bytes.Equal(v.Result, v.Approved) {
				t.Fatal("open answers were not shown")
			}
			if _, err := w.revert(oscar); err != nil {
				t.Fatal(err)
			}
			if got := w.hub.file(fakeBase, policyPath); got != string(v.Result) {
				t.Fatalf("written:\n%s\npreviewed:\n%s", got, v.Result)
			}
		})
	}
}

func TestRevertPreviewRefusesAMovedProposalBeforePasskeyConsumption(t *testing.T) {
	for _, change := range []string{"unchanged", "approval voided", "proposal closed", "approval changed"} {
		t.Run(change, func(t *testing.T) {
			w := newWorld(t)
			w.request(431, "graph", "pkg_tc", "pkg_util")
			open := w.mustAnswer(olivia, 431, requests.Answer{Kind: policy3.Approve}, true)
			w.outsideTheFlow()
			seen, err := w.svc.RevertPreview(t.Context(), testRepo)
			if err != nil {
				t.Fatal(err)
			}
			seenDigest := snapshotDigest(seen)
			redeemed := false
			_, err = w.svc.Revert(t.Context(), testRepo, RevertInput{Owner: olivia, Digest: seen.Digest, Redeem: func(ctx context.Context, digest string) (string, error) {
				if digest != seen.Digest {
					t.Fatal("underlying digest replaced")
				}
				switch change {
				case "approval voided", "approval changed":
					a := load[records.Approval](t, w.store, records.ApprovalKey(testRepo.FullName, open.Approval))
					if change == "approval voided" {
						a.State = records.StateVoid
					} else {
						a.Login = "another owner"
					}
					raw, err := records.Encode(&a)
					if err != nil {
						return "", err
					}
					if err := w.store.Put(ctx, tenant(testRepo), records.ApprovalKey(testRepo.FullName, open.Approval), raw); err != nil {
						return "", err
					}
				case "proposal closed":
					pl := w.hub.pulls[open.PullRequest]
					pl.State = "closed"
					w.hub.pulls[open.PullRequest] = pl
				}
				now, err := w.svc.RevertPreview(ctx, testRepo)
				if err != nil {
					return "", err
				}
				if snapshotDigest(now) != seenDigest {
					return "", ErrMoved
				}
				redeemed = true
				return "passkey-1", nil
			}})
			if change == "unchanged" {
				if err != nil || !redeemed {
					t.Fatalf("unchanged preview: %v, redeemed=%t", err, redeemed)
				}
			} else if !errors.Is(err, ErrMoved) || redeemed {
				t.Fatalf("moved preview: %v, redeemed=%t", err, redeemed)
			}
		})
	}
}

func TestRevertPreviewDoesNotCleanUpAClosedProposal(t *testing.T) {
	w := newWorld(t)
	w.request(431, "graph", "pkg_tc", "pkg_util")
	open := w.mustAnswer(olivia, 431, requests.Answer{Kind: policy3.Approve}, true)
	w.outsideTheFlow()
	pl := w.hub.pulls[open.PullRequest]
	pl.State = "closed"
	w.hub.pulls[open.PullRequest] = pl
	before, writes := w.snapshot(), len(w.hub.writes())
	view, err := w.svc.RevertPreview(t.Context(), testRepo)
	if err != nil {
		t.Fatal(err)
	}
	if !maps.Equal(before, w.snapshot()) || len(w.hub.writes()) != writes {
		t.Fatal("preview cleaned up a closed proposal")
	}
	if len(view.Proposals) != 0 || !bytes.Equal(view.Approved, view.Result) {
		t.Fatalf("closed proposal included in preview: %+v", view)
	}
}

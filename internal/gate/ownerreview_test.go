package gate

import (
	"errors"
	"strings"
	"testing"

	"github.com/alexei-led/archfit-app/internal/gate/approvals"
	"github.com/alexei-led/archfit-app/internal/gate/records"
)

func TestOwnerReviewUsesApprovalEligibility(t *testing.T) {
	for _, tt := range []struct {
		name, kind string
		user       OwnerUser
		want       error
	}{
		{"named administrator first", OwnerFirst, oliviaAdmin, nil},
		{"owner without admin first", OwnerFirst, OwnerUser{Login: "olivia"}, ErrOwnerNotAdmin},
		{"unnamed administrator first", OwnerFirst, OwnerUser{Login: "mallory", Admin: true}, ErrOwnerNotAdmin},
		{"approved owner workflow", OwnerWorkflow, OwnerUser{Login: "olivia"}, nil},
		{"administrator not owner workflow", OwnerWorkflow, OwnerUser{Login: "mallory", Admin: true}, ErrOwnerNotOwner},
		{"viewer workflow", OwnerWorkflow, OwnerUser{Login: "mallory"}, ErrOwnerNotOwner},
	} {
		t.Run(tt.name, func(t *testing.T) {
			w := ownerViewWorld(t, tt.kind, 7)
			err := w.gate.OwnerReview(t.Context(), Target{Repo: w.shop}, OwnerAction{Kind: tt.kind, Pull: 7}, tt.user)
			if !errors.Is(err, tt.want) {
				t.Fatalf("review = %v, want %v", err, tt.want)
			}
		})
	}
}

func TestOwnerReviewDoesNotTrustProposedOwnersOrBrokenApprovedDigest(t *testing.T) {
	for _, corrupt := range []bool{false, true} {
		t.Run(map[bool]string{false: "proposed owner", true: "approved digest mismatch"}[corrupt], func(t *testing.T) {
			w := ownerViewWorld(t, OwnerWorkflow, 7)
			p := w.gh.pulls[pullKey(w.shop.FullName, 7)]
			w.setFile(w.shop, p.HeadSHA, approvals.PolicyPath, []byte(strings.ReplaceAll(policy4, "olivia", "mallory")))
			if corrupt {
				r := w.repo4()
				r.ApprovedDigest = "sha256:incorrect"
				w.put4(records.RepoKey(w.shop.FullName), &r)
			}
			err := w.gate.OwnerReview(t.Context(), Target{Repo: w.shop}, OwnerAction{Kind: OwnerWorkflow, Pull: 7}, OwnerUser{Login: "mallory", Admin: true})
			want := ErrOwnerNotOwner
			if corrupt {
				want = ErrOwnerMoved
			}
			if !errors.Is(err, want) {
				t.Fatalf("review = %v, want %v", err, want)
			}
		})
	}
}

func TestOwnerReviewNotDueAfterApproval(t *testing.T) {
	for _, kind := range []string{OwnerFirst, OwnerWorkflow} {
		t.Run(kind, func(t *testing.T) {
			w := ownerViewWorld(t, kind, 7)
			a := OwnerAction{Kind: kind, Pull: 7}
			if _, err := w.act(oliviaAdmin, a); err != nil {
				t.Fatal(err)
			}
			if err := w.gate.OwnerReview(t.Context(), Target{Repo: w.shop}, a, oliviaAdmin); !errors.Is(err, ErrOwnerNotDue) {
				t.Fatalf("approved head review = %v", err)
			}
		})
	}
}

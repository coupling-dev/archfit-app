package ui

import (
	"context"
	"errors"
	"net/http"
	"strconv"
	"sync"

	"github.com/alexei-led/archfit-app/internal/control/answers"
	"github.com/alexei-led/archfit-app/internal/gate"
	"github.com/alexei-led/archfit-app/internal/github/api"
)

// reviewJSON is navigation, not approval authority. The destination rechecks on submission.
type reviewJSON struct {
	Label string `json:"label"`
	Href  string `json:"href,omitempty"`
}

// reviewConcurrency bounds how many owner reviews of one response run at once.
const reviewConcurrency = 4

// reviewPermission is the signed-in user's admin status on one repository, read once per request.
// Only the owner review reads it; an approval reads it fresh.
type reviewPermission struct {
	repo, token string
	once        sync.Once
	admin       bool
}

type reviewPermissionKey struct{}

// withReviewPermission lets the owner reviews of one request share one permission read on that repository.
func withReviewPermission(ctx context.Context, sc scope) context.Context {
	return context.WithValue(ctx, reviewPermissionKey{}, &reviewPermission{repo: sc.repo.FullName, token: sc.user.Token})
}

func (s *server) reviewAdmin(ctx context.Context, sc scope) bool {
	memo, ok := ctx.Value(reviewPermissionKey{}).(*reviewPermission)
	if !ok || memo.repo != sc.repo.FullName || memo.token != sc.user.Token {
		memo = &reviewPermission{}
	}
	memo.once.Do(func() {
		permission, err := s.gh.users.Permission(ctx, sc.user.Token, sc.repo.FullName)
		memo.admin = err == nil && permission == permAdmin
	})
	return memo.admin
}

// forEachBounded calls fn for every index with at most limit calls in flight and returns when all have finished.
// Each call writes only its own result slot, so the caller reads results in index order.
func forEachBounded(n, limit int, fn func(int)) {
	slots := make(chan struct{}, limit)
	var calls sync.WaitGroup
	for i := range n {
		calls.Go(func() {
			slots <- struct{}{}
			defer func() { <-slots }()
			fn(i)
		})
	}
	calls.Wait()
}

func (s *server) ownerReview(r *http.Request, sc scope, pull int) *reviewJSON {
	// The probes of one review share their reads. Home installs the memo first, to share them across its reviews too.
	ctx := gate.WithReviewReads(r.Context())
	done := api.StartStage(ctx, "owner_review")
	actions := []answers.OwnerAction{}
	if pull > 0 {
		actions = append(actions, answers.OwnerAction{Kind: gate.OwnerFirst, Pull: pull})
	}
	actions = append(actions, answers.OwnerAction{Kind: gate.OwnerFirst})
	if pull > 0 {
		actions = append(actions, answers.OwnerAction{Kind: gate.OwnerWorkflow, Pull: pull})
	}
	defer func() { done(len(actions), 0) }()
	// The user's admin status matters only to the last step of a review, so it is read while the views are. A view is
	// read again by OwnerReview below, from the memo, so the first read costs nothing twice. Where the request has read
	// the status already (Home), it is not waited for.
	var u gate.OwnerUser
	var permission sync.WaitGroup
	permission.Go(func() { u = gate.OwnerUser{ID: sc.user.UserID, Login: sc.user.Login, Admin: s.reviewAdmin(ctx, sc)} })
	// The probes are independent reads. The answer is the first decisive one in action order, as the sequential
	// probes gave it, so it never depends on which probe finishes first.
	errs := make([]error, len(actions))
	var probes sync.WaitGroup
	for i, a := range actions {
		probes.Go(func() {
			// Its refusal, if any, is the one OwnerReview returns from the same memo.
			_, _ = s.owners.OwnerView(ctx, sc.repo, a)
			permission.Wait()
			errs[i] = s.owners.OwnerReview(ctx, sc.repo, a, u)
		})
	}
	probes.Wait()
	for i, a := range actions {
		switch err := errs[i]; {
		case errors.Is(err, gate.ErrOwnerNotDue), errors.Is(err, gate.ErrOwnerNotPossible):
			continue
		case errors.Is(err, gate.ErrOwnerNotAdmin):
			return &reviewJSON{Label: "A repository administrator named as an architecture owner in the proposed policy can review this."}
		case errors.Is(err, gate.ErrOwnerNotOwner):
			return &reviewJSON{Label: "An architecture owner named in the approved policy can review this."}
		case err != nil:
			return &reviewJSON{Label: "The App could not determine who can review this. Try again."}
		}
		label := "Review first policy"
		if a.Kind == gate.OwnerWorkflow {
			label = "Review workflow change"
		}
		href := "/r/" + sc.repo.FullName + "/owner/" + a.Kind
		if a.Pull > 0 {
			href += "?pull=" + strconv.Itoa(a.Pull)
		}
		return &reviewJSON{Label: label, Href: href}
	}
	return nil
}

package ui

import (
	"cmp"
	"fmt"
	"net/http"
	"slices"
	"sort"
	"strconv"
	"sync"
	"time"

	"github.com/coupling-dev/archfit-app/internal/gate"
	"github.com/coupling-dev/archfit-app/internal/github/api"
)

// maxOwnerActionRepos bounds the repositories whose Home is read for owner steps, as maxSetupProbes bounds the probes.
const maxOwnerActionRepos = 25

type ownerActionRow struct {
	Repo  string `json:"repo"`
	Kind  string `json:"kind"`
	Label string `json:"label"`
	Pull  int    `json:"pull_request,omitempty"`
	Since string `json:"since,omitempty"` // RFC 3339; absent when no time is recorded
	Href  string `json:"href"`
}

type ownerActionsBody struct {
	Actions   []ownerActionRow `json:"actions"`
	Unread    int              `json:"unread"`    // repositories whose owner steps could not be read: missing evidence is not none
	Truncated bool             `json:"truncated"` // more approved repositories than are read
}

// apiOwnerActions lists the owner steps that wait on the user's repositories, oldest first, so the list page can show
// them without the owner knowing a URL. A repository with no approved policy has none: its setup is on the list.
func (s *server) apiOwnerActions(r *http.Request, sc scope) (any, error) {
	ctx := gate.WithRequestMemo(r.Context())
	repos, err := s.gh.users.UserRepos(ctx, sc.user.Token)
	if err != nil {
		return nil, err
	}
	sort.Slice(repos, func(i, j int) bool { return repos[i].FullName < repos[j].FullName })
	body := ownerActionsBody{Actions: []ownerActionRow{}, Truncated: len(repos) > maxOverviewRepos}
	if body.Truncated {
		repos = repos[:maxOverviewRepos]
	}
	admitted := s.admittedSet(ctx, repos)
	read := make([]api.Repo, 0, len(repos))
	for _, repo := range repos {
		if admitted[repo.InstallationID] {
			read = append(read, repo)
		}
	}
	rows := make([][]ownerActionRow, len(read))
	failed := make([]bool, len(read))
	slots := make(chan struct{}, parallelLookups)
	var wg sync.WaitGroup
	reads := 0
	for i, repo := range read {
		state, err := s.repoStates.RepoState(ctx, repo)
		if err != nil {
			failed[i] = true
			continue
		}
		if state == gate.RepoNotSetUp {
			continue
		}
		if reads == maxOwnerActionRepos {
			body.Truncated = true
			break
		}
		reads++
		wg.Add(1)
		slots <- struct{}{}
		go func() {
			defer wg.Done()
			defer func() { <-slots }()
			rctx, _ := gate.WithHomeSnapshot(ctx, repo)
			view, err := s.home.Home(gate.WithReviewReads(rctx), repo)
			if err != nil {
				failed[i] = true
				return
			}
			view.OutsideFlow = view.OutsideFlow || state == gate.RepoOutsideFlow
			for _, a := range view.OwnerActions() {
				row := ownerActionRow{Repo: repo.FullName, Kind: a.Kind, Label: a.Label, Pull: a.Pull, Href: "/r/" + repo.FullName + a.Path}
				if !a.Since.IsZero() {
					row.Since = a.Since.UTC().Format(time.RFC3339)
				}
				rows[i] = append(rows[i], row)
			}
			for _, req := range view.Requests {
				row := ownerActionRow{Repo: repo.FullName, Kind: "request", Label: fmt.Sprintf("Answer request #%d", req.Number), Href: "/r/" + repo.FullName + "/requests/" + strconv.Itoa(req.Number)}
				if !req.AskedAt.IsZero() {
					row.Since = req.AskedAt.UTC().Format(time.RFC3339)
				}
				rows[i] = append(rows[i], row)
			}
			today := s.now().UTC().Truncate(24 * time.Hour)
			for _, x := range view.Policy.Exceptions {
				left, ok := homeDays(x.Expires, today)
				if !ok || left > homeExpiryDays {
					continue
				}
				key := gate.HomeException{Rule: x.Rule, From: x.From, To: x.To, Expires: x.Expires}
				rows[i] = append(rows[i], ownerActionRow{Repo: repo.FullName, Kind: "exception", Label: "Open the exception", Href: issueHref(repo.FullName, view.ExceptionIssues[key])})
			}
			if view.DriftKnown && len(view.Drift) > 0 || view.DriftIssue > 0 {
				rows[i] = append(rows[i], ownerActionRow{Repo: repo.FullName, Kind: "drift", Label: "Open the drift issue", Href: issueHref(repo.FullName, view.DriftIssue)})
			}

		}()
	}
	wg.Wait()
	for i := range read {
		body.Actions = append(body.Actions, rows[i]...)
		if failed[i] {
			body.Unread++
		}
	}
	// The longest wait first; a step with no recorded time comes after those that have one.
	slices.SortStableFunc(body.Actions, func(a, b ownerActionRow) int {
		switch {
		case a.Since == "" && b.Since != "":
			return 1
		case a.Since != "" && b.Since == "":
			return -1
		}
		return cmp.Or(cmp.Compare(a.Since, b.Since), cmp.Compare(a.Repo, b.Repo), cmp.Compare(a.Pull, b.Pull))
	})
	return body, nil
}

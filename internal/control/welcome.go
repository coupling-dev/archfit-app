package control

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"time"

	"github.com/coupling-dev/archfit-app/internal/github/api"
	"github.com/coupling-dev/archfit-app/internal/github/webhook"
	"github.com/coupling-dev/archfit-app/internal/store"
)

const welcomeMarker = "<!-- archfit:welcome -->"
const welcomeBody = welcomeMarker + `
## Archfit is set up on this repository

Merge this pull request to start the checks. Each pull request then gets one verdict: pass, fail or unknown.
Until an owner makes the check required, a fail does not block a merge.

- To ask for a change to the policy, run ` + "`archfit request -m \"<why>\"`" + ` on your branch.
- The owners answer requests in the App.
- The setup page shows the counts for this week and the next step.
`

func (c *Control) welcome(ctx context.Context, e webhook.Event) error {
	if c.setup == nil || e.HeadRef != "archfit/setup" || e.RepoID <= 0 || e.HeadRepoID != e.RepoID || e.PullRequest <= 0 {
		return nil
	}
	ctx, cancel := context.WithTimeout(ctx, eventTimeout)
	defer cancel()
	admitted, err := c.admit.Admitted(ctx, e.InstallationID, "")
	if err != nil || !admitted {
		return err
	}
	s := c.setup
	tenant := store.Tenant(e.InstallationID)
	key := fmt.Sprintf("welcome/%d/%d", e.RepoID, e.PullRequest)
	var mine []byte
	for {
		cur, err := s.store.Get(ctx, tenant, key)
		if errors.Is(err, store.ErrNotFound) {
			cur = nil
		} else if err != nil {
			return err
		}
		if cur != nil {
			var old setupLease
			if err := json.Unmarshal(cur, &old); err != nil {
				return err
			}
			if old.Outcome != "" {
				return nil
			}
			if s.now().Before(old.Expires) {
				return errors.New("welcome: lease busy")
			}
		}
		mine, err = json.Marshal(setupLease{Expires: s.now().Add(setupLeaseTTL), Token: fmt.Sprint(time.Now().UnixNano())})
		if err != nil {
			return err
		}
		ok, err := s.store.CompareAndSwap(ctx, tenant, key, cur, mine)
		if err != nil {
			return err
		}
		if ok {
			break
		}
	}
	repo := api.Repo{RepoRef: api.RepoRef{InstallationID: e.InstallationID, FullName: e.Repo}, ID: e.RepoID}
	marked, err := s.gh.HasMarkerComment(ctx, repo, e.PullRequest, welcomeMarker)
	if err != nil {
		return err
	}
	if !marked {
		if err := s.gh.Comment(ctx, repo, e.PullRequest, welcomeBody); err != nil {
			return err
		}
	}
	done, _ := json.Marshal(setupLease{Outcome: "done"})
	ok, err := s.store.CompareAndSwap(ctx, tenant, key, mine, done)
	if err != nil {
		return err
	}
	if !ok {
		return errors.New("welcome: lease lost")
	}
	return nil
}

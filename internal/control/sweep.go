package control

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"slices"
	"strings"
	"time"

	"github.com/coupling-dev/archfit-app/internal/github/api"
	"github.com/coupling-dev/archfit-app/internal/github/webhook"
	"github.com/coupling-dev/archfit-app/internal/store"
)

const eventTimeout = 10 * time.Second

// HandleEvent maintains entitlements on installation events and forgets a tenant on uninstall.
func (c *Control) HandleEvent(ctx context.Context, e webhook.Event) error {
	if e.InstallationID <= 0 {
		return errors.New("control: event without an installation ID")
	}
	switch e.Kind {
	case webhook.PullOpened:
		return c.welcome(ctx, e)
	case webhook.InstallationAdded, webhook.ReposRemoved:
		discovery, cancel := context.WithTimeout(ctx, eventTimeout)
		defer cancel()
		// An account waiting for approval costs no GitHub call and gets no entitlement record, so its trial starts when
		// it is approved: the first sweep after the approval observes it. An event without an account is judged by
		// the installation's record alone: only a configured login admits without one.
		admitted, err := c.admit.Admitted(discovery, e.InstallationID, e.Account)
		if err != nil {
			return fmt.Errorf("control: read admission of installation %d: %w", e.InstallationID, err)
		}
		if !admitted {
			return nil
		}
		// hasPrivate is read from GitHub: installation events carry no repository.
		repos, err := c.repos.ListRepos(discovery, e.InstallationID)
		if err != nil {
			return fmt.Errorf("control: list repositories of installation %d: %w", e.InstallationID, err)
		}
		if err := c.account.Observe(discovery, e.InstallationID, hasPrivate(repos), c.now()); err != nil {
			return fmt.Errorf("control: observe installation %d: %w", e.InstallationID, err)
		}
		if e.Kind == webhook.InstallationAdded && c.setup != nil {
			if err := c.recheckAdded(ctx, e, repos); err != nil {
				return err
			}
		}
	case webhook.InstallationRemoved:
		if err := c.account.Forget(ctx, e.InstallationID); err != nil {
			return fmt.Errorf("control: forget installation %d: %w", e.InstallationID, err)
		}
	}
	return nil
}

// Sweep refreshes every installation's Entitlement record and forgets Store tenants
// whose installation no longer exists.
func (c *Control) Sweep(ctx context.Context) error {
	installations, err := c.repos.ListInstallations(ctx)
	if err != nil {
		return fmt.Errorf("control: list installations: %w", err)
	}
	var observed, refreshed, waiting, failed int
	live := make(map[int64]bool, len(installations))
	for _, in := range installations {
		live[in.ID] = true
		admitted, err := c.admit.Admitted(ctx, in.ID, in.Account)
		if err == nil && !admitted {
			waiting++
			continue
		}
		wasObserved := false
		if err == nil {
			wasObserved, err = c.refresh(ctx, in.ID)
		}
		switch {
		case err != nil:
			failed++
			slog.Warn("control: sweep skipped installation", "installation_id", in.ID, "error", err.Error())
		case wasObserved:
			observed++
		default:
			refreshed++
		}
	}
	forgotten, forgetFailed := c.forgetOrphans(ctx, live)
	slog.Info("control: sweep done", "installations", len(installations), "observed", observed,
		"refreshed", refreshed, "waiting", waiting, "forgotten", forgotten, "failed", failed+forgetFailed)
	return nil
}

// refresh observes the installation, or only refreshes its record when GitHub cannot list its repositories.
func (c *Control) refresh(ctx context.Context, id int64) (observed bool, err error) {
	repos, err := c.repos.ListRepos(ctx, id)
	if err != nil {
		return false, c.account.Refresh(ctx, id, c.now())
	}
	return true, c.account.Observe(ctx, id, hasPrivate(repos), c.now())
}

// forgetOrphans repairs a missed uninstall webhook. It deletes only against a complete,
// non-empty installation list; on any doubt the keys stay.
func (c *Control) forgetOrphans(ctx context.Context, live map[int64]bool) (forgotten, failed int) {
	if len(live) == 0 {
		return 0, 0
	}
	tenants, err := c.tenants.Tenants(ctx)
	if err != nil {
		slog.Warn("control: sweep skipped orphan cleanup", "error", err.Error())
		return 0, 0
	}
	for _, t := range tenants {
		id := int64(t)
		if t == store.SystemTenant || live[id] {
			continue
		}
		if err := c.account.Forget(ctx, id); err != nil {
			failed++
			slog.Warn("control: sweep could not forget orphan tenant", "installation_id", id, "error", err.Error())
			continue
		}
		forgotten++
		slog.Info("control: sweep forgot orphan tenant", "installation_id", id)
	}
	return forgotten, failed
}

// Grant is the operator-only manual beta grant.
func (c *Control) Grant(ctx context.Context, installationID int64, until time.Time) error {
	return c.account.Grant(ctx, installationID, until, c.now())
}

func hasPrivate(repos []api.Repo) bool {
	for _, r := range repos {
		if r.Private {
			return true
		}
	}
	return false
}

type setupPullLister interface {
	SearchSetupPulls(context.Context, int64, string) ([]api.SetupCandidate, error)
}

func (c *Control) recheckAdded(ctx context.Context, e webhook.Event, repos []api.Repo) error {
	selected := make(map[string]api.Repo)
	for _, r := range repos {
		if (e.InstallationCreated && e.RepositoriesOmitted) || slices.Contains(e.AddedRepositories, r.FullName) {
			selected[strings.ToLower(r.FullName)] = r
		}
	}
	if len(selected) == 0 {
		return nil
	}
	account := e.Account
	if account == "" {
		for _, repo := range selected {
			account, _, _ = strings.Cut(repo.FullName, "/")
			break
		}
	}
	searchCtx, cancel := context.WithTimeout(ctx, eventTimeout)
	candidates, err := c.setupPulls.SearchSetupPulls(searchCtx, e.InstallationID, account)
	cancel()
	if err != nil {
		return err
	}
	for _, candidate := range candidates {
		repo, ok := selected[strings.ToLower(candidate.Repo)]
		if !ok {
			continue
		}
		prCtx, done := context.WithTimeout(ctx, eventTimeout)
		outcome, err := c.setup.run(prCtx, repo, candidate.Number, "", true)
		done()
		slog.InfoContext(ctx, "setup recheck", "repository", repo.FullName, "pr", candidate.Number, "outcome", outcome)
		if err != nil {
			if api.Permanent(err) {
				slog.WarnContext(ctx, "setup recheck skipped pull", "repository", repo.FullName, "pr", candidate.Number, "error", err.Error())
				continue
			}
			return err
		}
	}
	return nil
}

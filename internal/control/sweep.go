package control

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"strings"
	"time"

	"github.com/alexei-led/archfit-app/internal/github/api"
	"github.com/alexei-led/archfit-app/internal/github/webhook"
	"github.com/alexei-led/archfit-app/internal/store"
)

const eventTimeout = 10 * time.Second

// HandleEvent maintains entitlements on installation events and forgets a tenant on uninstall.
func (c *Control) HandleEvent(ctx context.Context, e webhook.Event) error {
	if e.InstallationID <= 0 {
		return errors.New("control: event without an installation ID")
	}
	ctx, cancel := context.WithTimeout(ctx, eventTimeout)
	defer cancel()
	switch e.Kind {
	case webhook.InstallationAdded, webhook.ReposRemoved:
		// hasPrivate is read from GitHub: installation events carry no repository.
		repos, err := c.repos.ListRepos(ctx, e.InstallationID)
		if err != nil {
			return fmt.Errorf("control: list repositories of installation %d: %w", e.InstallationID, err)
		}
		// An account waiting for approval gets no entitlement record, so its trial starts when it is approved: the
		// first sweep after the approval observes it. Its owner is the owner of any of its repositories.
		if len(repos) > 0 {
			admitted, err := c.admit.Admitted(ctx, e.InstallationID, ownerOf(repos[0].FullName))
			if err != nil {
				return fmt.Errorf("control: read admission of installation %d: %w", e.InstallationID, err)
			}
			if !admitted {
				return nil
			}
		}
		if err := c.account.Observe(ctx, e.InstallationID, hasPrivate(repos), c.now()); err != nil {
			return fmt.Errorf("control: observe installation %d: %w", e.InstallationID, err)
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

func ownerOf(fullName string) string {
	owner, _, _ := strings.Cut(fullName, "/")
	return owner
}

func hasPrivate(repos []api.Repo) bool {
	for _, r := range repos {
		if r.Private {
			return true
		}
	}
	return false
}

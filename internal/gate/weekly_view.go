package gate

import (
	"context"
	"time"

	"github.com/coupling-dev/archfit-app/internal/gate/weekly"
	"github.com/coupling-dev/archfit-app/internal/github/api"
	"github.com/coupling-dev/archfit-app/internal/store"
)

type WeeklySummary struct {
	Checked, Passed, Owner int
	Known                  bool
}

const WeeklyCaveat = weekly.Caveat

func ReadWeekly(ctx context.Context, s store.Store, repo api.Repo, now time.Time) (WeeklySummary, error) {
	summary, err := weekly.Read(ctx, s, store.Tenant(repo.InstallationID), repo.FullName, now)
	return WeeklySummary(summary), err
}

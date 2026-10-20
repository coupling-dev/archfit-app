package firestore

import (
	"context"
	"errors"
	"strconv"
	"sync"
	"testing"
	"time"

	"github.com/alexei-led/archfit-app/internal/control/account"
	"github.com/alexei-led/archfit-app/internal/store"
)

// Two administrators allowing a feature at once, through different App instances, leave exactly one consent: the
// winner's who and when. Every other allow sees ErrConsentExists, which the UI reports as kept.
func TestConcurrentConsentsKeepTheFirst(t *testing.T) {
	t.Run("fake", func(t *testing.T) { testConcurrentConsents(t, fakeClients(t, 4)) })
	t.Run("emulator", func(t *testing.T) { testConcurrentConsents(t, newEmulatorStores(t, emulatorHost(t), 4)) })
}

func testConcurrentConsents(t *testing.T, clients []store.Store) {
	ctx := context.Background()
	const installation, admins = 7, 8
	at := time.Date(2026, 10, 20, 9, 0, 0, 0, time.UTC)
	errs := make([]error, admins)
	var wg sync.WaitGroup
	for i := range admins {
		_, consents, _ := account.New(clients[i%len(clients)])
		wg.Go(func() {
			errs[i] = consents.Record(ctx, installation, account.Consent{
				Kind: account.ConsentManagedLLM, By: "admin" + strconv.Itoa(i), ByID: int64(100 + i), At: at,
			})
		})
	}
	wg.Wait()

	winner := -1
	for i, err := range errs {
		switch {
		case err == nil && winner < 0:
			winner = i
		case err == nil:
			t.Fatalf("admins %d and %d both recorded the consent", winner, i)
		case !errors.Is(err, account.ErrConsentExists):
			t.Fatalf("admin %d: %v, want ErrConsentExists", i, err)
		}
	}
	if winner < 0 {
		t.Fatal("no admin recorded the consent")
	}
	_, consents, _ := account.New(clients[0])
	got, ok, err := consents.Get(ctx, installation, account.ConsentManagedLLM)
	if !ok || err != nil || got.By != "admin"+strconv.Itoa(winner) || got.ByID != int64(100+winner) {
		t.Errorf("Get = %+v, %v, %v; want the winner admin%d", got, ok, err, winner)
	}
}

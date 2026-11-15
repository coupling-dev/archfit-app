package approvals

import (
	"errors"
	"testing"

	"github.com/alexei-led/archfit-app/internal/gate/records"
)

func TestFirstOnboardingApprovalRequiresANamedAdministrator(t *testing.T) {
	for _, owner := range []Owner{
		{ID: 1, Login: "olivia"},
		{ID: 3, Login: "mallory", Admin: true},
		{ID: 1, Login: "olivia", Admin: true},
	} {
		t.Run(owner.Login+map[bool]string{true: "-admin", false: "-not-admin"}[owner.Admin], func(t *testing.T) {
			w := newWorld(t)
			w.gh.bare("unconverted")
			w.openPull(7, "onboarding", policyV(1), "workflow")
			_, err := w.act(owner, Action{Kind: records.KindFirst, Pull: 7})
			if owner.Login == "olivia" && owner.Admin {
				if err != nil {
					t.Fatal(err)
				}
			} else if !errors.Is(err, ErrNotAdmin) {
				t.Fatalf("Act = %v, want ErrNotAdmin", err)
			}
		})
	}
}

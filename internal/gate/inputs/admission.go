package inputs

import (
	"context"
	"encoding/json"
	"errors"
	"log/slog"
	"strings"
	"time"

	"github.com/alexei-led/archfit-app/internal/store"
)

// AdmissionKey is the Store key, under the installation tenant, of the JSON-encoded Admission. control/account writes
// it when an operator approves the installation, and deletes it on revoke and on uninstall.
const AdmissionKey = "admission"

const admissionTimeout = 2 * time.Second

// Admission records who let the App serve an installation: an operator's approval. Its presence is the approval.
// "Admission" keeps it apart from Approval, a policy owner's approval of a pull request.
type Admission struct {
	Account string    `json:"account"` // the installation's account login when approved
	By      string    `json:"by"`      // the operator's login when approved
	ByID    int64     `json:"by_id"`   // the operator's GitHub user ID
	At      time.Time `json:"at"`
}

// Admissions answers whether the App serves an installation. The App is public on GitHub, so anyone can install it;
// nothing runs for an account until it is approved.
type Admissions struct {
	store store.Store
	auto  map[string]bool // lower-case logins approved by configuration
}

// NewAdmissions admits the accounts named in auto by configuration, and every other one by its Store record.
func NewAdmissions(s store.Store, auto []string) *Admissions {
	a := &Admissions{store: s, auto: make(map[string]bool, len(auto))}
	for _, login := range auto {
		if login = strings.TrimSpace(login); login != "" {
			a.auto[strings.ToLower(login)] = true
		}
	}
	return a
}

// Auto reports whether the configuration approves the account. GitHub logins are case-insensitive.
func (a *Admissions) Auto(account string) bool {
	return a.auto[strings.ToLower(account)]
}

// Admitted reports whether the App serves the installation. owner is its account login, which is the owner of every
// repository it holds. A configured account needs no read. An error means the record could not be read: the caller
// does nothing for the installation and retries, so an approved account loses no event to a Store outage.
func (a *Admissions) Admitted(ctx context.Context, installationID int64, owner string) (bool, error) {
	if a.Auto(owner) {
		return true, nil
	}
	_, found, err := a.Record(ctx, installationID)
	if err != nil {
		slog.Warn("inputs: admission unreadable", "installation_id", installationID)
		return false, err
	}
	return found, nil
}

// Record reads the operator's admission of the installation: (record, true, nil) when approved, (zero, false, nil) when
// not, and an error when the Store fails or the record does not decode.
func (a *Admissions) Record(ctx context.Context, installationID int64) (Admission, bool, error) {
	if installationID <= 0 {
		return Admission{}, false, nil
	}
	ctx, cancel := context.WithTimeout(ctx, admissionTimeout)
	defer cancel()
	raw, err := a.store.Get(ctx, store.Tenant(installationID), AdmissionKey)
	switch {
	case errors.Is(err, store.ErrNotFound):
		return Admission{}, false, nil
	case err != nil:
		return Admission{}, false, err
	}
	// Only an operator's approval is one: a record without the operator's ID is unreadable, never an approval.
	var rec Admission
	if err := json.Unmarshal(raw, &rec); err != nil || rec.ByID <= 0 {
		return Admission{}, false, errors.New("inputs: admission record does not decode")
	}
	return rec, true, nil
}

// Owner is the account login of a repository's "owner/name".
func Owner(fullName string) string {
	owner, _, _ := strings.Cut(fullName, "/")
	return owner
}

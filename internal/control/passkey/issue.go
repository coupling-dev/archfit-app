package passkey

import (
	"encoding/base64"
	"fmt"
	"time"

	"github.com/alexei-led/archfit-app/internal/github/api"
)

// shownID is how many characters of a passkey id an issue shows: enough to tell passkeys apart, never a secret.
const shownID = 12

// registeredIssue is the issue every registration opens, so the user and the owners learn of a passkey they did not
// add (design 9.5: an agent can drive the owner's browser).
func registeredIssue(login string, r api.Repo, p stored, now time.Time) (title, body string) {
	title = "Archfit: passkey registered for " + login
	body = fmt.Sprintf("%s registered a passkey in the Archfit App on %s, from %s.\n\n"+
		"Passkey: `%s…`\n\n"+
		"Answers that change the policy need a passkey. If %s did not register this one, remove it in the App with "+
		"another passkey of theirs, or uninstall and reinstall the App: that removes every passkey of the installation.\n",
		login, stamp(now), r.FullName, prefix(p.ID), login)
	return title, body
}

// removedIssue is the issue the removal of a user's last passkey opens: their next registration needs no passkey.
func removedIssue(login string, r api.Repo, p stored, now time.Time) (title, body string) {
	title = "Archfit: last passkey removed for " + login
	body = fmt.Sprintf("%s removed their last passkey in the Archfit App on %s, from %s.\n\n"+
		"Passkey: `%s…`\n\n"+
		"%s has no passkey now, so their next registration needs no other passkey. If %s did not remove it, "+
		"check who can use their passkeys and their browser.\n",
		login, stamp(now), r.FullName, prefix(p.ID), login, login)
	return title, body
}

func stamp(t time.Time) string { return t.UTC().Format("2006-01-02 15:04 UTC") }

func prefix(id []byte) string {
	s := base64.RawURLEncoding.EncodeToString(id)
	if len(s) > shownID {
		s = s[:shownID]
	}
	return s
}

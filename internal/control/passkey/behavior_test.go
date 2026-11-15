package passkey

import (
	"errors"
	"slices"
	"strings"
	"testing"

	"github.com/go-webauthn/webauthn/protocol/webauthncose"

	"github.com/alexei-led/archfit-app/internal/github/api"
	"github.com/alexei-led/archfit-app/internal/store"
)

func TestWhoRegisters(t *testing.T) {
	githubDown := errors.New("github down")
	for _, tc := range []struct {
		name      string
		user      User
		owners    []string
		hasPolicy bool
		ownersErr error
		want      error
	}{
		{"an owner", alice, []string{"alice"}, true, nil, nil},
		{"an owner, login in another case", alice, []string{"ALICE"}, true, nil, nil},
		{"not an owner", bob, []string{"alice"}, true, nil, ErrNotOwner},
		{"an administrator who is not an owner", User{ID: bob.ID, Login: "bob", Admin: true}, []string{"alice"}, true, nil, ErrNotOwner},
		{"an administrator before the first policy", User{ID: bob.ID, Login: "bob", Admin: true}, nil, false, nil, nil},
		{"not an administrator before the first policy", bob, nil, false, nil, ErrNotOwner},
		{"a policy that cannot be read", alice, nil, false, githubDown, githubDown},
		{"no login", User{ID: alice.ID, Admin: true}, nil, false, nil, ErrNotOwner},
	} {
		t.Run(tc.name, func(t *testing.T) {
			e := newEnv(t)
			e.owners.logins, e.owners.hasPolicy, e.owners.err = tc.owners, tc.hasPolicy, tc.ownersErr
			_, err := e.register(tc.user, newAuthenticator(t, webauthncose.AlgES256), "")
			if !errors.Is(err, tc.want) {
				t.Fatalf("error = %v, want %v", err, tc.want)
			}
			if tc.want != nil && len(e.list(tc.user)) != 0 {
				t.Error("a refused registration stored a passkey")
			}
		})
	}
}

func TestTheRuleIsCheckedAgainAtFinish(t *testing.T) {
	e := newEnv(t)
	a := newAuthenticator(t, webauthncose.AlgES256)
	options, err := e.pk.BeginRegistration(ctx, repo, alice, "")
	if err != nil {
		t.Fatal(err)
	}
	e.owners.logins = []string{"carol"} // alice left the owners between begin and finish
	_, err = e.pk.FinishRegistration(ctx, repo, alice, a.create(options))
	wantErr(t, err, ErrNotOwner)
}

func TestEveryRegistrationOpensAnIssueForTheUserAndTheOwners(t *testing.T) {
	e := newEnv(t)
	a := newAuthenticator(t, webauthncose.AlgES256)
	e.mustRegister(alice, a, "")
	issues := e.issues.all()
	if len(issues) != 1 {
		t.Fatalf("issues = %d, want 1", len(issues))
	}
	got := issues[0]
	if got.repo != repo.RepoRef {
		t.Errorf("issue repository = %+v, want %+v", got.repo, repo.RepoRef)
	}
	if !slices.Equal(got.assignees, []string{"alice", "carol"}) {
		t.Errorf("assignees = %v, want the user, then the other owners, once each", got.assignees)
	}
	for _, want := range []string{"alice", repo.FullName, b64(a.id)[:shownID], "2026-11-12 10:00 UTC"} {
		if !strings.Contains(got.title+got.body, want) {
			t.Errorf("issue does not name %q:\n%s\n%s", want, got.title, got.body)
		}
	}
	if strings.Contains(got.body, b64(a.id)) {
		t.Error("the issue shows the whole passkey id")
	}

	second := newAuthenticator(t, webauthncose.AlgES256)
	e.mustRegister(alice, second, e.mustStepUp(alice, a, Purpose{Action: ActionRegister}))
	if n := len(e.issues.all()); n != 2 {
		t.Errorf("issues after a second registration = %d, want 2", n)
	}
}

func TestBeforeTheFirstPolicyTheIssueGoesToTheAdministrator(t *testing.T) {
	e := newEnv(t)
	e.owners.logins, e.owners.hasPolicy = nil, false
	e.mustRegister(User{ID: bob.ID, Login: "bob", Admin: true}, newAuthenticator(t, webauthncose.AlgES256), "")
	if got := e.issues.all(); len(got) != 1 || !slices.Equal(got[0].assignees, []string{"bob"}) {
		t.Errorf("issues = %+v, want one assigned to bob", got)
	}
}

func TestARegistrationWhoseIssueFailsStoresNothing(t *testing.T) {
	e := newEnv(t)
	a := newAuthenticator(t, webauthncose.AlgES256)
	options, err := e.pk.BeginRegistration(ctx, repo, alice, "")
	if err != nil {
		t.Fatal(err)
	}
	response := a.create(options)
	e.issues.err = errors.New("github down")
	if _, err := e.pk.FinishRegistration(ctx, repo, alice, response); err == nil {
		t.Fatal("FinishRegistration succeeded without its issue")
	}
	if got := e.list(alice); len(got) != 0 {
		t.Fatalf("List = %+v, want nothing stored", got)
	}

	e.issues.err = nil
	if _, err := e.pk.FinishRegistration(ctx, repo, alice, response); err != nil {
		t.Fatalf("the same ceremony, once the issue opens: %v", err)
	}
}

func TestUninstallRemovesThePasskeysAndAReinstallRegistersAfresh(t *testing.T) {
	e, a := registered(t)
	if _, err := e.pk.BeginStepUp(ctx, repo, alice, approve); err != nil {
		t.Fatal(err)
	}

	// The uninstall: control/account.Forget deletes the installation's tenant.
	if err := e.store.DeleteTenant(ctx, store.Tenant(repo.InstallationID)); err != nil {
		t.Fatal(err)
	}
	if got := e.list(alice); len(got) != 0 {
		t.Fatalf("List after uninstall = %+v, want none", got)
	}
	_, err := e.stepUp(alice, a, approve)
	wantErr(t, err, ErrNoPasskey)

	fresh := newAuthenticator(t, webauthncose.AlgES256)
	e.mustRegister(alice, fresh, "")
	if slices.Equal(fresh.handle, a.handle) {
		t.Error("a reinstall reused the old user handle")
	}
	_, err = e.stepUp(alice, a, approve)
	wantErr(t, err, ErrCeremony) // the old passkey is gone
}

func TestPasskeysBelongToOneInstallation(t *testing.T) {
	e, a := registered(t)
	elsewhere := api.Repo{RepoRef: api.RepoRef{InstallationID: 8, FullName: "rival/app"}, ID: 80}
	got, err := e.pk.List(ctx, elsewhere, alice.ID)
	if err != nil || len(got) != 0 {
		t.Fatalf("List in another installation = %+v, %v; want none", got, err)
	}
	_, err = e.pk.BeginStepUp(ctx, elsewhere, alice, approve)
	wantErr(t, err, ErrNoPasskey)

	token := e.mustStepUp(alice, a, approve)
	_, err = e.pk.Redeem(ctx, elsewhere, alice.ID, token, approve)
	wantErr(t, err, ErrToken)
}

func TestRemoveNeedsAFreshAssertion(t *testing.T) {
	e, a := registered(t)
	second := newAuthenticator(t, webauthncose.AlgES256)
	p2 := e.mustRegister(alice, second, e.mustStepUp(alice, a, Purpose{Action: ActionRegister}))
	p1 := b64(a.id)

	wantErr(t, e.pk.Remove(ctx, repo, alice, p2.ID, ""), ErrToken)
	wantErr(t, e.pk.Remove(ctx, repo, alice, p2.ID, e.mustStepUp(alice, a, approve)), ErrToken)
	forFirst := e.mustStepUp(alice, a, Purpose{Action: ActionRemove, Passkey: p1})
	wantErr(t, e.pk.Remove(ctx, repo, alice, p2.ID, forFirst), ErrToken)
	wantErr(t, e.pk.Remove(ctx, repo, alice, "bm9wZQ", e.mustStepUp(alice, a, Purpose{Action: ActionRemove, Passkey: "bm9wZQ"})), ErrUnknown)
	if got := e.list(alice); len(got) != 2 {
		t.Fatalf("List = %d passkeys after refused removals, want 2", len(got))
	}

	issuesBefore := len(e.issues.all())
	token := e.mustStepUp(alice, a, Purpose{Action: ActionRemove, Passkey: p2.ID})
	if err := e.pk.Remove(ctx, repo, alice, p2.ID, token); err != nil {
		t.Fatalf("Remove: %v", err)
	}
	if got := e.list(alice); len(got) != 1 || got[0].ID != p1 {
		t.Errorf("List = %+v, want only %s", got, p1)
	}
	if len(e.issues.all()) != issuesBefore {
		t.Error("removing a passkey that is not the last opened an issue")
	}
	wantErr(t, e.pk.Remove(ctx, repo, alice, p2.ID, token), ErrUnknown)
}

func TestRemovingTheLastPasskeyOpensAnIssueAndAllowsAFirstRegistration(t *testing.T) {
	e, a := registered(t)
	id := b64(a.id)
	token := e.mustStepUp(alice, a, Purpose{Action: ActionRemove, Passkey: id})
	if err := e.pk.Remove(ctx, repo, alice, id, token); err != nil {
		t.Fatalf("Remove: %v", err)
	}
	if got := e.list(alice); len(got) != 0 {
		t.Fatalf("List = %+v, want none", got)
	}
	issues := e.issues.all()
	last := issues[len(issues)-1]
	if len(issues) != 2 || !strings.Contains(last.title, "last passkey removed") ||
		!slices.Equal(last.assignees, []string{"alice", "carol"}) {
		t.Errorf("issues = %+v, want a removal issue assigned to alice and carol", issues)
	}
	e.mustRegister(alice, newAuthenticator(t, webauthncose.AlgES256), "")
}

func TestRemovingTheLastPasskeyWhoseIssueFailsKeepsIt(t *testing.T) {
	e, a := registered(t)
	id := b64(a.id)
	token := e.mustStepUp(alice, a, Purpose{Action: ActionRemove, Passkey: id})
	e.issues.err = errors.New("github down")
	if err := e.pk.Remove(ctx, repo, alice, id, token); err == nil {
		t.Fatal("Remove succeeded without its issue")
	}
	if got := e.list(alice); len(got) != 1 {
		t.Fatalf("List = %+v, want the passkey kept", got)
	}
}

package control

import (
	"context"
	"reflect"
	"testing"

	"github.com/alexei-led/archfit-app/internal/control/account"
	"github.com/alexei-led/archfit-app/internal/github/api"
	"github.com/alexei-led/archfit-app/internal/github/webhook"
	"github.com/alexei-led/archfit-app/internal/store"
)

func TestNewValidatesWiring(t *testing.T) {
	gh := newFakeGitHub(t)
	gh.install(7, ghr(70, "org7/app", true))
	valid := func() Deps {
		return Deps{
			GitHub: gh.client(), Tokens: fakeTokens{}, OAuth: fakeOAuth{}, Store: store.NewMemory(),
			Receiver: &fakeReceiver{}, Sandbox: &fakeSandbox{}, Provider: &fakeProvider{},
			CookieKey: testCookieKey, BaseURL: testBaseURL, Now: newClock().Now,
		}
	}
	tests := []struct {
		name    string
		mutate  func(*Deps)
		wantErr bool
	}{
		{"nil GitHub", func(d *Deps) { d.GitHub = nil }, true},
		{"nil OAuth", func(d *Deps) { d.OAuth = nil }, true},
		{"nil Store", func(d *Deps) { d.Store = nil }, true},
		{"nil Receiver", func(d *Deps) { d.Receiver = nil }, true},
		{"16-byte CookieKey", func(d *Deps) { d.CookieKey = testCookieKey[:16] }, true},
		{"empty BaseURL", func(d *Deps) { d.BaseURL = "" }, true},
		{"Sandbox without Tokens", func(d *Deps) { d.Tokens = nil }, true},
		{"nil Now", func(d *Deps) { d.Now = nil }, false},
		{"nil Sandbox, Provider and Tokens", func(d *Deps) { d.Sandbox, d.Provider, d.Tokens = nil, nil, nil }, false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			d := valid()
			tt.mutate(&d)
			c, err := New(d)
			if tt.wantErr {
				if err == nil || c != nil {
					t.Fatalf("New = %v, %v; want nil Control and an error", c, err)
				}
				return
			}
			if err != nil {
				t.Fatalf("New: %v", err)
			}
			if c.UIHandler() == nil || c.DiscoveryHandler() == nil || c.BaselineHandler() == nil {
				t.Fatal("handlers are not mounted")
			}
			if err := c.HandleEvent(context.Background(), webhook.Event{Kind: webhook.InstallationAdded, InstallationID: 7}); err != nil {
				t.Fatalf("HandleEvent: %v", err)
			}
			if c.now().IsZero() {
				t.Error("clock returns the zero time")
			}
		})
	}
}

func TestHandleEventRouting(t *testing.T) {
	kinds := []webhook.EventKind{
		webhook.PullOpened, webhook.PullSynchronized, webhook.PullClosed, webhook.ReviewChanged,
		webhook.DefaultBranchPush, webhook.InstallationAdded, webhook.InstallationRemoved,
		webhook.ReposRemoved, webhook.Ignored,
	}
	want := map[webhook.EventKind]struct{ github, account []string }{
		webhook.InstallationAdded:   {[]string{"repos:7"}, []string{"observe:7:true"}},
		webhook.ReposRemoved:        {[]string{"repos:7"}, []string{"observe:7:true"}},
		webhook.InstallationRemoved: {nil, []string{"forget:7"}},
	}
	for _, kind := range kinds {
		t.Run(string(kind), func(t *testing.T) {
			acct := &fakeAccount{}
			repos := &fakeRepos{repos: map[int64][]api.Repo{7: {repo(7, "org7/app", true)}}}
			clk := newClock()
			c := newControl(acct, repos, &fakeTenants{}, okHandler, okHandler, clk.Now)

			if err := c.HandleEvent(context.Background(), webhook.Event{Kind: kind, InstallationID: 7, DeliveryID: "d1"}); err != nil {
				t.Fatalf("HandleEvent: %v", err)
			}
			if !reflect.DeepEqual(repos.calls, want[kind].github) {
				t.Errorf("GitHub calls = %v, want %v", repos.calls, want[kind].github)
			}
			if !reflect.DeepEqual(acct.calls, want[kind].account) {
				t.Errorf("account calls = %v, want %v", acct.calls, want[kind].account)
			}
			for _, at := range acct.times {
				if !at.Equal(t0) {
					t.Errorf("now = %v, want the injected clock %v", at, t0)
				}
			}
			for _, bounded := range repos.deadlines {
				if !bounded {
					t.Error("GitHub call without a deadline")
				}
			}
		})
	}
	for _, kind := range kinds {
		for _, id := range []int64{0, -1} {
			t.Run(string(kind)+" without installation", func(t *testing.T) {
				acct, repos := &fakeAccount{}, &fakeRepos{}
				c := newControl(acct, repos, &fakeTenants{}, okHandler, okHandler, newClock().Now)
				if err := c.HandleEvent(context.Background(), webhook.Event{Kind: kind, InstallationID: id}); err == nil {
					t.Error("HandleEvent = nil, want an error")
				}
				if len(acct.calls)+len(repos.calls) != 0 {
					t.Errorf("calls = %v %v, want none", acct.calls, repos.calls)
				}
			})
		}
	}
	t.Run("errors return to app", func(t *testing.T) {
		repos := &fakeRepos{reposErr: map[int64]error{7: errBoom}}
		acct := &fakeAccount{}
		c := newControl(acct, repos, &fakeTenants{}, okHandler, okHandler, newClock().Now)
		if err := c.HandleEvent(context.Background(), webhook.Event{Kind: webhook.InstallationAdded, InstallationID: 7}); err == nil {
			t.Error("HandleEvent = nil, want the ListRepos error")
		}
		if len(acct.calls) != 0 {
			t.Errorf("account calls = %v, want none", acct.calls)
		}
	})
}

func TestHasPrivateComesFromGitHub(t *testing.T) {
	tests := []struct {
		name          string
		eventPrivate  bool
		githubPrivate bool
		wantPlan      account.Plan
	}{
		{"event says private, GitHub lists public only", true, false, account.PlanPublic},
		{"event says public, GitHub lists a private repository", false, true, account.PlanTrial},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			st := store.NewMemory()
			acct, _, _ := account.New(st)
			repos := &fakeRepos{repos: map[int64][]api.Repo{7: {repo(7, "org7/app", tt.githubPrivate), repo(7, "org7/docs", false)}}}
			c := newControl(acct, repos, st, okHandler, okHandler, newClock().Now)

			e := webhook.Event{Kind: webhook.InstallationAdded, InstallationID: 7, RepoPrivate: tt.eventPrivate}
			if err := c.HandleEvent(context.Background(), e); err != nil {
				t.Fatalf("HandleEvent: %v", err)
			}
			got, err := acct.Status(context.Background(), 7, t0)
			if err != nil {
				t.Fatalf("Status: %v", err)
			}
			if got.Plan != tt.wantPlan {
				t.Errorf("plan = %q, want %q", got.Plan, tt.wantPlan)
			}
			if started := !got.TrialEnds.IsZero(); started != tt.githubPrivate {
				t.Errorf("trial started = %t, want %t", started, tt.githubPrivate)
			}
		})
	}
}

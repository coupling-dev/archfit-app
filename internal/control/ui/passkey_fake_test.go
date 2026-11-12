package ui

import (
	"context"
	"encoding/json"
	"errors"
	"slices"
	"sync"
	"time"

	"github.com/alexei-led/archfit-app/internal/control/passkey"
	"github.com/alexei-led/archfit-app/internal/github/api"
)

const (
	fakeOptions   = `{"publicKey":{"challenge":"Y2hhbGxlbmdl"}}`
	fakePasskeyID = "cGFzc2tleQ"
	fakeToken     = "dG9rZW4"
)

var fakeCreated = time.Date(2026, 11, 12, 10, 0, 0, 0, time.UTC)

// passkeyCall is one call that reached the fake passkeys: what ui handed to control/passkey.
type passkeyCall struct {
	method     string
	repo       api.Repo
	user       passkey.User
	token      string
	purpose    passkey.Purpose
	credential string
	id         string
}

// fakePasskeys answers every call with success unless err is set, and records what it got.
type fakePasskeys struct {
	mu    sync.Mutex
	err   error
	calls []passkeyCall
}

func (f *fakePasskeys) record(c passkeyCall) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.calls = append(f.calls, c)
	return f.err
}

func (f *fakePasskeys) all() []passkeyCall {
	f.mu.Lock()
	defer f.mu.Unlock()
	return slices.Clone(f.calls)
}

func (f *fakePasskeys) BeginRegistration(_ context.Context, r api.Repo, u passkey.User, token string) (json.RawMessage, error) {
	if err := f.record(passkeyCall{method: "BeginRegistration", repo: r, user: u, token: token}); err != nil {
		return nil, err
	}
	return json.RawMessage(fakeOptions), nil
}

func (f *fakePasskeys) FinishRegistration(_ context.Context, r api.Repo, u passkey.User, credential []byte) (passkey.Passkey, error) {
	if err := f.record(passkeyCall{method: "FinishRegistration", repo: r, user: u, credential: string(credential)}); err != nil {
		return passkey.Passkey{}, err
	}
	return passkey.Passkey{ID: fakePasskeyID, Created: fakeCreated}, nil
}

func (f *fakePasskeys) BeginStepUp(_ context.Context, r api.Repo, u passkey.User, p passkey.Purpose) (json.RawMessage, error) {
	if err := f.record(passkeyCall{method: "BeginStepUp", repo: r, user: u, purpose: p}); err != nil {
		return nil, err
	}
	return json.RawMessage(fakeOptions), nil
}

func (f *fakePasskeys) FinishStepUp(_ context.Context, r api.Repo, u passkey.User, credential []byte) (string, error) {
	if err := f.record(passkeyCall{method: "FinishStepUp", repo: r, user: u, credential: string(credential)}); err != nil {
		return "", err
	}
	return fakeToken, nil
}

func (f *fakePasskeys) Redeem(context.Context, api.Repo, int64, string, passkey.Purpose) (string, error) {
	return "", errors.New("fake passkeys: ui never redeems")
}

func (f *fakePasskeys) List(_ context.Context, r api.Repo, userID int64) ([]passkey.Passkey, error) {
	if err := f.record(passkeyCall{method: "List", repo: r, user: passkey.User{ID: userID}}); err != nil {
		return nil, err
	}
	return []passkey.Passkey{{ID: fakePasskeyID, Created: fakeCreated}}, nil
}

func (f *fakePasskeys) Remove(_ context.Context, r api.Repo, u passkey.User, id, token string) error {
	return f.record(passkeyCall{method: "Remove", repo: r, user: u, id: id, token: token})
}

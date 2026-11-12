package proposals

import (
	"context"
	"errors"
	"strconv"
	"sync"
	"testing"
	"time"

	"github.com/alexei-led/archfit-app/internal/gate/records"
	"github.com/alexei-led/archfit-app/internal/gate/requests"
	"github.com/alexei-led/archfit-app/internal/github/api"
	"github.com/alexei-led/archfit-app/internal/store"
	"github.com/alexei-led/archfit-app/internal/wire/policy3"
)

const testPolicy = `version: 3
owners: [olivia, oscar]
modules:
  pkg_tc:
    paths: [pkg/tc/**]
    depends_on: [pkg_net]   # reviewed 2026-10-01
  pkg_util:
    paths: [pkg/util/**]
  pkg_net:
    paths: [pkg/net/**]
  pkg_db:
    paths: [pkg/db/**]
rules:
  - id: no-util-to-tc
    deny:
      from: pkg_util
      to: pkg_tc
    why: Util stays at the bottom.
    instead: Pass a value in.
`

var (
	testRepo = api.Repo{RepoRef: api.RepoRef{InstallationID: 7, FullName: "acme/shop"}, ID: 9, DefaultBranch: fakeBase}
	testNow  = time.Date(2026, 11, 12, 10, 0, 0, 0, time.UTC)
	olivia   = Owner{ID: 501, Login: "olivia"}
	oscar    = Owner{ID: 502, Login: "Oscar"}
	errToken = errors.New("passkey: the token is not valid")
)

// world is a repository with its policy on the default branch, a Store, and a passkey that knows one-use tokens.
type world struct {
	t     *testing.T
	hub   *fakeHub
	store store.Store
	svc   *Service

	mu       sync.Mutex
	tokens   map[string]string // token → the digest it was made for
	redeemed []string          // the digests redeemed, in order
}

func newWorld(t *testing.T) *world {
	t.Helper()
	w := &world{t: t, hub: newFakeHub(testPolicy), store: store.NewMemory(), tokens: map[string]string{}}
	w.svc = New(w.hub, w.store, func() time.Time { return testNow })
	w.svc.sleep = func(ctx context.Context, _ time.Duration) error { return ctx.Err() }
	return w
}

// request stores request number open, as task 16 does, and its open issue.
func (w *world) request(number int, rule, from, to string) {
	w.t.Helper()
	key := rule + " " + from + " " + to
	req := records.Request{Key: key, Reason: "why", Rule: rule, From: from, To: to, State: records.StateOpen}
	if rule == "unowned" {
		req.Key, req.Path, req.From, req.To = "unowned "+from, from, "", ""
	}
	err := records.Indexed(context.Background(), w.store, tenant(testRepo), records.RequestKey(testRepo.FullName, number),
		records.RequestIndexKey(testRepo.FullName), records.SchemaRequestIndex, func(_ []byte, x *records.Index) ([]byte, error) {
			if err := x.Add(req.Key, strconv.Itoa(number)); err != nil {
				return nil, err
			}
			return records.Encode(&req)
		})
	if err != nil {
		w.t.Fatal(err)
	}
	w.hub.issues[number] = true
}

// stepUp is the owner's passkey step-up for answer a to request number: a token bound to the change digest.
func (w *world) stepUp(number int, a requests.Answer) string {
	w.t.Helper()
	digest, err := w.svc.Digest(context.Background(), testRepo, number, a)
	if err != nil {
		w.t.Fatalf("Digest: %v", err)
	}
	w.mu.Lock()
	defer w.mu.Unlock()
	token := "token-" + strconv.Itoa(len(w.tokens)+1)
	w.tokens[token] = digest
	return token
}

// redeem is the fake passkey's Redeem: a token is used once, for the digest it was made for.
func (w *world) redeem(token string) func(context.Context, string) (string, error) {
	return func(_ context.Context, digest string) (string, error) {
		w.mu.Lock()
		defer w.mu.Unlock()
		want, ok := w.tokens[token]
		if !ok || want != digest {
			return "", errToken
		}
		delete(w.tokens, token)
		w.redeemed = append(w.redeemed, digest)
		return "cred-" + token, nil
	}
}

// answer answers request number as owner, with a step-up when the answer loosens the policy.
func (w *world) answer(owner Owner, number int, a requests.Answer, later bool) (Outcome, error) {
	w.t.Helper()
	in := Input{Request: number, Answer: a, Owner: owner, Later: later}
	if loosens, _ := policy3.Loosens(a.Kind); loosens {
		in.Redeem = w.redeem(w.stepUp(number, a))
	}
	return w.svc.Answer(context.Background(), testRepo, in)
}

func (w *world) mustAnswer(owner Owner, number int, a requests.Answer, later bool) Outcome {
	w.t.Helper()
	out, err := w.answer(owner, number, a, later)
	if err != nil {
		w.t.Fatalf("answer %d: %v", number, err)
	}
	return out
}

func load[T any, P interface {
	*T
	records.Value
}](t *testing.T, s store.Store, key string) T {
	t.Helper()
	var v T
	if err := records.Load(context.Background(), s, tenant(testRepo), key, P(&v)); err != nil {
		t.Fatalf("Load(%q): %v", key, err)
	}
	return v
}

func (w *world) requestState(number int) string {
	return load[records.Request](w.t, w.store, records.RequestKey(testRepo.FullName, number)).State
}

func (w *world) proposalOf(out Outcome) records.Proposal {
	a := load[records.Approval](w.t, w.store, records.ApprovalKey(testRepo.FullName, out.Approval))
	return load[records.Proposal](w.t, w.store, records.ProposalKey(testRepo.FullName, a.Proposal))
}

// keys is every Store key the world holds, for "nothing written" checks.
func (w *world) snapshot() map[string]string {
	w.t.Helper()
	out := map[string]string{}
	for _, k := range []string{records.RequestIndexKey(testRepo.FullName), records.ProposalIndexKey(testRepo.FullName),
		records.RequestKey(testRepo.FullName, 431), records.RequestKey(testRepo.FullName, 432)} {
		v, err := w.store.Get(context.Background(), tenant(testRepo), k)
		if err != nil && !errors.Is(err, store.ErrNotFound) {
			w.t.Fatal(err)
		}
		out[k] = string(v)
	}
	return out
}

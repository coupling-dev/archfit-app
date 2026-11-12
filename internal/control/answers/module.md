# Answers

**Path**: internal/control/answers/
**Parent**: internal/control/ (control plane façade)
**Submodules**: none (leaf)

## Purpose

An owner answers a request in the App: approve, approve until a date, or reject with what to do instead (design 10.4).
An answer that loosens the policy needs the owner's passkey (design 9.2), which `control/passkey` owns; recording the
answer and carrying it to the default branch is the gate's (`gate.Answers`, `gate/proposals`). `control` may not be
imported by `gate`, so this module joins the two: it hands the gate a `Redeem` bound to the owner, the request and the
answer kind, which the gate calls with the change digest it computes. Without it the request page would either skip the
passkey or reach into the gate's records.

## Functional Responsibilities

- `Digest`: the change digest of an answer, from `gate.Answers.AnswerDigest`. The page binds the passkey step-up to it.
- `Answer`: the answer of the signed-in user, with `Redeem` = `passkey.Redeem(repo, user, token, Purpose{Action: the
  answer kind, Request: the number, Digest})`, to `gate.Answers.Answer`. The gate calls `Redeem` only for an answer that
  loosens the policy, after the owner, request and answer checks: a refusal before it leaves the token unused, one
  after it (a failed branch write or record) leaves it used, and the owner confirms again. A reject redeems nothing.

Not provided: the owner rule, the records, the policy pull request (the gate), the passkey ceremonies (`passkey`), the
pages (M2 task 24). Owner actions outside requests (M2 task 23) and the YAML edit (M3 task 10) come here later.

## Subdomain Classification

Supporting glue between two core modules. Volatility high during milestones 2 and 3, as answer kinds are added.

## Encapsulated Knowledge

- That an answer's step-up `Purpose` is `{Action: kind, Request: number, Digest: change digest}`, so a token made for
  one answer to one request never redeems another.

## Public Contract

```go
type Answer struct {
    Kind    string // approve, approve-until or reject
    Until   string // approve-until: YYYY-MM-DD
    Instead string // reject: what to do instead
    Later   bool   // merge later
    Token   string // the passkey step-up token, for an answer that loosens the policy
}

type Answers interface {
    Digest(ctx context.Context, r api.Repo, number int, a Answer) (string, error)
    Answer(ctx context.Context, r api.Repo, u passkey.User, number int, a Answer) (gate.AnswerOutcome, error)
}

func New(g gate.Answers, p passkey.Passkeys) Answers
```

Errors are the gate's (`gate.ErrNotPolicyOwner`, `gate.ErrNeedsPasskey`, `gate.ErrAnswerBusy`, `gate.ErrProposalFull`,
the request page's) and the passkey's (`passkey.ErrToken`), returned as they are. An error means the answer was not
recorded.

## Integrations

- **control** (parent): `New(d.Answers, passkeys)` in `control.New`, handed to `ui`. The worker role does not build it.
- **control/passkey** (sibling): `Redeem`. **control/ui** (sibling): the answer routes call `Digest` and `Answer`.
- **gate** (façade): `Answers`, `Target`, `RequestAnswer`, `AnswerInput`, `AnswerOwner`, `AnswerOutcome`.
- Contract strength; LCA control / rank 1 / distance 1 for the siblings, root / 2 / 2 for the gate; balanced.

## Constraints and Invariants

- Tenant and repository come only from the `api.Repo` the caller authorized; the user from the session.
- No token, passkey id or owner text in a log line (this module logs nothing).

## Test Specification

Over a fake `gate.Answers` that calls `Redeem` for a loosening answer, as `gate/proposals` does, and a fake `Redeem`:

- `TestAnAnswerRedeemsTheTokenForItsRequestKindAndDigest`: the Purpose, the user id, the token, and what the gate gets.
- `TestARejectRedeemsNothing`, `TestARefusedTokenRefusesTheAnswer`, `TestDigestAsksTheGate`.

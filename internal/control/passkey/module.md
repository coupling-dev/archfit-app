# Passkey

**Path**: internal/control/passkey/
**Parent**: internal/control/ (control plane façade)
**Submodules**: none (leaf)

## Purpose

`passkey` is the App's WebAuthn relying party. An answer that loosens the policy needs a passkey with user
verification, registered by that owner (design 9.2): a session cookie is not enough, because an agent can drive the
owner's browser. `passkey` registers those passkeys and turns one assertion into a one-use token for one action, which
the answer redeems. Verification is `go-webauthn`'s (M2 D9): the App never parses WebAuthn, CBOR or COSE itself.

## Functional Responsibilities

- Run the registration ceremony with the parameters of the M2 plan, "Passkey": relying party id the App host, origin
  `https://` and that host, attestation `none`, ES256, Ed25519 (COSE -19, and -8 that authenticators still send for
  it), RS256, user verification required, resident key preferred, timeout 5 minutes.
- Apply "who registers": an owner named in the target policy, or, before the first schema 3 policy, a repository
  administrator. A second passkey needs a step-up token for `register`. Every registration opens an issue assigned to
  the user and the owners before the passkey is stored.
- Run the step-up: one assertion with user verification gives a token bound to a `Purpose` (repository, action,
  request, policy change digest, passkey), usable once within 5 minutes. `Redeem` consumes it.
- Refuse an assertion whose sign counter does not grow when it is not zero. go-webauthn only flags it
  (`CloneWarning`).
- List the user's passkeys; remove one with a step-up token for `remove` and that passkey. Removing the last one opens
  a removal issue, assigned like the registration issue to the user and the owners, deletes the record, and the
  user's next registration needs no passkey.
- Provide the two ports over GitHub: `NewOwners` (the owners of the policy on the default branch) and `NewIssues`
  (open the issue as the App, then assign it).

Not provided: the answers (`control/answers`, M2 task 19), the pages (M2 task 24), discoverable login, attestation
trust (no metadata service), a passkey name.

## Subdomain Classification

Generic: WebAuthn is a solved problem, and the library solves it. The rules around it (who registers, what a token
allows) are the App's and follow design 9.2. Volatility **low**: the parameters are fixed by M2 D9; a library upgrade
changes no stored value, because the stored passkey is the App's own type.

## Encapsulated Knowledge

- Keys, under the installation tenant (`store.Tenant(installationID)`), so an uninstall deletes them with the rest of
  the installation (`control/account.Forget` → `DeleteTenant`):
  - `v4/passkey/<github-user-id>`: `archfit-app.passkey.v1`, the user handle (32 random bytes, one per user in an
    installation) and 1 to 10 passkeys **[default]** (id, COSE public key, flags byte, AAGUID, sign count,
    transports, created, last used). The record exists only while it holds a passkey.
  - `v4/challenge/<github-user-id>`: `archfit-app.challenge.v1`, the user's one open ceremony (`register`, `assert`)
    or the token one assertion gave (`token`: its SHA-256, the purpose, the passkey, the time). A new ceremony
    replaces the old one, so storage stays bounded although the Store has no expiry: **in two tabs at once, the
    older ceremony fails.** A challenge holds go-webauthn's `SessionData` whole, as the library asks; it lives at
    most 5 minutes.
- Values are JSON with `schema`, read strictly: another schema, an unknown field or trailing data is refused, and the
  error never quotes the value. A challenge that cannot be read is no challenge (a new ceremony replaces it); a record
  that cannot be read refuses every step (nothing overwrites it).
- The user handle is random, not derived from the user id: the same handle in two installations would make an
  authenticator replace the first installation's resident key.
- Expiry is checked against the injected clock; go-webauthn's own check reads the wall clock, so its `Expires` stays
  zero.
- A begin, the step-up finish and a redeem change the record and the challenge through `store.Update`, whose change
  verifies again on every read: a response whose challenge was replaced or used fails. The registration finish and a
  removal read, verify, open their issue, then write once with `CompareAndSwapAll` from the bytes read: a key that
  moved in between fails the step. So a response is never applied twice.
- The token: 32 random bytes in base64url. Only its SHA-256 is stored; the comparison is constant-time.
- Before the first policy: no `.archfit.yaml` on the default branch, or an Archfit 3 (version 2) one, which the
  onboarding pull request converts. Archfit 3 means a text `policy.Diagnose` accepts in full, its schema's
  `version: 2` included; `policy.Validate` checks only key names and would take a schema 3 file without `owners`. A
  file that is neither schema 3 nor Archfit 3 is `ErrPolicy`. The Archfit 3 check goes with `wire/policy` (M2 task
  25).

## Public Contract

```go
const (
    ActionRegister = "register" // a second passkey
    ActionRemove   = "remove"   // removing a passkey; Purpose.Passkey names it
)

const TTL = 5 * time.Minute // a challenge and a token
const MaxPasskeys = 10       // per user in an installation [default]

type Config struct{ BaseURL string } // relying party id: its host; origin: its scheme and host

type User struct {
    ID    int64
    Login string
    Admin bool // a repository administrator, from session.Authorize(…, "admin")
}

// Purpose is what one step-up token allows. A token is redeemed only for an equal Purpose. Repo is set from the
// repository the call names.
type Purpose struct {
    Repo    string // lower-case owner/name
    Action  string // ActionRegister, ActionRemove, or an answer kind of control/answers
    Request int    // the request issue; 0 when none
    Digest  string // the policy change digest; "" when none
    Passkey string // ActionRemove: the id of the passkey to remove
}

type Passkey struct {
    ID       string // the credential id, base64url
    Created  time.Time
    LastUsed time.Time // zero until its first step-up
}

// Ports.
type Owners interface {
    Owners(ctx context.Context, r api.Repo) (logins []string, hasPolicy bool, err error)
}
type Issues interface {
    Open(ctx context.Context, r api.RepoRef, title, body string, assignees []string) error
}

type Passkeys interface {
    BeginRegistration(ctx context.Context, r api.Repo, u User, token string) (json.RawMessage, error)
    FinishRegistration(ctx context.Context, r api.Repo, u User, credential []byte) (Passkey, error)
    BeginStepUp(ctx context.Context, r api.Repo, u User, p Purpose) (json.RawMessage, error)
    FinishStepUp(ctx context.Context, r api.Repo, u User, credential []byte) (token string, err error)
    Redeem(ctx context.Context, r api.Repo, userID int64, token string, p Purpose) (passkeyID string, err error)
    List(ctx context.Context, r api.Repo, userID int64) ([]Passkey, error)
    Remove(ctx context.Context, r api.Repo, u User, passkeyID, token string) error
}

var ErrNotOwner, ErrNeedsPasskey, ErrNoPasskey, ErrCeremony, ErrCounter, ErrToken, ErrTooMany, ErrUnknown,
    ErrInvalid, ErrPolicy error

func New(c Config, s store.Store, owners Owners, issues Issues, now func() time.Time) (Passkeys, error)
func NewOwners(files fileReader) Owners                             // api.Repos.File
func NewIssues(issues issueCreator, triage api.IssueTriage) Issues // api.Issues.CreateIssue
```

- The options a begin returns are the JSON of `{"publicKey": …}` for `navigator.credentials.create` or `.get`; a
  finish takes the browser's `PublicKeyCredential` as JSON. No go-webauthn type crosses the contract.
- `New` fails unless `BaseURL` is an absolute `https` URL (`http` only for `localhost`, `127.0.0.1`, `::1`) and every
  dependency is given.
- `BeginStepUp` refuses with `ErrInvalid`: an action that is not 1 to 32 of `a-z` and `-` starting with a letter; a
  negative request; a digest longer than 128 or with a space or control character; a passkey that is not base64url or
  longer than 1364 characters.

## Integrations

### internal/control (parent)

- **Direction**: `control.New` constructs it with `NewOwners(d.GitHub)` and `NewIssues(d.GitHub, d.GitHub)` and hands
  it to `ui`. The worker role does not build it.
- **Strength**: functional (composition). **LCA / Rank / Distance**: control / 1 / 1. **Balanced?**: yes.

### internal/control/ui (sibling)

- **Direction**: ui depends on passkey (`Passkeys`, `User`, `Purpose`, the sentinels).
- **Strength**: contract. **LCA / Rank / Distance**: control / 1 / 1. **Volatility**: high on ui's side, low here.
- **Shared knowledge**: the contract above. ui supplies the user's id, login and administrator bit from the session;
  passkey never reads a cookie or a request.

### internal/control/answers (sibling, M2 task 19)

- **Direction**: answers will depend on passkey: `Redeem` with the Purpose of the answer it records.
- **Strength**: contract. **LCA / Rank / Distance**: control / 1 / 1.

### internal/store, internal/github/api, internal/wire/policy3, internal/wire/policy

- **Direction**: passkey depends on them: the Store and `Update`; `Repo`, `RepoRef`, `File`, `CreateIssue`,
  `IssueTriage`; `policy3.Decode` for the owners; `policy.Diagnose` to recognise an Archfit 3 policy.
- **Strength**: contract. **LCA / Rank / Distance**: root / 2 / 2. **Balanced?**: yes, contract coupling tolerates the
  distance.

### github.com/go-webauthn/webauthn (third party, M2 D9)

- Only this package imports it (`internal/imports_test.go`). Version and reason: `docs/tech-stack.md`.

## Change Vectors

- An answer kind of `control/answers` is only a new `Action` value; nothing here changes.
- A fourth algorithm, or attestation trust with a metadata service: the `webauthn.Config` in `New`.
- A passkey name chosen by the user: one field in the stored passkey and in `Passkey`.
- Discoverable login without a session: a new begin and finish; the stored handle already allows it.

## Constraints and Invariants

- Fail closed: every error path stores nothing and returns no token. A ceremony is consumed once; a token is redeemed
  once, for one user, one repository and one `Purpose`, within 5 minutes.
- A registration that no issue announced never exists; the issue comes first.
- Tenant identity comes only from the `api.Repo` the caller authorized. No id is read from a request body.
- Logs carry the user id, repository id, installation id, step and outcome. Never a token, challenge, user handle,
  passkey id or public key.
- Inherited: control-plane code. Nothing on the gate path calls it.

Stated limit (design 9.5): a passkey belongs to the installation, but its registration issue goes to the owners of the
repository it was registered in. A user who is an administrator of a repository without a policy and an owner of
another can register in the first; the issue is assigned to that user, and the owners of the second repository are
not told until an answer records the owner and the passkey (M2 task 19). The notice exists so the person whose
browser registered the passkey learns of it, and that person is always assigned. Announcing in every repository the
passkey acts in would put a GitHub read and an issue on the step-up path without blocking anything.

## Test Specification

A software authenticator (`helpers_test.go`) makes the bytes a browser and a security key make, with stdlib ECDSA,
Ed25519 and RSA keys and go-webauthn's own CBOR and COSE encoders; its knobs break one property at a time. The Store is
`store.NewMemory()`, the clock is injected, the ports are fakes.

- `TestRegisterStepUpAndRedeemEachAlgorithm`: ES256, Ed25519, EdDSA and RS256 register, step up and redeem.
- `TestCreationOptionsFollowThePasskeyParameters`, `TestStepUpOptionsAllowOnlyTheUsersPasskeys`.
- `TestSecondRegistrationNeedsAnExistingPasskey`, `TestAtMostMaxPasskeys`.
- `TestUserVerificationIsRequired` (registration and step-up), `TestWrongOriginOrRelyingPartyIsRefused`.
- `TestAChallengeIsUsedOnce`, `TestAChallengeExpiresAfterFiveMinutes`, `TestANewCeremonyReplacesTheOlderOne`,
  `TestACeremonyFinishesInTheRepositoryItBeganIn`.
- `TestTheSignCounterMustGrow`: lower and equal refused and not stored; 0 to 0 allowed.
- `TestATokenIsUsedOnceForItsPurpose`, `TestATokenIsBoundToItsPurposeUserAndTime`, `TestTwoRedeemsAtOnceOneWins`,
  `TestStepUpRefusesAMalformedPurpose`, `TestStepUpNeedsAPasskey`, `TestMalformedCredentialsAreRefused`.
- `TestWhoRegisters`, `TestTheRuleIsCheckedAgainAtFinish`, `TestEveryRegistrationOpensAnIssueForTheUserAndTheOwners`,
  `TestBeforeTheFirstPolicyTheIssueGoesToTheAdministrator`, `TestARegistrationWhoseIssueFailsStoresNothing`.
- `TestUninstallRemovesThePasskeysAndAReinstallRegistersAfresh`, `TestPasskeysBelongToOneInstallation`.
- `TestRemoveNeedsAFreshAssertion`, `TestRemovingTheLastPasskeyOpensAnIssueAndAllowsAFirstRegistration`,
  `TestRemovingTheLastPasskeyWhoseIssueFailsKeepsIt`.
- `TestW3CVectors`: WebAuthn level 3 §16 vectors (`testdata/w3c-vectors.json`) through the App's own ceremonies:
  packed self attestation with user verification registers; its assertion and the `none` registration, both without
  user verification, are refused for that reason; the vectors fail at another relying party.
- `TestRecordsAreReadStrictly`, `TestAChallengeThatCannotBeReadIsReplacedNotTrusted`,
  `TestARecordThatCannotBeReadRefusesEverything`, `TestAssignees`, `TestNewDerivesTheRelyingPartyFromBaseURL`,
  `TestSecretsNeverReachALogLine`.
- `TestOwnersComeFromThePolicyOnTheDefaultBranch`, `TestTheIssueIsOpenedThenAssigned`: the GitHub ports.

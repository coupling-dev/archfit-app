# Operator guide

This guide is for Archfit operators: the people whose GitHub user IDs are in `ARCHFIT_OPERATORS`. Operators admit
installations to the beta and confirm an owner before the owner's first passkey. Owners and developers read the
[user guide](user-guide.md). Deployment and routine work are in [cloud operations](design/cloud/operations.md).

## Installation administration

Operators use `/admin` (**Installations**) to admit or revoke installations, separately from policy approval. **Managed by configuration** marks an account whose admission is set in the App settings by pull request; the page says so once and cannot change it. Revocation help appears only when at least one row can be revoked. Revocation still asks for confirmation and stops checks from the account's next event; existing GitHub checks remain.

Repository names become App links only when the signed-in user's repository list grants access. Installation visibility alone does not grant repository access. An empty or unreadable access list leaves names as text.

## First-passkey confirmation

A GitHub sign-in alone does not authorize the registration of a first passkey. The owner requests confirmation in the
App: from Home's owner-approval step or the owner page (**Request the token**), or from **Passkeys** (**Request
confirmation**). A request from the same session returns the same attempt. The owner sends you the installation and
attempt IDs through the trusted private channel used for beta admission.

The token is single-use and valid for 10 minutes. A pending request can be issued for 30 minutes. Five wrong tokens lock the attempt for 15 minutes. If registration is canceled or fails after confirmation, the owner requests confirmation again and you issue a token for that new attempt; the earlier token may already be consumed and cannot be reissued. If an unused token is lost or exposed, revoke and reissue it. Never put tokens in a URL, GitHub issue, pull request, ticket, shared document or screenshot.

### Runbook

Only configured immutable GitHub operator IDs can use the existing **Admin** page and its confirmation controls. Find the attempt by its installation and attempt IDs. Verify the numeric GitHub user and repository shown against the owner you know through the established private admission channel; do not trust a contact address supplied in the request. If identity or channel is unclear, do not issue.

After verification select **Issue token** and send the one-time value only through that private channel. It appears once; **Hide token** clears it. Lookup never returns it again. If lost or exposed, select **Revoke token** and issue another after any lock expires. Issuing another invalidates the previous token. Operators never register a credential on the owner's behalf. Audit records contain issue, validation, revocation, lockout and registration events without tokens.

### Token algorithm

The public algorithm is standard: exactly 16 random bytes from Go crypto/rand.Read, encoded as 22 unpadded base64url characters; only SHA-256(token) is stored. TLS, authenticated scope binding, constant-time hash comparison and atomic single use provide security, not secrecy of this algorithm.

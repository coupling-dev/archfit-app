# Security

This page sets the security direction of the [cloud architecture](architecture.md): assets, boundaries, identities,
secrets, supply chain, data, and incident response. Application trust rules, for example protected-ref inputs and
fail-closed verdicts, are in [Trust rules](../../architecture.md#trust-rules).

## Assets

Ordered by impact if the asset leaks or fails:

1. **GitHub App private key.** It acts as the App on every installation: checks, comments, contents, and workflow reruns.
2. **Gate integrity.** A wrong `success` lets a blocked change merge. This is the product's core promise.
3. **Google Cloud owner account and deploy identity.** Together they control the runtime, the secrets, and the images.
4. **Webhook secret.** A forged webhook starts work, but inputs still come from GitHub, so it cannot pass a gate.
5. **Operator token.** It grants or clears subscriptions.
6. **OAuth client secret and cookie key.** Session forgery or theft for UI users.
7. **Tenant state.** Entitlements and consent. No source and no reports.
8. **Anthropic key.** Spend on the provider account. The App budget counter caps managed use.

## Boundaries and controls

| Boundary                  | Threat                                       | Control                                                                                  |
| ------------------------- | -------------------------------------------- | ---------------------------------------------------------------------------------------- |
| Internet → public service | Forged webhook or upload                     | Signature check on webhooks. OIDC token, envelope, and revision binding on uploads       |
| Internet → public service | Request flood, cost growth                   | Body caps, an instance cap, budget alerts, and the Cloud Run spend cap                   |
| Internet → worker service | Direct call to internal routes               | The worker is unreachable from the internet. See [Worker protection](#worker-protection) |
| Public service → queue    | A compromised public service queues harm     | The worker verifies the webhook signature again. The public service cannot reach grants  |
| Operator → grants         | Unauthorized grant                           | Operator queue only, operator token, audit log                                           |
| CI → Google Cloud         | Deploy from a fork, branch, or stolen secret | Workload Identity Federation bound to one repository, `main`, and one workflow. No keys  |
| Supply chain → image      | Changed base image, action, or module        | See [Supply chain](#supply-chain)                                                        |
| Tenant → tenant           | One installation reads another's state       | Tenant from a verified identity only. Tenant-scoped keys tested by `store/storetest`     |
| App → logs                | Secrets or content in logs                   | Log IDs, digests, outcomes, and timings only                                             |

## Identities

- Each component has its own service account with only the roles it needs. No service-account keys exist.
- The public service can read and write state, read its secrets, and add tasks to the webhook queue. Nothing else.
- The worker can read and write state and read its secrets. Only the invoker identity of the queues and the Scheduler can call it.
- The deploy identity can push images and deploy revisions. It cannot change IAM. It can read the settings that
  the post-deploy check verifies.
- The owner holds the project owner role, with 2-step verification. The owner applies infrastructure and adds secret values.
- Terraform owns every IAM binding and every service setting. CI changes only images and traffic.
- Firestore has no per-collection IAM. Both runtime identities can read all tenants. Isolation is a code property.

### Worker protection

The worker serves grants, so four independent guards protect it:

1. **Network.** Internal ingress. Only same-project Cloud Tasks and Cloud Scheduler reach it.
2. **Identity.** The invoker check is on. Only the invoker identity can call it.
3. **Routing.** Each queue sets its own target URL and identity (queue-level `uriOverride`, enforced).
   A task creator names only the queue. So the public service cannot aim a task at the grants route.
4. **Grants token.** The grants route also needs the operator token. Only the worker has it.

The deploy identity can change ingress and the invoker check. So after each release, CI checks the worker's exposure
and the queue routing, and the release fails if either differs from the design.

The public service needs no IAM invoker binding for `allUsers`. It turns the invoker check off instead.
That also keeps it compatible with the "domain restricted sharing" policy of an organization.

## Secrets

- Secret Manager holds every secret. Each service mounts only the secrets its role needs, as files.
- Values never enter Terraform state, environment variables, images, logs, or task payloads.
  The exception is the operator token in a grant task, which lives until the task runs.
- Each revision pins secret versions. A new value reaches the App only through a new revision.
- A previous version stays enabled while a revision that can get traffic pins it. That keeps rollbacks working.
  After a compromise, destroy it at once and roll back by image. See [Roll back](operations.md#roll-back).

Rotation rules:

| Secret          | Interval                    | Rule                                                                                                      |
| --------------- | --------------------------- | --------------------------------------------------------------------------------------------------------- |
| App private key | 6 months, and on suspicion  | GitHub allows several keys. Add the new key, deploy it, then delete the old key                           |
| Webhook secret  | 12 months, and on suspicion | Both services accept the old and the new value during the change, because the worker verifies again later |
| OAuth secret    | 12 months, and on suspicion | Deploy the new value and change GitHub close together                                                     |
| Cookie key      | 12 months, and on suspicion | Every user signs in again. A key list can remove that later                                               |
| Operator token  | 90 days                     | Rotate while no grant task is queued                                                                      |
| Anthropic key   | 12 months, and on suspicion | Deploy the new key, then revoke the old one at the provider                                               |

The App private key is the most valuable secret. For the beta it lives in Secret Manager and in App memory.
Before paying customers, move the signing to Cloud KMS: import the key and sign each App JWT with a KMS call.
The key then never enters App memory. The change is local to `internal/github/auth`, which signs JWTs without a library.

## Supply chain

Facts in the repository today: the `Dockerfile` pins base images by tag, CI pins actions by version tag, and CI has no
vulnerability scan. The module has one third-party dependency.

Direction:

- Pin base images by digest and actions by commit SHA. Dependabot keeps both current through reviewed pull requests.
- Run `govulncheck` in CI.
- Deploy images by digest, built once by CI.
- Protect `main`: CI must pass and force-push is blocked.
- Options with a trigger: Artifact Registry scanning when the image gains OS packages, and Binary Authorization when a
  second person can deploy.

## Data protection

- Tenant state and backups stay in the chosen region. Logs hold IDs, digests, and outcomes, never source or report content.
- An uninstall deletes the tenant's state. The sweep catches missed uninstalls. Backups keep deleted data for up to 7 days.
- Google-managed encryption at rest is enough. Customer-managed keys protect no stored content here.
- Reports, checks, and comments stay in GitHub. Assist prompts go to Anthropic only after consent.

## Audit logs

- Admin Activity logs are always on. They record IAM changes, deploys, and queue changes.
- Turn on data-read audit logs for Secret Manager. Each secret read then has a principal.
- Turn on data-write audit logs for Cloud Tasks. They record `RunTask` and `DeleteTask`. Cloud Tasks writes no audit
  log for `CreateTask`, so a grant task has no recorded creator. Only the project owner can add tasks to the operator
  queue, which names the creator while one admin exists.

## Organization

A personal Google account has no Organization resource. Without one, Security Command Center and central organization
policies are not available. The beta runs without one. The compensating controls are the post-release checks, no
service-account keys, Terraform as the only way to change IAM, and the
[project guardrails](operations.md#project-guardrails): disabled APIs, Cloud Run quotas of 0 outside `europe-west1`,
and an alert on every guardrail change. Create an organization before a second admin joins, or before paying
customers.

## Incident response

If you suspect a compromise, contain first. A new secret written into a compromised runtime is a new secret for the attacker.

1. **Stop** intake and processing: webhooks, uploads, queues, and the Scheduler.
2. **Cut** deploy access at the Workload Identity provider.
3. **Scope** the incident from the audit logs: deploys, IAM changes, secret reads, task creation, and the image digest of each serving revision.
4. **Restore** a trusted runtime: IAM from Terraform only, and a known-good image digest.
5. **Rotate** each secret the compromised component can read. Delete the old App key in GitHub. Destroy compromised versions.
6. **Repair** entitlements against the expected grants.
7. **Resume** in reverse order, then run the sweep. It republishes feedback from trusted inputs.
8. **Tell** the affected installation owners what happened and what they must do.

## Known gaps

- Tenant isolation in Firestore depends on code and tests, not IAM.
- The App private key lives in memory until KMS signing is built.
- Without an organization, no central policy prevents service-account key creation.

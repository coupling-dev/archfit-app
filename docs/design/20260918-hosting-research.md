# Closed-beta hosting research

Researched 18 September 2026 from official public sources. No cloud accounts
were inspected, resources provisioned, or deployment experiments run.

Inputs: [product design](20260918-high-level-design.md) and
[implementation-plan T05](../plans/20260918-closed-beta-implementation.md).
Infrastructure budget is USD 100/month before paying customers; managed LLM
has a separate capped budget. Estimates below are workload models, not quotes,
benchmarks, or availability guarantees.

## Selected validation target

**Owner decision, 18 September 2026: validate Cloud Run with PostgreSQL and
managed GCP services first.** Preserve the Go reporter and its independent
deployment boundary. The researched $45–75/month pilot envelope remains an
estimate to verify against the selected region, database tier, and actual load.

Cloud SQL is the proposed PostgreSQL service for this GCP-first experiment;
its tier and availability concession still need confirmation. Keep Cloudflare
and Fly as researched alternatives. The owner selected the validation target,
not a production configuration or authorization to create cloud resources.

### Cloudflare alternative

Cloudflare is now a credible candidate without a Go rewrite: Containers and
Sandboxes became GA in April 2026, and its official starter runs a Go HTTP
server. Workers/Container orchestration still requires integration code. The
Sandbox SDK 1.0 preview should not be confused with the platform's GA status.
[GA status](https://developers.cloudflare.com/changelog/post/2026-04-13-containers-sandbox-ga/),
[Go starter](https://developers.cloudflare.com/containers/get-started/),
[SDK status](https://developers.cloudflare.com/sandbox/)

Retain PostgreSQL initially to preserve the original row-isolation model and
ordinary Go database access. D1 is an alternative architecture decision, not a
transparent PostgreSQL substitution. It is SQLite-based, has a 10-GB per-database
limit and different concurrency/recovery characteristics. Hyperdrive connects
to an external database; it does not include database hosting.
[D1 limits](https://developers.cloudflare.com/d1/platform/limits/),
[storage choices](https://developers.cloudflare.com/workers/platform/storage-options/),
[Hyperdrive pricing](https://developers.cloudflare.com/hyperdrive/platform/pricing/)

## Compared shapes

| Option                      | Proposed components                                                                                                                                             | Pilot planning envelope                                                                              | Main trade-off                                                                                                     |
| --------------------------- | --------------------------------------------------------------------------------------------------------------------------------------------------------------- | ---------------------------------------------------------------------------------------------------- | ------------------------------------------------------------------------------------------------------------------ |
| Cloudflare + PostgreSQL     | Separate Go reporter containers; separate control API/container; thin Workers adapters/static UI; Neon Postgres; R2; Queues/Cron; isolated discovery containers | Approximately $65–90/month including planning reserve under the assumptions below                    | Fits the budget; requires proving container lifecycle, database access and edge/job adapters                       |
| Cloud Run + PostgreSQL      | Separate Go reporter and control services; Cloud SQL or Neon; Cloud Storage; Cloud Tasks/Scheduler; isolated discovery jobs                                     | Approximately $45–75/month with small single-region Cloud SQL and one or two warm reporter instances | Familiar container service; more GCP components; small shared-core database is an explicit availability concession |
| Fly.io + managed PostgreSQL | Go Machines, managed Postgres, object storage and application job handling                                                                                      | Roughly $65–90/month with a small configuration and reserve                                          | Low compute floor; more machine/job/network assembly to validate; managed DB operations need clarification         |

These are not equal-SLA configurations. Region, tax, domain, paid support, log
retention, staging, and actual workload can change the total. No promotional
credits are assumed. Published recurring allowances are identified where used.

The Fly comparison is a cross-check rather than a full deployment design. Its
pricing page lists a shared-CPU/512-MB preset at $3.89/month and managed Postgres
Basic at $38/month plus $0.28/provisioned-GB-month. Three such app Machines and
10 GB of database storage are approximately $52.47 before jobs, object storage,
transfer, and reserve. Region and suitable sizing need verification. Its current
managed-database documentation also lists patching/upgrades among developing
features, which needs clarification before relying on it for this service.
[Compute pricing](https://fly.io/pricing/),
[managed database](https://fly.io/docs/mpg/)

## Workload assumptions

These are an initial costing scenario, not customer entitlements or measured
capacity:

- Five installations and ten connected repositories.
- 1,000 uploaded reports/month, 2 MiB each; 30-day raw-report history.
- Approximately 1 GB PostgreSQL data, 1 GB retained change history, and one
  1-GB snapshot. Change history depends on writes and retention, not data size.
- Twenty managed discovery jobs/month, ten minutes each, about one active vCPU.
- Modest UI/API traffic, retries, object operations, and logs; no source in logs.
- Standard PR analysis remains in customer CI; no hosted execution of arbitrary
  repository install/build scripts.
- No Omni managed discovery: the owner selected a local-only benchmark.

The report payloads alone occupy about 1.95 GiB at steady-state retention.
Baselines, indexes, backups, soft-deleted objects and logs are additional.
Do not extrapolate this model to a large monorepo or unrestricted public signup.

## Cloudflare cost model

Use a 30-day month, two always-running `basic` reporter instances (each
quarter-vCPU, 1 GiB RAM, 4 GB disk), and a separate basic Go control API active
for forty hours/month. Assume each basic instance averages 0.025 active vCPU.
This is a cost assumption to test, not a measured reporter requirement.

Discovery instances use 1 vCPU, **3 GiB** RAM, and 6 GB disk, destroyed after
completion. Cloudflare custom instances require at least 3 GiB per vCPU, so a
1-vCPU/2-GiB example would be invalid.
[Instance limits](https://developers.cloudflare.com/containers/platform/limits/)

| Component                   | Calculation after included allowances                                                                             | Approximate cost |
| --------------------------- | ----------------------------------------------------------------------------------------------------------------- | ---------------- |
| Workers Paid                | Base subscription                                                                                                 | $5.00            |
| Container RAM               | 1,490 GiB-hours minus 25 included; $0.0000025/GiB-second                                                          | $13.19           |
| Container disk              | 5,940 GB-hours minus 200 included; $0.00000007/GB-second                                                          | $1.45            |
| Active CPU                  | 145,200 vCPU-seconds minus 22,500 included; $0.000020/vCPU-second                                                 | $2.45            |
| Container Durable Objects   | Conservative continuously active duration: 683,520 GB-seconds minus 400,000 included, rounded to the next million | $12.50           |
| Light Worker/Queue/R2 usage | Assumed within published included usage                                                                           | $0 additional    |
| Cloudflare subtotal         | Before external DB and operating reserve                                                                          | **About $35**    |

Container and Durable Object charges are separate. Durable Object compute
overages round up to a whole billable unit; do not price them solely by the
fraction above the allowance. Actual hibernation can reduce this estimate.
[Container rates](https://developers.cloudflare.com/containers/platform/pricing/),
[Durable Object rates and rounding](https://developers.cloudflare.com/durable-objects/platform/pricing/)

For Neon Launch, continuously active 0.25-CU compute over 720 hours is
`0.25 × 720 × $0.106 = $19.08`. Add $0.35 for the assumed 1 GB data, $0.20 for
1 GB change history, and $0.09 for a 1-GB snapshot: **about $19.72/month**.
Launch can disable scale-to-zero; its enabled default is five minutes. Keeping
the database active is a deliberate pilot cost to remove one wakeup dependency
from the gate path. Launch PITR supports up to seven days; its default retention
and snapshot policy must be configured and restore-tested.
[Neon plan/pricing source](https://github.com/neondatabase/website/blob/main/content/docs/introduction/plans.md),
[disable scale-to-zero](https://neon.com/faster),
[compute settings](https://neon.com/docs/manage/endpoints/)

Combined modeled cost is approximately **$54/month**. If both reporter CPUs are
continuously saturated at their quarter-vCPU allocation, the same model becomes
approximately **$78/month**, before additional traffic or storage overages.
Reserve the remainder up to the $100 ceiling for backup growth, transfer, image
storage, observability and errors in assumptions. Two warm instances do not
establish an availability SLA or prove that this instance size handles the
maximum report under concurrency.

R2 includes 10 GB-month of Standard storage; the assumed reports fit, but total
retention still needs accounting. Queues messages must contain references and
small metadata: its 128-KB message cap is not enough for a report. Consumer
execution has a 15-minute wall-time limit; long discovery work needs durable
orchestration, not a single assumed-infinite queue handler.
[R2 pricing](https://developers.cloudflare.com/r2/pricing/),
[Workers pricing](https://developers.cloudflare.com/workers/platform/pricing/),
[Queues limits](https://developers.cloudflare.com/queues/platform/limits/)

## Cloud Run cost basis

At the researched US-central1 rates, one request-billed minimum reporter
instance at 1 vCPU/512 MiB costs about **$9.86/month idle** over 730 hours before
allowances. Ten thousand one-second API requests are approximately $0.26 in
active CPU/memory/request charges, excluding startup overhead. Twenty specified
1-vCPU/2-GiB, ten-minute jobs are approximately $0.264 before overhead. Instance
minimums reduce wakeups but do not guarantee response latency. Enqueue durable
work before responding; do not assume request-billed CPU continues afterward.
[Cloud Run prices](https://cloud.google.com/run/pricing),
[minimum instances](https://docs.cloud.google.com/run/docs/configuring/min-instances)

Cloud SQL `db-g1-small` at $0.035/hour is approximately $25.55/month compute over
730 hours; allow about $28–32 including modest SSD and backups. Shared-core
instances have no Cloud SQL SLA. A cheaper Neon database uses the pricing model
above, adding cross-provider networking and operational dependencies. Neither
choice automatically provides the required availability or tenant isolation.
[Cloud SQL prices](https://cloud.google.com/sql/pricing),
[SLA limitation](https://docs.cloud.google.com/sql/docs/postgres/faq)

Allow another $5–15 for objects, transfer, tasks, scheduling, secrets, images,
builds, logs, and model error. Published starting units include $0.020/GiB-month
for regional Standard object storage, $0.40/million billable task operations,
$0.10/scheduler-job-month and $0.06/active secret-version-month, with applicable
allowances. Logs/egress/build traffic can dominate these small workload charges
if left unbounded.
[Storage](https://cloud.google.com/storage/pricing),
[Tasks](https://cloud.google.com/tasks/pricing),
[Scheduler](https://cloud.google.com/scheduler/pricing),
[Secrets](https://cloud.google.com/secret-manager/pricing),
[Logging](https://cloud.google.com/products/observability/pricing)

Cloud Run now also has a **public-preview sandbox capability**; this is distinct
from an ordinary container Job. Documentation describes network-disabled
execution by default but permits reading the parent filesystem. Investigate
this rather than assuming it safely isolates any repository subprocess from
hosted credentials or other tenants' files. A preview feature is not an
established privacy or reliability guarantee for our workload.
[Sandbox documentation](https://docs.cloud.google.com/run/docs/code-execution),
[announcement](https://cloud.google.com/blog/topics/developers-practitioners/google-cloud-run-sandboxes-are-in-public-preview/)

## Cloud Run validation sequence

The owner plans a dedicated GCP beta project; it does not exist yet. Its project
ID and region remain unselected. US-central1 is a pricing reference only. Do not
infer a target from a developer machine's active cloud configuration. Confirm
these inputs, the specific temporary resources, expected cost, and cleanup
before live execution.

The proposed component split is:

| Component                         | Responsibility and boundary                                                                                                        |
| --------------------------------- | ---------------------------------------------------------------------------------------------------------------------------------- |
| Reporter Cloud Run service        | Existing Go binary, ingest, provenance verification when implemented, and check delivery; separate identity from optional services |
| Control API Cloud Run service     | UI/API orchestration and governance; independently deployed and scaled                                                             |
| Cloud SQL PostgreSQL              | Durable tenant-scoped metadata, approvals, jobs and baseline references; least-privilege application roles                         |
| Cloud Storage                     | Private report/baseline objects and verified backup needs; no persistent customer source archives                                  |
| Cloud Tasks and Scheduler         | Bounded delivery, expiry work and reconciliation; references in messages, not full reports                                         |
| Cloud Run Jobs                    | Short-lived managed discovery, subject to a validated source/credential/network boundary                                           |
| Secret Manager and image registry | Scoped secret references and immutable service/worker artifacts                                                                    |

Exercise these incrementally:

1. **Current reporter runtime.** Use the existing binary and controlled inputs:
   health returns 200; a valid-shaped report still returns 501. This deliberately
   preserves the current refusal contract. Measure startup, request limits,
   memory and termination behavior; do not add an acceptance bypass for the
   hosting experiment. These measurements do not establish the performance of
   the future full verifier.
2. **Durable-state and delivery fixtures.** Independently test PostgreSQL role
   isolation, tenant-bound object access, queue retries/deduplication, revocation,
   restore and reconciliation. Use controlled fixture handlers; do not represent
   them as completed App integration tasks.
3. **Managed-discovery boundary.** Test controlled repositories only after the
   isolation design is reviewed. Ordinary Jobs and the preview sandbox have
   different properties. Keep source and credentials out of shared parent
   workspaces and logs; prove the needed outbound/cleanup behavior before
   admitting customer repositories to managed discovery.
4. **Integrated reporter.** After T09–T13 implement verification and gating,
   repeat the cost/latency/failure tests with real eligible report processing.
   Test App/repository authorization and the separate gate rehearsal remain
   prerequisites for enabling any required check.

Record cold/warm timings, maximum-payload memory, retry counts, tenant-isolation
outcomes, restore time, source/log leakage checks, and normalized monthly costs.
Accept or reject the experiment against owner-reviewed targets from T05. A
small shared-core Cloud SQL instance is a pilot trade-off without an SLA, not
an implicit promise of production database availability.

## Security and reliability decisions independent of price

1. **Separate execution identities and lifecycles.** Reporter, control API and
   discovery workers must not share unrestricted credentials. Kill or overload
   discovery without taking down report delivery.
2. **Make metadata isolation explicit.** PostgreSQL RLS is defense in depth,
   with non-owner application roles and no `BYPASSRLS`. Test tenant context in
   pooled connections and tenant-scoped relationships. Table ownership can
   bypass ordinary RLS. Object paths, encryption and worker authorization need
   their own controls. [PostgreSQL RLS](https://www.postgresql.org/docs/17/ddl-rowsecurity.html)
3. **Do not treat a container as a complete discovery boundary.** Pin the trusted
   analyzer; treat repository files as data; avoid target install/build scripts;
   bound memory, CPU, time, output, and retries. Use a fresh environment per job,
   with no reuse of tenant source workspaces or persistence of source archives.
4. **Control outbound traffic and output.** Cloudflare offers internet-disable
   and outbound-handler controls, but DNS remains available and documented
   controls alone do not establish zero exfiltration. Handlers need tenant/job,
   path/method, redirect, payload and budget enforcement; keep provider keys
   outside the sandbox. Disable source-bearing stdout/artifact exports. The
   source-free mode still executes at the customer, regardless of vendor sandbox
   capabilities. [Outbound controls](https://developers.cloudflare.com/containers/guides/outbound-traffic/),
   [sandbox responsibilities](https://developers.cloudflare.com/sandbox/concepts/security/)
5. **Test restarts and cold paths.** Cloudflare does not guarantee an instance's
   lifetime; disk is ephemeral, and stateless autoscaling is not automatic for
   the proposed fixed-instance pattern. Prove timeout/retry/idempotence and
   authoritative state recovery. [Lifecycle limitations](https://developers.cloudflare.com/containers/faq/)
6. **Select data placement deliberately.** Customer jurisdiction, policy/report
   locations, database region, backups and deletion must be specified before
   invited private repositories. A low-cost reference region is not a consent
   or residency decision.

## Budget control and the acceptance experiment

Billing notifications alone are not a hard cap. Google now documents preview
spend caps for selected services, but they do not cap the entire database,
storage and task bill. Neither this estimate nor an account alarm guarantees a
$100 invoice. [Ordinary budgets](https://docs.cloud.google.com/billing/docs/how-to/budgets),
[preview service caps](https://docs.cloud.google.com/billing/docs/how-to/budgets-spend-caps)

Before inviting teams, configure and test:

- A bounded installation/repository allowlist, instance counts, job concurrency,
  per-job time limits, retry limits, payload sizes and retention.
- Managed-discovery admission and a separate managed-LLM allowance; pause optional
  work before it consumes reporter operating reserve.
- Authenticated ingestion and inexpensive rejection before costly processing;
  per-tenant quotas and backpressure, without turning missing evidence into a
  successful required check.
- Per-component usage accounting, cleanup of failed/stopped jobs, and daily
  reconciliation of expected versus invoiced resources.
- A cost projection that includes backups, stale artifacts, logs, test
  environments and free public-repository activity.

Run the bounded experiment on the selected Cloud Run candidate after project,
region, resources, and live execution have been authorized:

1. Deploy the unchanged Go reporter with controlled fixtures: health must return
   200 and valid-shaped report ingestion must still return 501. Do not make a
   real gate depend on this initial runtime experiment.
2. Exercise cold/warm startup, maximum report size, concurrent delivery,
   duplicate jobs, forced restarts, failed database connections and recovery.
3. Execute source-only discovery on controlled fixtures with poisoned commands,
   seeded source/log content, outbound attempts and forced cancellation. Verify
   the intended data boundary and cleanup rather than only successful completion.
4. Restore tenant-scoped metadata and blobs; test unauthorized access, revoked
   credentials and pooled-connection isolation.
5. Compare measured resource consumption, feedback latency, recovery and the
   projected bill against owner-selected acceptance targets. If isolation or
   runtime fit fails, revisit the design and compare the researched alternatives
   with the owner before changing scope.

Research task T05 is **partially complete**: documented options and cost models
exist, and the owner selected Cloud Run/PostgreSQL as the primary validation
target. Creation of the planned beta project, region, database tier, acceptance
targets, account-specific availability, real isolation tests and billing
measurements remain open. The
candidate selection is not approval to deploy.

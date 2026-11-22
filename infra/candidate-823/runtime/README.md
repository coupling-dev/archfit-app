# Candidate 8.23 runtime

This root is only for the candidate in `coupling-dev-510710/europe-west1`.
It does not create a backend bucket or DNS records. Use a dedicated candidate
state bucket unless access to a shared bucket can be proven scoped to candidate
objects and locks. Supply the bucket explicitly when initializing the runtime
backend and as `data_state_bucket`; runtime state uses
`candidate-823/runtime`, and data state uses only `candidate-823/data`.
Verify both prefixes and access before any live initialization. Never initialize
a production root or read production state.

Offline checks:

```sh
tofu -chdir=infra/candidate-823/runtime init -backend=false -input=false -lockfile=readonly
tofu -chdir=infra/candidate-823/runtime validate
tofu -chdir=infra/candidate-823/runtime test
```

Mock tests do not prove live IAM, routing, readiness or state isolation. CI does
not yet run this root; integration is a separate change. The owner must review
the exact candidate-only saved plan and resource addresses, identity, project,
quota, effective IAM, registry digest, numeric secret versions and state access
before a separately approved apply. DNS for candidate-823.coupling.dev is an
owner prerequisite; Cloud Run domain mapping is Preview.

Queues and Scheduler start paused. Before any separately reviewed activation,
capture both Cloud Run revision names and traffic allocations for rollback,
probe public readiness at `https://candidate-823.coupling.dev/ready`, verify the internal worker audience and IAM, and test
only the candidate database. Then review the paused-to-running plan. On failure,
pause candidate dispatch and restore only the captured candidate revisions;
never redirect to production. Deletion and teardown require separate approval.
Outputs for owner inspection are `base_url`, `worker_url`,
`service_urls`, `queues`, `sweep_job` and `dns_records`.

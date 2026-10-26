This check does not block merging: the gate is advisory for this repository.

- **Next:** Fix the rule violations, then run archfit check again.
- Reason: `blocking_findings` (architecture)

### Blocking findings

- `no-module-cycles`: `pkg_chaos` → `pkg_container` at `pkg/chaos/cmd/builder.go:14`, finding `0d5aae18`. Goal: Break the dependency cycle among declared modules pkg_chaos, pkg_container, pkg_util by removing one direction of it: drop the pkg_chaos -&gt; pkg_container dependency, or the dependency path from pkg_container back to pkg_chaos. Routing it through another module keeps the cycle.
- `no-module-cycles`: `pkg_chaos` → `pkg_util` at `pkg/chaos/iptables/parse.go:12`, finding `17a7a962`. Goal: Break the dependency cycle among declared modules pkg_chaos, pkg_container, pkg_util by removing one direction of it: drop the pkg_chaos -&gt; pkg_util dependency, or the dependency path from pkg_util back to pkg_chaos. Routing it through another module keeps the cycle.
- `no-module-cycles`: `pkg_container` → `pkg_chaos` at `pkg/container/appfilter.go:4`, finding `e128caa5`. Goal: Break the dependency cycle among declared modules pkg_chaos, pkg_container, pkg_util by removing one direction of it: drop the pkg_container -&gt; pkg_chaos dependency, or the dependency path from pkg_chaos back to pkg_container. Routing it through another module keeps the cycle.
- `no-module-cycles`: `pkg_util` → `pkg_chaos` at `pkg/util/flagports.go:4`, finding `dd9d76ed`. Goal: Break the dependency cycle among declared modules pkg_chaos, pkg_container, pkg_util by removing one direction of it: drop the pkg_util -&gt; pkg_chaos dependency, or the dependency path from pkg_chaos back to pkg_util. Routing it through another module keeps the cycle.

<details>
<summary>Evidence and inputs</summary>

### Inputs and identity

- Policy digest: `1111111111111111111111111111111111111111111111111111111111111111`
- Baseline digest: `2222222222222222222222222222222222222222222222222222222222222222` (stated by the producer)
- Labels digest: `3333333333333333333333333333333333333333333333333333333333333333` (stated by the producer)
- Measurement profile: version `archfit.measurement.v2`, settings hash `eb46ade8e8c37ee1f509da1f83bddfac13e4d162602d9065ccc964e165810ef6`
- Producer identity, claimed: engine v3.0.0, image sha256:9f86d081884c7d659a2feaa0c55ad015a3bf4f1b2b0b822cd15d6c15b0f00a08, linux/amd64

Reports are customer-attested. Upload authentication does not independently verify report content.

</details>

<!-- archfit:receipt {"run_id":7,"run_attempt":1,"report_digest":"4444444444444444444444444444444444444444444444444444444444444444","policy_digest":"1111111111111111111111111111111111111111111111111111111111111111","baseline_digest":"2222222222222222222222222222222222222222222222222222222222222222","labels_digest":"3333333333333333333333333333333333333333333333333333333333333333","earliest_waiver_expiry":"0001-01-01T00:00:00Z","conclusion":"failure","reason":"blocking_findings"} -->
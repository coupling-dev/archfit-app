<!-- archfit:comment -->
<!-- archfit:head bbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb run 7.1 -->
## Archfit: Architecture violations found · advisory

Assessment: 4 active findings, 0 advisories, 6 accepted findings. Checked with engine `v3.0.0` and policy digest `111111111111`.

PR impact: 6 introduced findings, 6 pre-existing, 0 unknown. Finding origin does not establish that each module pair or listed source site was added.

Cause: Architecture rule violations.

- **active finding** `pkg/chaos/cmd/builder.go:14` `no-module-cycles`: `pkg_chaos` → `pkg_container`. Why: Module pkg_chaos depends on pkg_container, and the two are in a dependency cycle among 3 declared modules. Fix: Remove one direction of the module cycle; routing the dependency through another module keeps the cycle
- **active finding** `pkg/chaos/iptables/parse.go:12` `no-module-cycles`: `pkg_chaos` → `pkg_util`. Why: Module pkg_chaos depends on pkg_util, and the two are in a dependency cycle among 3 declared modules. Fix: Remove one direction of the module cycle; routing the dependency through another module keeps the cycle
- **active finding** `pkg/container/appfilter.go:4` `no-module-cycles`: `pkg_container` → `pkg_chaos`. Why: Module pkg_container depends on pkg_chaos, and the two are in a dependency cycle among 3 declared modules. Fix: Remove one direction of the module cycle; routing the dependency through another module keeps the cycle
- 1 more active findings/advisories: read the exact archfit/state check.

**Next:** PR author/coding agent: review the reported constraints. Use only the supplied validation commands below; no repair direction is selected here.

HEAD `bbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb` · [Run 7 · attempt 1](https://github.com/acme/shop/actions/runs/7/attempts/1). Detailed findings/evidence: `archfit/state` for this HEAD and run.

<details>
<summary>Evidence and inputs</summary>

### Evidence gaps

Declared rule checks: 1/1 evaluated. Implicit accepted-reference metric checks: inventory not reported. Accepted reference: comparable.

Reports are customer-attested. Upload authentication does not independently verify report content.

</details>

<details>
<summary>For coding agents</summary>

### Agent tasks

<!-- archfit:agent-tasks -->
```json
[
{"finding_id":"0d5aae18e2b632a4796190153b0c89b3","rule_id":"no-module-cycles","repair_kind":"code_change","goal":"Break the dependency cycle among declared modules pkg_chaos, pkg_container, pkg_util by removing one direction of it: drop the pkg_chaos -\u003e pkg_container dependency, or the dependency path from pkg_container back to pkg_chaos. Routing it through another module keeps the cycle.","constraints":["Remove one direction of the module cycle; routing the dependency through another module keeps the cycle"],"files":["pkg/chaos/cmd/builder.go","pkg/chaos/command.go","pkg/chaos/iptables/cmd/loss.go","pkg/chaos/iptables/iptables.go","pkg/chaos/iptables/loss.go","pkg/chaos/iptables/parse.go","pkg/chaos/lifecycle/cmd/exec.go","pkg/chaos/lifecycle/cmd/kill.go","pkg/chaos/lifecycle/cmd/pause.go","pkg/chaos/lifecycle/cmd/remove.go","pkg/chaos/lifecycle/cmd/restart.go","pkg/chaos/lifecycle/cmd/stop.go","pkg/chaos/lifecycle/exec.go","pkg/chaos/lifecycle/kill.go","pkg/chaos/lifecycle/pause.go","pkg/chaos/lifecycle/remove.go","pkg/chaos/lifecycle/restart.go","pkg/chaos/lifecycle/stop.go","pkg/chaos/netem/cmd/combine.go","pkg/chaos/netem/cmd/corrupt.go","pkg/chaos/netem/cmd/delay.go","pkg/chaos/netem/cmd/duplicate.go","pkg/chaos/netem/cmd/loss.go","pkg/chaos/netem/cmd/loss_ge.go","pkg/chaos/netem/cmd/loss_state.go","pkg/chaos/netem/cmd/rate.go","pkg/chaos/netem/combine.go","pkg/chaos/netem/corrupt.go","pkg/chaos/netem/delay.go","pkg/chaos/netem/duplicate.go","pkg/chaos/netem/loss.go","pkg/chaos/netem/loss_ge.go","pkg/chaos/netem/loss_state.go","pkg/chaos/netem/netem.go","pkg/chaos/netem/parse.go","pkg/chaos/netem/rate.go","pkg/chaos/netem/target.go","pkg/chaos/runner.go","pkg/chaos/stress/cmd/stress.go","pkg/chaos/stress/stress.go"],"validation":["archfit check -c /bundle/.archfit.yaml --root /src --base 3f53e38675a8ab6c453634fb51ebe58b9e5cde19"],"origin":"introduced"},
{"finding_id":"17a7a9622f148c7b21e252903ad0d0f5","rule_id":"no-module-cycles","repair_kind":"code_change","goal":"Break the dependency cycle among declared modules pkg_chaos, pkg_container, pkg_util by removing one direction of it: drop the pkg_chaos -\u003e pkg_util dependency, or the dependency path from pkg_util back to pkg_chaos. Routing it through another module keeps the cycle.","constraints":["Remove one direction of the module cycle; routing the dependency through another module keeps the cycle"],"files":["pkg/chaos/iptables/parse.go","pkg/chaos/netem/parse.go"],"validation":["archfit check -c /bundle/.archfit.yaml --root /src --base 3f53e38675a8ab6c453634fb51ebe58b9e5cde19"],"origin":"introduced"},
{"finding_id":"dd9d76ed1e290e7a210957c96c978a8a","rule_id":"no-module-cycles","repair_kind":"code_change","goal":"Break the dependency cycle among declared modules pkg_chaos, pkg_container, pkg_util by removing one direction of it: drop the pkg_util -\u003e pkg_chaos dependency, or the dependency path from pkg_chaos back to pkg_util. Routing it through another module keeps the cycle.","constraints":["Remove one direction of the module cycle; routing the dependency through another module keeps the cycle"],"files":["pkg/util/flagports.go"],"validation":["archfit check -c /bundle/.archfit.yaml --root /src --base 3f53e38675a8ab6c453634fb51ebe58b9e5cde19"],"origin":"introduced"},
{"finding_id":"e128caa5c2e58c5eba7cc3bc84157923","rule_id":"no-module-cycles","repair_kind":"code_change","goal":"Break the dependency cycle among declared modules pkg_chaos, pkg_container, pkg_util by removing one direction of it: drop the pkg_container -\u003e pkg_chaos dependency, or the dependency path from pkg_chaos back to pkg_container. Routing it through another module keeps the cycle.","constraints":["Remove one direction of the module cycle; routing the dependency through another module keeps the cycle"],"files":["pkg/container/appfilter.go"],"validation":["archfit check -c /bundle/.archfit.yaml --root /src --base 3f53e38675a8ab6c453634fb51ebe58b9e5cde19"],"origin":"introduced"}
]
```
<!-- /archfit:agent-tasks -->

</details>
<!-- archfit:comment -->
<!-- archfit:head bbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb run 7.1 -->
## Archfit: Architecture gate failing

Assessment: 1 active finding, 0 advisories, 0 accepted findings. Checked with engine `v3.0.0` and policy digest `111111111111`.

PR impact: unknown — no comparable Git-base finding-origin evidence.

Cause: Blocking findings.

- **active finding** `pkg/a/a.go:3` `no_direct_b_dependency`: `a` → `b`. Why: Import from pkg/a/** to pkg/b is explicitly forbidden. Fix: Remove the dependency or move the code

**Next:** PR author/coding agent: review the reported constraints. Use only the supplied validation commands below; no repair direction is selected here. Architecture owner: resolve the supplied owner-decision task before selecting a policy change.

HEAD `bbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb` · [Run 7 · attempt 1](https://github.com/acme/shop/actions/runs/7/attempts/1). Detailed findings/evidence: `archfit/state` for this HEAD and run.

<details>
<summary>Evidence and inputs</summary>

### Evidence gaps

Declared rule checks: 1/1 evaluated. Implicit accepted-reference metric checks: inventory not reported. Accepted reference: non comparable.

Reports are customer-attested. Upload authentication does not independently verify report content.

</details>

<details>
<summary>For coding agents</summary>

### Agent tasks

<!-- archfit:agent-tasks -->
```json
[
{"finding_id":"ba3803eca947bf3c1b7efa8f37854d5e","rule_id":"no_direct_b_dependency","repair_kind":"code_change","goal":"Remove the forbidden dependency from pkg/a/a.go on pkg/b; move shared behavior to a location permitted by the existing dependency rules.","constraints":["Remove the dependency or move the code","public surface of module \"b\": [pkg/b/b.go]"],"files":["pkg/a/a.go","pkg/b"],"validation":["archfit check -c .archfit.yaml"],"origin":"introduced"},
{"finding_id":"coupling-gate","rule_id":"bc/coupling_gate","repair_kind":"needs_owner_decision","goal":"Decide whether the coupling between modules a and b is intended; declare volatility or restructure.","constraints":["Do not baseline the seam without an owner decision"],"files":["pkg/a/**","pkg/b"],"validation":["archfit check -c .archfit.yaml --base main --lang go --require-tools"],"origin":"pre_existing"}
]
```
<!-- /archfit:agent-tasks -->

</details>
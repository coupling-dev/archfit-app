<!-- archfit:comment -->
<!-- archfit:head bbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb run 7.1 -->
## Archfit: Architecture gate failing

Assessment: 0 active findings, 1 advisory, 0 accepted findings. Checked with engine `v3.0.0` and policy digest `111111111111`.

PR impact: unknown — no comparable Git-base finding-origin evidence.

Cause: Blocking findings.

**Next:** PR author/coding agent: review the reported constraints. Use only the supplied validation commands below; no repair direction is selected here.

- **advisory** `pkg/a/a.go:3` `bc/imbalanced_coupling`: `a` → `b`

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

No automated source-repair task supplied.

<!-- archfit:agent-tasks -->
```json
[]
```
<!-- /archfit:agent-tasks -->

</details>
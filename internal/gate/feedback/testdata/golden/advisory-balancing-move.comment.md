<!-- archfit:comment -->
<!-- archfit:head bbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb run 7.1 -->
## Archfit: Architecture needs attention · advisory

Assessment: 0 active findings, 2 advisories, 0 accepted findings.

PR impact: unknown — no comparable Git-base finding-origin evidence.

**Next:** Advisories never block. Review the listed dimensions and diagnostics on the archfit/state check.

- **advisory** `bc/imbalanced_coupling`: `orders` → `pricing` at `pkg/orders/price.go:3`
- **advisory** `bc/imbalanced_coupling`: `billing` → `pricing` at `pkg/billing/rate.go:3`

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

No automated source-repair task supplied.

<!-- archfit:agent-tasks -->
```json
[]
```
<!-- /archfit:agent-tasks -->

</details>
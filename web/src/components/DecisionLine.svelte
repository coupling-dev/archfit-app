<script lang="ts">
  import type { Decision } from '../lib/api';
  import { decisionWord } from '../lib/seam';

  // The engine's decision under the pull request's headline: its hard gates, the required rules it could not evaluate
  // and what it says about attribution. Nothing when the gates passed and it notes nothing.
  let { decision }: { decision?: Decision } = $props();

  const gates = $derived(decision ? decisionWord(decision) : '');
</script>

{#if decision && (gates || decision.unevaluated.length || decision.origin_reasons.length)}
  <div class="stack-sm small" id="decision">
    {#if gates}<p><strong>Engine decision:</strong> {gates}</p>{/if}
    {#if decision.unevaluated.length}
      <p>Required rules not evaluated:</p>
      <ul>
        {#each decision.unevaluated as u, i (i)}<li><code>{u.rule_id}</code>: {u.reason}</li>{/each}
      </ul>
    {/if}
    {#if decision.origin_reasons.length}
      <p class="muted">Attribution notes: {decision.origin_reasons.join('; ')}</p>
    {/if}
  </div>
{/if}

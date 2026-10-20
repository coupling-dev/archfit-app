<script lang="ts">
  import type { Decision } from '../lib/api';

  // The engine's decision under the pull request's headline, always, in the comment's words: its verdict, hard gates,
  // active blockers and required rules, then the rules it could not evaluate and what it says about attribution.
  let { decision }: { decision?: Decision } = $props();
</script>

{#if decision}
  <div class="stack-sm small" id="decision">
    <p><strong>Engine verdict {decision.verdict}:</strong> {decision.detail}</p>
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

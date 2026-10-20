<script lang="ts">
  // The engine's reason and repairs: Why, then Fix, then the allowed alternatives. The App writes no repair of its own.
  let { why, fixes, alternatives }: { why: string; fixes: string[]; alternatives: string[] } = $props();
</script>

{#if why}<p><strong>Why:</strong> {why}</p>{/if}
{#each fixes as fix, i (i)}<p><strong>Fix:</strong> {fix}</p>{/each}
{#if alternatives.length}
  <p>
    <strong>Allowed alternatives:</strong>
    {#each alternatives as a, i (i)}{#if i}, {/if}<code>{a}</code>{/each}
  </p>
{/if}

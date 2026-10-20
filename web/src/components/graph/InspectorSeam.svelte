<script lang="ts">
  import type { GLink } from '../../lib/graph';
  import { crossesWord, flagWord, moveWord, quadrantWord } from '../../lib/seam';

  // The engine's balance reading of one dependency: its suggested move, quadrant, boundary and flag. Nothing when the
  // engine gives none.
  let { link }: { link: GLink } = $props();

  const move = $derived(moveWord[link.hypothesis] ?? '');
  const shape = $derived(quadrantWord[link.quadrant] ?? '');
  const crosses = $derived(crossesWord(link));
  const flag = $derived(flagWord(link));
</script>

{#if flag}<p class="small"><strong>Flagged:</strong> {flag}.</p>{/if}
{#if move || shape || crosses}
  <dl class="g-kv">
    {#if move}<dt>Suggested move</dt>
      <dd>{move} <span class="muted small">A hypothesis: Archfit measures, you decide.</span></dd>{/if}
    {#if shape}<dt>Quadrant</dt>
      <dd>{shape}</dd>{/if}
    {#if crosses}<dt>Crosses</dt>
      <dd>{crosses}</dd>{/if}
  </dl>
{/if}

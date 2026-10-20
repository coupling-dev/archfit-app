<script lang="ts">
  import type { GLink } from '../../lib/graph';
  import { flaggedSummary } from '../../lib/seam';

  // The dependencies the engine flags, counted once above the graph; each one's reason is in the inspector.
  let { links }: { links: GLink[] } = $props();

  const summary = $derived(flaggedSummary(links));
</script>

{#if summary}<p class="small muted" id="flagged">{summary}</p>{/if}

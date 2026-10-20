<script lang="ts">
  // total is the engine's full count: it lists at most 50 locations of a finding.
  let { locations, total = 0, shown = 3 }: { locations: string[]; total?: number; shown?: number } = $props();
  const count = $derived(Math.max(total, locations.length));
</script>

{#if locations.length === 0}
  <span class="muted small">no location reported</span>
{:else}
  <span class="locations">
    {#each locations.slice(0, shown) as l, i (i)}<code>{l}</code>{/each}
  </span>
  {#if count > shown}
    <details class="disclosure locations-all">
      <summary>All {count} locations{count > locations.length ? ` (the first ${locations.length} listed)` : ''}</summary>
      <div class="details-body locations">
        {#each locations as l, i (i)}<code>{l}</code>{/each}
      </div>
    </details>
  {/if}
{/if}

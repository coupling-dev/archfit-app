<script lang="ts">
  let { locations, shown = 3 }: { locations: string[]; shown?: number } = $props();
</script>

{#if locations.length === 0}
  <span class="muted small">no location reported</span>
{:else}
  <span class="locations">
    {#each locations.slice(0, shown) as l, i (i)}<code>{l}</code>{/each}
  </span>
  {#if locations.length > shown}
    <details class="disclosure locations-all">
      <summary>All {locations.length} locations</summary>
      <div class="details-body locations">
        {#each locations as l, i (i)}<code>{l}</code>{/each}
      </div>
    </details>
  {/if}
{/if}

<script lang="ts">
  import type { Snippet } from 'svelte';
  import Icon from './Icon.svelte';

  // The one disclosure: a chevron that turns when open, the summary in the default colour, one padding.
  let { summary, open = $bindable(false), children }: { summary: string; open?: boolean; children: Snippet } = $props();
</script>

<details class="disclosure-ui" bind:open>
  <summary><span class="chevron" aria-hidden="true"><Icon name="chevron" size={12} /></span>{summary}</summary>
  <div class="body">{@render children()}</div>
</details>

<style>
  .disclosure-ui > summary {
    display: flex; align-items: center; gap: 8px; padding: 8px 0; font-weight: 500; cursor: pointer; list-style: none;
  }
  .disclosure-ui > summary::-webkit-details-marker { display: none; }
  .chevron { display: inline-flex; transition: transform 0.12s; }
  .disclosure-ui[open] > summary .chevron { transform: rotate(90deg); }
  .body { padding: 0 0 8px 20px; }
</style>

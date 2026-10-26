<script lang="ts">
  // Finding identity origin only; it does not date each dependency or source site.
  let { origin }: { origin: string } = $props();
</script>

{#if origin === 'added'}
  <span class="label solid danger" title="Finding identity is new against a comparable Git base; pair and source-site novelty are not established.">introduced finding</span>
{:else if origin === 'existing'}
  <span class="label">pre-existing finding</span>
{/if}

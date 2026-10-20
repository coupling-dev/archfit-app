<script lang="ts">
  // What the report shows about a dependency on the pull request base, in the comment's words.
  let { origin }: { origin: string } = $props();
</script>

{#if origin === 'added'}
  <span class="label solid danger">added by this PR</span>
{:else if origin === 'existing'}
  <span class="label">existed before</span>
{/if}

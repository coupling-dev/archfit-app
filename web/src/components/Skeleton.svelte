<script lang="ts">
  // The shape of a section still loading: grey lines where its text will be, so the page has its frame at once.
  let { rows = 3, box = true }: { rows?: number; box?: boolean } = $props();
</script>

<div class="skeleton" class:box role="status" aria-label="Loading">
  {#each { length: rows }, i (i)}<span class="skeleton-line"></span>{/each}
</div>

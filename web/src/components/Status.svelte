<script lang="ts">
  import type { Snippet } from 'svelte';

  let { children, tone = 'neutral', state }: {
    children: Snippet;
    tone?: 'neutral' | 'success' | 'danger' | 'attention';
    state?: string;
  } = $props();
</script>

<span class="status {tone}" data-status={state}><span>{@render children()}</span></span>

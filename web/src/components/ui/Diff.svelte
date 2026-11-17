<script lang="ts">
  import { diffLines } from '../../lib/diff';

  // A unified diff with added and removed lines coloured. Keyboard users scroll the unwrapped diff, so the region is
  // focusable; the legend says what the colours mean, so colour is never the only signal.
  let { text, label, legend = true }: { text: string; label: string; legend?: boolean } = $props();
  const lines = $derived(diffLines(text));
</script>

{#if legend}<p class="small legend"><span class="add">+ Added</span> <span class="del">- Removed</span></p>{/if}
<!-- svelte-ignore a11y_no_noninteractive_tabindex (Keyboard users must be able to scroll the unwrapped diff.) -->
<pre class="diff" role="region" tabindex="0" aria-label={label}><code>{#each lines as line, i (i)}<span class="line {line.kind}">{line.text}</span>{/each}</code></pre>

<style>
  pre { max-width: 100%; overflow: auto; white-space: pre; }
  .diff code { display: block; width: max-content; min-width: 100%; }
  .line { display: block; }
  .legend { display: flex; gap: 16px; margin: 0; }
  .legend span { padding: 0 4px; }
  .add { background: var(--success-subtle); }
  .del { background: var(--danger-subtle); }
  .hunk, .header { color: var(--fg-muted); }
</style>

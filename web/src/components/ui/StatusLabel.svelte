<script lang="ts">
  import type { Snippet } from 'svelte';
  import { statusStyle } from '../../lib/status';
  import Icon from './Icon.svelte';

  // The one status element. The tone and icon come from the table in lib/status.ts, looked up by state (or by the
  // label when no state is given). The text keeps its case and copy; children render it when it holds links.
  let { label = '', state = label, children }: { label?: string; state?: string; children?: Snippet } = $props();
  const style = $derived(statusStyle(state));
</script>

<span class="status status-label {style.tone}"><Icon name={style.icon} size={12} />{#if children}{@render children()}{:else}{label}{/if}</span>

<style>
  .status-label {
    display: inline-flex; align-items: center; gap: 4px; max-width: 100%; min-height: 20px; padding: 0 7px 0 5px;
    font: 500 12px/18px var(--font); white-space: normal; overflow-wrap: anywhere;
    color: var(--fg-muted); background: var(--neutral-subtle); border: 1px solid var(--border); border-radius: 999px;
  }
  .status-label.success { color: var(--success); background: var(--success-subtle); border-color: var(--success-border); }
  .status-label.attention { color: var(--attention); background: var(--attention-subtle); border-color: var(--attention-border); }
  .status-label.danger { color: var(--danger); background: var(--danger-subtle); border-color: var(--danger-border); }
  .status-label.accent { color: var(--accent); background: var(--accent-subtle); border-color: var(--accent-border); }
  .status-label.done { color: var(--done); background: var(--done-subtle); border-color: var(--done-border); }
</style>

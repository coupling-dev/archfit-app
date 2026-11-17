<script lang="ts">
  import { statusStyle } from '../../lib/status';
  import Icon from './Icon.svelte';

  // The one status element. The tone and icon come from the table in lib/status.ts; the text keeps its case.
  let { label }: { label: string } = $props();
  const style = $derived(statusStyle(label));
</script>

<span class="status-label {style.tone}"><Icon name={style.icon} size={12} />{label}</span>

<style>
  .status-label {
    display: inline-flex; align-items: center; gap: 4px; width: fit-content; max-width: 100%; min-height: 20px; padding: 0 7px 0 5px;
    font: 500 12px/18px var(--font); white-space: normal; overflow-wrap: anywhere;
    color: var(--fg-muted); background: var(--neutral-subtle); border: 1px solid var(--border); border-radius: 999px;
  }
  .status-label.success { color: var(--success); background: var(--success-subtle); border-color: var(--success-border); }
  .status-label.attention { color: var(--attention); background: var(--attention-subtle); border-color: var(--attention-border); }
  .status-label.danger { color: var(--danger); background: var(--danger-subtle); border-color: var(--danger-border); }
  .status-label.accent { color: var(--accent); background: var(--accent-subtle); border-color: var(--accent-border); }
  .status-label.done { color: var(--done); background: var(--done-subtle); border-color: var(--done-border); }
</style>

<script lang="ts">
  import type { Snippet } from 'svelte';
  import Icon from './Icon.svelte';
  import type { IconName } from './icon-names';

  type Tone = 'info' | 'success' | 'attention' | 'danger';
  const icons: Record<Tone, IconName> = { info: 'info', success: 'check-circle', attention: 'alert', danger: 'x-circle' };

  // A message that stays on the page. The icon repeats the tone, so the tone is never the only signal.
  let { tone = 'info', title = '', role, children }: {
    tone?: Tone;
    title?: string;
    role?: 'status' | 'alert';
    children: Snippet;
  } = $props();
</script>

<div class="banner {tone}" {role}>
  <span class="mark"><Icon name={icons[tone]} size={16} /></span>
  <div class="text">
    {#if title}<p class="title">{title}</p>{/if}
    {@render children()}
  </div>
</div>

<style>
  .banner {
    display: flex; gap: 8px; align-items: flex-start; padding: 12px 16px; border: 1px solid; border-radius: var(--radius);
    color: var(--fg); background: var(--accent-subtle); border-color: var(--accent-border);
  }
  .banner.success { background: var(--success-subtle); border-color: var(--success-border); }
  .banner.attention { background: var(--attention-subtle); border-color: var(--attention-border); }
  .banner.danger { background: var(--danger-subtle); border-color: var(--danger-border); }
  .mark { flex: none; display: inline-flex; padding-top: 2px; color: var(--accent); }
  .banner.success .mark { color: var(--success); }
  .banner.attention .mark { color: var(--attention); }
  .banner.danger .mark { color: var(--danger); }
  .text { display: flex; flex-direction: column; gap: 4px; min-width: 0; overflow-wrap: anywhere; }
  .title { font-weight: 600; }
  .text :global(a) { text-decoration: underline; text-underline-offset: 2px; }
</style>

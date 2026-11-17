<script lang="ts">
  import type { IconName } from './icon-names';

  // Decorative: the words beside an icon carry the meaning, so the icon is hidden from assistive technology.
  let { name, size = 16 }: { name: IconName; size?: number } = $props();
</script>

<svg class="icon" width={size} height={size} viewBox="0 0 16 16" aria-hidden="true" focusable="false">
  {#if name === 'check-circle'}
    <circle cx="8" cy="8" r="6.25" fill="none" stroke="currentColor" stroke-width="1.5" />
    <path d="M5.25 8.25l1.75 1.75 3.75-3.75" fill="none" stroke="currentColor" stroke-width="1.5" stroke-linecap="round" stroke-linejoin="round" />
  {:else if name === 'x-circle'}
    <circle cx="8" cy="8" r="6.25" fill="none" stroke="currentColor" stroke-width="1.5" />
    <path d="M6 6l4 4M10 6l-4 4" fill="none" stroke="currentColor" stroke-width="1.5" stroke-linecap="round" />
  {:else if name === 'clock'}
    <circle cx="8" cy="8" r="6.25" fill="none" stroke="currentColor" stroke-width="1.5" />
    <path d="M8 4.75V8l2 1.25" fill="none" stroke="currentColor" stroke-width="1.5" stroke-linecap="round" stroke-linejoin="round" />
  {:else if name === 'alert'}
    <path d="M8 2.25l6 10.75H2z" fill="none" stroke="currentColor" stroke-width="1.5" stroke-linejoin="round" />
    <path d="M8 6.5v3" stroke="currentColor" stroke-width="1.5" stroke-linecap="round" />
    <path d="M8 11.25v.01" stroke="currentColor" stroke-width="1.75" stroke-linecap="round" />
  {:else if name === 'question'}
    <circle cx="8" cy="8" r="6.25" fill="none" stroke="currentColor" stroke-width="1.5" />
    <path d="M6.25 6.25a1.75 1.75 0 1 1 2.5 1.6c-.5.25-.75.6-.75 1.15" fill="none" stroke="currentColor" stroke-width="1.5" stroke-linecap="round" />
    <path d="M8 11.5v.01" stroke="currentColor" stroke-width="1.75" stroke-linecap="round" />
  {:else if name === 'dot'}
    <circle cx="8" cy="8" r="3" fill="currentColor" />
  {:else if name === 'chevron'}
    <path d="M6 4l4 4-4 4" fill="none" stroke="currentColor" stroke-width="1.5" stroke-linecap="round" stroke-linejoin="round" />
  {:else if name === 'link-external'}
    <path d="M9.5 3.25h3.25v3.25M12.75 3.25L7.5 8.5M11 9.25v3.5H3.25V4.75H6.5" fill="none" stroke="currentColor" stroke-width="1.5" stroke-linecap="round" stroke-linejoin="round" />
  {:else if name === 'info'}
    <circle cx="8" cy="8" r="6.25" fill="none" stroke="currentColor" stroke-width="1.5" />
    <path d="M8 7.25v4" stroke="currentColor" stroke-width="1.5" stroke-linecap="round" />
    <path d="M8 4.75v.01" stroke="currentColor" stroke-width="1.75" stroke-linecap="round" />
  {/if}
</svg>

<style>
  .icon { flex: none; display: inline-block; vertical-align: middle; }
</style>

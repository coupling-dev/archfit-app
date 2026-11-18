<script lang="ts">
  import type { Snippet } from 'svelte';
  import { repoPage } from '../../lib/route';

  // The head of every page. The title renders before any data, so a loading or failed page keeps its heading.
  // Actions sit at the right from 768px and wrap under the title below it. Use at most one primary button.
  let {
    title,
    repo = '',
    status,
    actions,
    children,
  }: {
    title: string;
    repo?: string;
    status?: Snippet;
    actions?: Snippet;
    children?: Snippet;
  } = $props();
</script>

<div class="page-head">
  <div class="page-identity">
    <h1>{title}</h1>
    {#if repo}<p class="lede"><a href={repoPage(repo)}>{repo}</a></p>{/if}
    {#if status}<div class="page-status">{@render status()}</div>{/if}
    {@render children?.()}
  </div>
  {#if actions}<div class="row page-actions">{@render actions()}</div>{/if}
</div>

<style>
  .page-head { display: flex; align-items: flex-start; justify-content: space-between; gap: 16px; flex-wrap: wrap; padding-bottom: 16px; border-bottom: 1px solid var(--border-muted); }
  .page-identity { flex: 1 1 320px; min-width: 0; display: flex; flex-direction: column; gap: 8px; overflow-wrap: anywhere; }
  .page-status { display: flex; }
  .page-actions { flex: 0 1 auto; min-width: 0; max-width: 100%; justify-content: flex-end; }
  .lede { margin: 0; color: var(--fg-muted); font-size: 14px; }
  .lede a { color: inherit; text-decoration: none; }
  .lede a:hover { text-decoration: underline; }
  @media (max-width: 768px) {
    .page-identity { flex-basis: 100%; }
    .page-actions { justify-content: flex-start; }
  }
</style>

<script lang="ts">
  import { onMount } from 'svelte';
  import Dsm from './Dsm.svelte';
  import type { MapModel } from './model';

  let { model, repo }: { model: MapModel; repo: string } = $props();
  const storageKey = 'archfit-map-view';
  let view = $state<'matrix' | 'diagram'>('matrix');
  onMount(() => {
    try { if (localStorage.getItem(storageKey) === 'diagram') view = 'diagram'; }
    catch { /* Storage is optional; Matrix remains the default. */ }
  });
  function select(next: 'matrix' | 'diagram') {
    view = next;
    try { localStorage.setItem(storageKey, next); }
    catch { /* The selection still works for this page. */ }
  }
</script>

<div class="views" role="group" aria-label="Map view">
  <button class="btn" aria-pressed={view === 'matrix'} onclick={() => select('matrix')}>Matrix</button>
  <button class="btn" aria-pressed={view === 'diagram'} onclick={() => select('diagram')}>Diagram</button>
</div>
{#if view === 'matrix'}
  <Dsm {model} {repo} />
{:else}
  {#await import('./Diagram.svelte')}
    <p role="status">Placing the modules…</p>
  {:then component}
    <component.default {model} detailView="matrix" />
  {:catch}
    <p class="flash error" role="alert">The diagram could not load. Select Matrix for relationship details.</p>
  {/await}
{/if}

<style>
  .views { display: flex; gap: 8px; margin-bottom: 16px; }
  button[aria-pressed="true"] { background: var(--accent-subtle); border-color: var(--accent); }
  button { min-height: 44px; }
</style>

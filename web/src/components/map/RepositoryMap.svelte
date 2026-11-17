<script lang="ts">
  import { onMount } from 'svelte';
  import SegmentedControl from '../ui/SegmentedControl.svelte';
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

<div class="views">
  <SegmentedControl
    label="Map view"
    options={[
      { value: 'matrix', label: 'Matrix' },
      { value: 'diagram', label: 'Diagram' },
    ]}
    value={view}
    onselect={(next) => select(next as 'matrix' | 'diagram')}
  />
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
  .views { margin-bottom: 16px; }
</style>

<script lang="ts">
  import { onMount } from 'svelte';
  import { repoPage } from '../../lib/route';
  import { lineLabel, mapLayers, mapLines, type MapModel } from './model';

  let { model, repo }: { model: MapModel; repo: string } = $props();
  let listOpen = $state(false);
  let diagramOpen = $state(false);

  onMount(() => {
    // Set the default once: resizing must not undo the reader's choice. The diagram stays closed so its 1.4 MB layout
    // worker loads only when asked.
    listOpen = window.matchMedia('(width < 720px)').matches;
  });
  const lines = $derived(mapLines(model.lines));
</script>

<details class="map-list" bind:open={listOpen}>
  <summary>Show the map as a list</summary>
  <h3>Dependencies</h3>
  <ul>{#each lines as line (line)}
    <li>{line.from} → {line.to}:
      {#if line.kind === 'requested' && line.request}
        Requested dependency · <a class="inline" href={repoPage(repo, `/requests/${line.request}`)}>#{line.request}</a>
      {:else}{lineLabel(line)}{/if}
    </li>
  {:else}<li>None.</li>{/each}</ul>
  {#each mapLayers(model) as layer (layer)}
    {@const modules = model.modules.filter((m) => m.layer === layer)}
    {#if modules.length}
      <h3>{layer || 'No layer'}</h3>
      <ul>{#each modules as m (m.name)}<li>{m.name} · Volatility: {m.volatility}</li>{/each}</ul>
    {/if}
  {/each}
</details>

{#if !model.modules.length}
  <p>No modules.</p>
{:else}
  <details class="map-diagram" bind:open={diagramOpen}>
    <summary>Show the map as a diagram</summary>
    {#if diagramOpen}
      {#await import('./Diagram.svelte')}
        <p role="status">Placing the modules…</p>
      {:then component}
        <component.default {model} />
      {:catch}
        <p class="flash error" role="alert">The map layout worker could not place the modules. The map is available as a list above.</p>
      {/await}
    {/if}
  </details>
{/if}

<p class="muted map-note">Dependencies not allowed by this policy need a request.</p>

<style>
  .map-list { overflow-wrap: anywhere; }
  summary { cursor: pointer; }
  .map-list a { display: inline-block; }
  @media (width < 720px), (pointer: coarse) {
    .map-list a { min-height: 44px; min-width: 44px; padding-block: 12px; }
  }
  details { padding-block: 12px; border-top: 1px solid var(--border-muted); }
  details h3 { margin-block: 12px 4px; }
  .map-note { margin-top: 8px; }
</style>

<script lang="ts">
  import type { Policy } from '../lib/api';
  import { policyGraph } from '../lib/policy';

  let { modules, layers, inspect }: {
    modules: Policy['modules'];
    layers: Policy['layers'];
    inspect: (index: number) => void;
  } = $props();
  const id = $props.id();
  const graph = $derived(policyGraph(modules, layers));
</script>

<div class="graph-scroll">
  <svg viewBox="0 0 480 {graph.height}" role="group" aria-labelledby={id + '-title'}>
    <title id={id + '-title'}>Allowed module dependencies</title>
    <defs>
      <marker id={id + '-arrow'} viewBox="0 0 8 8" refX="7" refY="4" markerWidth="6" markerHeight="6" orient="auto">
        <path d="M0 0L8 4L0 8Z" class="arrow" />
      </marker>
    </defs>
    {#each graph.links as link, i (i)}
      <path d={link.path} class="dependency" marker-end={`url(#${id}-arrow)`}>
        <title>{link.from} → {link.to}</title>
      </path>
    {/each}
    {#each graph.nodes as node (node.module.name)}
      <g transform="translate({node.x} {node.y})" data-layer={node.layer % 4}>
        <a href={'#policy-module-' + node.index} aria-label={'Inspect ' + node.module.name}
          onclick={(event) => { event.preventDefault(); inspect(node.index); }}>
        <title>{node.module.name} · {node.module.layer || 'Layer not provided'}</title>
        <rect width="140" height="44" rx="6" />
        <text x="10" y="19" class="name">{node.module.name.length > 17 ? node.module.name.slice(0, 16) + '…' : node.module.name}</text>
        <text x="10" y="34">{node.module.layer.length > 20 ? node.module.layer.slice(0, 19) + '…' : node.module.layer || 'Not provided'}</text>
      </a>
      </g>
    {/each}
  </svg>
</div>
<ul class="legend" aria-label="Graph legend">
  {#each graph.layers as layer, i (layer)}
    <li data-layer={i % 4}><span class="swatch" aria-hidden="true"></span>{layer || 'Layer not provided'}</li>
  {/each}
  <li>→ Allowed dependency</li>
</ul>
{#if graph.missing}
  <p class="small muted">{graph.missing} {graph.missing === 1 ? 'dependency target is' : 'dependency targets are'} not in the module list. See the table for all declared dependencies.</p>
{/if}

<style>
  .graph-scroll { max-height: 420px; overflow-y: auto; }
  svg { display: block; width: 100%; max-width: 480px; margin: 0 auto; overflow: visible; }
  .dependency { fill: none; stroke: var(--fg-muted); stroke-width: 1.4; }
  .arrow { fill: var(--fg-muted); }
  [data-layer="0"] { --layer: var(--accent); --layer-bg: var(--accent-subtle); }
  [data-layer="1"] { --layer: var(--done); --layer-bg: var(--done-subtle); }
  [data-layer="2"] { --layer: var(--success); --layer-bg: var(--success-subtle); }
  [data-layer="3"] { --layer: var(--attention); --layer-bg: var(--attention-subtle); }
  rect { fill: var(--layer-bg); stroke: var(--layer); stroke-width: 1; }
  a:hover rect, a:focus-visible rect { fill: var(--layer-bg); stroke-width: 2; }
  text { font: 10px var(--font); fill: var(--fg-muted); }
  text.name { font: 600 11px var(--mono); fill: var(--fg); }
  .legend { display: flex; flex-wrap: wrap; justify-content: center; gap: 8px 16px; list-style: none; margin: 12px 0; padding: 0; font-size: 12px; color: var(--fg-muted); }
  .legend li { max-width: 100%; overflow-wrap: anywhere; display: inline-flex; align-items: center; gap: 5px; }
  .swatch { width: 8px; height: 8px; background: var(--layer); border-radius: 2px; }
</style>

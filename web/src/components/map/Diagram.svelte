<script lang="ts">
  import { layoutMap, type MapLayout } from './layout';
  import { lineLabel, type MapModel } from './model';

  let { model, detailView = 'list' }: { model: MapModel; detailView?: 'list' | 'matrix' } = $props();
  const id = $props.id();
  let placed = $state<MapLayout | null>(null);
  let failure = $state('');

  $effect(() => {
    const controller = new AbortController();
    placed = null;
    failure = '';
    if (model.modules.length) {
      layoutMap(model, controller.signal).then(
        (p) => { if (!controller.signal.aborted) placed = p; },
        () => { if (!controller.signal.aborted) failure = detailView === 'matrix'
          ? 'The map layout worker could not place the modules. Select Matrix for relationship details.'
          : 'The map layout worker could not place the modules. The map is available as a list above.'; },
      );
    }
    return () => controller.abort();
  });
</script>

{#if failure}
  <p class="flash error" role="alert">{failure}</p>
{:else if !model.modules.length}
  <p>No modules.</p>
{:else if !placed}
  <p role="status">Placing the modules…</p>
{:else}
  <p class="muted small">Diagram fitted to this box. Use {detailView === 'matrix' ? 'Matrix' : 'the list above'} for full-size dependency labels.</p>
  <div class="map-canvas">
    <svg width={placed.width} height={placed.height} viewBox={`0 0 ${placed.width} ${placed.height}`} role="img" aria-labelledby={id + '-title'}>
      <title id={id + '-title'}>Modules by layer and their dependencies</title>
      <defs>
        <marker id={id + '-arrow'} viewBox="0 0 10 10" refX="9" refY="5" markerWidth="7" markerHeight="7" orient="auto-start-reverse">
          <path d="M 0 0 L 10 5 L 0 10 z" fill="context-stroke" />
        </marker>
      </defs>
      {#each placed.bands as band (band.name)}
        <g class="map-band">
          <rect x="8" y={band.y} width={placed.width - 16} height={band.height} rx="6" />
          <text x="20" y={band.y + 20}>{band.name || 'No layer'}</text>
        </g>
      {/each}
      {#each placed.edges as edge (edge.line)}
        <g class={'map-edge ' + edge.line.kind}>
          <path d={edge.path} marker-end={`url(#${id}-arrow)`} />
          <text x={edge.labelX} y={edge.labelY}>{lineLabel(edge.line)}</text>
        </g>
      {/each}
      {#each placed.nodes as node (node.name)}
        <g class="map-node" data-module={node.name}>
          <rect x={node.x} y={node.y} width={node.width} height={node.height} rx="6" />
          <text x={node.x + 12} y={node.y + 25}>{node.name}</text>
          <text class="volatility" x={node.x + 12} y={node.y + 47}>Volatility: {node.volatility}</text>
        </g>
      {/each}
    </svg>
  </div>

<ul class="map-legend" aria-label="Map legend">
  <li><span class="sample allowed" aria-hidden="true"></span>An allowed dependency</li>
  <li><span class="sample requested" aria-hidden="true"></span>A requested dependency</li>
  <li><span class="sample deny" aria-hidden="true"></span>One a rule denies</li>
  <li><span class="sample exception" aria-hidden="true"></span>An exception (with its date)</li>
  <li><span class="sample drift" aria-hidden="true"></span>Drift</li>
</ul>
{/if}

<style>
  .map-canvas { border: 1px solid var(--border); border-radius: var(--radius); margin-top: 8px; }
  svg { display: block; max-width: 100%; height: auto; font: 14px var(--font); color: var(--fg); }
  p { margin-top: 8px; }
  text { fill: currentColor; }
  .map-band rect { fill: var(--bg-subtle); stroke: var(--border-muted); }
  .map-band text { font-size: 12px; font-weight: 600; }
  .map-node rect { fill: var(--bg); stroke: var(--border); }
  .map-node text { font-weight: 600; }
  .map-node .volatility { font-size: 12px; font-weight: 400; }
  .map-edge { color: var(--fg-muted); }
  .map-edge path { fill: none; stroke: currentColor; stroke-width: 2; }
  .map-edge text { font-size: 12px; paint-order: stroke; stroke: var(--bg-subtle); stroke-width: 4; stroke-linejoin: round; }
  .map-edge.requested path { stroke-dasharray: 8 5; }
  .map-edge.exception path { stroke-dasharray: 2 5; }
  .map-edge.deny path { stroke-width: 3; stroke-dasharray: 12 3 2 3; }
  .map-edge.drift, .sample.drift { color: var(--danger); }
  .map-legend { display: flex; flex-wrap: wrap; gap: 12px 20px; list-style: none; padding: 16px 0; }
  .map-legend li { display: flex; align-items: center; gap: 8px; font-size: 12px; }
  .sample { display: inline-block; width: 32px; border-top: 2px solid currentColor; flex: none; }
  .sample.requested { border-top-style: dashed; }
  .sample.exception { border-top-style: dotted; }
  .sample.deny { border-top: 3px double currentColor; }
</style>

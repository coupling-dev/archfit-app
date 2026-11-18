<script lang="ts">
  import { onMount } from 'svelte';
  import { layoutMap, type MapLayout, type MapNode } from './layout';
  import { focusLabel, legendKinds, legendWords, mapCaption, mapLines, type LineKind, type MapModel } from './model';
  import { routeLines } from './routes';
  import { centerOn, fitView, panBy, viewBox, visible, zoomAt, type View } from './viewport';

  // `kinds` is the legend: the Home map shows all five line kinds; the policy graph shows only allowed dependencies.
  // `onfocus` reports the module in focus (or '' when none), so a page can show its details in place.
  let { model, detailView = 'list', kinds = legendKinds, label = 'Modules by layer and their dependencies', onfocus }: {
    model: MapModel;
    detailView?: 'list' | 'matrix' | 'table';
    kinds?: LineKind[];
    label?: string;
    onfocus?: (name: string) => void;
  } = $props();
  const id = $props.id();
  const STEP = 80;
  const ZOOM_STEP = 1.25;
  const DRAG_PX = 4;

  let placed = $state<MapLayout | null>(null);
  let failure = $state('');
  let view = $state<View | null>(null);
  let width = $state(0);
  let height = $state(0);
  let focused = $state('');
  let query = $state('');
  let fullscreen = $state(false);
  let root = $state<HTMLElement>();
  let viewport = $state<HTMLDivElement>();
  const canFullscreen = typeof document !== 'undefined' && document.fullscreenEnabled === true;

  const vp = $derived({ w: width, h: height });
  const world = $derived({ w: placed?.width ?? 0, h: placed?.height ?? 0 });
  const box = $derived(view && width > 0 ? viewBox(view, vp) : `0 0 ${world.w} ${world.h}`);
  const touches = (line: { from: string; to: string }) => line.from === focused || line.to === focused;
  const lines = $derived(mapLines(model.lines));
  const near = $derived(
    focused ? [focused, ...lines.filter(touches).flatMap((line) => [line.from, line.to])] : [],
  );
  // Only the lines of the module in focus are drawn; every other dependency is in the line between its two layers.
  const routes = $derived(placed && focused ? routeLines(placed.nodes, lines, focused, placed) : []);
  const count = (pick: (line: { from: string; to: string }) => string, name: string) =>
    lines.filter((line) => pick(line) === name).length;
  const layerName = (name: string) => name || 'No layer';

  $effect(() => {
    const controller = new AbortController();
    placed = null;
    view = null;
    focused = '';
    failure = '';
    if (model.modules.length) {
      layoutMap(model, controller.signal).then(
        (p) => { if (!controller.signal.aborted) placed = p; },
        () => { if (!controller.signal.aborted) failure = detailView === 'matrix'
          ? 'The map layout worker could not place the modules. Select Matrix for relationship details.'
          : detailView === 'table'
            ? 'The graph could not place the modules. The table below lists every dependency.'
            : 'The map layout worker could not place the modules. The map is available as a list above.'; },
      );
    }
    return () => controller.abort();
  });

  $effect(() => { onfocus?.(focused); });

  // Open readable: the first time the chart and the viewport both have a size.
  $effect(() => {
    if (placed && width > 0 && height > 0 && !view) view = fitView(world, vp);
  });

  // A wheel handler must be non-passive to stop the page zooming with the chart; Svelte's onwheel is passive.
  $effect(() => {
    const el = viewport;
    if (!el) return;
    const wheel = (e: WheelEvent) => {
      if (!(e.ctrlKey || e.metaKey) || !view) return;
      e.preventDefault();
      const p = local(e);
      const delta = e.deltaMode === 1 ? e.deltaY * 20 : e.deltaY;
      view = zoomAt(view, world, vp, view.k * Math.exp(-delta * 0.01), p.x, p.y);
    };
    el.addEventListener('wheel', wheel, { passive: false });
    return () => el.removeEventListener('wheel', wheel);
  });

  onMount(() => {
    const sync = () => { fullscreen = document.fullscreenElement === root; };
    document.addEventListener('fullscreenchange', sync);
    return () => document.removeEventListener('fullscreenchange', sync);
  });

  function local(e: { clientX: number; clientY: number }) {
    const r = viewport!.getBoundingClientRect();
    return { x: e.clientX - r.left, y: e.clientY - r.top };
  }
  function zoomBy(factor: number) {
    if (view) view = zoomAt(view, world, vp, view.k * factor, width / 2, height / 2);
  }
  const fit = () => { view = fitView(world, vp); };
  const actual = () => { if (view) view = zoomAt(view, world, vp, 1, width / 2, height / 2); };
  async function toggleFullscreen() {
    try {
      if (document.fullscreenElement) await document.exitFullscreen();
      else await root?.requestFullscreen();
    } catch { /* The chart keeps its normal size. */ }
  }

  // Drag pans, two fingers pinch. A drag that moved is not a click on whatever was under the pointer.
  let pointers: { id: number; x: number; y: number }[] = [];
  let start = { x: 0, y: 0 };
  let moved = false;
  let suppressClick = false;
  let pinch = 0;
  function down(e: PointerEvent) {
    if (e.pointerType === 'mouse' && e.button !== 0) return;
    suppressClick = false;
    pressing = true;
    pointers = [...pointers.filter((p) => p.id !== e.pointerId), { id: e.pointerId, ...local(e) }];
    if (pointers.length === 1) { start = local(e); moved = false; }
    else { moved = true; pinch = spread(); }
  }
  function spread() {
    const [a, b] = pointers;
    return Math.hypot(a.x - b.x, a.y - b.y);
  }
  function move(e: PointerEvent) {
    const prev = pointers.find((p) => p.id === e.pointerId);
    if (!prev || !view) return;
    const next = local(e);
    pointers = pointers.map((p) => (p.id === e.pointerId ? { id: p.id, ...next } : p));
    if (pointers.length === 1) {
      if (!moved && Math.hypot(next.x - start.x, next.y - start.y) < DRAG_PX) return;
      if (!moved) viewport?.setPointerCapture(e.pointerId);
      moved = true;
      view = panBy(view, world, vp, next.x - prev.x, next.y - prev.y);
    } else if (pointers.length === 2) {
      const d = spread();
      const [a, b] = pointers;
      if (pinch > 0) view = zoomAt(view, world, vp, (view.k * d) / pinch, (a.x + b.x) / 2, (a.y + b.y) / 2);
      pinch = d;
    }
  }
  function up(e: PointerEvent) {
    if (!pointers.some((p) => p.id === e.pointerId)) return;
    pointers = pointers.filter((p) => p.id !== e.pointerId);
    pinch = 0;
    pressing = false;
    if (moved) suppressClick = true;
  }

  function key(e: KeyboardEvent) {
    if (!view || e.ctrlKey || e.metaKey || e.altKey) return;
    const pan: Record<string, [number, number]> = {
      ArrowLeft: [STEP, 0], ArrowRight: [-STEP, 0], ArrowUp: [0, STEP], ArrowDown: [0, -STEP],
    };
    if (pan[e.key]) view = panBy(view, world, vp, ...pan[e.key]);
    else if (e.key === '+' || e.key === '=') zoomBy(ZOOM_STEP);
    else if (e.key === '-' || e.key === '_') zoomBy(1 / ZOOM_STEP);
    else if (e.key === '0') fit();
    else if (e.key === 'Escape') focused = '';
    else return;
    e.preventDefault();
  }

  function reveal(node: MapNode, always = false) {
    if (view && (always || !visible(view, vp, node))) view = centerOn(view, world, vp, node);
  }
  // A pointer press focuses the module before the click; only the click decides, so a drag that starts on a module
  // pans without selecting it.
  let wasFocused = false;
  let pressing = false;
  function press(node: MapNode) { wasFocused = focused === node.name; }
  function focusNode(node: MapNode) {
    if (pressing) return;
    focused = node.name;
    reveal(node);
  }
  function clickNode(e: MouseEvent, node: MapNode) {
    e.stopPropagation();
    if (suppressClick) { suppressClick = false; return; }
    // A pointer press has already focused the module; only a second press clears it. Enter and Space (detail 0) toggle.
    const clear = e.detail === 0 ? focused === node.name : wasFocused;
    focused = clear ? '' : node.name;
  }
  function keyNode(e: KeyboardEvent, node: MapNode) {
    if (e.key !== 'Enter' && e.key !== ' ') return;
    e.preventDefault();
    e.stopPropagation();
    focused = focused === node.name ? '' : node.name;
    reveal(node);
  }
  function clickBackground() {
    if (suppressClick) suppressClick = false;
    else focused = '';
  }
  function zoomIn(e: MouseEvent) {
    if (!view) return;
    const p = local(e);
    view = zoomAt(view, world, vp, view.k * 2, p.x, p.y);
  }

  function find() {
    const wanted = query.trim().toLowerCase();
    const node = placed?.nodes.find((n) => n.name.toLowerCase() === wanted);
    if (!node) return false;
    focused = node.name;
    reveal(node, true);
    return true;
  }
  function findKey(e: KeyboardEvent) {
    if (e.key !== 'Enter' || find()) return;
    const wanted = query.trim().toLowerCase();
    const node = wanted ? placed?.nodes.find((n) => n.name.toLowerCase().includes(wanted)) : undefined;
    if (!node) return;
    query = node.name;
    find();
  }
</script>

{#if failure}
  <p class="flash error" role="alert">{failure}</p>
{:else if !model.modules.length}
  <p>No modules.</p>
{:else}
  <div class="map-root" bind:this={root}>
    {#if placed}
      <ul class="map-legend" aria-label="Map legend">
        {#each kinds as kind (kind)}
          <li><span class={'sample ' + kind} aria-hidden="true"></span>{legendWords[kind]}</li>
        {/each}
      </ul>
      <p class="muted small">{mapCaption}</p>
      <div class="map-tools">
        <label class="find">Find a module
          <input class="form-control" type="search" list={id + '-modules'} autocomplete="off" bind:value={query}
            oninput={find} onkeydown={findKey} />
        </label>
        <datalist id={id + '-modules'}>{#each placed.nodes as node (node.name)}<option value={node.name}></option>{/each}</datalist>
        <div class="btn-group" role="group" aria-label="Zoom">
          <button class="btn" type="button" aria-label="Zoom in" title="Zoom in" onclick={() => zoomBy(ZOOM_STEP)}>+</button>
          <button class="btn" type="button" aria-label="Zoom out" title="Zoom out" onclick={() => zoomBy(1 / ZOOM_STEP)}>−</button>
          <button class="btn" type="button" onclick={fit}>Fit</button>
          <button class="btn" type="button" onclick={actual}>100%</button>
          {#if canFullscreen}
            <button class="btn" type="button" aria-pressed={fullscreen} onclick={toggleFullscreen}>Full screen</button>
          {/if}
        </div>
      </div>
    {/if}
    <!-- svelte-ignore a11y_no_noninteractive_tabindex, a11y_no_noninteractive_element_interactions (The chart is one focusable surface: arrow keys pan and plus and minus zoom, as the label says.) -->
    <div
      class="map-viewport"
      bind:this={viewport}
      bind:clientWidth={width}
      bind:clientHeight={height}
      role="application"
      aria-label="Dependency diagram. Arrow keys move, plus and minus zoom, 0 fits, Escape clears the selected module."
      tabindex="0"
      onpointerdown={down}
      onpointermove={move}
      onpointerup={up}
      onpointercancel={up}
      onkeydown={key}
      onclick={clickBackground}
      ondblclick={zoomIn}
    >
      {#if !placed}
        <p role="status">Placing the modules…</p>
      {:else}
        <svg viewBox={box} role="group" aria-labelledby={id + '-title'} data-focus={focused || undefined}>
          <title id={id + '-title'}>{label}</title>
          <defs>
            <marker id={id + '-arrow'} viewBox="0 0 10 10" refX="9" refY="5" markerUnits="userSpaceOnUse" markerWidth="12" markerHeight="12" orient="auto-start-reverse">
              <path d="M 0 0 L 10 5 L 0 10 z" fill="context-stroke" />
            </marker>
          </defs>
          {#each placed.bands as band (band.name)}
            <g class="map-band">
              <rect x={placed.left} y={band.y} width={placed.width - placed.left - 8} height={band.height} rx="6" />
              <text x={placed.left + 12} y={band.y + 20}>{band.name || 'No layer'}</text>
            </g>
          {/each}
          {#each placed.links as link (link.from + '\u0000' + link.to)}
            <g class={'map-link ' + link.kind} class:dim={focused}>
              <title>{layerName(link.from)} → {layerName(link.to)}: {link.count} {link.count === 1 ? 'dependency' : 'dependencies'}</title>
              <path d={link.path} marker-end={`url(#${id}-arrow)`} />
              <text x={link.labelX} y={link.labelY} text-anchor="middle">{link.count}</text>
            </g>
          {/each}
          {#each routes as route (route.line)}
            <g class={'map-edge ' + route.line.kind}><path d={route.path} marker-end={`url(#${id}-arrow)`} /></g>
          {/each}
          {#each placed.nodes as node (node.name)}
            <g
              class="map-node"
              class:selected={focused === node.name}
              class:dim={focused && !near.includes(node.name)}
              data-module={node.name}
              role="button"
              tabindex="0"
              aria-pressed={focused === node.name}
              aria-label={`${node.name}, volatility ${node.volatility}. Depends on ${count((l) => l.from, node.name)}, used by ${count((l) => l.to, node.name)}.`}
              onpointerdown={() => press(node)}
              onfocus={() => focusNode(node)}
              onclick={(e) => clickNode(e, node)}
              onkeydown={(e) => keyNode(e, node)}
            >
              <rect x={node.x} y={node.y} width={node.width} height={node.height} rx="6" />
              <text x={node.x + 12} y={node.y + 25}>{node.name}</text>
              <text class="volatility" x={node.x + 12} y={node.y + 47}>Volatility: {node.volatility}</text>
            </g>
          {/each}
          {#each routes as route (route.line)}
            <text class={'map-label ' + route.line.kind} x={route.labelX} y={route.labelY} text-anchor="middle">{focusLabel(route.line)}</text>
          {/each}
        </svg>
      {/if}
    </div>
  </div>
{/if}

<style>
  .map-root { margin-top: 8px; }
  .map-root:fullscreen { display: flex; flex-direction: column; background: var(--bg); padding: 16px; }
  .map-root:fullscreen .map-viewport { height: auto; flex: 1; min-height: 0; }
  p { margin-top: 8px; }
  .map-tools { display: flex; flex-wrap: wrap; align-items: end; justify-content: space-between; gap: 12px; margin-block: 12px; }
  .find { display: grid; gap: 4px; font-size: 12px; font-weight: 600; }
  .find input { width: min(260px, 100%); font-weight: 400; }
  .map-viewport {
    height: min(70vh, 720px);
    overflow: hidden;
    touch-action: none;
    user-select: none;
    cursor: grab;
    border: 1px solid var(--border);
    border-radius: var(--radius);
  }
  .map-viewport:active { cursor: grabbing; }
  .map-viewport:focus-visible { outline: 2px solid var(--accent); outline-offset: 2px; }
  .map-viewport > p { padding: 16px; }
  svg { display: block; width: 100%; height: 100%; font: 14px var(--font); color: var(--fg); }
  text { fill: currentColor; }
  .map-band rect { fill: var(--bg-subtle); stroke: var(--border-muted); }
  .map-band text { font-size: 12px; font-weight: 600; }
  .map-node { cursor: pointer; }
  .map-node:focus { outline: none; }
  .map-node rect { fill: var(--bg); stroke: var(--border); }
  .map-node:focus-visible rect, .map-node.selected rect { stroke: var(--accent); stroke-width: 3; }
  .map-node text { font-weight: 600; }
  .map-node .volatility { font-size: 12px; font-weight: 400; }
  .dim { opacity: 0.2; }
  .map-edge, .map-link { color: var(--fg-muted); }
  .map-edge path, .map-link path { fill: none; stroke: currentColor; stroke-width: 1.5; vector-effect: non-scaling-stroke; }
  .map-label, .map-link text { font-size: 14px; font-weight: 600; paint-order: stroke; stroke: var(--bg-subtle); stroke-width: 4; stroke-linejoin: round; }
  .map-edge.requested path, .map-link.requested path { stroke-width: 2; stroke-dasharray: 8 5; }
  .map-edge.exception path, .map-link.exception path { stroke-width: 2; stroke-dasharray: 2 4; }
  .map-edge.deny path, .map-link.deny path { stroke-width: 3; stroke-dasharray: 12 3 2 3; }
  .map-edge.drift path, .map-link.drift path { stroke-width: 4; }
  .map-edge.drift, .map-link.drift, .sample.drift { color: var(--danger); }
  .map-label { color: var(--fg); }
  .map-label.drift { color: var(--danger); }
  .map-legend { display: flex; flex-wrap: wrap; gap: 12px 20px; list-style: none; padding: 0; margin-block: 8px; }
  .map-legend li { display: flex; align-items: center; gap: 8px; font-size: 12px; }
  .sample { display: inline-block; width: 32px; border-top: 1.5px solid currentColor; flex: none; }
  .sample.requested { border-top: 2px dashed currentColor; }
  .sample.exception { border-top: 2px dotted currentColor; }
  .sample.deny { border-top: 3px double currentColor; }
  .sample.drift { border-top: 4px solid currentColor; }
</style>

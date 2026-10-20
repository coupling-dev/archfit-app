<script lang="ts">
  import '@xyflow/svelte/dist/style.css';
  import { Background, Controls, SvelteFlow, type Edge, type Node } from '@xyflow/svelte';
  import { isAdded, linkKey, statuses, worst, type GLink, type GNode } from '../../lib/graph';
  import { place, type Placed } from '../../lib/layout';
  import { ui } from '../../lib/selection.svelte';
  import FlowEdge from './FlowEdge.svelte';
  import FlowNode from './FlowNode.svelte';

  // The canvas: Svelte Flow draws what ELK placed. Nothing on it moves or connects; it pans, zooms and selects.
  let { nodes: modules, links, all, pull }: { nodes: GNode[]; links: GLink[]; all: GLink[]; pull: boolean } = $props();

  const minHeight = 360;
  const maxHeight = 900;

  let placed = $state.raw<Placed | null>(null);
  let failure = $state('');
  let nodes = $state.raw<Node[]>([]);
  let edges = $state.raw<Edge[]>([]);
  let version = $state(0);
  // With emphasis on, the dependencies this pull request did not add are muted, when it added any.
  const pullHasAdded = $derived(links.some((l) => isAdded(l, pull)));

  $effect(() => {
    const ns = modules;
    const ls = links;
    let stale = false;
    place(ns, ls).then(
      (p) => {
        if (stale) return;
        placed = p;
        version++;
      },
      (e: unknown) => (failure = `The layout failed: ${e instanceof Error ? e.message : String(e)}`),
    );
    return () => (stale = true);
  });

  // related: whether a module or a dependency stays at full strength under the selection.
  function relatedNode(name: string): boolean {
    const s = ui.sel;
    if (!s) return true;
    if (s.kind === 'node') {
      return s.name === name || links.some((l) => (l.from === s.name && l.to === name) || (l.to === s.name && l.from === name));
    }
    // a selected dependency that is not drawn (hidden by a chip or by focus) dims nothing
    const l = links.find((x) => linkKey(x) === s.key);
    return !l || l.from === name || l.to === name;
  }
  function relatedLink(l: GLink): boolean {
    const s = ui.sel;
    if (!s) return true;
    if (s.kind === 'node') return l.from === s.name || l.to === s.name;
    const picked = links.find((x) => linkKey(x) === s.key);
    return !picked || (l.from === picked.from && l.to === picked.to) || (l.from === picked.to && l.to === picked.from);
  }

  $effect(() => {
    const p = placed;
    if (!p) return;
    const emphasis = ui.emphasis;
    nodes = modules
      .filter((n) => p.nodes.has(n.name))
      .map((n) => ({
        id: n.name,
        type: 'module',
        position: p.nodes.get(n.name)!,
        data: { n, worst: worst(all, n.name), dim: !relatedNode(n.name) },
      }));
    edges = links
      .filter((l) => p.edges.has(linkKey(l)))
      .map((l) => {
        const key = linkKey(l);
        const e = p.edges.get(key)!;
        const added = isAdded(l, pull);
        return {
          id: key,
          type: 'dependency',
          source: l.from,
          target: l.to,
          // the button inside is the dependency; the wrapper must not be an image around an interactive element
          ariaRole: 'group' as const,
          // added first, then by status, so the strongest line is drawn on top
          zIndex: (added && emphasis ? 10 : 0) + statuses.length - statuses.indexOf(l.status),
          data: {
            l,
            key,
            pts: e.pts,
            label: e.label,
            added,
            halo: added && emphasis,
            muted: pull && emphasis && !added && pullHasAdded,
            dim: !relatedLink(l),
            sel: ui.sel?.kind === 'link' && ui.sel.key === key,
          },
        };
      });
  });

  // The canvas is as tall as the layout at full size, within bounds, so a small graph is not shrunk to fit a big frame
  // and a big one scrolls the page before it shrinks.
  const height = $derived(placed ? Math.min(Math.max(placed.height + 64, minHeight), maxHeight) : minHeight);
  const nodeTypes = { module: FlowNode };
  const edgeTypes = { dependency: FlowEdge };
</script>

<div class="g-canvas" style:height="{height}px">
  <svg class="g-defs" aria-hidden="true">
    <defs>
      {#each statuses as s (s)}
        <marker
          id="g-arrow-{s}"
          class="g-arrow {s}"
          viewBox="0 0 10 10"
          refX="9"
          refY="5"
          markerWidth="11"
          markerHeight="11"
          markerUnits="userSpaceOnUse"
          orient="auto"
        >
          <path d="M 0 0 L 10 5 L 0 10 L 2.5 5 z" />
        </marker>
      {/each}
    </defs>
  </svg>
  {#if failure}
    <p class="flash error" role="alert">{failure}</p>
  {:else if !placed}
    <p class="muted g-canvas-note" aria-live="polite">Laying out {modules.length} modules…</p>
  {:else}
    {#key version}
      <SvelteFlow
        bind:nodes
        bind:edges
        {nodeTypes}
        {edgeTypes}
        fitView
        fitViewOptions={{ padding: 0.06, minZoom: 0.6, maxZoom: 1 }}
        minZoom={0.3}
        maxZoom={2.5}
        nodesDraggable={false}
        nodesConnectable={false}
        elementsSelectable={false}
        nodesFocusable={false}
        edgesFocusable={false}
        zoomOnScroll={false}
        preventScrolling={false}
        proOptions={{ hideAttribution: true }}
        onpaneclick={() => (ui.sel = null)}
      >
        <Background gap={20} size={1} />
        <Controls showLock={false} />
      </SvelteFlow>
    {/key}
  {/if}
</div>

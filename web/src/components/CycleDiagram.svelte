<script lang="ts">
  import { layout, type Direction } from '../lib/cycle';

  let { modules, directions }: { modules: string[]; directions: Direction[] } = $props();

  const drawing = $derived(layout(modules, directions));
  // Marker IDs must be unique on the page, which can hold several cycles.
  const id = `cycle-${Math.random().toString(36).slice(2, 8)}`;
  const kinds = ['added', 'existing', 'cycle'] as const;
</script>

<div class="cycle-drawing">
  <svg
    viewBox="0 0 {drawing.width} {drawing.height}"
    width={drawing.width}
    role="img"
    aria-label="Dependency cycle among {modules.join(', ')}; the table below lists each direction"
  >
    <defs>
      {#each kinds as kind (kind)}
        <marker
          id="{id}-{kind}"
          class="arrowhead {kind}"
          viewBox="0 0 10 10"
          refX="9"
          refY="5"
          markerWidth="12"
          markerHeight="12"
          markerUnits="userSpaceOnUse"
          orient="auto-start-reverse"
        >
          <path d="M 0 0 L 10 5 L 0 10 z" />
        </marker>
      {/each}
    </defs>
    {#each drawing.arrows as a, i (i)}
      <path class="arrow {a.kind}" d={a.d} stroke-width={a.width} marker-end="url(#{id}-{a.kind})" />
    {/each}
    {#each drawing.arrows as a, i (i)}
      {#if a.label}
        <text class="arrow-label" x={a.lx} y={a.ly} text-anchor="middle" dominant-baseline="middle">{a.label}</text>
      {/if}
    {/each}
    {#each drawing.boxes as b (b.module)}
      <g class="module">
        <title>{b.module}</title>
        <rect x={b.x - b.w / 2} y={b.y - b.h / 2} width={b.w} height={b.h} rx="6" />
        <text x={b.x} y={b.y} text-anchor="middle" dominant-baseline="central">{b.label}</text>
      </g>
    {/each}
  </svg>
</div>

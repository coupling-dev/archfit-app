<script lang="ts">
  import { acceptedWidth, countWord, lineWidth, linkStatusWord, measured, smooth, strengthWord, type GLink } from '../../lib/graph';
  import { pickLink } from '../../lib/selection.svelte';
  import type { Edge, EdgeProps } from '@xyflow/svelte';

  interface Dependency extends Record<string, unknown> {
    l: GLink;
    key: string;
    pts: [number, number][];
    label: { x: number; y: number } | null;
    added: boolean;
    halo: boolean;
    muted: boolean;
    dim: boolean;
    sel: boolean;
  }

  // One dependency: colour is its status, width its integration strength, dashed when no seam measured one; the label
  // is its dependency count. A dependency this pull request added carries a halo.
  let { data: props }: EdgeProps<Edge<Dependency>> = $props();
  const data = $derived(props!);

  const l = $derived(data.l);
  const d = $derived(smooth(data.pts));
  const at = $derived(data.label ?? { x: data.pts[Math.floor(data.pts.length / 2)][0], y: data.pts[Math.floor(data.pts.length / 2)][1] });
  const text = $derived(String(l.count || '·'));
  const w = $derived(12 + text.length * 7);
  // Accepted debt is drawn thin and faint, so a repository with no active problem does not look alarming. What this pull
  // request added is new, accepted or not: the faint style is only for debt accepted earlier.
  const faint = $derived(l.status === 'accepted' && !data.added);
  const width = $derived(faint ? Math.min(lineWidth(l), acceptedWidth) : lineWidth(l));
  const name = $derived(
    `${l.from} to ${l.to}, ${countWord(l)}, ${linkStatusWord(l)}, ${measured(l) ? strengthWord[l.strength] : 'strength not measured'}${data.added ? ', added by this PR' : ''}`,
  );

  function onkey(e: KeyboardEvent) {
    if (e.key !== 'Enter' && e.key !== ' ') return;
    e.preventDefault();
    pickLink(data.key);
  }
</script>

<g
  class="g-edge {l.status}"
  class:unmeasured={!measured(l)}
  class:faint
  class:muted={data.muted}
  class:dim={data.dim}
  class:sel={data.sel}
  tabindex="0"
  role="button"
  aria-label={name}
  onclick={(e) => {
    e.stopPropagation();
    pickLink(data.key);
  }}
  onkeydown={onkey}
>
  <path {d} class="hit" />
  {#if data.halo}<path {d} class="halo" stroke-width={width + 8} />{/if}
  <path {d} class="line" stroke-width={width} marker-end="url(#g-arrow-{l.status})" />
  <rect x={at.x - w / 2} y={at.y - 9} width={w} height="18" rx="9" class="badge" />
  <text x={at.x} y={at.y} text-anchor="middle" dominant-baseline="central" class="badge-t">{text}</text>
</g>

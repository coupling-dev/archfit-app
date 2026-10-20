// ELK places the drawn part of the graph. It is part of the graph pages' lazy chunk and runs on the main thread: the
// bundled build needs no worker and no blob: URL, so the CSP stays as it is.
import ELK, { type ElkNode } from 'elkjs/lib/elk.bundled.js';
import { elkInput, type GLink, type GNode } from './graph';

export interface Placed {
  width: number;
  height: number;
  nodes: Map<string, { x: number; y: number }>;
  edges: Map<string, { pts: [number, number][]; label: { x: number; y: number } | null }>;
}

interface Point {
  x: number;
  y: number;
}

interface PlacedEdge {
  id: string;
  sections?: { startPoint: Point; endPoint: Point; bendPoints?: Point[] }[];
  labels?: { x?: number; y?: number; width?: number; height?: number }[];
}

const elk = new ELK();

export async function place(nodes: GNode[], links: GLink[]): Promise<Placed> {
  const g: ElkNode = await elk.layout(elkInput(nodes, links));
  const out: Placed = { width: g.width ?? 0, height: g.height ?? 0, nodes: new Map(), edges: new Map() };
  for (const c of g.children ?? []) out.nodes.set(c.id, { x: c.x ?? 0, y: c.y ?? 0 });
  for (const e of (g.edges ?? []) as PlacedEdge[]) {
    const s = e.sections?.[0];
    if (!s) continue;
    const pts: [number, number][] = [
      [s.startPoint.x, s.startPoint.y],
      ...(s.bendPoints ?? []).map((b): [number, number] => [b.x, b.y]),
      [s.endPoint.x, s.endPoint.y],
    ];
    const l = e.labels?.[0];
    const label =
      l && l.x !== undefined && l.y !== undefined
        ? { x: l.x + (l.width ?? 0) / 2, y: l.y + (l.height ?? 0) / 2 }
        : null;
    out.edges.set(e.id, { pts, label });
  }
  return out;
}

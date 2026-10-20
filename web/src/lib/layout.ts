// ELK places the drawn part of the graph. It runs in a worker (layout.worker.ts), so a large graph never blocks the
// page while it is placed; the graph pages' chunk keeps only the small API that talks to it.
import ELK, { type ElkNode } from 'elkjs/lib/elk-api.js';
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

// One worker for the page, started on the first layout: elk-api numbers its messages, so layouts may overlap.
let elk: InstanceType<typeof ELK> | undefined;
const layoutOf = (g: ElkNode) =>
  (elk ??= new ELK({
    workerFactory: () => new Worker(new URL('./layout.worker.ts', import.meta.url), { type: 'module' }),
  })).layout(g);

export async function place(nodes: GNode[], links: GLink[]): Promise<Placed> {
  const g: ElkNode = await layoutOf(elkInput(nodes, links));
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

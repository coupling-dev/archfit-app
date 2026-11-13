import ELK, { type ElkNode } from 'elkjs/lib/elk-api.js';
import { lineLabel, mapLayers, mapLines, type MapModel, type MapModule, type MapLine } from './model';

export interface MapNode extends MapModule {
  x: number;
  y: number;
  width: number;
  height: number;
}
export interface MapBand {
  name: string;
  y: number;
  height: number;
}
export interface MapEdge {
  line: MapLine;
  path: string;
  labelX: number;
  labelY: number;
}
export interface MapLayout {
  width: number;
  height: number;
  nodes: MapNode[];
  bands: MapBand[];
  edges: MapEdge[];
}

type Layout = (graph: ElkNode) => Promise<ElkNode>;

// Each declared layer is laid out by ELK separately, then stacked: disconnected modules cannot escape their band.
export async function placeBands(model: MapModel, layout: Layout): Promise<MapLayout> {
  const lines = mapLines(model.lines);
  const layers = mapLayers(model);
  const nodes: MapNode[] = [];
  const bands: MapBand[] = [];
  const lanes = new Map<string, Map<MapLine, number>>();
  let y = 12;
  let width = 0;
  for (const name of layers) {
    const modules = model.modules.filter((m) => m.layer === name);
    if (!modules.length) continue;
    const touching = lines.filter((l) => modules.some((m) => m.name === l.from || m.name === l.to));
    lanes.set(name, new Map(touching.map((l, i) => [l, i])));
    const graph = await layout({
      id: 'band',
      layoutOptions: { 'elk.algorithm': 'box', 'elk.spacing.nodeNode': '40', 'elk.aspectRatio': '10000' },
      children: modules.map((m, i) => ({
        id: String(i),
        width: Math.max(180, m.name.length * 9 + 32, m.volatility.length * 8 + 110),
        height: 64,
      })),
    });
    const height = (graph.height ?? 84) + 50 + touching.length * 30;
    for (const [i, m] of modules.entries()) {
      const p = graph.children?.find((n) => n.id === String(i));
      if (!p) throw new Error('A module could not be placed.');
      nodes.push({ ...m, x: (p.x ?? 0) + 20, y: (p.y ?? 0) + y + 30, width: p.width ?? 180, height: 64 });
    }
    bands.push({ name, y, height });
    width = Math.max(width, (graph.width ?? 200) + 40);
    y += height + 20;
  }
  const labelWidth = Math.max(0, ...lines.map((l) => lineLabel(l).length * 8));
  const right = width + labelWidth + 24;
  const edges = lines.map((line, i): MapEdge => {
    const from = nodes.find((n) => n.name === line.from);
    const to = nodes.find((n) => n.name === line.to);
    if (!from || !to) throw new Error('A dependency has no module.');
    const lane = (node: MapNode) => {
      const band = bands.find((b) => b.name === node.layer);
      if (!band) throw new Error('A module has no layer.');
      return band.y + band.height - 20 - (lanes.get(node.layer)?.get(line) ?? 0) * 30;
    };
    const a = { x: from.x + from.width / 2, y: from.y + from.height };
    const b = { x: to.x + to.width / 2, y: to.y + to.height };
    const ay = lane(from), by = lane(to), x = right + i * 24;
    if (from === to) {
      return {
        line,
        path: `M ${a.x} ${a.y} V ${ay} H ${from.x - 10} V ${from.y + 32} H ${from.x}`,
        labelX: from.x + 4,
        labelY: ay - 6,
      };
    }
    if (from.layer === to.layer) {
      return {
        line,
        path: `M ${a.x} ${a.y} V ${ay} H ${b.x} V ${b.y}`,
        labelX: Math.min(a.x, b.x) + 8,
        labelY: ay - 6,
      };
    }
    return {
      line,
      path: `M ${a.x} ${a.y} V ${ay} H ${x} V ${by} H ${b.x} V ${b.y}`,
      labelX: a.x + 8,
      labelY: ay - 6,
    };
  });
  return {
    width: Math.max(360, right + lines.length * 24 + 20),
    height: Math.max(100, y),
    nodes,
    bands,
    edges,
  };
}

// One worker per mounted map; abort/unmount terminates it. Worker errors reject, never leave an empty picture.
export async function layoutMap(model: MapModel, signal: AbortSignal): Promise<MapLayout> {
  let worker: Worker | undefined;
  const elk = new ELK({
    workerFactory: () => {
      worker = new Worker(new URL('../../lib/layout.worker.ts', import.meta.url), { type: 'module' });
      return worker;
    },
  });
  let abort = () => {};
  const stopped = new Promise<never>((_, reject) => {
    abort = () => reject(new Error('The map was closed.'));
    worker?.addEventListener('error', () => reject(new Error('The layout worker stopped.')), { once: true });
    worker?.addEventListener('messageerror', () => reject(new Error('The layout worker answer could not be read.')), {
      once: true,
    });
  });
  signal.addEventListener('abort', abort, { once: true });
  try {
    if (signal.aborted) throw new Error('The map was closed.');
    return await Promise.race([placeBands(model, (g) => elk.layout(g)), stopped]);
  } finally {
    signal.removeEventListener('abort', abort);
    elk.terminateWorker();
  }
}

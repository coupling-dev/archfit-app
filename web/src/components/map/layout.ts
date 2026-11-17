import ELK, { type ElkNode } from 'elkjs/lib/elk-api.js';
import { lineKinds, mapLayers, mapLines, type LineKind, type MapModel, type MapModule } from './model';
import type { Box } from './viewport';

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
// One line for all the dependencies from one layer to another: the strongest kind wins, and the count is in `count`.
export interface MapLink {
  from: string;
  to: string;
  kind: LineKind;
  count: number;
  path: string;
  labelX: number;
  labelY: number;
}
export interface MapLayout {
  // Where the bands start: the lines between layers run in the margin to the left of them.
  left: number;
  width: number;
  height: number;
  nodes: MapNode[];
  bands: MapBand[];
  links: MapLink[];
  // Text the chart always draws (band names, link counts): the labels of a focused module keep off it.
  avoid: Box[];
}

type Layout = (graph: ElkNode) => Promise<ElkNode>;

// The widest a band's modules may spread. About what a 1280 px page shows at the size that keeps names readable, so a
// band wraps into rows instead of growing the chart sideways.
const ROW = 1250;
const NODE_HEIGHT = 64;
const GAP = 40;
const LABEL = 34;
const PAD = 16;
const SLOT = 12;
const PAIR = 30;

export function nodeWidth(m: MapModule): number {
  return Math.max(140, Math.ceil(m.name.length * 8.5 + 28), Math.ceil(`Volatility: ${m.volatility}`.length * 7 + 28));
}

// Each declared layer is laid out by ELK separately, then stacked: disconnected modules cannot escape their band. One
// line joins each pair of layers on the left, so the chart does not grow with the number of dependencies.
export async function placeBands(model: MapModel, layout: Layout): Promise<MapLayout> {
  const lines = mapLines(model.lines);
  const layers = mapLayers(model).filter((name) => model.modules.some((m) => m.layer === name));
  const layerOf = new Map<string, string>();
  for (const m of model.modules) layerOf.set(m.name, m.layer);
  const pairs = new Map<string, { from: string; to: string; count: number; kind: LineKind }>();
  for (const line of lines) {
    const from = layerOf.get(line.from);
    const to = layerOf.get(line.to);
    if (from === undefined || to === undefined) throw new Error('A dependency has no module.');
    if (from === to) continue;
    const key = JSON.stringify([from, to]);
    const old = pairs.get(key);
    if (!old) pairs.set(key, { from, to, count: 1, kind: line.kind });
    else {
      old.count++;
      if (lineKinds.indexOf(line.kind) < lineKinds.indexOf(old.kind)) old.kind = line.kind;
    }
  }
  const ordered = [...pairs.values()].sort(
    (a, b) => layers.indexOf(a.from) - layers.indexOf(b.from) || layers.indexOf(a.to) - layers.indexOf(b.to),
  );
  const gutter = 12 + Math.max(1, ordered.length) * PAIR;
  const slots = new Map<string, number>();
  const slot = (layer: string) => {
    const n = slots.get(layer) ?? 0;
    slots.set(layer, n + 1);
    return n;
  };
  const ends = ordered.map((p) => ({ p, a: slot(p.from), b: slot(p.to) }));

  const nodes: MapNode[] = [];
  const bands: MapBand[] = [];
  let y = 24;
  let width = 0;
  for (const name of layers) {
    const modules = model.modules.filter((m) => m.layer === name);
    const widths = modules.map(nodeWidth);
    const rows = Math.max(1, Math.ceil(widths.reduce((n, w) => n + w + GAP, 0) / ROW));
    const graph = await layout({
      id: 'band',
      layoutOptions: {
        'elk.algorithm': 'box',
        'elk.spacing.nodeNode': String(GAP),
        'elk.aspectRatio': String(ROW / (rows * (NODE_HEIGHT + GAP))),
      },
      children: modules.map((m, i) => ({ id: String(i), width: widths[i], height: NODE_HEIGHT })),
    });
    const height = Math.max(LABEL + (graph.height ?? NODE_HEIGHT) + PAD, LABEL + (slots.get(name) ?? 0) * SLOT + PAD);
    for (const [i, m] of modules.entries()) {
      const p = graph.children?.find((n) => n.id === String(i));
      if (!p) throw new Error('A module could not be placed.');
      nodes.push({ ...m, x: (p.x ?? 0) + gutter + PAD, y: (p.y ?? 0) + y + LABEL, width: widths[i], height: NODE_HEIGHT });
    }
    bands.push({ name, y, height });
    width = Math.max(width, (graph.width ?? 200) + 2 * PAD);
    y += height + 20;
  }
  const bandOf = (name: string) => bands.find((b) => b.name === name)!;
  const links = ends.map(({ p, a, b }, i): MapLink => {
    const x = 12 + i * PAIR;
    const ya = bandOf(p.from).y + LABEL + a * SLOT;
    const yb = bandOf(p.to).y + LABEL + b * SLOT;
    return {
      ...p,
      path: `M ${gutter} ${ya} H ${x} V ${yb} H ${gutter - 2}`,
      labelX: x,
      labelY: (ya + yb) / 2,
    };
  });
  const avoid: Box[] = [
    ...bands.map((b) => ({ x: gutter + 12, y: b.y + 6, width: (b.name || 'No layer').length * 7 + 8, height: 18 })),
    ...links.map((l) => ({ x: l.labelX - 12, y: l.labelY - 14, width: 24, height: 18 })),
  ];
  return { left: gutter, width: Math.max(360, gutter + width + 8), height: Math.max(100, y), nodes, bands, links, avoid };
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

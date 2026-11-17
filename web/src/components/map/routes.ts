import type { MapNode } from './layout';
import { focusLabel, type MapLine } from './model';
import type { Box } from './viewport';

export interface Route {
  line: MapLine;
  path: string;
  labelX: number;
  labelY: number;
}

type Side = 'top' | 'bottom';
interface End {
  line: MapLine;
  node: MapNode;
  other: MapNode;
  side: Side;
  role: 'from' | 'to';
}

const center = (n: MapNode) => n.x + n.width / 2;
// A label is 14 px semibold: about 7.6 px a character and 18 px high.
const CHAR = 7.6;
const LINE = 18;

// Lines of the module in focus only. A line leaves the near side of its module toward the other one and arrives at the
// near side of that one; where several share a side they spread along it, ordered by where the other end is, so they
// do not cross at the module. Same-row lines arch over the row.
export interface Area {
  width: number;
  height: number;
  // Other text on the chart that a label must not cover.
  avoid: Box[];
}

export function routeLines(nodes: MapNode[], lines: MapLine[], focused: string, area: Area): Route[] {
  const byName = new Map(nodes.map((n) => [n.name, n]));
  const mine = lines.filter((l) => l.from === focused || l.to === focused);
  const sides = (from: MapNode, to: MapNode): [Side, Side] =>
    to.y > from.y ? ['bottom', 'top'] : to.y < from.y ? ['top', 'bottom'] : ['top', 'top'];
  const ends: End[] = [];
  for (const line of mine) {
    const from = byName.get(line.from);
    const to = byName.get(line.to);
    if (!from || !to) continue;
    const [a, b] = sides(from, to);
    ends.push({ line, node: from, other: to, side: a, role: 'from' }, { line, node: to, other: from, side: b, role: 'to' });
  }
  const port = (end: End): { x: number; y: number } => {
    const group = ends
      .filter((e) => e.node === end.node && e.side === end.side)
      .sort((p, q) => center(p.other) - center(q.other) || p.line.from.localeCompare(q.line.from) || p.line.to.localeCompare(q.line.to));
    const rank = group.indexOf(end);
    return {
      x: end.node.x + (end.node.width * (rank + 1)) / (group.length + 1),
      y: end.side === 'top' ? end.node.y : end.node.y + end.node.height,
    };
  };
  // Labels keep off every module and off the labels placed before them.
  const taken = [...nodes, ...area.avoid].map((n) => ({ x0: n.x, x1: n.x + n.width, y0: n.y, y1: n.y + n.height }));
  // Move a label down, then up, in steps until it is clear.
  // Labels stay inside the chart; the search is wide enough that it ends well before running out for any real module.
  const clear = (wanted: number, y: number, text: string): [number, number] => {
    const half = (text.length * CHAR) / 2;
    const x = Math.min(Math.max(wanted, half + 4), Math.max(half + 4, area.width - half - 4));
    // Where nothing is free, the position that covers the least is used, and it is reserved like any other.
    let best = { at: y, hits: Infinity };
    // Candidates step up and down, and sideways along the line, so a crowded line finds room near its own path.
    let bestX = x;
    for (let n = 0; n < 400 && best.hits > 0; n++) {
      const dy = (n % 2 ? 1 : -1) * Math.ceil((n >> 2) / 2) * LINE;
      const dx = ((n >> 1) % 2 ? 1 : -1) * Math.ceil(((n >> 2) + 1) / 2) * 36;
      const cx = Math.min(Math.max(x + dx, half + 4), Math.max(half + 4, area.width - half - 4));
      const at = Math.min(Math.max(y + dy, LINE + 2), area.height - 4);
      const box = { x0: cx - half, x1: cx + half, y0: at - LINE, y1: at };
      const hits = taken.filter((t) => box.x0 < t.x1 && t.x0 < box.x1 && box.y0 < t.y1 && t.y0 < box.y1).length;
      if (hits < best.hits) {
        best = { at, hits };
        bestX = cx;
      }
    }
    taken.push({ x0: bestX - half, x1: bestX + half, y0: best.at - LINE, y1: best.at });
    return [bestX, best.at];
  };
  return mine.flatMap((line): Route[] => {
    const from = byName.get(line.from);
    const to = byName.get(line.to);
    if (!from || !to) return [];
    const a = port(ends.find((e) => e.line === line && e.role === 'from')!);
    const b = port(ends.find((e) => e.line === line && e.role === 'to')!);
    if (from === to) {
      const x = from.x + from.width;
      const text = focusLabel(line);
      const [labelX, labelY] = clear(x + 12 + (text.length * CHAR) / 2, from.y + 36, text);
      return [{
        line,
        path: `M ${x} ${from.y + 20} C ${x + 50} ${from.y + 20}, ${x + 50} ${from.y + 44}, ${x} ${from.y + 44}`,
        labelX,
        labelY,
      }];
    }
    const sameRow = from.y === to.y;
    // An arch stays under 60 px, so it never leaves the top of the chart.
    const reach = sameRow ? 40 + Math.min(20, Math.abs(b.x - a.x) / 20) : Math.max(40, Math.abs(b.y - a.y) / 2);
    const out = a.y === from.y ? -reach : reach;
    const into = b.y === to.y ? -reach : reach;
    // Near the arrowhead the lines of one module are furthest apart, so a label there is the easiest to read.
    const t = 0.8;
    const at = (p0: number, p1: number, p2: number, p3: number) =>
      (1 - t) ** 3 * p0 + 3 * (1 - t) ** 2 * t * p1 + 3 * (1 - t) * t ** 2 * p2 + t ** 3 * p3;
    const [labelX, labelY] = clear(at(a.x, a.x, b.x, b.x), at(a.y, a.y + out, b.y + into, b.y), focusLabel(line));
    return [{
      line,
      path: `M ${a.x} ${a.y} C ${a.x} ${a.y + out}, ${b.x} ${b.y + into}, ${b.x} ${b.y}`,
      labelX,
      labelY,
    }];
  });
}

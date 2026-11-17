import type { MapNode } from './layout';
import { focusLabel, type MapLine } from './model';

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
export function routeLines(nodes: MapNode[], lines: MapLine[], focused: string): Route[] {
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
  const taken = nodes.map((n) => ({ x0: n.x, x1: n.x + n.width, y0: n.y, y1: n.y + n.height }));
  // Move a label down, then up, in steps until it is clear.
  const clear = (x: number, y: number, text: string) => {
    const half = (text.length * CHAR) / 2;
    for (let n = 0; n < 24; n++) {
      const dy = (n % 2 ? 1 : -1) * Math.ceil(n / 2) * LINE;
      const box = { x0: x - half, x1: x + half, y0: y + dy - LINE, y1: y + dy };
      if (!taken.some((t) => box.x0 < t.x1 && t.x0 < box.x1 && box.y0 < t.y1 && t.y0 < box.y1)) {
        taken.push(box);
        return y + dy;
      }
    }
    return y;
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
      const labelX = x + 12 + (text.length * CHAR) / 2;
      return [{
        line,
        path: `M ${x} ${from.y + 20} C ${x + 50} ${from.y + 20}, ${x + 50} ${from.y + 44}, ${x} ${from.y + 44}`,
        labelX,
        labelY: clear(labelX, from.y + 36, text),
      }];
    }
    const sameRow = from.y === to.y;
    const reach = sameRow ? 50 + Math.abs(b.x - a.x) / 4 : Math.max(40, Math.abs(b.y - a.y) / 2);
    const out = a.y === from.y ? -reach : reach;
    const into = b.y === to.y ? -reach : reach;
    // Near the arrowhead the lines of one module are furthest apart, so a label there is the easiest to read.
    const t = 0.8;
    const at = (p0: number, p1: number, p2: number, p3: number) =>
      (1 - t) ** 3 * p0 + 3 * (1 - t) ** 2 * t * p1 + 3 * (1 - t) * t ** 2 * p2 + t ** 3 * p3;
    const labelX = at(a.x, a.x, b.x, b.x);
    return [{
      line,
      path: `M ${a.x} ${a.y} C ${a.x} ${a.y + out}, ${b.x} ${b.y + into}, ${b.x} ${b.y}`,
      labelX,
      labelY: clear(labelX, at(a.y, a.y + out, b.y + into, b.y), focusLabel(line)),
    }];
  });
}

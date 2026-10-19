// The drawing of one dependency cycle: the same picture as the PR comment's Mermaid diagram. One box per module, one
// arrow per direction labelled with its location count. A direction the pull request added is red and wide, one that
// existed before grey and thin, and one the report cannot place red and heavier for more locations.

export interface Direction {
  from: string;
  to: string;
  origin: string; // 'added' | 'existing' | ''
  locations: string[];
}

export interface Box {
  module: string;
  label: string; // the module, shortened to fit; the full name goes in a title
  x: number; // centre
  y: number;
  w: number;
  h: number;
}

export interface Arrow {
  d: string; // SVG path
  kind: 'added' | 'existing' | 'cycle';
  width: number;
  label: string;
  lx: number;
  ly: number;
}

const charWidth = 7.4; // 13px system UI font, an average glyph
const maxLabel = 28;
const boxH = 32;
const pad = 16;
const bend = 0.22; // how far each arrow bows from the straight line, as a share of its length; opposite directions bow apart
const thinBelow = 3;
const mediumBelow = 10;

export function shorten(module: string): string {
  return module.length <= maxLabel ? module : '…' + module.slice(module.length - maxLabel + 1);
}

function arrowStyle(d: Direction): Pick<Arrow, 'kind' | 'width'> {
  const n = d.locations.length;
  if (d.origin === 'added') return { kind: 'added', width: 4 };
  if (d.origin === 'existing') return { kind: 'existing', width: 1.5 };
  return { kind: 'cycle', width: n < thinBelow ? 2 : n < mediumBelow ? 3 : 4 };
}

// clip moves a point from a box's centre toward (tx, ty) until it leaves the box, so an arrow starts and ends at the
// border instead of under the label.
function clip(b: Box, tx: number, ty: number): [number, number] {
  const dx = tx - b.x;
  const dy = ty - b.y;
  const sx = dx === 0 ? Infinity : b.w / 2 / Math.abs(dx);
  const sy = dy === 0 ? Infinity : b.h / 2 / Math.abs(dy);
  const s = Math.min(sx, sy);
  return [b.x + dx * s, b.y + dy * s];
}

export function layout(
  modules: string[],
  directions: Direction[],
): { width: number; height: number; boxes: Box[]; arrows: Arrow[] } {
  const n = modules.length;
  const sizes = modules.map((m) => Math.ceil(shorten(m).length * charWidth) + 2 * pad);
  const widest = Math.max(...sizes);
  // Two modules side by side; more on a circle wide enough that neighbours do not touch.
  const rx = n === 2 ? widest / 2 + 150 : Math.max(widest * 0.9, (n * (widest + 24)) / (2 * Math.PI));
  const ry = n === 2 ? 0 : Math.max(70, rx * 0.6);
  const width = Math.ceil(2 * rx + widest + 2 * pad);
  const height = Math.ceil(2 * ry + boxH + 2 * pad + (n === 2 ? 100 : 40));
  const cx = width / 2;
  const cy = height / 2;
  const boxes = modules.map((module, i) => {
    const a = n === 2 ? Math.PI * (1 - i) : -Math.PI / 2 + (2 * Math.PI * i) / n;
    return { module, label: shorten(module), x: cx + rx * Math.cos(a), y: cy + ry * Math.sin(a), w: sizes[i], h: boxH };
  });
  const at = new Map(boxes.map((b) => [b.module, b]));
  const arrows: Arrow[] = [];
  for (const d of directions) {
    const a = at.get(d.from);
    const b = at.get(d.to);
    if (!a || !b) continue;
    // The control point sits off the midpoint, on the left of the travel direction: A→B and B→A bow to opposite sides.
    const len = Math.hypot(b.x - a.x, b.y - a.y) || 1;
    const nx = (b.y - a.y) / len;
    const ny = -(b.x - a.x) / len;
    const qx = (a.x + b.x) / 2 + nx * len * bend;
    const qy = (a.y + b.y) / 2 + ny * len * bend;
    const [x1, y1] = clip(a, qx, qy);
    const [x2, y2] = clip(b, qx, qy);
    const n = d.locations.length;
    arrows.push({
      d: `M ${r(x1)} ${r(y1)} Q ${r(qx)} ${r(qy)} ${r(x2)} ${r(y2)}`,
      ...arrowStyle(d),
      label: `${n} ${n === 1 ? 'location' : 'locations'}`,
      // the curve's own midpoint: halfway between the chord's midpoint and the control point
      lx: r((x1 + x2) / 4 + qx / 2),
      ly: r((y1 + y2) / 4 + qy / 2),
    });
  }
  return { width, height, boxes, arrows };
}

function r(v: number): number {
  return Math.round(v * 10) / 10;
}

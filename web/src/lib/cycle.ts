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
  label: string; // "" when no spot along the arrow is free
  lx: number;
  ly: number;
}

const charWidth = 7.4; // 13px system UI font, an average glyph
const maxLabel = 28;
const boxH = 32;
const pad = 16;
const bend = 0.22; // how far each arrow bows from the straight line, as a share of its length; opposite directions bow apart
// A cycle of many modules keeps a drawing of readable size: past this radius neighbours may touch, and the table under
// the drawing still lists every direction.
const maxRadius = 480;
const labelGlyph = 6.8; // the 12px arrow label font, an average glyph
const labelH = 16;
const spots = [0.5, 0.35, 0.65, 0.25, 0.75]; // where along its arrow a label may sit, the middle first
const columnGap = 120; // between the centres of two modules stacked in a column
const columnBend = 0.5; // a column's arrows are short: they bow further, so the two directions and their labels part
const widestLabel = '999 locations'.length * labelGlyph + 8;
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

// Rect is an area by its centre and size: a module's box or an arrow's label.
interface Rect {
  x: number;
  y: number;
  w: number;
  h: number;
}

// labelArea is the area an arrow's label covers, centred on (x, y).
function labelArea(text: string, x: number, y: number): Rect {
  return { x, y, w: text.length * labelGlyph + 8, h: labelH };
}

function overlaps(a: Rect, b: Rect): boolean {
  return Math.abs(a.x - b.x) * 2 < a.w + b.w && Math.abs(a.y - b.y) * 2 < a.h + b.h;
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

// layout places the modules side by side or on a circle; column stacks a pair top to bottom, for a narrow screen, where
// the wide drawing would be scaled down until its labels cannot be read. A longer cycle stays a circle: in a column its
// arrows between non-adjacent modules would run under the modules between them.
export function layout(
  modules: string[],
  directions: Direction[],
  column = false,
): { width: number; height: number; boxes: Box[]; arrows: Arrow[] } {
  const n = modules.length;
  const sizes = modules.map((m) => Math.ceil(shorten(m).length * charWidth) + 2 * pad);
  const widest = Math.max(...sizes);
  if (column && n === 2) {
    // A curve bows half its control point's offset; its label sits on the curve, so both fit in half the width.
    const bow = (columnGap * columnBend) / 2 + widestLabel / 2;
    const width = Math.ceil(Math.max(widest, 2 * bow) + 2 * pad);
    const height = Math.ceil(columnGap + boxH + 2 * pad);
    const boxes = modules.map((module, i) => ({
      module,
      label: shorten(module),
      x: width / 2,
      y: pad + boxH / 2 + i * columnGap,
      w: sizes[i],
      h: boxH,
    }));
    return { width, height, boxes, arrows: arrowsOf(boxes, directions, columnBend) };
  }
  // Two modules side by side; more on a circle wide enough that neighbours do not touch and an arrow between two of
  // them has room for its label, up to maxRadius.
  const rx =
    n === 2 ? widest / 2 + 150 : Math.min(maxRadius, Math.max(widest + 80, (n * (widest + 24)) / (2 * Math.PI)));
  const ry = n === 2 ? 0 : Math.max(90, rx * 0.6);
  const width = Math.ceil(2 * rx + widest + 2 * pad);
  const height = Math.ceil(2 * ry + boxH + 2 * pad + (n === 2 ? 100 : 40));
  const cx = width / 2;
  const cy = height / 2;
  const boxes = modules.map((module, i) => {
    const a = n === 2 ? Math.PI * (1 - i) : -Math.PI / 2 + (2 * Math.PI * i) / n;
    return { module, label: shorten(module), x: cx + rx * Math.cos(a), y: cy + ry * Math.sin(a), w: sizes[i], h: boxH };
  });
  return { width, height, boxes, arrows: arrowsOf(boxes, directions, bend) };
}

// arrowsOf draws one bowed arrow per direction and labels each where its label covers no module and no other label.
function arrowsOf(boxes: Box[], directions: Direction[], bow: number): Arrow[] {
  const at = new Map(boxes.map((b) => [b.module, b]));
  const arrows: Arrow[] = [];
  const taken: Rect[] = [...boxes]; // what a label may not cover: the modules and the labels placed so far
  for (const d of directions) {
    const a = at.get(d.from);
    const b = at.get(d.to);
    if (!a || !b) continue;
    // The control point sits off the midpoint, on the left of the travel direction: A→B and B→A bow to opposite sides.
    const len = Math.hypot(b.x - a.x, b.y - a.y) || 1;
    const nx = (b.y - a.y) / len;
    const ny = -(b.x - a.x) / len;
    const qx = (a.x + b.x) / 2 + nx * len * bow;
    const qy = (a.y + b.y) / 2 + ny * len * bow;
    const [x1, y1] = clip(a, qx, qy);
    const [x2, y2] = clip(b, qx, qy);
    const count = d.locations.length;
    const text = `${count} ${count === 1 ? 'location' : 'locations'}`;
    // The first spot along the curve whose label covers no module and no other label. With none free, the arrow goes
    // unlabelled: the table under the drawing lists every direction's locations.
    const spot = spots
      .map((t): Rect => {
        const u = 1 - t;
        const x = u * u * x1 + 2 * u * t * qx + t * t * x2;
        const y = u * u * y1 + 2 * u * t * qy + t * t * y2;
        return labelArea(text, x, y);
      })
      .find((c) => !taken.some((o) => overlaps(c, o)));
    if (spot) taken.push(spot);
    arrows.push({
      d: `M ${r(x1)} ${r(y1)} Q ${r(qx)} ${r(qy)} ${r(x2)} ${r(y2)}`,
      ...arrowStyle(d),
      label: spot ? text : '',
      lx: spot ? r(spot.x) : 0,
      ly: spot ? r(spot.y) : 0,
    });
  }
  return arrows;
}

function r(v: number): number {
  return Math.round(v * 10) / 10;
}

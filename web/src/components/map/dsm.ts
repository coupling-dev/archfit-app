import { mapLayers, type LineKind, type MapLine, type MapModel } from './model';

export const symbols: Record<LineKind, string> = {
  allowed: '✓',
  deny: '×',
  requested: '?',
  drift: '!',
  exception: '◷',
};
const statuses: Record<LineKind, string> = {
  allowed: 'Explicit allow',
  deny: 'Explicit deny',
  requested: 'Request',
  drift: 'Drift',
  exception: 'Exception',
};

export const pairKey = (from: string, to: string): string => JSON.stringify([from, to]);

// Index the raw lines: diagram precedence must never discard a rule, request or exception here.
export function pairIndex(lines: MapLine[]): Map<string, MapLine[]> {
  const index = new Map<string, MapLine[]>();
  for (const line of lines) {
    const key = pairKey(line.from, line.to);
    const pair = index.get(key);
    if (pair) pair.push(line);
    else index.set(key, [line]);
  }
  return index;
}

export function statusLabel(line: MapLine): string {
  return [
    statuses[line.kind],
    line.rule && `rule: ${line.rule}`,
    line.request !== undefined && `request #${line.request}`,
    line.expires && `expires ${line.expires}`,
    line.days_left !== undefined && `${line.days_left} days left`,
  ]
    .filter((part) => typeof part === 'string' && part.length)
    .join(' · ');
}

export interface MatrixFilters {
  search: string;
  focus: string;
  direction: 'inbound' | 'outbound';
  attention: boolean;
  collapsed: string[];
}

export function visibleModules(model: MapModel, filters: MatrixFilters) {
  const layers = mapLayers(model);
  const search = filters.search.trim().toLowerCase();
  const neighbors = new Set([filters.focus]);
  const attention = new Set<string>();
  for (const line of model.lines) {
    if (line.kind !== 'allowed') {
      attention.add(line.from);
      attention.add(line.to);
    }
    if (filters.direction === 'outbound' && line.from === filters.focus) neighbors.add(line.to);
    if (filters.direction === 'inbound' && line.to === filters.focus) neighbors.add(line.from);
  }
  return model.modules
    .filter(
      (m) =>
        !filters.collapsed.includes(m.layer) &&
        (!search || `${m.name} ${m.layer}`.toLowerCase().includes(search)) &&
        (!filters.focus || neighbors.has(m.name)) &&
        (!filters.attention || attention.has(m.name)),
    )
    .sort((a, b) => layers.indexOf(a.layer) - layers.indexOf(b.layer) || a.name.localeCompare(b.name));
}

export interface Cell {
  row: number;
  col: number;
}
const navKeys = new Set(['ArrowDown', 'ArrowUp', 'ArrowLeft', 'ArrowRight', 'Home', 'End']);
export const isNavKey = (key: string): boolean => navKeys.has(key);

// Where a navigation key moves from a cell, skipping cells that cannot take focus. The result is the same cell when
// nothing further is reachable. Home and End go to the row's ends (Ctrl: the table's first and last row).
export function nextCell(key: string, ctrl: boolean, from: Cell, size: number, open: (cell: Cell) => boolean): Cell {
  const last = size - 1;
  const step = (dRow: number, dCol: number): Cell => {
    let { row, col } = from;
    for (;;) {
      row += dRow;
      col += dCol;
      if (row < 0 || col < 0 || row > last || col > last) return from;
      if (open({ row, col })) return { row, col };
    }
  };
  const edge = (dCol: 1 | -1): Cell => {
    const row = ctrl ? (dCol === 1 ? 0 : last) : from.row;
    for (let col = dCol === 1 ? 0 : last; col >= 0 && col <= last; col += dCol) {
      if (open({ row, col })) return { row, col };
    }
    return from;
  };
  switch (key) {
    case 'ArrowDown': return step(1, 0);
    case 'ArrowUp': return step(-1, 0);
    case 'ArrowRight': return step(0, 1);
    case 'ArrowLeft': return step(0, -1);
    case 'Home': return edge(1);
    case 'End': return edge(-1);
    default: return from;
  }
}

// From this many modules the matrix draws 32 px cells and vertical column names, so 24 columns fit a laptop page.
export const compactFrom = 12;
// The App opens an expiry issue seven days before an exception ends, so that is "near" here too.
export const soonDays = 7;

// Split a module name where a line may break: after "_" and "-", never inside a word.
export function breakable(name: string): string[] {
  return name.match(/[^_-]*[_-]|[^_-]+$/g) ?? [name];
}

export interface CellView {
  kind: LineKind;
  count: number;
  // An exception that ends within soonDays, or already ended: the only exception drawn strongly.
  soon: boolean;
  // Only exceptions that are far off: drawn quiet so they do not mark every allow.
  quiet: boolean;
}

const isSoon = (line: MapLine) => line.kind === 'exception' && line.days_left !== undefined && line.days_left <= soonDays;

// One symbol per cell: the strongest status, with the number of statuses when there are several. Order: drift,
// deny, an exception that is near its date, request, an exception with no date, allow, then a far exception. An
// undated exception is never read as a pass, so it outranks allow. Every status stays in the label and in the
// details, so nothing is dropped, only ranked.
export function cellView(lines: MapLine[]): CellView | null {
  if (!lines.length) return null;
  const order = ['drift', 'deny', 'soon', 'requested', 'undated', 'allowed', 'far'];
  const rank = (line: MapLine) => {
    if (line.kind !== 'exception') return order.indexOf(line.kind);
    return order.indexOf(isSoon(line) ? 'soon' : line.days_left === undefined ? 'undated' : 'far');
  };
  const top = lines.reduce((best, line) => (rank(line) < rank(best) ? line : best));
  const far = top.kind === 'exception' && top.days_left !== undefined && !isSoon(top);
  return { kind: top.kind, count: lines.length, soon: isSoon(top), quiet: far };
}

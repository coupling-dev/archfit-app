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

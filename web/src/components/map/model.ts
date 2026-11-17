// Public contract for Home and the request page: pass a MapModel, including just two modules for a request.
// Layers are outer-first ('' is the no-layer band). No positions, old report types or API calls are needed.
export interface MapModule {
  name: string;
  layer: string;
  volatility: string;
}
export type LineKind = 'allowed' | 'requested' | 'deny' | 'exception' | 'drift';
export interface MapLine {
  kind: LineKind;
  from: string;
  to: string;
  rule?: string;
  request?: number;
  expires?: string;
  days_left?: number;
}
export interface MapModel {
  layers: string[];
  modules: MapModule[];
  lines: MapLine[];
}

export const lineKinds: LineKind[] = ['drift', 'deny', 'requested', 'exception', 'allowed'];
export const lineWords: Record<LineKind, string> = {
  allowed: 'Allowed dependency',
  requested: 'Requested dependency',
  deny: 'A rule denies this dependency',
  exception: 'Exception',
  drift: 'Drift',
};

// A losing kind is not drawn, even if it has a request number or an exception date.
export function mapLayers(model: MapModel): string[] {
  const names = [...new Set([...model.layers, ...model.modules.map((m) => m.layer)])];
  return [...names.filter(Boolean), ...(names.includes('') ? [''] : [])];
}

export function mapLines(lines: MapLine[]): MapLine[] {
  const pairs = new Map<string, MapLine>();
  for (const line of lines) {
    const key = JSON.stringify([line.from, line.to]);
    const old = pairs.get(key);
    if (!old || lineKinds.indexOf(line.kind) < lineKinds.indexOf(old.kind)) pairs.set(key, line);
  }
  return [...pairs.values()];
}

export function lineLabel(line: MapLine): string {
  const label =
    line.kind === 'requested' && line.request
      ? '#' + line.request
      : line.kind === 'exception'
        ? line.expires
        : line.rule;
  return lineWords[line.kind] + (label ? ' · ' + label : '');
}

// The legend and caption of the diagram, in the words of design 12.2. The Matrix reuses them.
export const legendKinds: LineKind[] = ['allowed', 'requested', 'deny', 'exception', 'drift'];
export const legendWords: Record<LineKind, string> = {
  allowed: 'An allowed dependency',
  requested: 'A requested dependency',
  deny: 'One a rule denies',
  exception: 'An exception (with its date)',
  drift: 'Drift',
};
export const mapCaption =
  'An arrow points at the module it depends on. Outer layer on top. Allowed dependencies, not observed imports.';

// A line named by both ends, for the module in focus: "handlers → api · exception until 2027-01-15".
export function focusLabel(line: MapLine): string {
  const status: Record<LineKind, string> = {
    allowed: 'allowed',
    requested: line.request ? 'requested #' + line.request : 'requested',
    deny: 'a rule denies it' + (line.rule ? ' · ' + line.rule : ''),
    exception: line.expires ? 'exception until ' + line.expires : 'exception',
    drift: 'drift' + (line.rule ? ' · ' + line.rule : ''),
  };
  return `${line.from} → ${line.to} · ${status[line.kind]}`;
}

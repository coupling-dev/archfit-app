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

import type { LineKind, MapLine, MapModel } from './model';

// A repository the size of ccgram: 24 modules in 3 layers and 106 lines of every kind. Built from fixed rules, not a
// random generator, so tests and screenshots see the same picture on every run.
const layers = ['adapter', 'application', 'core'];
const names = [
  'handlers', 'api', 'telegram', 'tmux', 'terminal', 'providers', 'hooks', 'monitor',
  'sessions', 'topics', 'messages', 'commands', 'windows', 'polling', 'queue', 'settings',
  'state', 'events', 'paths', 'storage', 'claude', 'codex', 'gemini', 'shell',
];
const volatility = ['high', 'medium', 'low'];

export function bigMap(): MapModel {
  const modules = names.map((name, i) => ({ name, layer: layers[Math.floor(i / 8)], volatility: volatility[i % 3] }));
  const pairs: [number, number][] = [];
  for (let a = 0; a < names.length; a++)
    for (let b = 0; b < names.length; b++) if (a !== b && Math.floor(a / 8) <= Math.floor(b / 8)) pairs.push([a, b]);
  // Spread the chosen pairs over the whole grid with a fixed hash, then keep the first 106.
  const chosen = pairs
    .map(([a, b]): [number, number, number] => [(a * 31 + b * 17 + 7) % 101, a, b])
    .sort((x, y) => x[0] - y[0] || x[1] - y[1] || x[2] - y[2])
    .slice(0, 106);
  const kinds: Partial<Record<number, LineKind>> = { 3: 'drift', 40: 'deny', 77: 'drift' };
  const lines = chosen.map(([, a, b], i): MapLine => {
    const kind = kinds[i] ?? (i % 17 === 0 ? 'requested' : i % 9 === 0 ? 'exception' : 'allowed');
    const line: MapLine = { kind, from: names[a], to: names[b] };
    if (kind === 'requested') line.request = 400 + i;
    if (kind === 'exception') line.expires = '2027-01-15';
    if (kind === 'deny' || kind === 'drift') line.rule = 'graph';
    return line;
  });
  return { layers, modules, lines };
}

import { describe, expect, it } from 'vitest';
import { readHome } from '../../lib/home';
import { pairIndex, pairKey, statusLabel, visibleModules } from './dsm';
import type { MapModel, MapLine } from './model';

const lines: MapLine[] = [
  { from: 'app', to: 'store', kind: 'allowed' },
  { from: 'app', to: 'store', kind: 'deny', rule: 'boundary' },
  { from: 'app', to: 'store', kind: 'deny', rule: 'second-rule' },
  { from: 'app', to: 'store', kind: 'requested', request: 42 },
  { from: 'app', to: 'store', kind: 'requested', request: 43 },
  { from: 'app', to: 'store', kind: 'exception', expires: '2027-01-01', days_left: 0 },
  { from: 'app', to: 'store', kind: 'drift', rule: 'graph' },
];
const model: MapModel = {
  layers: ['outer', 'inner'],
  modules: [
    { name: 'store', layer: 'inner', volatility: 'low' },
    { name: 'zebra', layer: 'outer', volatility: 'high' },
    { name: 'app', layer: 'outer', volatility: 'high' },
    { name: 'alone', layer: '', volatility: 'low' },
  ],
  lines,
};

describe('DSM raw pair index', () => {
  it('preserves every status, rule, request and expiry from the Home payload without overwriting', () => {
    const parsed = readHome({ status: 'clean', map: model }).map;
    expect(parsed).toBeDefined();
    const index = pairIndex(parsed?.lines ?? []);
    expect(index.get(pairKey('app', 'store'))).toEqual(lines);
    expect(index.get(pairKey('store', 'app'))).toBeUndefined();
    expect(index.size).toBe(1);
    expect(lines).toHaveLength(7);
  });

  it('keeps direction, self-pairs and delimiter-containing names distinct', () => {
    const pairs: MapLine[] = [
      { from: 'a → b', to: 'c', kind: 'deny' },
      { from: 'a', to: 'b → c', kind: 'allowed' },
      { from: 'c', to: 'a → b', kind: 'requested', request: 1 },
      { from: 'c', to: 'c', kind: 'drift' },
    ];
    const index = pairIndex(pairs);
    expect(index.size).toBe(4);
    for (const line of pairs) expect(index.get(pairKey(line.from, line.to))).toEqual([line]);
    expect(pairIndex([]).size).toBe(0);
  });

  it.each([-3, 0, 9])('retains signed server days_left: %s', (days_left) => {
    const line = { ...lines[5], days_left };
    expect(readHome({ status: 'clean', map: { ...model, lines: [line] } }).map?.lines[0]).toEqual(line);
    expect(statusLabel(line)).toContain(`${days_left} days left`);
  });

  it.each(['3', 1.2, null, Infinity])('rejects invalid days_left: %s', (days_left) => {
    expect(() => readHome({ status: 'clean', map: { ...model, lines: [{ ...lines[5], days_left }] } })).toThrow();
  });
});

describe('DSM axes', () => {
  const filters = {
    search: '',
    focus: '',
    direction: 'outbound' as const,
    attention: false,
    collapsed: [] as string[],
  };
  it('orders both axes by payload layer order then module name', () => {
    expect(visibleModules(model, filters).map((m) => m.name)).toEqual(['app', 'zebra', 'store', 'alone']);
    expect(model.modules[0].name).toBe('store');
  });
  it('searches names and layers and hides collapsed layers', () => {
    expect(visibleModules(model, { ...filters, search: ' OUTER ' }).map((m) => m.name)).toEqual(['app', 'zebra']);
    expect(visibleModules(model, { ...filters, collapsed: ['outer'] }).map((m) => m.name)).toEqual(['store', 'alone']);
  });
  it('focuses on explicit inbound or outbound neighbors, not inferred imports', () => {
    expect(visibleModules(model, { ...filters, focus: 'app' }).map((m) => m.name)).toEqual(['app', 'store']);
    expect(visibleModules(model, { ...filters, focus: 'app', direction: 'inbound' }).map((m) => m.name)).toEqual([
      'app',
    ]);
    expect(visibleModules(model, { ...filters, focus: 'store', direction: 'inbound' }).map((m) => m.name)).toEqual([
      'app',
      'store',
    ]);
  });
  it('attention retains pairs with non-allow statuses, including exceptions', () => {
    expect(visibleModules(model, { ...filters, attention: true }).map((m) => m.name)).toEqual(['app', 'store']);
    expect(visibleModules({ ...model, lines: [lines[0]] }, { ...filters, attention: true })).toEqual([]);
    expect(visibleModules({ ...model, lines: [lines[5]] }, { ...filters, attention: true }).map((m) => m.name)).toEqual(
      ['app', 'store'],
    );
  });
});

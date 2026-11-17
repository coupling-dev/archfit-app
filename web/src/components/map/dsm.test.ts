import { describe, expect, it } from 'vitest';
import { readHome } from '../../lib/home';
import { breakable, cellView, isNavKey, nextCell, pairIndex, pairKey, soonDays, statusLabel, visibleModules, type Cell } from './dsm';
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

describe('DSM cell navigation', () => {
  const offDiagonal = (c: Cell) => c.row !== c.col;
  it.each([
    ['ArrowRight', false, { row: 0, col: 1 }, { row: 0, col: 2 }],
    ['ArrowRight', false, { row: 1, col: 0 }, { row: 1, col: 2 }],
    ['ArrowDown', false, { row: 0, col: 1 }, { row: 2, col: 1 }],
    ['ArrowLeft', false, { row: 2, col: 3 }, { row: 2, col: 1 }],
    ['ArrowUp', false, { row: 2, col: 1 }, { row: 0, col: 1 }],
    ['Home', false, { row: 2, col: 3 }, { row: 2, col: 0 }],
    ['Home', false, { row: 0, col: 3 }, { row: 0, col: 1 }],
    ['End', false, { row: 3, col: 0 }, { row: 3, col: 2 }],
    ['Home', true, { row: 2, col: 3 }, { row: 0, col: 1 }],
    ['End', true, { row: 0, col: 1 }, { row: 3, col: 2 }],
  ])('%s (ctrl %s) from %j goes to %j on a diagonal-skipping 4x4 grid', (key, ctrl, from, want) => {
    expect(nextCell(key, ctrl, from, 4, offDiagonal)).toEqual(want);
  });
  it('stays on the cell at an edge, or when nothing else can take focus', () => {
    expect(nextCell('ArrowLeft', false, { row: 0, col: 1 }, 4, offDiagonal)).toEqual({ row: 0, col: 1 });
    expect(nextCell('ArrowUp', false, { row: 1, col: 0 }, 4, offDiagonal)).toEqual({ row: 1, col: 0 });
    expect(nextCell('End', false, { row: 0, col: 0 }, 1, offDiagonal)).toEqual({ row: 0, col: 0 });
  });
  it('knows which keys it handles', () => {
    expect(['ArrowUp', 'Home', 'End'].every(isNavKey)).toBe(true);
    expect(['Enter', 'Tab', 'a'].some(isNavKey)).toBe(false);
  });
});

describe('DSM kind words', () => {
  it('labels every status with the legend words', () => {
    expect(statusLabel({ from: 'a', to: 'b', kind: 'allowed' })).toBe('An allowed dependency');
    expect(statusLabel({ from: 'a', to: 'b', kind: 'requested', request: 7 })).toBe('A requested dependency · request #7');
    expect(statusLabel({ from: 'a', to: 'b', kind: 'deny', rule: 'r' })).toBe('One a rule denies · rule: r');
    expect(statusLabel({ from: 'a', to: 'b', kind: 'exception' })).toBe('An exception');
  });
});

describe('DSM names', () => {
  it.each([
    ['multiplexer_backends', ['multiplexer_', 'backends']],
    ['cli-composition', ['cli-', 'composition']],
    ['a_b-c', ['a_', 'b-', 'c']],
    ['plain', ['plain']],
    ['_lead-', ['_', 'lead-']],
  ])('breaks %s only after _ or -', (name, parts) => {
    expect(breakable(name)).toEqual(parts);
    expect(breakable(name).join('')).toBe(name);
  });
});

describe('DSM cell view', () => {
  const allow: MapLine = { from: 'a', to: 'b', kind: 'allowed' };
  const exception = (days_left?: number): MapLine => ({ from: 'a', to: 'b', kind: 'exception', days_left });
  it('is empty without lines', () => expect(cellView([])).toBeNull());
  it('shows one symbol and no count for one status', () => {
    expect(cellView([allow])).toEqual({ kind: 'allowed', count: 1, soon: false, quiet: false });
  });
  it('shows the strongest status with the number of statuses', () => {
    const view = cellView([allow, { ...allow, kind: 'requested' }, { ...allow, kind: 'deny' }, { ...allow, kind: 'drift' }]);
    expect(view).toMatchObject({ kind: 'drift', count: 4 });
    expect(cellView([allow, allow, allow])).toMatchObject({ kind: 'allowed', count: 3 });
  });
  it.each([
    [soonDays, true],
    [0, true],
    [-2, true],
    [soonDays + 1, false],
  ])('an exception with %s days left is near its date: %s', (days, soon) => {
    expect(cellView([allow, exception(days)])).toMatchObject(
      soon ? { kind: 'exception', soon: true, quiet: false } : { kind: 'allowed', soon: false },
    );
  });
  it('never reads an exception without a date as a pass', () => {
    expect(cellView([allow, exception(undefined)])).toEqual({ kind: 'exception', count: 2, soon: false, quiet: false });
    expect(cellView([exception(undefined), { ...allow, kind: 'requested' }])).toMatchObject({ kind: 'requested' });
  });
  it('draws a far exception alone quiet, and ranks a near one below deny and drift', () => {
    expect(cellView([exception(30)])).toEqual({ kind: 'exception', count: 1, soon: false, quiet: true });
    expect(cellView([exception(1), { ...allow, kind: 'requested' }])).toMatchObject({ kind: 'exception', soon: true });
    expect(cellView([exception(1), { ...allow, kind: 'deny' }])).toMatchObject({ kind: 'deny', soon: false });
  });
});

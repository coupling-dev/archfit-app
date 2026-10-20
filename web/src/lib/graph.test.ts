import { describe, expect, it } from 'vitest';
import {
  balanceWord,
  elkInput,
  focus,
  isAdded,
  layers,
  lineWidth,
  linkStatusWord,
  linkVolatility,
  measured,
  moduleVolatility,
  order,
  problemRows,
  acceptedRows,
  smooth,
  worst,
  type GLink,
  type GNode,
  type Status,
  type View,
} from './graph';

const node = (name: string, layer = '', policy_line = 0, more: Partial<GNode> = {}): GNode => ({
  name,
  layer,
  policy_line,
  volatility: '',
  subdomain: '',
  ...more,
});
const link = (from: string, to: string, status: Status, count = 1, more: Partial<GLink> = {}): GLink => ({
  from,
  to,
  status,
  count,
  origin: '',
  strength: '',
  distance: '',
  volatility: '',
  volatility_source: '',
  imbalance: '',
  accepted: '',
  ...more,
});
const all: Record<Status, boolean> = { violation: true, advisory: true, accepted: true, allowed: true, observed: true };
const view = (nodes: GNode[], links: GLink[]): View => ({
  nodes,
  links,
  findings: [],
  stale: false,
  incomplete: false,
});

describe('words', () => {
  it('an accepted link says what it accepts', () => {
    expect(linkStatusWord(link('a', 'b', 'accepted', 1, { accepted: 'advisory' }))).toBe('accepted advisory');
    expect(linkStatusWord(link('a', 'b', 'accepted', 1, { accepted: 'violation' }))).toBe('accepted violation');
    expect(linkStatusWord(link('a', 'b', 'accepted'))).toBe('accepted');
    expect(linkStatusWord(link('a', 'b', 'advisory', 1, { accepted: 'advisory' }))).toBe('advisory');
  });

  it.each([
    [{ volatility: 'low' }, 'volatility low'],
    [{ volatility: 'undeclared' }, 'volatility undeclared'],
    [{ subdomain: 'core' }, 'subdomain core'],
    [{}, ''],
  ])('a module declares %o', (facts, want) => {
    expect(moduleVolatility(node('m', '', 1, facts))).toBe(want);
  });

  it.each([
    [{ volatility: 'high', volatility_source: 'declared' }, 'high'],
    [{ volatility: 'high', volatility_source: 'cascade' }, 'high (cascade)'],
    [{ volatility: 'undeclared', volatility_source: 'undeclared' }, 'undeclared'],
    [{ volatility: 'medium' }, 'medium'],
    [{}, ''],
  ])('a dependency of %o', (facts, want) => {
    expect(linkVolatility(link('a', 'b', 'observed', 1, facts))).toBe(want);
  });
});

describe('line style', () => {
  it.each([
    ['contract', 1.5, true],
    ['model', 2.5, true],
    ['functional', 3.5, true],
    ['intrusive', 5, true],
    ['', 1.5, false],
    ['unknown', 1.5, false],
  ])('%s is %d wide, measured %s', (strength, width, isMeasured) => {
    const l = link('a', 'b', 'observed', 1, { strength });
    expect(lineWidth(l)).toBe(width);
    expect(measured(l)).toBe(isMeasured);
  });

  it('words the balance only from what the engine reported', () => {
    expect(balanceWord(link('a', 'b', 'advisory', 1, { strength: 'functional', imbalance: 'medium' }))).toBe(
      'imbalanced · medium',
    );
    expect(balanceWord(link('a', 'b', 'observed', 1, { strength: 'model' }))).toBe('no imbalance reported');
    expect(balanceWord(link('a', 'b', 'violation', 0))).toBe('not measured');
  });
});

describe('added in this pull request', () => {
  it('is the comment rule, and only on the pull request graph', () => {
    const added = link('a', 'b', 'violation', 1, { origin: 'added' });
    const existing = link('b', 'a', 'violation', 1, { origin: 'existing' });
    expect(isAdded(added, true)).toBe(true);
    expect(isAdded(existing, true)).toBe(false);
    expect(isAdded(added, false)).toBe(false);
  });
});

describe('problemRows', () => {
  const links = [
    link('a', 'b', 'violation', 2),
    link('b', 'a', 'violation', 40),
    link('c', 'd', 'advisory', 9),
    link('d', 'c', 'accepted', 3),
    link('e', 'f', 'observed', 5, { origin: 'added' }),
    link('g', 'h', 'allowed', 7),
  ];

  it('is one row per active dependency: a cycle is two, accepted debt is none', () => {
    const rows = problemRows(links, false, false, all);
    expect(rows.map((r) => [r.link.from, r.link.to, r.cycle])).toEqual([
      ['b', 'a', true],
      ['a', 'b', true],
      ['c', 'd', false],
    ]);
  });

  it('adds up with the chips: rows are the violations plus the advisories', () => {
    const n = (s: string) => links.filter((l) => l.status === s).length;
    expect(problemRows(links, false, false, all)).toHaveLength(n('violation') + n('advisory'));
  });

  it('lists what the pull request added, first with emphasis on', () => {
    const rows = problemRows(links, true, true, all);
    expect(rows[0].link.from).toBe('e');
    expect(rows[0].added).toBe(true);
    expect(problemRows(links, true, false, all).at(-1)?.link.from).toBe('e');
  });

  it('follows the status filter', () => {
    const rows = problemRows(links, false, false, { ...all, violation: false });
    expect(rows.map((r) => r.link.status)).toEqual(['advisory']);
  });
});

describe('acceptedRows', () => {
  const links = [
    link('a', 'b', 'accepted', 2, { accepted: 'advisory' }),
    link('b', 'a', 'accepted', 9, { accepted: 'violation' }),
    link('c', 'd', 'violation', 1),
  ];

  it('is the accepted dependencies only, the biggest first', () => {
    expect(acceptedRows(links, all).map((l) => l.from)).toEqual(['b', 'a']);
  });

  it('follows the accepted filter', () => {
    expect(acceptedRows(links, { ...all, accepted: false })).toEqual([]);
  });
});

describe('focus', () => {
  it('keeps the problem dependencies and the modules they join', () => {
    const v = view(
      [node('a'), node('b'), node('c'), node('d')],
      [link('a', 'b', 'violation'), link('c', 'd', 'observed'), link('b', 'c', 'observed', 1, { origin: 'added' })],
    );
    expect(focus(v, false).nodes.map((n) => n.name)).toEqual(['a', 'b']);
    expect(focus(v, true).nodes.map((n) => n.name)).toEqual(['a', 'b', 'c']);
  });

  it('is the whole view without a problem', () => {
    const v = view([node('a'), node('b')], [link('a', 'b', 'allowed')]);
    expect(focus(v, false)).toEqual({ nodes: v.nodes, links: v.links });
  });
});

describe('worst', () => {
  it('is the strongest problem on a module', () => {
    const ls = [link('a', 'b', 'accepted'), link('c', 'a', 'violation'), link('a', 'd', 'allowed')];
    expect(worst(ls, 'a')).toBe('violation');
    expect(worst(ls, 'd')).toBe('');
    expect(worst([link('a', 'b', 'accepted')], 'a')).toBe('');
  });
});

describe('layers and order', () => {
  const nodes = [node('z', 'infra', 9), node('b', 'app', 3), node('a', 'app', 6), node('loose'), node('x', 'edge', 1)];

  it('reads the layers in policy order', () => {
    expect(layers(nodes)).toEqual(['edge', 'app', 'infra']);
  });

  it('orders modules by layer, then name, undeclared last', () => {
    expect(order(nodes).map((n) => n.name)).toEqual(['x', 'a', 'b', 'z', 'loose']);
  });
});

describe('elkInput', () => {
  it('gives each direction its own edge, weighted by count, and skips a dependency on itself', () => {
    const g = elkInput(
      [node('a'), node('b')],
      [link('a', 'b', 'violation', 12), link('b', 'a', 'violation', 0), link('a', 'a', 'observed')],
    );
    expect(g.edges.map((e) => [e.sources[0], e.targets[0], e.layoutOptions['elk.layered.priority.direction']])).toEqual(
      [
        ['a', 'b', '12'],
        ['b', 'a', '0'],
      ],
    );
    expect(g.edges[1].labels[0].text).toBe('·');
    expect(g.layoutOptions['elk.partitioning.activate']).toBe('false');
  });

  it('turns declared layers into partitions in policy order', () => {
    const g = elkInput([node('db', 'infra', 6), node('api', 'app', 3), node('x')], []);
    expect(g.layoutOptions['elk.partitioning.activate']).toBe('true');
    expect(g.children.map((c) => [c.id, c.layoutOptions['elk.partitioning.partition']])).toEqual([
      ['db', '1'],
      ['api', '0'],
      ['x', '2'],
    ]);
  });
});

describe('smooth', () => {
  it('keeps both ends exact', () => {
    const d = smooth([
      [0, 0],
      [10, 20],
      [30, 40.04],
    ]);
    expect(d.startsWith('M 0 0')).toBe(true);
    expect(d.endsWith('30 40')).toBe(true);
    expect(smooth([[1, 2]])).toBe('');
  });
});

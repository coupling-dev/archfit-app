import { describe, expect, it } from 'vitest';
import type { Policy } from './api';
import { exceptionExpiry, filterPolicy, policyCounts, policyGraph, policyRules, ruleMode } from './policy';

const module = (name: string, layer = 'core', deps: string[] = []): Policy['modules'][number] => ({
  name,
  layer,
  depends_on: deps,
  paths: [`src/${name}/**`],
  public: null,
  volatility: 'low',
  subdomain: '',
});
const exception = (days_left?: number): Policy['exceptions'][number] => ({
  rule: 'graph',
  from: 'app',
  to: 'util',
  why: 'Migration',
  days_left,
});
const policy: Policy = {
  owners: ['alice'],
  gate: 'advisory',
  layers: ['core', 'entry'],
  modules: [module('app', 'entry', ['util']), module('util')],
  builtin: {
    graph: 'ask',
    coupling: 'deny',
    duplication: 'off',
    cycle: 'deny',
    layers: 'ask',
    unowned: 'off',
    min_balance: 7,
  },
  rules: [
    {
      id: 'boundary',
      deny: { from: ['util'], to: ['app'] },
      not_checked: false,
      why: 'Independence',
      instead: 'Inject a client',
    },
    { id: 'sdk', only: { use: 'vendor/sdk', in: ['app'] }, not_checked: false, why: 'One adapter' },
    { id: 'note', note: 'Use the runner', not_checked: true, why: 'Shared limits' },
  ],
  exceptions: [exception(-1), exception(0), exception(1), exception(30), exception(31), exception()],
  yaml: '',
  digest: '',
  edit: { available: false, reason: '' },
};

describe('policy summary', () => {
  it('counts ask and deny rules, not off settings, notes, or the minimum balance', () => {
    expect(policyCounts(policy)).toEqual({ modules: 2, blocking: 6, exceptions: 6, expiring: 3, expired: 1 });
    expect(policyCounts({ ...policy, gate: 'required' })).toEqual(policyCounts(policy));
  });

  it('does not call unknown modes or unchecked declarations blocking', () => {
    const body = {
      ...policy,
      builtin: { ...policy.builtin, graph: 'future-mode' },
      rules: [
        { id: 'unknown', not_checked: false, why: '' },
        { id: 'unchecked-deny', deny: { from: ['app'], to: ['util'] }, not_checked: true, why: '' },
      ],
    };
    expect(policyCounts(body).blocking).toBe(3);
    expect(ruleMode('future-mode')).toBe('future-mode');
    expect(ruleMode('')).toBe('Not provided');
  });

  it('retains repeated custom IDs and their distinct explanations', () => {
    const body = { ...policy, rules: policy.rules.map((r) => ({ ...r, id: 'same-id' })) };
    expect(
      policyRules(body)
        .slice(6)
        .map((r) => r.why),
    ).toEqual(['Independence', 'One adapter', 'Shared limits']);
  });
});

describe('exception expiry uses server days, not the browser clock', () => {
  it.each([
    [undefined, 'Not provided', '', null],
    [-3, 'Expired 3 days ago', 'danger', 0],
    [-1, 'Expired 1 day ago', 'danger', 0],
    [0, 'Expires today', 'attention', 0],
    [1, 'Expires in 1 day', 'attention', 1],
    [30, 'Expires in 30 days', 'attention', 30],
    [31, 'Expires in 31 days', '', 30],
  ])('%s days', (days, label, tone, remaining) => {
    expect(exceptionExpiry(exception(days))).toEqual({ label, tone, remaining });
  });

  it('does not derive missing days from an expiry date', () => {
    expect(exceptionExpiry({ ...exception(), expires: '2026-11-19' }).label).toBe('Days remaining not provided');
  });
});

describe('policy search', () => {
  it('returns every item for an empty or whitespace query', () => {
    for (const query of ['', '   ']) {
      expect(filterPolicy(policy, query)).toEqual({
        modules: policy.modules,
        rules: policyRules(policy),
        exceptions: policy.exceptions,
      });
    }
  });

  it.each([
    [' SRC/APP ', 1, 0, 0],
    ['inject a client', 0, 1, 0],
    ['ask owner', 0, 2, 0],
    ['vendor/sdk', 0, 1, 0],
    ['expired', 0, 0, 1],
    ['not-a-match', 0, 0, 0],
  ])('searches %s across names, paths, rules and expiry', (query, modules, rules, exceptions) => {
    const result = filterPolicy(policy, query);
    expect([result.modules.length, result.rules.length, result.exceptions.length]).toEqual([
      modules,
      rules,
      exceptions,
    ]);
  });

  it('searches public paths and request numbers without needing owner metadata', () => {
    const body = {
      ...policy,
      modules: [{ ...module('app'), public: ['api/client'] }],
      exceptions: [{ ...exception(), request: 431, request_href: '/requests/431' }],
    };
    expect(filterPolicy(body, 'api/client').modules).toHaveLength(1);
    expect(filterPolicy(body, '#431').exceptions).toHaveLength(1);
  });
});

describe('secondary module graph', () => {
  it('draws declared dependencies, counts missing targets, and preserves the input', () => {
    const modules = [module('app', 'entry', ['util', 'absent']), module('util')];
    const graph = policyGraph(modules, ['core', 'entry']);
    expect(graph.links.map(({ from, to }) => [from, to])).toEqual([['app', 'util']]);
    expect(graph.missing).toBe(1);
    expect(graph.nodes.map(({ module }) => module.name)).toEqual(['app', 'util']);
    expect(graph.nodes[0]?.y).toBeLessThan(graph.nodes[1]?.y ?? 0);
    expect(modules.map((m) => m.depends_on)).toEqual([['util', 'absent'], []]);
  });

  it('places same-layer consumers above their dependencies', () => {
    const graph = policyGraph([module('a', 'core', ['b']), module('b', 'core', ['c']), module('c')], ['core']);
    const y = new Map(graph.nodes.map((node) => [node.module.name, node.y]));
    expect(y.get('a')).toBeLessThan(y.get('b') ?? 0);
    expect(y.get('b')).toBeLessThan(y.get('c') ?? 0);
  });

  it('supports cycles, self-dependencies and undeclared layers without recursion', () => {
    const graph = policyGraph([module('a', '', ['a', 'b']), module('b', 'other', ['a'])], null);
    expect(graph.links).toHaveLength(3);
    expect(graph.links.every((link) => !link.path.includes('NaN'))).toBe(true);
    expect(graph.layers).toEqual(['', 'other']);
  });

  it('wraps large layers in three-column rows and handles an empty policy', () => {
    const graph = policyGraph(
      Array.from({ length: 10 }, (_, i) => module(String(i))),
      ['core'],
    );
    expect(new Set(graph.nodes.map((n) => `${n.x},${n.y}`)).size).toBe(10);
    expect(graph.nodes.every((n) => n.x >= 0 && n.x + 140 <= 480)).toBe(true);
    expect(policyGraph([], null)).toMatchObject({ nodes: [], links: [], missing: 0 });
  });
});

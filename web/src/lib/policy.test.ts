import { describe, expect, it } from 'vitest';
import { ApiError, type Policy } from './api';
import {
  exceptionExpiry,
  filterPolicy,
  groupExceptions,
  isPolicyMissing,
  policyCounts,
  policyMap,
  policyRules,
  ruleMode,
} from './policy';

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
    expect(policyCounts(policy)).toEqual({
      rules: 9,
      modules: 2,
      askDeny: 6,
      exceptions: 6,
      expiring: 3,
      expired: 1,
    });
    expect(policyCounts({ ...policy, gate: 'required' })).toEqual(policyCounts(policy));
  });

  it('does not count unknown modes or unchecked declarations as ask or deny', () => {
    const body = {
      ...policy,
      builtin: { ...policy.builtin, graph: 'future-mode' },
      rules: [
        { id: 'unknown', not_checked: false, why: '' },
        { id: 'unchecked-deny', deny: { from: ['app'], to: ['util'] }, not_checked: true, why: '' },
      ],
    };
    expect(policyCounts(body).askDeny).toBe(3);
    expect(ruleMode('future-mode')).toBe('future-mode');
    expect(ruleMode('')).toBe('unknown');
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

describe('policy rules', () => {
  it('names every rule by its id, built-in rules first', () => {
    expect(policyRules(policy).map((r) => r.id)).toEqual([
      'graph',
      'coupling',
      'duplication',
      'cycle',
      'layers',
      'unowned',
      'boundary',
      'sdk',
      'note',
    ]);
  });

  it('states the minimum balance and what balance means', () => {
    const coupling = policyRules({ ...policy, builtin: { ...policy.builtin, min_balance: 8 } })[1];
    expect(coupling?.summary).toBe(
      'New knowledge across a module boundary needs a balance of at least 8. Balance is the coupling number, out of 10.',
    );
  });
});

describe('missing policy', () => {
  it.each([
    [409, 'No policy on the default branch yet.', true],
    [404, 'No policy on the default branch yet.', false],
    [409, 'The policy on the default branch could not be read. Fix .archfit.yaml first.', false],
    [502, 'GitHub or an Archfit service failed. Nothing was changed. Try again.', false],
  ])('%i %s', (status, message, missing) => {
    expect(isPolicyMissing(new ApiError(status, message))).toBe(missing);
  });
});

describe('exception expiry uses server days, not the browser clock', () => {
  it.each([
    [undefined, 'No end date', '', null],
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

describe('policy map', () => {
  it('draws each declared dependency as an allowed line, counts missing targets, and keeps the input', () => {
    const modules = [module('app', 'entry', ['util', 'absent']), module('util')];
    const { model, missing } = policyMap(modules, ['core', 'entry']);
    expect(model.lines).toEqual([{ kind: 'allowed', from: 'app', to: 'util' }]);
    expect(missing).toBe(1);
    expect(model.modules.map((m) => m.name)).toEqual(['app', 'util']);
    expect(modules.map((m) => m.depends_on)).toEqual([['util', 'absent'], []]);
  });

  it('lists layers outer-first, the reverse of the policy file', () => {
    const { model } = policyMap([module('a', 'core'), module('b', 'entry')], ['core', 'entry']);
    expect(model.layers).toEqual(['entry', 'core']);
  });

  it('keeps a self-dependency and a cycle as lines, and an empty policy as nothing', () => {
    const cyclic = policyMap([module('a', '', ['a', 'b']), module('b', 'other', ['a'])], null);
    expect(cyclic.model.lines).toHaveLength(3);
    expect(policyMap([], null)).toEqual({ model: { layers: [], modules: [], lines: [] }, missing: 0 });
  });
});

describe('exceptions by rule', () => {
  it('groups by rule, largest first, and keeps the file order inside a group', () => {
    const items = [
      { ...exception(), rule: 'duplication', from: 'a' },
      { ...exception(), rule: 'coupling', from: 'b' },
      { ...exception(), rule: 'coupling', from: 'c' },
    ];
    expect(groupExceptions(items).map((g) => [g.rule, g.items.map((i) => i.from)])).toEqual([
      ['coupling', ['b', 'c']],
      ['duplication', ['a']],
    ]);
  });
});

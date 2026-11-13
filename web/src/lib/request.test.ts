import { describe, expect, it } from 'vitest';
import { mapLines } from '../components/map/model';
import type { RequestView } from './api';
import { answerProblem, balanceText, dependency, earlierLine, loosens, moduleLine, requestMap, previewPath, pullLine, statusOf } from './request';

const open: RequestView = {
  number: 431,
  state: 'open',
  reason: 'netem needs the retry helper',
  rule: 'graph',
  from: 'pkg_tc',
  to: 'pkg_util',
  record: 'pkg/tc/netem.go:12 graph pkg_tc -> pkg_util is not an allowed dependency\n- balance: 9/10 (min 7)',
  verified: true,
  balance: { value: 9, max: 10, min: 7 },
  policy: true,
  dependencies: [],
  layers: [],
  modules: [],
  allowed: false,
  earlier: [],
};

describe('loosens', () => {
  it.each([
    ['approve', true],
    ['approve-until', true],
    ['reject', false],
  ] as const)('%s', (kind, want) => {
    expect(loosens(kind)).toBe(want);
  });
});

describe('answerProblem', () => {
  it.each([
    ['approve', '', '', ''],
    ['approve-until', '2026-12-01', '', ''],
    ['approve-until', '', '', 'Choose the date the exception ends.'],
    ['approve-until', '2026-13-01', '', 'Choose the date the exception ends.'],
    ['approve-until', '1 Dec 2026', '', 'Choose the date the exception ends.'],
    ['reject', '', 'Add a method to container.Client.', ''],
    ['reject', '', '   ', 'Write what to do instead.'],
  ] as const)('%s until=%s instead=%s', (kind, until, instead, want) => {
    expect(answerProblem(kind, until, instead)).toBe(want);
  });
});

describe('balanceText', () => {
  it('is n/max (min m), and says when the numbers are as reported', () => {
    expect(balanceText(open)).toBe('9/10 (min 7)');
    expect(balanceText({ ...open, verified: false })).toBe('9/10 (min 7), as reported');
    expect(balanceText({ ...open, balance: undefined })).toBe('');
  });
});

describe('dependency', () => {
  it('names the two modules, or the path of unowned code', () => {
    expect(dependency(open)).toBe('pkg_tc -> pkg_util');
    expect(dependency({ ...open, from: 'pkg_tc', to: undefined })).toBe('pkg_tc');
    expect(dependency({ path: 'tools/x.go' })).toBe('tools/x.go');
  });
});

describe('statusOf', () => {
  it.each([
    [open, 'waiting', 'attention', 'Request #431 waits for an owner to answer.'],
    [
      { ...open, verified: false },
      'waiting',
      'attention',
      'Request #431 waits for an owner to answer. Its numbers are as reported: no pull request confirmed them yet.',
    ],
    [
      { ...open, allowed: true },
      'waiting',
      'attention',
      'Request #431 waits for an owner to answer. The policy already allows this dependency.',
    ],
    [
      { ...open, policy: false },
      'waiting',
      'attention',
      'Request #431 cannot be answered here: the default branch has no policy of version 3.',
    ],
    [{ ...open, state: 'answered' }, 'answered', 'neutral', 'Request #431 is answered.'],
    [{ ...open, state: 'closed' }, 'closed', 'neutral', 'Request #431 is closed.'],
  ] as const)('%o', (r, word, tone, line) => {
    expect(statusOf(r)).toEqual({ word, tone, line });
  });
});

describe('earlierLine', () => {
  it('says what an earlier answer wrote', () => {
    expect(
      earlierLine({ kind: 'deny', rule: 'no-tc-to-util', from: 'pkg_tc', to: 'pkg_util', instead: 'Copy nothing.' }),
    ).toBe('Rule no-tc-to-util denies pkg_tc -> pkg_util. Instead: Copy nothing.');
    expect(
      earlierLine({ kind: 'exception', rule: 'graph', from: 'pkg_tc', to: 'pkg_util', expires: '2026-12-01' }),
    ).toBe('Exception to graph for pkg_tc -> pkg_util until 2026-12-01.');
    expect(earlierLine({ kind: 'exception', rule: 'graph', from: 'pkg_tc', to: 'pkg_util' })).toBe(
      'Exception to graph for pkg_tc -> pkg_util.',
    );
  });
});

describe('previewPath', () => {
  it('names the answer and only the fields it takes, escaped', () => {
    expect(previewPath(431, 'approve', '2026-12-01', 'x')).toBe('requests/431/preview?answer=approve');
    expect(previewPath(431, 'approve-until', '2026-12-01', 'x')).toBe(
      'requests/431/preview?answer=approve-until&until=2026-12-01',
    );
    expect(previewPath(431, 'approve-until', '', 'x')).toBe('requests/431/preview?answer=approve-until');
    expect(previewPath(431, 'reject', '', 'Use a port & adapter')).toBe(
      'requests/431/preview?answer=reject&instead=Use+a+port+%26+adapter',
    );
  });
});

describe('pullLine', () => {
  it('says whether the pull request waits and what the answer clears', () => {
    expect(pullLine({ number: 12, status: 'waiting', waits: true, clears: 1, left: 0 })).toBe(
      '#12 (waiting), waits on this request: clears 1 error, 0 errors left',
    );
    expect(pullLine({ number: 15, status: 'fail', waits: false, clears: 2, left: 1 })).toBe(
      '#15 (fail): clears 2 errors, 1 error left',
    );
  });
});

describe('moduleLine', () => {
  it('names what the policy declares, and says when the volatility is not set', () => {
    expect(moduleLine({ name: 'pkg_tc', layer: 'adapters', volatility: 'high', depends_on: [] })).toBe(
      'Layer adapters · Volatility high',
    );
    expect(moduleLine({ name: 'pkg_util', subdomain: 'generic', depends_on: [] })).toBe(
      'Volatility not set · subdomain generic',
    );
  });
});

describe('requestMap', () => {
  it('keeps the policy layer order and only lines between the declared modules', () => {
    const result = requestMap({ ...open, layers: ['outer', 'core'], modules: [
      { name: 'pkg_tc', layer: 'outer', volatility: 'high', depends_on: ['pkg_util', 'other'] },
      { name: 'pkg_util', layer: 'core', depends_on: [] },
    ], earlier: [{ kind: 'deny', rule: 'rule', from: 'pkg_util', to: 'pkg_tc' }] });
    expect(result.layers).toEqual(['outer', 'core']);
    expect(result.modules).toEqual([{ name: 'pkg_tc', layer: 'outer', volatility: 'high' }, { name: 'pkg_util', layer: 'core', volatility: 'not set' }]);
    expect(result.lines).toEqual([
      { kind: 'allowed', from: 'pkg_tc', to: 'pkg_util' },
      { kind: 'requested', from: 'pkg_tc', to: 'pkg_util', rule: 'graph', request: 431 },
    ]);
  });
});

describe('requestMap policy lines', () => {
  it.each(['deny', 'exception'] as const)('keeps %s on an answered dependency', (kind) => {
    const result = requestMap({ ...open, state: 'answered', modules: [
      { name: 'pkg_tc', volatility: 'high', depends_on: ['pkg_util'] },
      { name: 'pkg_util', volatility: 'low', depends_on: [] },
    ], dependencies: [{ kind, from: 'pkg_tc', to: 'pkg_util', rule: 'rule', expires: '2026-12-10' }] });
    expect(mapLines(result.lines)).toEqual([{ kind, from: 'pkg_tc', to: 'pkg_util', rule: 'rule', expires: '2026-12-10' }]);
  });
});

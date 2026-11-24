import { describe, expect, it } from 'vitest';
import { mapLines } from '../components/map/model';
import type { AnswerKind, Preview, RequestView } from './api';
import {
  answerProblem,
  decisionState,
  answerKinds,
  balanceLine,
  balanceText,
  surfaceLine,
  dependency,
  earlierLine,
  loosens,
  moduleLine,
  requestMap,
  previewPath,
  pullLine,
  statusOf,
  question,
  storySteps,
  effectLines,
  defaultUntil,
  changedWhileOpen,
  effectLead,
  askedAgo,
  ledeLine,
  recheckLine,
  rechecking,
} from './request';

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
  it('names the inputs of a 0.9.0 balance, with the target as subject', () => {
    const inputs = { ...open, balance: { value: 9, max: 10, min: 7, strength: 'contract', volatility: 'low' } };
    expect(balanceText(inputs)).toBe('9/10 (min 7): contract, pkg_util is low');
    expect(balanceText({ ...inputs, verified: false })).toBe('9/10 (min 7): contract, pkg_util is low, as reported');
  });
});

describe('balanceLine', () => {
  it('prints a stored balance without inputs, and a balance with them', () => {
    expect(balanceLine({ value: 9, min: 7 })).toBe('9/10 (min 7)');
    expect(balanceLine({ value: 2, min: 7, strength: 'intrusive', volatility: 'high' }, 'assessment-repair')).toBe(
      '2/10 (min 7): intrusive, assessment-repair is high',
    );
    expect(balanceLine({ value: 2, min: 7, strength: 'intrusive', volatility: 'high' })).toBe('2/10 (min 7)');
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
    [open, 'waiting', 'attention', 'Waiting for an owner to answer.'],
    [
      { ...open, verified: false },
      'waiting',
      'attention',
      'Waiting for an owner to answer. Its numbers are as reported: no pull request confirmed them yet.',
    ],
    [
      { ...open, allowed: true },
      'waiting',
      'attention',
      'Waiting for an owner to answer. The policy already allows this dependency.',
    ],
    [
      { ...open, policy: false },
      'waiting',
      'attention',
      'Request #431 cannot be answered here: the default branch has no valid policy.',
    ],
    [{ ...open, first_approval: true }, 'waiting', 'attention', 'Waiting for an administrator to approve the first policy.'],
    [{ ...open, state: 'answered' }, 'answered', 'neutral', 'An owner answered this request.'],
    [
      { ...open, state: 'answered', answer: { kind: 'approve' } },
      'answered',
      'neutral',
      'An owner approved this request.',
    ],
    [
      { ...open, state: 'answered', answer: { kind: 'reject', instead: 'Use a port.' } },
      'answered',
      'neutral',
      'An owner rejected this request.',
    ],
    [
      { ...open, state: 'answered', answer: { kind: 'declare-public' } },
      'answered',
      'neutral',
      'An owner declared the package public.',
    ],
    [
      { ...open, state: 'answered', answer: { kind: 'declare-model' } },
      'answered',
      'neutral',
      'An owner declared the package a model part.',
    ],
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

describe('decisionState', () => {
  const preview = (answer: AnswerKind, diff = '+ policy line'): Preview => ({
    answer,
    diff,
    loosens: answer !== 'reject',
    policy_digest: 'digest',
    pulls: [],
  });

  it.each([
    ['approve', 'Answer with passkey'],
    ['approve-until', 'Answer with passkey'],
    ['declare-public', 'Answer with passkey'],
    ['reject', 'Answer'],
  ] as const)('%s is confirmed with "%s"', (kind, confirm) => {
    expect(decisionState(kind, preview(kind))).toEqual({ ready: true, changesPolicy: true, confirm });
    expect(decisionState(kind, preview(kind, ''))).toMatchObject({ ready: true, changesPolicy: false });
  });

  it('does not confirm a missing preview or another answer', () => {
    expect(decisionState('approve', undefined)).toMatchObject({ ready: false, changesPolicy: false });
    expect(decisionState('approve', preview('reject'))).toMatchObject({ ready: false, changesPolicy: false });
  });
});

describe('answerKinds', () => {
  const kinds = (r: Parameters<typeof answerKinds>[0]) => answerKinds(r).map((k) => (k.minor ? `(${k.title})` : k.title));
  it('offers allow, allow until a date and reject for a request that is not about the public part', () => {
    expect(kinds(open)).toEqual(['Allow', 'Allow until a date', 'Reject, and say what to do instead']);
    expect(kinds(null)).toEqual(['Allow', 'Allow until a date', 'Reject, and say what to do instead']);
  });
  it('never approves a use past the public part: three answers and a smaller shared-model one', () => {
    const surface = { public: ['pkg/util'], model: [], package: 'pkg/util/retry' };
    expect(kinds({ surface })).toEqual([
      'Make it public',
      'Allow until a date',
      'Reject, and say what to do instead',
      '(Declare it as shared model)',
    ]);
    expect(kinds({ surface: { ...surface, package: undefined } })).toEqual(['Allow until a date', 'Reject, and say what to do instead']);
  });
});

describe('question', () => {
  it.each([
    [{ from: 'billing', to: 'reports' }, 'May billing depend on reports?'],
    [{ from: 'pkg_tc', to: 'pkg_util', surface: { public: [], model: [], package: 'pkg/util/retry' } }, 'Should pkg/util/retry be public in pkg_util?'],
    [{ from: 'billing' }, 'May billing keep this change?'],
    [{ path: 'scripts/x.go' }, 'May scripts/x.go stay as it is?'],
    [{}, 'May this change stay as it is?'],
  ])('%o asks "%s"', (r, want) => {
    expect(question(r)).toBe(want);
  });
});

describe('storySteps', () => {
  it.each([
    [{ state: 'open' }, ['done', 'failed', 'current', 'later', 'later', 'later']],
    [{ state: 'answered', answer: { kind: 'approve' } }, ['done', 'failed', 'done', 'done', 'current', 'later']],
    [{ state: 'answered', answer: { kind: 'reject', instead: 'x' } }, ['done', 'failed', 'done', 'failed', 'skipped', 'skipped']],
    [{ state: 'answered' }, ['done', 'failed', 'done', 'done', 'current', 'later']],
    [{ state: 'closed' }, ['done', 'failed', 'done', 'skipped', 'skipped', 'skipped']],
  ] as const)('%o', (r, want) => {
    expect(storySteps(r as Pick<RequestView, 'state' | 'answer'>)).toEqual(want);
  });
  it('marks the policy done after a merge this page saw', () => {
    expect(storySteps({ state: 'answered', answer: { kind: 'approve' } }, true)).toEqual(['done', 'failed', 'done', 'done', 'done', 'current']);
  });
  const approved = { state: 'answered', answer: { kind: 'approve' }, policy_pull: 77 } as const;
  it.each([
    ['policy pull request open', { ...approved, policy_merged: false, rechecks: [] }, 'current', 'later', true],
    ['a pull request runs again', { ...approved, policy_merged: true, rechecks: [{ number: 12, status: 'running' }, { number: 14, status: 'pass' }] }, 'done', 'current', true],
    ['every result landed', { ...approved, policy_merged: true, rechecks: [{ number: 12, status: 'pass' }, { number: 14, status: 'waiting' }] }, 'done', 'done', false],
    ['a result fails', { ...approved, policy_merged: true, rechecks: [{ number: 12, status: 'fail' }] }, 'done', 'failed', false],
    ['a new push is needed', { ...approved, policy_merged: true, rechecks: [{ number: 12, status: 'push' }] }, 'done', 'failed', false],
    ['no open pull request waits', { ...approved, policy_merged: true, rechecks: [] }, 'done', 'skipped', false],
  ] as const)('reads the re-check from the request: %s', (_, r, policy, recheck, moving) => {
    expect(storySteps(r).slice(4)).toEqual([policy, recheck]);
    expect(rechecking(r)).toBe(moving);
  });
  it('stops reading again for an open, rejected or closed request', () => {
    expect(rechecking({ state: 'open', policy_pull: 77 })).toBe(false);
    expect(rechecking({ state: 'answered', answer: { kind: 'reject', instead: 'x' }, policy_pull: 77 })).toBe(false);
    expect(rechecking({ state: 'closed' })).toBe(false);
  });
});

describe('recheckLine', () => {
  it.each([
    [{ policy_merged: false, rechecks: [] }, ''],
    [{ policy_merged: true, rechecks: [] }, ''],
    [{ policy_merged: true, rechecks: [{ number: 12, status: 'running' }] }, 'Re-check: pull request #12 has no result with the new policy yet.'],
    [
      { policy_merged: true, rechecks: [{ number: 12, status: 'pass' }, { number: 14, status: 'push' }] },
      'Re-check: pull request #12 passes; pull request #14 runs again after a new push.',
    ],
  ] as const)('%o says "%s"', (r, want) => {
    expect(recheckLine(r)).toBe(want);
  });
});

describe('effectLines', () => {
  const r = { from: 'billing', to: 'reports', surface: { public: [], model: [], package: 'reports/totals' } };
  const p = (answer: AnswerKind, diff: string, pulls: Preview['pulls'] = []): Preview => ({ answer, diff, loosens: true, policy_digest: 'd', pulls });
  const passes = [{ number: 412, status: 'waiting', waits: true, clears: 1, left: 0 }];
  it.each([
    ['approve', p('approve', '+a', passes), ['Adds 1 line to the policy: billing may depend on reports.', 'Pull request #412 passes when the policy is updated.']],
    ['approve', p('approve', '+a\n+b'), ['Adds 2 lines to the policy: billing may depend on reports.', 'No open pull request changes.']],
    ['approve', undefined, ['Adds 1 line to the policy: billing may depend on reports.']],
    [
      'approve-until',
      p('approve-until', '+a', [{ number: 15, status: 'fail', waits: false, clears: 1, left: 2 }]),
      ['Adds an exception for this one place that ends on 2027-02-20. After it, the check fails again and asks for a new answer.', 'Pull request #15 still has 2 errors.'],
    ],
    ['declare-public', p('declare-public', '+a', passes), ['Adds 1 line: reports/totals joins the public part of reports. Every module may then use it.', 'Pull request #412 passes when the policy is updated.']],
    [
      'approve',
      p('approve', '+a', [{ number: 20, status: 'unknown', waits: true, clears: 0, left: 0 }]),
      ['Adds 1 line to the policy: billing may depend on reports.', 'Pull request #20 needs a new check result before it can pass.'],
    ],
    ['reject', undefined, ['The author sees what you say to do instead.']],
    ['reject', p('reject', '+deny', passes), ['Adds a rule: billing may not depend on reports, with what you say to do instead. The author sees your text.']],
    ['reject', p('reject', ''), ['Changes nothing in the policy. The author sees what you say to do instead.']],
  ] as const)('%s', (kind, preview, want) => {
    expect(effectLines(kind, r, preview, '2027-02-20')).toEqual(want);
  });
});

describe('effectLead', () => {
  it.each([
    ['Adds 1 line to the policy: a may depend on b.', ['Adds 1 line', ' to the policy: a may depend on b.']],
    ['Adds 2 lines: x.', ['Adds 2 lines', ': x.']],
    ['Adds an exception for this one place.', ['Adds an exception', ' for this one place.']],
    ['Adds a rule: a may not depend on b.', ['Adds a rule', ': a may not depend on b.']],
    ['Changes nothing in the policy.', ['Changes nothing', ' in the policy.']],
    ['Pull request #12 passes.', ['', 'Pull request #12 passes.']],
  ])('%s', (line, want) => {
    expect(effectLead(line)).toEqual(want);
  });
});

describe('askedAgo', () => {
  const now = new Date('2026-11-22T12:00:00Z');
  it.each([
    ['2026-11-22T11:59:30Z', 'just now'],
    ['2026-11-22T11:59:00Z', '1 minute ago'],
    ['2026-11-22T11:48:00Z', '12 minutes ago'],
    ['2026-11-22T10:00:00Z', '2 hours ago'],
    ['2026-11-21T12:00:00Z', '1 day ago'],
    ['2026-11-22T12:05:00Z', 'just now'],
    ['not a time', ''],
  ])('%s is %s', (at, want) => {
    expect(askedAgo(at, now)).toBe(want);
  });
});

describe('ledeLine', () => {
  const now = new Date('2026-11-22T12:00:00Z');
  const r = { number: 40, state: 'open' as const, reason: 'invoice needs the totals' };
  it.each([
    [{ ...r, asked_by: 'dev', asked_at: '2026-11-22T11:48:00Z', waiting: [412] }, 'Request #40. Asked by @dev 12 minutes ago: “invoice needs the totals” Pull request #412 waits for your answer.'],
    [{ ...r, asked_by: 'dev' }, 'Request #40. Asked by @dev: “invoice needs the totals”'],
    [{ ...r, waiting: [412, 415] }, 'Request #40. The author says: “invoice needs the totals” Pull request #412, #415 wait for your answer.'],
    [{ ...r, state: 'answered' as const, waiting: [412] }, 'Request #40. The author says: “invoice needs the totals”'],
  ])('%#', (req, want) => {
    expect(ledeLine(req, now)).toBe(want);
  });
});

describe('defaultUntil', () => {
  it('is 90 days later in UTC', () => {
    expect(defaultUntil(new Date('2026-11-22T23:30:00Z'))).toBe('2027-02-20');
  });
});

describe('changedWhileOpen', () => {
  const answered = { ...open, state: 'answered' as const, answer: { kind: 'approve' as const } };
  it.each([
    [open, answered, false, 'Request #431 was answered while you looked. The page shows the answer.'],
    [open, { ...open, state: 'closed' as const }, false, 'Request #431 was closed while you looked.'],
    [open, open, true, 'The policy on the default branch changed while you looked. The effects below are the new ones.'],
    [open, open, false, ''],
  ])('%#', (before, after, moved, want) => {
    expect(changedWhileOpen(before, after, moved)).toBe(want);
  });
});

describe('surfaceLine', () => {
  it('lists the public globs, then the model globs, or says none is declared', () => {
    expect(surfaceLine({ public: ['pkg/util'], model: ['pkg/util/model'] })).toBe('pkg/util (public), pkg/util/model (model)');
    expect(surfaceLine({ public: [], model: [] })).toBe('none declared');
  });
});

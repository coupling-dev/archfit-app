import { describe, expect, it } from 'vitest';
import { homeHref, readHome, readOwnerReview, setupChipClass, setupPullStep, setupRuleStatus, setupStepState } from './home';
import { mapLines, lineLabel } from '../components/map/model';

describe('Home boundary', () => {
  it('rejects an unreadable or incomplete answer instead of showing clean', () => {
    const missingPasskey = { status: 'clean', needs_you: [], setup: { state: 'approved', policy: true, workflow: true, missing: [], rules: [] } };
    for (const body of [null, {}, { status: 12 }, { status: 'clean', map: {} }, { status: 'clean', needs_you: [{}] }, missingPasskey])
      expect(() => readHome(body)).toThrow('The Home answer could not be read.');
  });

  it('accepts GitHub repository names regardless of case and rebuilds links', () => {
    const repo = { owner: 'acme', name: 'app', fullName: 'acme/app' };
    for (const [href, expected] of [
      ['/r/Acme/App/requests/431', '/r/acme/app/requests/431'],
      ['/r/Acme/App/policy', '/r/acme/app/policy'],
      ['/r/Acme/App/owner/first?pull=8', '/r/acme/app/owner/first?pull=8'],
      ['/r/Acme/App/owner/first', '/r/acme/app/owner/first'],
      ['/r/Acme/App/owner/confirm', '/r/acme/app/owner/confirm'],
      ['/r/Acme/App/owner/workflow?pull=4', '/r/acme/app/owner/workflow?pull=4'],
      ['https://github.com/Acme/App/issues/72', 'https://github.com/acme/app/issues/72'],
    ]) expect(homeHref(repo, href)).toBe(expected);
  });

  it('builds links only for the expected repository and route shape', () => {
    const repo = { owner: 'acme', name: 'app', fullName: 'acme/app' };
    for (const href of [
      'javascript:alert(1)',
      '//elsewhere',
      '/r/other/app/requests/431',
      '/r/acme/app/requests/431?next=evil',
      'https://github.com/other/app/issues/72',
    ])
      expect(homeHref(repo, href)).toBeUndefined();
    for (const href of [
      '/r/acme/app/requests/431',
      '/r/acme/app/owner/confirm',
      '/r/acme/app/owner/revert',
      '/r/acme/app/owner/workflow?pull=4',
      '/r/acme/app/policy',
      'https://github.com/acme/app/issues/72',
    ])
      expect(homeHref(repo, href)).toBe(href);
  });
});

describe('map rules', () => {
  it('draws only the winning kind and retains its own label', () => {
    const pair = { from: 'a', to: 'b' };
    const lines = [
      { ...pair, kind: 'allowed' as const },
      { ...pair, kind: 'exception' as const, expires: '2026-11-19' },
      { ...pair, kind: 'requested' as const, request: 431 },
      { ...pair, kind: 'deny' as const, rule: 'no-a-to-b' },
      { ...pair, kind: 'drift' as const, rule: 'graph' },
    ];
    const labels = [
      'Allowed dependency',
      'Exception · 2026-11-19',
      'Requested dependency · #431',
      'A rule denies this dependency · no-a-to-b',
      'Drift · graph',
    ];
    for (let n = 1; n <= lines.length; n++) {
      const selected = mapLines(lines.slice(0, n));
      expect(selected).toEqual([lines[n - 1]]);
      expect(lineLabel(selected[0])).toBe(labels[n - 1]);
    }
    expect(mapLines([...lines].reverse())).toEqual([lines[4]]);
  });
});


describe('setup step labels and progress', () => {
  const base = {
    state: 'not_admitted',
    policy: false,
    workflow: false,
    passkey: false,
    open_pull: 0,
    missing: [],
    rules: [],
  };
  it.each([
    ['ok', 'On'],
    ['unknown', 'Check'],
    ['not_applicable', 'Not available'],
    ['warning', 'Action needed'],
  ])('maps rule state %s to %s', (state, label) => expect(setupRuleStatus(state)).toBe(label));

  it.each([
    ['Action needed', 'attention'],
    ['Review needed', 'attention'],
    ['Check', 'attention'],
    ['Check with an admin', 'attention'],
    ['Ready for review', 'good'],
    ['Waiting', 'neutral'],
    ['Not available', 'neutral'],
    ['Done', 'good'],
    ['On', 'good'],
  ])('gives %s a %s chip', (status, tone) => expect(setupChipClass(status)).toBe(tone));

  it.each([
    [base, 'Next: add the policy'],
    [{ ...base, policy: true }, 'Next: create the pull request'],
    [{ ...base, policy: true, open_pull: 203 }, 'Next: approve the workflow with your passkey'],
    [{ ...base, policy: true, workflow: true }, 'Next: approve the workflow with your passkey'],
    [{ ...base, first_pull: 12 }, 'Next: approve the workflow with your passkey'],
    [{ ...base, first_pull: 12, state: 'approved' }, 'Next: turn on the required check'],
    [
      { ...base, first_pull: 12, state: 'approved', rules: [{ code: 'gate_check', state: 'ok', text: '' }] },
      'Setup is complete.',
    ],
  ])('shows one next action for %j', (setup, next) => expect(setupStepState(setup).next).toBe(next));

  it.each([
    [base, { kind: 'create', status: 'Action needed' }, false],
    [{ ...base, policy: true }, { kind: 'create', status: 'Action needed' }, false],
    [{ ...base, workflow: true }, { kind: 'create', status: 'Action needed' }, false],
    [{ ...base, first_pull: 12 }, { kind: 'first-policy', status: 'Ready for review', number: 12 }, true],
    [{ ...base, policy: true, open_pull: 203 }, { kind: 'review', status: 'Ready for review', number: 203 }, true],
    [{ ...base, policy: true, workflow: true }, { kind: 'current', status: 'Done' }, true],
    [{ ...base, first_pull: 12, open_pull: 203 }, { kind: 'review', status: 'Ready for review', number: 203 }, true],
  ])('keeps the PR step and its completion consistent for %j', (setup, step, done) => {
    expect(setupPullStep(setup)).toEqual(step);
    expect(setupStepState(setup).done[1]).toBe(done);
  });

  it('derives progress from policy, first pull, approval and rules', () => {
    expect(setupStepState({ ...base, policy: true, rules: [{ code: 'x', state: 'ok', text: '' }] })).toEqual({
      done: [true, false, false, true],
      completed: 2,
      approval: 'Waiting',
      gate: 'On',
      next: 'Next: create the pull request',
    });
    expect(
      setupStepState({
        ...base,
        first_pull: 12,
        state: 'first_approval',
        rules: [{ code: 'admin', state: 'unknown', text: '' }],
      }),
    ).toEqual({
      done: [true, true, false, false],
      completed: 2,
      approval: 'Action needed',
      gate: 'Check with an admin',
      next: 'Next: approve the workflow with your passkey',
    });
    expect(setupStepState({ ...base, policy: true, open_pull: 203 })).toEqual({
      done: [true, true, false, false],
      completed: 2,
      approval: 'Waiting',
      gate: 'Action needed',
      next: 'Next: approve the workflow with your passkey',
    });
    expect(
      setupStepState({ ...base, policy: true, state: 'approved', rules: [{ code: 'x', state: 'ok', text: '' }] })
        .completed,
    ).toBe(3);
  });
});

describe('setup applicable rules', () => {
  it.each([
    [['ok', 'not_applicable'], true],
    [['ok', 'unknown', 'not_applicable'], false],
    [['ok', 'warning'], false],
    [[], false],
  ])('keeps unknown blocking but excludes unavailable prerequisites: %j', (states, complete) => {
    const setup = { state: 'approved', policy: true, workflow: true, passkey: true, open_pull: 0, missing: [],
      rules: states.map((state) => ({ code: state, state, text: '' })) };
    expect(setupStepState(setup).done[3]).toBe(complete);
  });
});


describe('server-resolved owner actions', () => {
  it.each(['Review first policy', 'Review workflow change'])('preserves the server label %s', (label) => {
    expect(readOwnerReview({ label, href: '/r/acme/app/owner/first?pull=8' })).toEqual({ label, href: '/r/acme/app/owner/first?pull=8' });
  });
  it('accepts a role handoff without a link and rejects malformed links', () => {
    expect(readOwnerReview({ label: 'An architecture owner can act.' })).toEqual({ label: 'An architecture owner can act.' });
    expect(() => readOwnerReview({ label: 'Review first policy', href: 8 })).toThrow();
  });
  it('marks a resolved review as work to do, not waiting', () => {
    expect(setupStepState({ state: 'not_admitted', policy: true, workflow: false, passkey: true, open_pull: 203,
      missing: [], rules: [], owner_action: { label: 'Review workflow change', href: '/r/acme/app/owner/workflow?pull=203' } }).approval).toBe('Review needed');
  });
});

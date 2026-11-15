import { describe, expect, it } from 'vitest';
import { homeHref, readHome, setupPasskeyActions, setupPullStep, setupRuleStatus, setupStepState } from './home';
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
  const base = { state: 'not_admitted', policy: false, workflow: false, passkey: false, open_pull: 0, missing: [], rules: [] };
  it.each([
    ['ok', 'On'], ['unknown', 'Check'], ['not_applicable', 'Not available'], ['warning', 'Action needed'],
  ])('maps rule state %s to %s', (state, label) => expect(setupRuleStatus(state)).toBe(label));

  it('derives the three pull-request step branches', () => {
    expect(setupPullStep(base)).toEqual({ kind: 'create', status: 'Action needed' });
    expect(setupPullStep({ ...base, first_pull: 12 })).toEqual({ kind: 'first-policy', status: 'Done', number: 12 });
    expect(setupPullStep({ ...base, open_pull: 203 })).toEqual({ kind: 'review', status: 'In review', number: 203 });
  });

  it('chooses passkey registration or approval actions from passkey and setup state', () => {
    expect(setupPasskeyActions({ ...base, state: 'first_approval' })).toEqual({
      register: true, manage: false, approve: false, approvalBlocked: true,
    });
    expect(setupPasskeyActions({ ...base, state: 'first_approval', passkey: true })).toEqual({
      register: false, manage: true, approve: true, approvalBlocked: false,
    });
    expect(setupPasskeyActions({ ...base, state: 'approved', passkey: true })).toEqual({
      register: false, manage: true, approve: false, approvalBlocked: false,
    });
  });

  it('derives progress from policy, first pull, approval and rules', () => {
    expect(setupStepState({ ...base, policy: true, rules: [{ code: 'x', state: 'ok', text: '' }] })).toEqual({
      done: [true, false, false, true], completed: 2, approval: 'Waiting', gate: 'On',
    });
    expect(setupStepState({ ...base, first_pull: 12, state: 'first_approval', rules: [{ code: 'admin', state: 'unknown', text: '' }] })).toEqual({
      done: [true, true, false, false], completed: 2, approval: 'Action needed', gate: 'Check with an admin',
    });
    expect(setupStepState({ ...base, policy: true, open_pull: 203 })).toEqual({
      done: [true, false, false, false], completed: 1, approval: 'Waiting', gate: 'Action needed',
    });
    expect(setupStepState({ ...base, policy: true, state: 'approved', rules: [{ code: 'x', state: 'ok', text: '' }] }).completed).toBe(3);
  });
});

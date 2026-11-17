import { describe, expect, it } from 'vitest';
import {
  homeAction,
  homeHref,
  homeStateLabel,
  homeStates,
  needIcon,
  readHome,
  type Home,
  readOwnerReview,
  setupStepChip,
  setupPullStep,
  setupRuleStatus,
  setupStepState,
  type HomeSetup,
} from './home';
import { mapLines, lineLabel } from '../components/map/model';

const needed = { status: 'needed', kind: 'first' } as const;
const active = { status: 'active', kind: 'first' } as const;

describe('Home boundary', () => {
  it('rejects an unreadable or incomplete answer instead of showing clean', () => {
    const missingPasskey = {
      state: 'clean',
      status: 'clean',
      needs_you: [],
      setup: { state: 'approved', policy: true, workflow: true, missing: [], rules: [] },
    };
    for (const body of [
      null,
      {},
      { status: 12 },
      { state: 'healthy', status: 'clean', needs_you: [] },
      { state: 'clean', status: 'clean', map: {} },
      { state: 'clean', status: 'clean', needs_you: [{}] },
      missingPasskey,
    ])
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
    ])
      expect(homeHref(repo, href)).toBe(expected);
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
  const base: HomeSetup = {
    approval: needed,
    passkey_known: true,
    state: 'not_admitted',
    policy: false,
    workflow: false,
    passkey: false,
    open_pull: 0,
    missing: [],
    rules: [],
  };
  it.each([
    ['ok', 'on'],
    ['unknown', 'verify in GitHub'],
    ['not_applicable', 'does not apply'],
    ['warning', 'off'],
  ])('maps required rule state %s to %s', (state, label) => expect(setupRuleStatus(state, 'gate_check')).toBe(label));

  it.each([
    [base, 0, 'to do'],
    [{ ...base, policy: true, workflow: true, first_pull: 12 }, 1, 'done'],
    [{ ...base, policy: true }, 1, 'to do'],
    [{ ...base, approval: needed }, 2, 'after step 2'],
    [{ ...base, first_pull: 12, approval: active, state: 'approved' }, 2, 'done'],
    [{ ...base, policy: true, workflow: true, first_pull: 12, approval: needed }, 2, 'to do'],
  ])('labels step %#', (setup, index, chip) => {
    expect(setupStepChip(setupStepState(setup).done, index)).toBe(chip);
  });

  it('never labels the gate step as waiting for the approval', () => {
    expect(setupStepChip([true, true, false, false], 3)).toBe('to do');
  });

  it.each([
    [base, 'Next: add the policy'],
    [{ ...base, policy: true }, 'Next: create the pull request'],
    [{ ...base, policy: true, open_pull: 203 }, 'Next: review owner approval'],
    [{ ...base, policy: true, workflow: true }, 'Next: review owner approval'],
    [{ ...base, first_pull: 12 }, 'Next: review owner approval'],
    [{ ...base, first_pull: 12, state: 'approved', approval: active }, 'Next: turn on the required check'],
    [
      {
        ...base,
        first_pull: 12,
        state: 'approved',
        approval: active,
        rules: [{ code: 'pull_requests', state: 'ok', text: '' }, { code: 'gate_check', state: 'ok', text: '' }],
      },
      'Setup is complete.',
    ],
  ])('shows one next action for %j', (setup, next) => expect(setupStepState(setup).next).toBe(next));

  it.each([
    [base, { kind: 'create' }, false],
    [{ ...base, policy: true }, { kind: 'create' }, false],
    [{ ...base, workflow: true }, { kind: 'create' }, false],
    [{ ...base, first_pull: 12 }, { kind: 'first-policy', number: 12 }, true],
    [{ ...base, policy: true, open_pull: 203 }, { kind: 'review', number: 203 }, true],
    [{ ...base, policy: true, workflow: true }, { kind: 'current' }, true],
    [{ ...base, first_pull: 12, open_pull: 203 }, { kind: 'review', number: 203 }, true],
  ])('keeps the PR step and its completion consistent for %j', (setup, step, done) => {
    expect(setupPullStep(setup)).toEqual(step);
    expect(setupStepState(setup).done[1]).toBe(done);
  });

  it('derives progress from policy, first pull, approval and rules', () => {
    expect(setupStepState({ ...base, policy: true, rules: [{ code: 'pull_requests', state: 'ok', text: '' }, { code: 'gate_check', state: 'ok', text: '' }] })).toEqual({
      done: [true, false, false, true],
      completed: 2,
      approvalUnknown: false,
      approvalText: 'Review the proposed files before approving.',
      next: 'Next: create the pull request',
    });
    expect(
      setupStepState({
        ...base,
        first_pull: 12,
        state: 'first_approval',
        rules: [{ code: 'rulesets', state: 'unknown', text: '' }],
      }),
    ).toEqual({
      done: [true, true, false, false],
      completed: 2,
      approvalUnknown: false,
      approvalText: 'Review the proposed files before approving.',
      next: 'Next: review owner approval',
    });
    expect(setupStepState({ ...base, policy: true, open_pull: 203 })).toEqual({
      done: [true, true, false, false],
      completed: 2,
      approvalUnknown: false,
      approvalText: 'Review the proposed files before approving.',
      next: 'Next: review owner approval',
    });
    expect(
      setupStepState({
        ...base,
        policy: true,
        state: 'approved',
        approval: active,
        rules: [{ code: 'pull_requests', state: 'ok', text: '' }, { code: 'gate_check', state: 'ok', text: '' }],
      }).completed,
    ).toBe(3);
  });
});

describe('setup enforcement facts and advisory findings', () => {
  const enforced = [
    { code: 'pull_requests', state: 'ok', text: '' },
    { code: 'gate_check', state: 'ok', text: '' },
  ];
  it.each([
    ['all enforcement on', enforced, true],
    ['administrator unreadable', [...enforced, { code: 'administrator_bypass', state: 'unknown', text: '' }], true],
    ['gate missing', [enforced[0]], false],
    ['pull request rule missing', [enforced[1]], false],
    ['gate not required', [enforced[0], { ...enforced[1], state: 'warning' }], false],
    ['gate not confirmed', [enforced[0], { ...enforced[1], state: 'unknown' }], false],
    ['rulesets unreadable', [...enforced, { code: 'rulesets', state: 'unknown', text: '' }], false],
    ['branch rules unreadable', [{ code: 'branch_rules', state: 'unknown', text: '' }], false],
    ['integration unavailable', [{ code: 'integration', state: 'unknown', text: '' }], false],
    ['conflicting enforcement findings', [...enforced, { ...enforced[1], state: 'warning' }], false],
    ['no findings', [], false],
  ])('%s', (_name, rules, complete) => {
    const state = setupStepState({
      approval: active, passkey_known: true, state: 'approved', policy: true,
      workflow: true, passkey: true, open_pull: 0, missing: [], rules,
    });
    expect(state.done[3]).toBe(complete);
    expect(setupStepChip(state.done, 3)).toBe(complete ? 'done' : 'to do');
    expect(state.completed).toBe(complete ? 4 : 3);
  });

  it.each(['administrator_bypass', 'app_bypass', 'policy_push', 'policy_push_bypass', 'approvals'])('%s is advisory, not an enforcement prerequisite', (code) => {
    for (const state of ['unknown', 'warning', 'not_applicable', 'ok']) {
      const result = setupStepState({
        approval: active, passkey_known: true, state: 'approved', policy: true,
        workflow: true, passkey: true, open_pull: 0, missing: [], rules: [...enforced, { code, state, text: '' }],
      });
      expect(result.done[3]).toBe(true);
    }
    expect(setupRuleStatus('unknown', code)).toBe('verify in GitHub');
    expect(setupRuleStatus('warning', code)).toBe('optional');
    expect(setupRuleStatus('not_applicable', code)).toBe('does not apply');
    expect(setupRuleStatus('ok', code)).toBe('on');
  });
});

describe('server-resolved owner actions', () => {
  it.each(['Review first policy', 'Review workflow change'])('preserves the server label %s', (label) => {
    expect(readOwnerReview({ label, href: '/r/acme/app/owner/first?pull=8' })).toEqual({
      label,
      href: '/r/acme/app/owner/first?pull=8',
    });
  });
  it('accepts a role handoff without a link and rejects malformed links', () => {
    expect(readOwnerReview({ label: 'An architecture owner can act.' })).toEqual({
      label: 'An architecture owner can act.',
    });
    expect(() => readOwnerReview({ label: 'Review first policy', href: 8 })).toThrow();
  });
  it('marks a resolved review as work to do, not waiting', () => {
    expect(
      setupStepState({
        approval: needed,
        passkey_known: true,
        state: 'not_admitted',
        policy: true,
        workflow: false,
        passkey: true,
        open_pull: 203,
        missing: [],
        rules: [],
        owner_action: { label: 'Review workflow change', href: '/r/acme/app/owner/workflow?pull=203' },
      }).done[2],
    ).toBe(false);
  });
});

describe('repository state', () => {
  it('names every state in the words of the design and refuses another word', () => {
    expect(homeStates.map(homeStateLabel)).toEqual([
      'clean',
      'drift',
      'expired',
      'changed outside the flow',
      'not set up',
      'unknown',
    ]);
    expect(readHome({ state: 'outside_flow', status: 'x', needs_you: [] }).state).toBe('outside_flow');
    expect(readHome({ status: 'clean', needs_you: [] }).state).toBe('unknown');
  });
});

describe('the one primary action', () => {
  const repo = { owner: 'acme', name: 'app', fullName: 'acme/app' };
  const github = 'https://github.com/acme/app';
  const home = (over: Partial<Home>): Home => ({ state: 'clean', status: '', needs_you: [], ...over });
  const setup = (over: Partial<HomeSetup>): HomeSetup => ({
    approval: needed,
    passkey_known: true,
    state: 'not_admitted',
    policy: true,
    workflow: true,
    passkey: true,
    open_pull: 0,
    missing: [],
    rules: [],
    ...over,
  });
  const gateOff = [
    { code: 'pull_requests', state: 'warning', text: '' },
    { code: 'gate_check', state: 'warning', text: '' },
  ];
  const workflowNeed = { kind: 'workflow', text: 'Review workflow change · pull request #417', href: '/r/acme/app/owner/workflow?pull=417' };

  it.each([
    ['a workflow change names its verb', home({ needs_you: [workflowNeed] }), 'Review workflow change', '/r/acme/app/owner/workflow?pull=417', false],
    [
      'a drift issue opens GitHub',
      home({ needs_you: [{ kind: 'drift', text: 'x', href: `${github}/issues/72` }] }),
      'Open the drift issue',
      `${github}/issues/72`,
      true,
    ],
    [
      'the first owner item with a reachable link wins',
      home({ needs_you: [{ kind: 'workflow', text: 'Workflow change in #4 waits for an owner.', href: '' }, { kind: 'request', text: 'Request #9 waits.', href: '/r/acme/app/requests/9' }] }),
      'Open the request',
      '/r/acme/app/requests/9',
      false,
    ],
    [
      'an owner item beats a setup step',
      home({ needs_you: [workflowNeed], setup: setup({ approval: active, state: 'approved', rules: gateOff }) }),
      'Review workflow change',
      '/r/acme/app/owner/workflow?pull=417',
      false,
    ],
    [
      'an eligible owner reviews first',
      home({ state: 'not_set_up', setup: setup({ open_pull: 5, owner_action: { label: 'Review', href: '/r/acme/app/owner/workflow?pull=5' } }) }),
      'Review and approve',
      '/r/acme/app/owner/workflow?pull=5',
      false,
    ],
    ['step 1 has no button', home({ state: 'not_set_up', setup: setup({ policy: false, workflow: false, first_pull: undefined }) }), undefined, undefined, undefined],
    ['step 4 opens the ruleset settings', home({ setup: setup({ approval: active, state: 'approved', rules: gateOff }) }), 'Open ruleset settings', `${github}/settings/rules`, true],
  ])('%s', (_name, input, label, href, external) => {
    const action = homeAction(input, repo);
    expect(action?.label).toBe(label);
    expect(action?.href).toBe(href);
    expect(action?.external).toBe(external);
  });

  it('does not name an issue that does not exist yet', () => {
    const noIssue = [
      { kind: 'drift', text: 'Drift on the default branch.', href: '/r/acme/app/policy' },
      { kind: 'exception', text: '1 exception expired', href: '/r/acme/app/policy' },
    ];
    expect(homeAction(home({ needs_you: noIssue }), repo)).toBeUndefined();
    const later = { kind: 'request', text: 'Request #9 waits.', href: '/r/acme/app/requests/9' };
    expect(homeAction(home({ needs_you: [...noIssue, later] }), repo)?.label).toBe('Open the request');
  });

  it('flags only the setup review as needing a passkey', () => {
    const review = { label: 'Review', href: '/r/acme/app/owner/workflow?pull=5' };
    expect(homeAction(home({ setup: setup({ open_pull: 5, owner_action: review }) }), repo)?.needsPasskey).toBe(true);
    expect(homeAction(home({ needs_you: [workflowNeed] }), repo)?.needsPasskey).toBeUndefined();
  });

  it('offers nothing when nothing is due and never an in-page anchor', () => {
    expect(homeAction(home({}), repo)).toBeUndefined();
    expect(homeAction(home({ needs_you: [{ kind: 'drift', text: 'x', href: 'https://github.com/other/app/issues/1' }] }), repo)).toBeUndefined();
    const created = homeAction(home({ state: 'not_set_up', setup: setup({ policy: true, workflow: false, open_pull: 0 }) }), repo);
    expect(created).toEqual({ label: 'Create a pull request on GitHub', href: `${github}/compare`, external: true });
    expect(created?.href.startsWith('#')).toBe(false);
  });

  it('marks work to fix and work that waits with different icons', () => {
    expect(['drift', 'confirm', 'revert'].map(needIcon)).toEqual(['alert', 'alert', 'alert']);
    expect(['request', 'workflow', 'exception'].map(needIcon)).toEqual(['clock', 'clock', 'clock']);
  });
});

describe('repository state', () => {
  it('names every state in the words of the design and refuses another word', () => {
    expect(homeStates.map(homeStateLabel)).toEqual([
      'clean',
      'drift',
      'expired',
      'changed outside the flow',
      'not set up',
      'unknown',
    ]);
    expect(readHome({ state: 'outside_flow', status: 'x', needs_you: [] }).state).toBe('outside_flow');
    expect(readHome({ status: 'clean', needs_you: [] }).state).toBe('unknown');
  });
});

describe('the one primary action', () => {
  const repo = { owner: 'acme', name: 'app', fullName: 'acme/app' };
  const github = 'https://github.com/acme/app';
  const home = (over: Partial<Home>): Home => ({ state: 'clean', status: '', needs_you: [], ...over });
  const setup = (over: Partial<HomeSetup>): HomeSetup => ({
    approval: needed,
    passkey_known: true,
    state: 'not_admitted',
    policy: true,
    workflow: true,
    passkey: true,
    open_pull: 0,
    missing: [],
    rules: [],
    ...over,
  });
  const gateOff = [
    { code: 'pull_requests', state: 'warning', text: '' },
    { code: 'gate_check', state: 'warning', text: '' },
  ];
  const workflowNeed = { kind: 'workflow', text: 'Review workflow change · pull request #417', href: '/r/acme/app/owner/workflow?pull=417' };

  it.each([
    ['a workflow change names its verb', home({ needs_you: [workflowNeed] }), 'Review workflow change', '/r/acme/app/owner/workflow?pull=417', false],
    [
      'a drift issue opens GitHub',
      home({ needs_you: [{ kind: 'drift', text: 'x', href: `${github}/issues/72` }] }),
      'Open the drift issue',
      `${github}/issues/72`,
      true,
    ],
    [
      'the first owner item with a reachable link wins',
      home({ needs_you: [{ kind: 'workflow', text: 'Workflow change in #4 waits for an owner.', href: '' }, { kind: 'request', text: 'Request #9 waits.', href: '/r/acme/app/requests/9' }] }),
      'Open the request',
      '/r/acme/app/requests/9',
      false,
    ],
    [
      'an owner item beats a setup step',
      home({ needs_you: [workflowNeed], setup: setup({ approval: active, state: 'approved', rules: gateOff }) }),
      'Review workflow change',
      '/r/acme/app/owner/workflow?pull=417',
      false,
    ],
    [
      'an eligible owner reviews first',
      home({ state: 'not_set_up', setup: setup({ open_pull: 5, owner_action: { label: 'Review', href: '/r/acme/app/owner/workflow?pull=5' } }) }),
      'Review and approve',
      '/r/acme/app/owner/workflow?pull=5',
      false,
    ],
    ['step 1 has no button', home({ state: 'not_set_up', setup: setup({ policy: false, workflow: false, first_pull: undefined }) }), undefined, undefined, undefined],
    ['step 4 opens the ruleset settings', home({ setup: setup({ approval: active, state: 'approved', rules: gateOff }) }), 'Open ruleset settings', `${github}/settings/rules`, true],
  ])('%s', (_name, input, label, href, external) => {
    const action = homeAction(input, repo);
    expect(action?.label).toBe(label);
    expect(action?.href).toBe(href);
    expect(action?.external).toBe(external);
  });

  it('does not name an issue that does not exist yet', () => {
    const noIssue = [
      { kind: 'drift', text: 'Drift on the default branch.', href: '/r/acme/app/policy' },
      { kind: 'exception', text: '1 exception expired', href: '/r/acme/app/policy' },
    ];
    expect(homeAction(home({ needs_you: noIssue }), repo)).toBeUndefined();
    const later = { kind: 'request', text: 'Request #9 waits.', href: '/r/acme/app/requests/9' };
    expect(homeAction(home({ needs_you: [...noIssue, later] }), repo)?.label).toBe('Open the request');
  });

  it('flags only the setup review as needing a passkey', () => {
    const review = { label: 'Review', href: '/r/acme/app/owner/workflow?pull=5' };
    expect(homeAction(home({ setup: setup({ open_pull: 5, owner_action: review }) }), repo)?.needsPasskey).toBe(true);
    expect(homeAction(home({ needs_you: [workflowNeed] }), repo)?.needsPasskey).toBeUndefined();
  });

  it('offers nothing when nothing is due and never an in-page anchor', () => {
    expect(homeAction(home({}), repo)).toBeUndefined();
    expect(homeAction(home({ needs_you: [{ kind: 'drift', text: 'x', href: 'https://github.com/other/app/issues/1' }] }), repo)).toBeUndefined();
    const created = homeAction(home({ state: 'not_set_up', setup: setup({ policy: true, workflow: false, open_pull: 0 }) }), repo);
    expect(created).toEqual({ label: 'Create a pull request on GitHub', href: `${github}/compare`, external: true });
    expect(created?.href.startsWith('#')).toBe(false);
  });

  it('marks work to fix and work that waits with different icons', () => {
    expect(['drift', 'confirm', 'revert'].map(needIcon)).toEqual(['alert', 'alert', 'alert']);
    expect(['request', 'workflow', 'exception'].map(needIcon)).toEqual(['clock', 'clock', 'clock']);
  });
});

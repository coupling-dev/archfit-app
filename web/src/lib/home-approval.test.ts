import { describe, expect, it } from 'vitest';
import {
  approvalNeedsReview,
  approvalText,
  homeHref,
  ownerReviewLabel,
  readApproval,
  setupStepState,
  type ApprovalSummary,
  type HomeSetup,
} from './home';

const base: HomeSetup = {
  approval: { status: 'needed', kind: 'workflow', pull_request: 203 },
  state: 'not_admitted',
  policy: true,
  workflow: false,
  passkey: true,
  passkey_known: true,
  open_pull: 203,
  missing: [],
  rules: [{ code: 'gate_check', state: 'warning', text: '' }],
  owner_action: { label: 'Review workflow change', href: '/r/acme/app/owner/workflow?pull=203' },
};
const pending: ApprovalSummary = {
  status: 'approved_pending_merge',
  kind: 'workflow',
  pull_request: 203,
  current_head: 'h1',
  approved_head: 'h1',
};

describe('owner approval is separate from activation and gate rules', () => {
  it.each([
    [pending, true, false, 'Approved for PR #203. Waiting for merge.'],
    [{ ...pending, merged: true }, true, false, 'Approval recorded; checking activation'],
    [
      { ...pending, status: 'stale', current_head: 'h2' },
      false,
      true,
      'PR #203 changed after approval. Review the new head.',
    ],
    [{ ...pending, status: 'unknown' }, false, false, 'Could not confirm approval'],
    [
      { status: 'active', kind: 'first' },
      true,
      false,
      'The approved policy and workflow are active on the default branch.',
    ],
    [{ status: 'needed', kind: 'workflow' }, false, true, 'Review the proposed files before approving.'],
  ] satisfies [ApprovalSummary, boolean, boolean, string][])(
    'renders %j without inferring success from navigation',
    (approval, signed, review, text) => {
      const setup = { ...base, approval };
      expect(setupStepState(setup).done[2]).toBe(signed);
      expect(setupStepState(setup).done[3]).toBe(false);
      expect(approvalNeedsReview(setup)).toBe(review);
      expect(approvalText(approval)).toBe(text);
    },
  );

  it('does not call setup complete while merge processing is outstanding', () => {
    const state = setupStepState({
      ...base,
      approval: pending,
      rules: [
        { code: 'pull_requests', state: 'ok', text: '' },
        { code: 'gate_check', state: 'ok', text: '' },
      ],
    });
    expect(state.done).toEqual([true, true, true, true]);
    expect(state.next).toBe('Waiting for activation on the default branch.');
  });

  it('does not infer a signature from a missing owner action', () => {
    expect(setupStepState({ ...base, owner_action: undefined }).done[2]).toBe(false);
  });

  it('allows review without a passkey', () => {
    expect(ownerReviewLabel(base)).toBe('Review and approve');
    expect(ownerReviewLabel({ ...base, passkey: false })).toBe('Review proposed files');
  });

  it.each([
    null,
    {},
    { status: 'success', kind: 'first' },
    { status: 'needed', kind: 'revert' },
    { ...pending, pull_request: 0 },
    { ...pending, approved_head: 'old' },
    { ...pending, current_head: '' },
    { ...pending, merged: 'yes' },
  ])('rejects malformed approval evidence %j', (value) => expect(() => readApproval(value)).toThrow());

  it('accepts pending evidence and rebuilds only same-repository PR navigation', () => {
    expect(readApproval(pending)).toEqual(pending);
    const repo = { owner: 'acme', name: 'app', fullName: 'acme/app' };
    expect(homeHref(repo, 'https://github.com/Acme/App/pull/203')).toBe('https://github.com/acme/app/pull/203');
    for (const href of [
      'https://github.com/other/app/pull/203',
      'https://github.com/acme/app/pull/203?evil=1',
      'javascript:alert(1)',
    ])
      expect(homeHref(repo, href)).toBeUndefined();
  });
});

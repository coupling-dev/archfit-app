import { describe, expect, it } from 'vitest';
import { setupLabel, enforcementLabel, nextStep } from './repos';
import type { RepoRow } from './api';

describe('repository readiness vocabulary', () => {
  it.each([
    ['not_started', 'Not started'],
    ['first_approval', 'First approval'],
    ['workflow_review', 'Workflow review'],
    ['policy_approved', 'Policy approved'],
    [undefined, 'Check'],
    ['unknown', 'Check'],
    ['clean', 'Check'],
    ['future', 'Check'],
  ])('labels setup %s as %s', (stage, label) => expect(setupLabel(stage)).toBe(label));

  it.each([
    ['required', 'Required'],
    ['not_required', 'Not required'],
    ['check', 'Check'],
    ['unknown', 'Check'],
    ['advisory', 'Check'],
  ])('labels enforcement %s as %s', (state, label) => expect(enforcementLabel(state)).toBe(label));
});

describe('repository next destination', () => {
  const row: RepoRow = {
    full_name: 'acme/app',
    state: 'unknown',
    enforcement: 'check',
    next_step: { text: 'Open Home', href: '/r/acme/app' },
  };
  it.each(['/r/acme/app', '/r/acme/app/pull/203', '/r/acme/app/pull/8'])(
    'keeps the server-selected destination %s',
    (href) => {
      const next = { text: 'Continue', href };
      expect(nextStep({ ...row, next_step: next })).toEqual(next);
    },
  );
  it.each([
    'javascript:alert(1)',
    '//other.example',
    '/r/other/app',
    '/r/acme/app/pull/0',
    '/r/acme/app/owner/first',
    '/r/acme/app/owner/first?pull=8',
    '/r/acme/app/owner/first?pull=-1',
    '/r/acme/app/owner/first?pull=8&other=x',
    '/r/acme/app/pull/203/../../other',
    '/r/acme/app/owner/workflow',
  ])('does not invent or accept an unexpected destination %s', (href) => {
    expect(nextStep({ ...row, next_step: { text: 'Continue', href } })).toEqual(row.next_step);
  });
});

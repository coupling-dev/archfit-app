import { describe, expect, it } from 'vitest';
import { enforcementLabel, nextStep, repoStateLabel } from './repos';
import type { RepoRow } from './api';

describe('repository state and archfit/gate words', () => {
  it.each([
    ['clean', 'clean'],
    ['drift', 'drift'],
    ['expired', 'expired'],
    ['policy changed outside the flow', 'changed outside the flow'],
    ['advisory', 'advisory'],
    ['not set up', 'not set up'],
    ['unknown', 'unknown'],
    ['not admitted', 'not admitted'],
  ])('labels state %s as %s', (state, label) => expect(repoStateLabel(state)).toBe(label));

  it.each([
    ['required', 'required'],
    ['not_required', 'not required'],
    ['check', 'not confirmed'],
    ['unknown', 'not confirmed'],
    ['future', 'not confirmed'],
  ])('labels enforcement %s as %s', (state, label) => expect(enforcementLabel(state)).toBe(label));
});

describe('repository next step', () => {
  const row: RepoRow = {
    full_name: 'acme/app',
    state: 'clean',
    setup_stage: 'policy_approved',
    enforcement: 'required',
    next_step: { text: 'Continue setup', href: '/r/acme/app' },
  };

  it('names no next step once setup is complete', () => {
    expect(nextStep(row)).toBeUndefined();
  });

  it('turns the required check on when the policy is approved but the check is not required', () => {
    expect(nextStep({ ...row, enforcement: 'not_required' })).toEqual({ text: 'Turn on the required check' });
  });

  it('keeps a server-selected pull request destination as a link', () => {
    const incomplete = { ...row, setup_stage: 'workflow_review', next_step: { text: 'Review workflow change', href: '/r/acme/app/pull/203' } };
    expect(nextStep(incomplete)).toEqual({ text: 'Review workflow change', href: '/r/acme/app/pull/203' });
  });

  it('shows the repository page as plain text, since the name already links there', () => {
    const incomplete = { ...row, setup_stage: 'not_started' };
    expect(nextStep(incomplete)).toEqual({ text: 'Continue setup' });
  });

  it.each([
    'javascript:alert(1)',
    '//other.example',
    '/r/other/app',
    '/r/acme/app/pull/0',
    '/r/acme/app/owner/first',
    '/r/acme/app/owner/first?pull=8',
    '/r/acme/app/pull/203/../../other',
  ])('does not invent or accept an unexpected destination %s', (href) => {
    expect(nextStep({ ...row, setup_stage: 'not_started', next_step: { text: 'Continue', href } })).toBeUndefined();
  });

  it('directs an unadmitted account to beta access', () => {
    expect(nextStep({ ...row, waiting: true })).toEqual({ text: 'Archfit is in a closed beta. Ask for access: https://github.com/coupling-dev/archfit/issues/new?template=beta-access.yml' });
  });
});

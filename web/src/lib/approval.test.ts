import { describe, expect, it } from 'vitest';
import type { Approval } from './api';
import { digestWord, offersApprove, offersOverride, stateLine } from './approval';

const full = 'ab'.repeat(32);
const missing: Approval = {
  head_sha: 'a'.repeat(40),
  base_sha: 'b'.repeat(40),
  paths: ['.archfit-baseline.json', '.archfit-labels.yaml'],
  digests: { '.archfit-baseline.json': full, '.archfit-labels.yaml': '' },
  short_digests: { '.archfit-baseline.json': full.slice(0, 7), '.archfit-labels.yaml': '' },
  state: 'missing',
  can_decide: true,
  can_approve: true,
  can_withdraw: false,
  notification_problem: false,
  owners: ['alice'],
  settings_ref: 'b'.repeat(40),
  settings_url: 'https://github.com/acme/repo/blob/' + 'b'.repeat(40) + '/.archfit-app.yaml',
  gate_mode: 'required',
  required: true,
  decision_url: 'https://app.example/r/acme/repo/pull/1',
  objections: [],
  history: [],
  author_is_agent: false,
};

const appObjection = {
  id: 'app:2',
  login: 'bob',
  source: 'app' as const,
  reason: 'Please clarify',
  head_sha: missing.head_sha,
  at: '2026-11-01T12:00:00Z',
  overridden: false,
};
const githubObjection = {
  id: 'github:77',
  login: 'bob',
  source: 'github' as const,
  reason: '',
  head_sha: missing.head_sha,
  at: '2026-11-01T12:00:00Z',
  overridden: false,
};

describe('owner decision actions', () => {
  it.each([
    ['an eligible owner with no objection', missing, true],
    ['someone the server refuses', { ...missing, can_approve: false, reason: 'Only an architecture owner …' }, false],
    ['an approved head', { ...missing, state: 'granted', by: 'alice', can_approve: false }, false],
    ['a granted state the server still marks approvable', { ...missing, state: 'granted', by: 'alice' }, false],
    ['a fork, whose files were not read', { ...missing, digests: null, short_digests: null }, false],
    ['another owner has an App objection', { ...missing, objections: [appObjection] }, false],
    ['a native GitHub objection remains independent', { ...missing, objections: [githubObjection] }, false],
    [
      'this owner can resolve their own App objection',
      { ...missing, objections: [{ ...appObjection, login: 'Alice' }] },
      true,
    ],
  ] as [string, Approval, boolean][])('%s: ordinary approval', (_name: string, a: Approval, want: boolean) => {
    expect(offersApprove(a, 'alice')).toBe(want);
  });

  it.each([
    ['no active objections', missing, false],
    ['another owner requested changes in the App', { ...missing, objections: [appObjection] }, true],
    ['a native review requested changes', { ...missing, objections: [githubObjection] }, true],
    ['approval is unavailable to this login', { ...missing, can_approve: false, objections: [githubObjection] }, false],
    ['a fork has no candidate digest', { ...missing, digests: null, objections: [githubObjection] }, false],
  ] as [string, Approval, boolean][])('%s: explicit override', (_name: string, a: Approval, want: boolean) => {
    expect(offersOverride(a)).toBe(want);
  });
});

describe('digestWord', () => {
  it('names the short digest of a file and says a deletion is what is approved', () => {
    expect(digestWord(missing, '.archfit-baseline.json')).toBe('sha256 abababa');
    expect(digestWord(missing, '.archfit-labels.yaml')).toBe('deleted by this commit');
    expect(digestWord({ ...missing, digests: null }, '.archfit-baseline.json')).toBe('not read: the head is in a fork');
  });
});

describe('stateLine', () => {
  it('does not imply that an approval is a successful check or a merge', () => {
    expect(stateLine({ ...missing, state: 'granted', by: 'alice' }, 'a1b2c3d')).toContain(
      'does not mean the check passed',
    );
    expect(stateLine(missing, 'a1b2c3d')).toContain('a new push needs a new decision');
    expect(stateLine({ ...missing, objections: [githubObjection] }, 'a1b2c3d')).toContain('requested changes');
  });
});

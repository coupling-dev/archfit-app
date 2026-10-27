import { describe, expect, it } from 'vitest';
import type { Approval } from './api';
import { digestWord, offersApprove, stateLine } from './approval';

const full = 'ab'.repeat(32);
const missing: Approval = {
  head_sha: 'a'.repeat(40),
  paths: ['.archfit-baseline.json', '.archfit-labels.yaml'],
  digests: { '.archfit-baseline.json': full, '.archfit-labels.yaml': '' },
  short_digests: { '.archfit-baseline.json': full.slice(0, 7), '.archfit-labels.yaml': '' },
  state: 'missing',
  can_approve: true,
  author_is_agent: false,
};

describe('offersApprove', () => {
  it.each([
    ['an owner while missing', missing, true],
    ['someone the server refuses', { ...missing, can_approve: false, reason: 'Only an architecture owner …' }, false],
    ['an approved head', { ...missing, state: 'granted', by: 'alice', can_approve: false }, false],
    ['a granted state the server still marks approvable', { ...missing, state: 'granted', by: 'alice' }, false],
    ['a fork, whose files were not read', { ...missing, digests: null, short_digests: null }, false],
  ] as [string, Approval, boolean][])('%s', (_, a, want) => {
    expect(offersApprove(a)).toBe(want);
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
  it('names the approver of the exact commit, or asks for one', () => {
    expect(stateLine({ ...missing, state: 'granted', by: 'alice' }, 'a1b2c3d')).toBe(
      'Approved by @alice for commit a1b2c3d.',
    );
    expect(stateLine(missing, 'a1b2c3d')).toContain('must approve commit a1b2c3d');
  });
});

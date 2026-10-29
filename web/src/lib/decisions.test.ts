import { describe, expect, it } from 'vitest';
import type { DecisionRequest } from './api';
import { decisionPath, offersDecision, statusTone, statusWord } from './decisions';

const pending: DecisionRequest = {
  finding_id: 'a'.repeat(32),
  policy_digest: 'b'.repeat(64),
  rule_id: 'boundary.no-import',
  from: 'a',
  to: 'b',
  status: 'pending',
  requester: 'agent-7',
  why: 'a needs b',
  alternatives: [],
  can_decide: true,
};

describe('offersDecision', () => {
  it.each([
    ['an owner while pending', pending, true],
    ['someone the server refuses', { ...pending, can_decide: false }, false],
    ['an approved request the server still marks decidable', { ...pending, status: 'approved' }, false],
    ['a stale request', { ...pending, status: 'stale' }, false],
  ] as [string, DecisionRequest, boolean][])('%s', (_, r, want) => {
    expect(offersDecision(r)).toBe(want);
  });
});

describe('statusWord and statusTone', () => {
  it('say the reason beside the status, and never rely on colour alone', () => {
    expect(statusWord({ ...pending, status: 'stale', reason: 'head moved' })).toBe('stale (head moved)');
    expect(statusWord(pending)).toBe('pending');
    expect(statusTone({ ...pending, status: 'approved' })).toBe('success');
    expect(statusTone(pending)).toBe('attention');
  });
});

describe('decisionPath', () => {
  it('names the pull request, the finding and the policy digest the page showed', () => {
    expect(decisionPath(pending, 4, 'decline')).toBe(`pulls/4/decisions/${'a'.repeat(32)}/${'b'.repeat(64)}/decline`);
  });
});

import { describe, expect, it } from 'vitest';
import { actionTitle, waitingFor } from './owner-actions';

const now = Date.parse('2026-11-21T12:00:00Z');

describe('waitingFor', () => {
  it.each([
    ['2026-11-21T11:59:50Z', '1 min'],
    ['2026-11-21T11:15:00Z', '45 min'],
    ['2026-11-21T09:00:00Z', '3 h'],
    ['2026-11-20T11:00:00Z', '1 day'],
    ['2026-11-18T08:00:00Z', '3 days'],
  ])('%s waits %s', (since, want) => expect(waitingFor(since, now)).toBe(want));

  it.each([undefined, '', 'not a time', '2026-11-22T00:00:00Z'])('says nothing for %s', (since) =>
    expect(waitingFor(since, now)).toBe(''),
  );
});

describe('actionTitle', () => {
  it('names the pull request when there is one', () => {
    expect(actionTitle({ label: 'Review the workflow change', pull_request: 413 })).toBe('Review the workflow change #413');
    expect(actionTitle({ label: 'Confirm the policy' })).toBe('Confirm the policy');
  });
});

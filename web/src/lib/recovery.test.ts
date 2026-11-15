import { describe, expect, it } from 'vitest';
import { ApiError } from './api';
import { canRetryRead, ownerUnavailable, policyReadFailure } from './recovery';

describe('read recovery', () => {
  it.each([0, 200, 408, 429, 500, 503])('offers retry for %s', (status) => {
    expect(canRetryRead(new ApiError(status, 'failed'))).toBe(true);
  });
  it.each([401, 403, 404, 409, 422])('does not retry deterministic refusal %s', (status) => {
    expect(canRetryRead(new ApiError(status, 'failed'))).toBe(false);
  });
  it('does not turn malformed artifacts into an absence of work', () => {
    expect(canRetryRead(new Error('bad artifact'))).toBe(false);
    expect(ownerUnavailable(new Error('bad artifact'), 'first', 0)).toBe('');
  });
  it('does not blame a missing first policy on broken YAML', () => {
    const text = policyReadFailure(
      new ApiError(409, 'The policy on the default branch could not be read. Fix .archfit.yaml first.'),
    );
    expect(text).not.toContain('Fix');
    expect(text).toContain('could not be read');
    expect(text).toContain('first policy');
  });
  it('preserves other policy diagnostics', () => {
    expect(policyReadFailure(new ApiError(409, 'Version 2'))).toBe('Version 2');
  });
  it.each([
    ['confirm', 0, 'The repository no longer needs this action. Reload the page.', 'No policy confirmation needed'],
    ['first', 7, 'The repository no longer needs this action. Reload the page.', 'This approval is not needed'],
    ['revert', 0, 'The policy has no change outside the flow to revert. Reload the page.', 'Nothing to revert'],
    ['revert', 0, 'The policy already matches the approved policy. There is nothing to revert.', 'Nothing to revert'],
  ] as const)('recognizes %s absence only from the known conflict', (action, pull, message, expected) => {
    expect(ownerUnavailable(new ApiError(409, message), action, pull)).toBe(expected);
    expect(ownerUnavailable(new ApiError(503, message), action, pull)).toBe('');
  });
  it('does not suggest defective PR contents when no PR was selected', () => {
    const error = new ApiError(
      409,
      'This pull request cannot be approved. Check its policy, workflow, head and target branch.',
    );
    expect(ownerUnavailable(error, 'first', 0)).toContain('Open current setup');
    expect(ownerUnavailable(error, 'first', 7)).toBe('');
  });
});

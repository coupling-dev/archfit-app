import { describe, expect, it } from 'vitest';
import { ApiError } from './api';
import { canRetryRead, ownerStale, policyReadFailure } from './recovery';

describe('read recovery', () => {
  it.each([0, 200, 408, 429, 500, 503])('offers retry for %s', (status) => {
    expect(canRetryRead(new ApiError(status, 'failed'))).toBe(true);
  });
  it.each([401, 403, 404, 409, 422])('does not retry deterministic refusal %s', (status) => {
    expect(canRetryRead(new ApiError(status, 'failed'))).toBe(false);
  });
  it('does not turn malformed artifacts into an absence of work', () => {
    expect(canRetryRead(new Error('bad artifact'))).toBe(false);
    expect(ownerStale(new Error('bad artifact'), 'first', 0)).toBeNull();
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
  const noLongerNeeded = 'The repository no longer needs this action. Reload the page.';
  const notApprovable =
    'This pull request cannot be approved. It must be open, from this repository, into the default branch, with a policy and a workflow file.';
  const noPolicy = 'The policy on the default branch could not be read. Fix .archfit.yaml first.';
  it.each([
    ['first', 7, noLongerNeeded, 'The first policy is already approved.', 'repository'],
    ['workflow', 7, noLongerNeeded, 'Nothing to approve: this repository does not need a workflow approval now.', 'repository'],
    ['confirm', 0, noLongerNeeded, 'Nothing to confirm: the policy has not changed outside the flow.', 'repository'],
    ['revert', 0, noLongerNeeded, 'Nothing to revert: the policy has not changed outside the flow.', 'repository'],
    ['revert', 0, 'The policy has no change outside the flow to revert. Reload the page.', 'Nothing to revert: the policy has not changed outside the flow.', 'repository'],
    ['revert', 0, 'The policy already matches the approved policy. There is nothing to revert.', 'Nothing to revert: the default branch already holds the approved policy.', 'repository'],
    ['workflow', 213, 'This pull request is merged. There is nothing to approve.', 'Nothing to approve: pull request #213 is merged.', 'pull'],
  ] as const)('says why %s #%s has nothing to do, only from the known conflict', (action, pull, message, reason, next) => {
    expect(ownerStale(new ApiError(409, message), action, pull)).toEqual({ reason, next });
    expect(ownerStale(new ApiError(503, message), action, pull)).toBeNull();
  });
  it('keeps a refused or unreadable approval an error: it may be fixable, not finished', () => {
    expect(ownerStale(new ApiError(409, notApprovable), 'first', 7)).toBeNull();
    expect(ownerStale(new ApiError(409, notApprovable), 'workflow', 7)).toBeNull();
    expect(ownerStale(new ApiError(409, notApprovable), 'first', 0)).toBeNull();
    expect(ownerStale(new ApiError(409, noPolicy), 'first', 0)).toBeNull();
  });
  it('does not call a refusal on another action a merged pull request', () => {
    expect(ownerStale(new ApiError(409, 'This pull request is merged. There is nothing to approve.'), 'first', 0)).toBeNull();
  });
});

import { ApiError } from './api';
import type { OwnerKind } from './owner';

// Only read/service failures offer a retry; a conflict needs a different destination.
export function canRetryRead(error: unknown): boolean {
  return (
    error instanceof ApiError &&
    (error.status === 0 || error.status === 200 || error.status === 408 || error.status === 429 || error.status >= 500)
  );
}

export function policyReadFailure(error: ApiError): string {
  if (
    error.status === 409 &&
    error.message === 'The policy on the default branch could not be read. Fix .archfit.yaml first.'
  ) {
    return 'The policy on the default branch could not be read. Open current setup to check whether a first policy is still being prepared or the existing file needs a change.';
  }
  return error.message;
}

// The existing API has no structured stale-action reason. Match only its exact known
// conflict sentences; other failures must remain errors rather than "nothing to do".
export function ownerUnavailable(error: unknown, action: OwnerKind, pull: number): string {
  if (!(error instanceof ApiError) || error.status !== 409) return '';
  if (error.message === 'The repository no longer needs this action. Reload the page.') {
    return action === 'confirm' ? 'No policy confirmation needed' : 'This approval is not needed';
  }
  if (
    action === 'revert' &&
    (error.message === 'The policy has no change outside the flow to revert. Reload the page.' ||
      error.message === 'The policy already matches the approved policy. There is nothing to revert.')
  ) {
    return 'Nothing to revert';
  }
  if (
    action === 'first' &&
    !pull &&
    error.message === 'This pull request cannot be approved. Check its policy, workflow, head and target branch.'
  ) {
    return 'This approval is not available. Open current setup to find the policy that needs approval.';
  }
  return '';
}

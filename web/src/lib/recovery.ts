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

// An owner link can outlive its action. The API has no structured reason, so match only its exact known conflict
// sentences (internal/control/ui/wording.go); any other failure stays an error rather than "nothing to do".
export interface OwnerStale {
  reason: string;
  // Where the one next step goes: the repository, or the pull request page of a merged pull request.
  next: 'repository' | 'pull';
}

const noLongerNeeded = 'The repository no longer needs this action. Reload the page.';
const notOutside = 'The policy has no change outside the flow to revert. Reload the page.';
const nothingToRevert = 'The policy already matches the approved policy. There is nothing to revert.';
const merged = 'This pull request is merged. There is nothing to approve.';
const notApprovable =
  'This pull request cannot be approved. It must be open, from this repository, into the default branch, with a policy and a workflow file.';

export function ownerStale(error: unknown, action: OwnerKind, pull: number): OwnerStale | null {
  if (!(error instanceof ApiError) || error.status !== 409) return null;
  const message = error.message;
  if (message === noLongerNeeded) {
    const reason = {
      first: 'The first policy is already approved.',
      workflow: 'Nothing to approve: this repository does not need a workflow approval now.',
      confirm: 'Nothing to confirm: the policy has not changed outside the flow.',
      revert: 'Nothing to revert: the policy has not changed outside the flow.',
    }[action];
    return { reason, next: 'repository' };
  }
  if (action === 'revert' && message === notOutside) {
    return { reason: 'Nothing to revert: the policy has not changed outside the flow.', next: 'repository' };
  }
  if (action === 'revert' && message === nothingToRevert) {
    return { reason: 'Nothing to revert: the default branch already holds the approved policy.', next: 'repository' };
  }
  if (pull > 0 && message === merged) {
    return { reason: `Nothing to approve: pull request #${pull} is merged.`, next: 'pull' };
  }
  if (action === 'first' && !pull && message === notApprovable) {
    return { reason: 'Nothing to approve yet: the default branch has no policy of version 3.', next: 'repository' };
  }
  return null;
}

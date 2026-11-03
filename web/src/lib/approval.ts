// The words of the architecture-decision banner. The server decides who may act; these helpers only hide actions
// that the server will refuse because another owner or a native review has an active objection.

import type { Approval } from './api';

export function offersApprove(a: Approval, login = ''): boolean {
  const blocked = a.objections.some(
    (objection) => !objection.overridden && (objection.source === 'github' || !sameLogin(objection.login, login)),
  );
  return a.state === 'missing' && a.can_approve && a.digests !== null && !blocked;
}

export function offersOverride(a: Approval): boolean {
  return (
    a.state === 'missing' &&
    a.can_approve &&
    a.digests !== null &&
    a.objections.some((objection) => !objection.overridden)
  );
}

function sameLogin(a: string, b: string): boolean {
  return a.toLowerCase() === b.toLowerCase();
}

// digestWord is what the page says a protected file holds at the head.
export function digestWord(a: Approval, path: string): string {
  if (!a.digests) return 'not read: the head is in a fork';
  return a.digests[path] ? `sha256 ${a.short_digests?.[path] ?? ''}` : 'deleted by this commit';
}

export function stateLine(a: Approval, head: string): string {
  if (a.objections.some((objection) => !objection.overridden))
    return `An owner requested changes on candidate ${head}. Review the objections below.`;
  return a.state === 'granted'
    ? `An eligible owner approved candidate ${head}. This does not mean the check passed or the pull request merged.`
    : `An architecture owner must decide on candidate ${head}; a new push needs a new decision.`;
}

// The words of the owner approval on the pull request page. The server decides who may approve; the page only shows
// the button it offers, and only while the approval is missing.

import type { Approval } from './api';

export function offersApprove(a: Approval): boolean {
  return a.state === 'missing' && a.can_approve && a.digests !== null;
}

// digestWord is what the page says a protected file holds at the head.
export function digestWord(a: Approval, path: string): string {
  if (!a.digests) return 'not read: the head is in a fork';
  return a.digests[path] ? `sha256 ${a.short_digests?.[path] ?? ''}` : 'deleted by this commit';
}

export function stateLine(a: Approval, head: string): string {
  return a.state === 'granted'
    ? `Approved by @${a.by} for commit ${head}.`
    : `An architecture owner must approve commit ${head} before the gate accepts these files.`;
}

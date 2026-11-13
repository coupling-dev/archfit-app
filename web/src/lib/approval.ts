// The words of the read-only architecture-decision box of the pull request page.

import type { Approval } from './api';

// digestWord is what the page says a protected file holds at the head.
export function digestWord(a: Approval, path: string): string {
  if (!a.digests) return 'not read: the head is in a fork';
  return a.digests[path] ? `sha256 ${a.short_digests?.[path] ?? ''}` : 'deleted by this commit';
}

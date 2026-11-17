import { repoPage } from './route';
import type { RepoRow } from './api';

// The repository state words of the design. The App sends its own phrasing for one of them.
const stateWords: Record<string, string> = {
  'policy changed outside the flow': 'changed outside the flow',
};

export function repoStateLabel(state: string): string {
  return stateWords[state] ?? state;
}

// archfit/gate reads required only when Home step 4 is done; anything the App could not read is not confirmed.
export function enforcementLabel(state: string): string {
  switch (state) {
    case 'required':
      return 'required';
    case 'not_required':
      return 'not required';
    default:
      return 'not confirmed';
  }
}

export interface RepoNext {
  text: string;
  href?: string; // absent when the text stands alone, e.g. the repository name already links there
}

// A row names its next step only while setup is incomplete. Home and Pull resolve owner-action eligibility; the list
// must not bypass them, so a server destination is kept only when it is one of those pages.
export function nextStep(row: RepoRow): RepoNext | undefined {
  const base = repoPage(row.full_name);
  if (row.waiting) return { text: 'Ask an operator to admit this installation.' };
  if (row.setup_stage === 'policy_approved') {
    return row.enforcement === 'required' ? undefined : { text: 'Turn on the required check' };
  }
  const next = row.next_step;
  // "Open Home" is the App's fallback for a row it cannot place: it names no step, so the list shows none.
  if (
    !next?.text ||
    next.text === 'Open Home' ||
    !(next.href === base || (next.href.startsWith(base + '/') && /^\/pull\/[1-9]\d*$/.test(next.href.slice(base.length))))
  )
    return undefined;
  return next.href === base ? { text: next.text } : next;
}

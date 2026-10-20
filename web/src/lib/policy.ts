// The words and steps of the policy page. Everything here reads the server's answer; nothing decides what may be
// written: the server checks the branch, the head and the permission on every write.

import type { Diagnostic, PolicyPage } from './api';

export interface Step {
  label: string;
  done: boolean;
  current: boolean;
}

// A check has run once it has a conclusion. No run yet, a run in progress and a state that cannot be read have not.
const notRun = new Set(['no state yet', 'in progress', 'unknown']);

const steps = ['Saved to a draft', 'Checked by Archfit', 'Ready for review', 'Owner approves', 'Merged on GitHub'];

// timeline is where the draft stands on the way to publication. The App never approves or merges: those two steps
// happen on GitHub, so an open review stays at "Owner approves" until the merge.
export function timeline(p: Pick<PolicyPage, 'draft' | 'checks'>): Step[] {
  let done = 0;
  switch (p.draft) {
    case 'draft':
      done = p.checks.length > 0 && p.checks.every((c) => !notRun.has(c.state)) ? 2 : 1;
      break;
    case 'awaiting_review':
      done = 3;
      break;
    case 'published':
      done = steps.length;
      break;
  }
  return steps.map((label, i) => ({ label, done: i < done, current: i === done }));
}

// lineOfHash is the line a #L<n> link names (the graph links each module to its line), 0 for none.
export function lineOfHash(hash: string): number {
  const m = /^#L([1-9][0-9]{0,6})$/.exec(hash);
  return m ? Number(m[1]) : 0;
}

// placeOf is where a problem is, in words; '' for a problem of the whole text.
export function placeOf(d: Diagnostic): string {
  if (!d.line) return '';
  return d.column ? `Line ${d.line}, column ${d.column}` : `Line ${d.line}`;
}

export const runWords: Record<NonNullable<PolicyPage['run']>['state'], string> = {
  pending: 'queued or running',
  failed: 'failed',
  completed: 'done: the proposal is a new commit on the draft',
  unknown: 'cannot tell',
};

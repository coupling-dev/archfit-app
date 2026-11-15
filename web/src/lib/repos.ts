import { repoPage } from './route';
import type { RepoRow } from './api';

export function setupLabel(stage: string | undefined): string {
  switch (stage) {
    case 'not_started':
      return 'Not started';
    case 'first_approval':
      return 'First approval';
    case 'workflow_review':
      return 'Workflow review';
    case 'policy_approved':
      return 'Policy approved';
    default:
      return 'Check';
  }
}

export function enforcementLabel(state: string): string {
  switch (state) {
    case 'required':
      return 'Required';
    case 'not_required':
      return 'Not required';
    default:
      return 'Check';
  }
}

// Home and Pull resolve owner-action eligibility; the list must not bypass them.
export function nextStep(row: RepoRow): { text: string; href: string } {
  const base = repoPage(row.full_name);
  const next = row.next_step;
  if (
    next &&
    next.text &&
    (next.href === base ||
      (next.href.startsWith(base + '/') && /^pull\/[1-9]\d*$/.test(next.href.slice(base.length + 1))))
  ) {
    return next;
  }
  return { text: 'Open Home', href: base };
}

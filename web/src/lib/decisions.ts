// The words of the decision requests on the pull request page. The server decides who may decide; the page only
// shows the buttons it offers, and only while a request is pending.

import type { DecisionRequest } from './api';

const tones: Record<DecisionRequest['status'], string> = {
  pending: 'attention',
  approved: 'success',
  declined: 'neutral',
  stale: 'neutral',
};

export function statusTone(r: DecisionRequest): string {
  return tones[r.status] ?? 'neutral';
}

export function statusWord(r: DecisionRequest): string {
  return r.reason ? `${r.status} (${r.reason})` : r.status;
}

export function offersDecision(r: DecisionRequest): boolean {
  return r.status === 'pending' && r.can_decide;
}

// decisionPath is the write of a request: the exact request the page showed, named in the path.
export function decisionPath(r: DecisionRequest, number: number, action: 'approve' | 'decline'): string {
  return `pulls/${number}/decisions/${r.finding_id}/${r.policy_digest}/${action}`;
}

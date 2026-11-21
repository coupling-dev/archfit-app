import type { OwnerActionRow } from './api';

// How long an owner step has waited, in the largest whole unit: "5 min", "3 h", "2 days". Nothing when no time is
// recorded or it lies in the future, so a row never invents an age.
export function waitingFor(since: string | undefined, now: number = Date.now()): string {
  const then = since ? Date.parse(since) : NaN;
  if (Number.isNaN(then) || then > now) return '';
  const minutes = Math.floor((now - then) / 60_000);
  if (minutes < 60) return `${Math.max(minutes, 1)} min`;
  const hours = Math.floor(minutes / 60);
  if (hours < 24) return `${hours} h`;
  const days = Math.floor(hours / 24);
  return days === 1 ? '1 day' : `${days} days`;
}

// The row's name for the step: its label, with the pull request when it has one.
export function actionTitle(row: Pick<OwnerActionRow, 'label' | 'pull_request'>): string {
  return row.pull_request ? `${row.label} #${row.pull_request}` : row.label;
}

// The words of the Data sharing page. A consent names the administrator as their login was when they gave it; a record
// that holds no login says only when, and never invents a name.

import type { Precondition, SharedFeature } from './api';

const day = new Intl.DateTimeFormat('en-GB', {
  day: 'numeric',
  month: 'long',
  year: 'numeric',
  timeZone: 'UTC',
});

export function allowedLine(f: Pick<SharedFeature, 'allowed' | 'by' | 'at'>): string {
  if (!f.allowed) return 'Not allowed';
  const at = f.at ? new Date(f.at) : null;
  const on = at && !Number.isNaN(at.getTime()) ? ` on ${day.format(at)}` : '';
  return f.by ? `Allowed by @${f.by}${on}` : `Allowed${on}`;
}

export const checkWords: Record<Precondition['state'], string> = {
  met: 'done',
  not_met: 'to do',
  unknown: 'cannot tell',
};

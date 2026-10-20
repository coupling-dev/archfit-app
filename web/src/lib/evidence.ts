// The evidence table of a report: what the engine measured per dimension, in the engine's own words. Display only.

import type { Dimension } from './api';

// basisText is how many the engine measured, then what it counted in its own words, quoted as on the comment:
// '3 of 3 ("classified source files")'. The quote marks the words as the engine's, not the App's. A dimension with
// nothing to count says so.
export function basisText(d: Pick<Dimension, 'basis' | 'observed' | 'measurable'>): string {
  const counted = d.measurable ? `${d.observed} of ${d.measurable}` : 'nothing to measure';
  return d.basis ? `${counted} ("${d.basis}")` : counted;
}

const resultWords: Record<string, string> = { pass: 'pass', warn: 'warn', fail: 'fail', not_applicable: 'not applicable' };

// resultWord is a dimension's result in the comment's words: pass, warn or fail; the engine's token otherwise.
export function resultWord(gate: string): string {
  return resultWords[gate] ?? gate;
}

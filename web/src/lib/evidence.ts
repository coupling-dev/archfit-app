// The evidence table of a report: what the engine measured per dimension, in the engine's own words. Display only.

import type { Dimension } from './api';

// basisText is what the engine counted, then how many it measured: "classified source files: 3 of 3". The basis comes
// from the engine, so a ratio never reads as missing evidence when the engine says it counted something else. A
// dimension with nothing to count says so.
export function basisText(d: Pick<Dimension, 'basis' | 'observed' | 'measurable'>): string {
  if (!d.measurable) return d.basis || 'nothing to measure';
  const ratio = `${d.observed} of ${d.measurable}`;
  return d.basis ? `${d.basis}: ${ratio}` : ratio;
}

const resultWords: Record<string, string> = { pass: 'pass', warn: 'warn', fail: 'fail', not_applicable: 'not applicable' };

// resultWord is a dimension's result in the comment's words: pass, warn or fail; the engine's token otherwise.
export function resultWord(gate: string): string {
  return resultWords[gate] ?? gate;
}

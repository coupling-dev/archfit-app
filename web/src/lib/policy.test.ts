import { describe, expect, it } from 'vitest';
import type { Check, DraftState } from './api';
import { lineOfHash, placeOf, timeline } from './policy';

const check = (state: string): Check => ({ name: 'archfit/state', state, tone: 'neutral', reason: '' });

describe('timeline', () => {
  it.each<[string, DraftState, Check[], number, number]>([
    ['no draft', 'none', [], 0, 0],
    ['a closed draft starts again', 'closed', [check('healthy')], 0, 0],
    ['saved, no check yet', 'draft', [], 1, 1],
    ['saved, check running', 'draft', [check('healthy'), check('in progress')], 1, 1],
    ['saved, check unreadable', 'draft', [check('unknown')], 1, 1],
    ['checked, violations found', 'draft', [check('violations found'), check('healthy')], 2, 2],
    ['ready, waiting for an owner', 'awaiting_review', [check('healthy')], 3, 3],
    ['merged', 'published', [], 5, -1],
  ])('%s', (_, draft, checks, done, current) => {
    const steps = timeline({ draft, checks });
    expect(steps.map((s) => s.label)).toEqual([
      'Saved to a draft',
      'Checked by Archfit',
      'Ready for review',
      'Owner approves',
      'Merged on GitHub',
    ]);
    expect(steps.filter((s) => s.done)).toHaveLength(done);
    expect(steps.findIndex((s) => s.current)).toBe(current);
  });
});

describe('lineOfHash', () => {
  it.each([
    ['#L12', 12],
    ['#L1', 1],
    ['#L0', 0],
    ['#L012', 0],
    ['#L12x', 0],
    ['#L99999999', 0],
    ['', 0],
    ['#main', 0],
  ])('%s', (hash, line) => {
    expect(lineOfHash(hash)).toBe(line);
  });
});

describe('placeOf', () => {
  it.each([
    [{ line: 2, column: 5, message: 'x' }, 'Line 2, column 5'],
    [{ line: 3, message: 'x' }, 'Line 3'],
    [{ message: 'x' }, ''],
  ])('%j', (d, want) => {
    expect(placeOf(d)).toBe(want);
  });
});

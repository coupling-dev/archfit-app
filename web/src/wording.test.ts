import { describe, expect, it } from 'vitest';
import cases from '../e2e/wording-cases.json';
import { notUsedIn } from '../e2e/wording';

describe('Not used words', () => {
  it.each(cases)('matches the engine for $text', ({ text, want }) => {
    expect(notUsedIn(text)).toEqual(want);
  });
});

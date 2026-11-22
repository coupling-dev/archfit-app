import { describe, expect, it } from 'vitest';
import cases from '../e2e/wording-cases.json';
import { notUsedIn, storyNotUsedIn } from '../e2e/wording';

describe('Not used words', () => {
  it.each(cases)('matches the engine for $text', (tc) => {
    const { text, want } = tc;
    expect(notUsedIn(text)).toEqual(want);
    expect(storyNotUsedIn(text)).toEqual('storyWant' in tc ? tc.storyWant : want);
  });
});

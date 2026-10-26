import { expect, it } from 'vitest';
import { pairHref, selectedPair } from './feedback';

it('focuses only an existing admitted pair, without interpreting paths or markup', () => {
  expect(pairHref('/r/acme/app/pull/4/graph', 'a&b', '<c>')).toBe('/r/acme/app/pull/4/graph?from=a%26b&to=%3Cc%3E');
  expect(selectedPair([], '?from=forged&to=pair')).toBeUndefined();
});

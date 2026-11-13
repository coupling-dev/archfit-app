import { expect, it } from 'vitest';
import { selectedPair } from './feedback';

it('focuses only an existing admitted pair, without interpreting paths or markup', () => {
  expect(selectedPair([], '?from=forged&to=pair')).toBeUndefined();
});

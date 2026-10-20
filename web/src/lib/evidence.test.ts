import { describe, expect, it } from 'vitest';
import { basisText, resultWord } from './evidence';

describe('basisText', () => {
  it.each([
    [{ basis: 'classified source files', observed: 3, measurable: 3 }, 'classified source files: 3 of 3'],
    [{ basis: 'declared modules with a corroborated deploy unit', observed: 0, measurable: 2 }, 'declared modules with a corroborated deploy unit: 0 of 2'],
    [{ basis: '', observed: 0, measurable: 0 }, 'nothing to measure'],
    [{ basis: 'declared rules evaluated', observed: 0, measurable: 0 }, 'declared rules evaluated'],
    [{ basis: '', observed: 1, measurable: 2 }, '1 of 2'],
  ])('%o is %s', (d, want) => expect(basisText(d)).toBe(want));
});

describe('resultWord', () => {
  it.each([
    ['pass', 'pass'],
    ['warn', 'warn'],
    ['fail', 'fail'],
    ['not_applicable', 'not applicable'],
    ['newer', 'newer'],
  ])('%s is %s', (gate, want) => expect(resultWord(gate)).toBe(want));
});

import { Text } from '@codemirror/state';
import { describe, expect, it } from 'vitest';
import { marksOf } from './editor';

const doc = Text.of(['version: 2', 'modlues: {}', '', 'rules: x']);

describe('marksOf', () => {
  it('marks each problem from its column to the end of its line', () => {
    expect(marksOf(doc, [{ line: 2, column: 1, message: 'Unknown key `modlues`.' }])).toEqual([
      { from: 11, to: 22, severity: 'error', message: 'Unknown key `modlues`.' },
    ]);
    expect(marksOf(doc, [{ line: 4, column: 8, message: 'x' }])).toEqual([
      { from: 31, to: 32, severity: 'error', message: 'x' },
    ]);
  });

  it.each([
    ['no column: the whole line', { line: 1, message: 'x' }, { from: 0, to: 10 }],
    ['an empty line: a point', { line: 3, column: 1, message: 'x' }, { from: 23, to: 23 }],
    ['a column past the end: the end of the line', { line: 1, column: 99, message: 'x' }, { from: 10, to: 10 }],
    ['a line past the end: the last line', { line: 9, column: 1, message: 'x' }, { from: 24, to: 32 }],
  ])('%s', (_, d, want) => {
    expect(marksOf(doc, [d])).toEqual([{ ...want, severity: 'error', message: 'x' }]);
  });

  it('leaves a problem of the whole text to the list', () => {
    expect(marksOf(doc, [{ message: 'Missing required key `version`.' }])).toEqual([]);
  });
});

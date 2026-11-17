import { describe, expect, it } from 'vitest';
import { diffLines } from './diff';

describe('diff lines', () => {
  it('keeps file headers as headers only before the first hunk', () => {
    const lines = diffLines('--- a/x\n+++ b/x\n@@ -1 +1 @@\n--- old\n+++ new\n');
    expect(lines.map((line) => line.kind)).toEqual(['header', 'header', 'hunk', 'del', 'add']);
  });

  it.each([
    ['+added\n', 'add'],
    ['-removed\n', 'del'],
    [' context\n', 'ctx'],
    ['no newline at end', 'ctx'],
  ])('classifies %j as %s', (text, kind) => {
    expect(diffLines(text)).toEqual([{ text, kind }]);
  });

  it('returns no lines for an empty diff', () => {
    expect(diffLines('')).toEqual([]);
  });
});

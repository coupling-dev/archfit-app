import { describe, expect, it } from 'vitest';
import { bigMap } from './fixtures';
import { mapLines } from './model';

describe('bigMap fixture', () => {
  it('has 24 modules and 106 distinct lines of every kind, the same every time', () => {
    const map = bigMap();
    expect(map.modules).toHaveLength(24);
    expect(map.lines).toHaveLength(106);
    expect(mapLines(map.lines)).toHaveLength(106);
    expect(new Set(map.lines.map((l) => l.kind))).toEqual(
      new Set(['allowed', 'requested', 'deny', 'exception', 'drift']),
    );
    expect(bigMap()).toEqual(map);
    const names = new Set(map.modules.map((m) => m.name));
    for (const line of map.lines) expect([names.has(line.from), names.has(line.to)]).toEqual([true, true]);
  });
});

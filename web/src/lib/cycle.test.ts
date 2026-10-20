import { describe, expect, it } from 'vitest';
import { layout, shorten, type Direction } from './cycle';

const dir = (from: string, to: string, origin = '', n = 1): Direction => ({
  from,
  to,
  origin,
  locations: Array.from({ length: n }, (_, i) => `f.go:${i + 1}`),
});

describe('cycle layout', () => {
  it('draws the two directions of a pair as two separate arrows', () => {
    const { boxes, arrows } = layout(['a', 'b'], [dir('a', 'b'), dir('b', 'a')]);
    expect(boxes.map((b) => b.module)).toEqual(['a', 'b']);
    expect(arrows).toHaveLength(2);
    expect(arrows[0].d).not.toEqual(arrows[1].d);
    expect(Math.abs(arrows[0].ly - arrows[1].ly)).toBeGreaterThan(20); // labels sit on opposite sides
  });

  it('styles each direction as the comment does', () => {
    const { arrows } = layout(
      ['a', 'b', 'c'],
      [
        dir('a', 'b', 'added', 1),
        dir('b', 'c', 'existing', 40),
        dir('c', 'a', '', 1),
        dir('a', 'c', '', 5),
        dir('c', 'b', '', 12),
      ],
    );
    expect(arrows.map((a) => [a.kind, a.width])).toEqual([
      ['added', 4],
      ['existing', 1.5],
      ['cycle', 2],
      ['cycle', 3],
      ['cycle', 4],
    ]);
    expect(arrows[1].label).toBe('40 locations');
    expect(arrows[2].label).toBe('1 location');
  });

  it('keeps every box inside the drawing', () => {
    const modules = ['internal/control/ui', 'internal/gate/feedback', 'internal/wire/report', 'cmd/reporter', 'x'];
    const { width, height, boxes } = layout(modules, []);
    for (const b of boxes) {
      expect(b.x - b.w / 2).toBeGreaterThanOrEqual(0);
      expect(b.x + b.w / 2).toBeLessThanOrEqual(width);
      expect(b.y - b.h / 2).toBeGreaterThanOrEqual(0);
      expect(b.y + b.h / 2).toBeLessThanOrEqual(height);
    }
  });

  // Every placed label clear of every module and of every other label. A label's area is measured here on its own:
  // 12px text, at most 7px a glyph, 14px tall.
  type Area = { x: number; y: number; w: number; h: number };
  const overlaps = (a: Area, b: Area) => Math.abs(a.x - b.x) * 2 < a.w + b.w && Math.abs(a.y - b.y) * 2 < a.h + b.h;
  const clear = (modules: string[], directions: Direction[]) => {
    const { boxes, arrows } = layout(modules, directions);
    const labels = arrows.filter((a) => a.label).map((a) => ({ x: a.lx, y: a.ly, w: a.label.length * 7, h: 14 }));
    labels.forEach((l, i) => {
      for (const b of boxes) expect(overlaps(l, b), `label ${i} covers ${b.module}`).toBe(false);
      labels.slice(i + 1).forEach((o, j) => expect(overlaps(l, o), `labels ${i} and ${i + j + 1}`).toBe(false));
    });
    return arrows;
  };

  it('labels both directions of a pair apart', () => {
    const arrows = clear(
      ['pkg_chaos', 'pkg_util'],
      [dir('pkg_chaos', 'pkg_util', '', 2), dir('pkg_util', 'pkg_chaos', '', 1)],
    );
    expect(arrows.map((a) => a.label)).toEqual(['2 locations', '1 location']);
  });

  it('labels a three-module cycle without overlap (pumba-pilot#11)', () => {
    const arrows = clear(
      ['pkg_chaos', 'pkg_container', 'pkg_util'],
      [
        dir('pkg_chaos', 'pkg_container', '', 40),
        dir('pkg_chaos', 'pkg_util', '', 2),
        dir('pkg_container', 'pkg_chaos', '', 1),
        dir('pkg_util', 'pkg_chaos', '', 1),
      ],
    );
    expect(arrows.map((a) => a.label)).toEqual(['40 locations', '2 locations', '1 location', '1 location']);
  });

  it('caps the size of the largest cycle the comment draws', () => {
    const modules = Array.from({ length: 50 }, (_, i) => `internal/services/module_${i}`);
    const ring = modules.flatMap((m, i) => [
      dir(m, modules[(i + 1) % 50], '', 3),
      dir(modules[(i + 1) % 50], m, '', 1),
    ]);
    const arrows = clear(modules, ring);
    const { width, height } = layout(modules, ring);
    expect(width).toBeLessThan(1300);
    expect(height).toBeLessThan(900);
    expect(arrows).toHaveLength(100);
  });

  it('skips a direction whose module is not drawn', () => {
    expect(layout(['a', 'b'], [dir('a', 'z')]).arrows).toEqual([]);
  });

  it('shortens long module names from the left', () => {
    const long = 'github.com/acme/platform/internal/services/billing';
    expect(shorten(long)).toHaveLength(28);
    expect(shorten(long).endsWith('services/billing')).toBe(true);
    expect(shorten('short')).toBe('short');
  });
});

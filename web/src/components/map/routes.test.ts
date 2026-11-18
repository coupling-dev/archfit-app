import { describe, expect, it } from 'vitest';
import type { MapNode } from './layout';
import type { MapLine } from './model';
import { focusLabel } from './model';
import { routeLines } from './routes';
const focusLabelLength = (l: MapLine) => focusLabel(l).length;

const node = (name: string, x: number, y: number): MapNode => ({ name, layer: '', volatility: 'low', x, y, width: 200, height: 64 });
const nodes = [node('api', 100, 100), node('db', 100, 400), node('ui', 400, 100), node('cache', 700, 100)];
const line = (from: string, to: string): MapLine => ({ kind: 'allowed', from, to });
const area = { width: 1000, height: 700, avoid: [] };

describe('routeLines', () => {
  it('draws only the lines of the module in focus', () => {
    const routes = routeLines(nodes, [line('api', 'db'), line('ui', 'cache'), line('ui', 'api')], 'api', area);
    expect(routes.map((r) => `${r.line.from}>${r.line.to}`)).toEqual(['api>db', 'ui>api']);
  });

  it('leaves the lower side of a module toward one below and enters the upper side of that one', () => {
    const [route] = routeLines(nodes, [line('api', 'db')], 'api', area);
    expect(route.path.startsWith('M 200 164 ')).toBe(true);
    expect(route.path.endsWith(' 200 400')).toBe(true);
  });

  it('arches over a row for two modules side by side', () => {
    const [route] = routeLines(nodes, [line('api', 'ui')], 'api', area);
    expect(route.path.startsWith('M 200 100 ')).toBe(true);
    expect(route.labelY).toBeLessThan(100);
  });

  it('spreads lines that share a side of a module and orders them by the other end', () => {
    const routes = routeLines(nodes, [line('ui', 'api'), line('cache', 'api')], 'api', area);
    const starts = routes.map((r) => Number(r.path.split(' ').at(-2)));
    const xs = routes.map((r) => Number(/ (\d+(?:\.\d+)?) \d+$/.exec(r.path)![1]));
    expect(new Set(xs).size).toBe(2);
    expect(starts).toHaveLength(2);
    expect(xs[0]).toBeLessThan(xs[1]);
  });

  it('puts a label on its line at 80 percent when nothing is in the way', () => {
    const [route] = routeLines(nodes, [line('api', 'db')], 'api', area);
    const [x0, y0, x1, y1, x2, y2, x3, y3] = route.path.match(/-?\d+(\.\d+)?/g)!.map(Number);
    const t = 0.8;
    const at = (a: number, b: number, c: number, d: number) => (1 - t) ** 3 * a + 3 * (1 - t) ** 2 * t * b + 3 * (1 - t) * t * t * c + t ** 3 * d;
    expect(route.labelX).toBeCloseTo(at(x0, x1, x2, x3), 6);
    expect(route.labelY).toBeCloseTo(at(y0, y1, y2, y3), 6);
  });

  it('has no NaN, even for a line from a module to itself or to a missing one', () => {
    const routes = routeLines(nodes, [line('api', 'api'), line('api', 'missing')], 'api', area);
    expect(routes).toHaveLength(1);
    expect(routes[0].path).not.toMatch(/NaN|undefined/);
  });
});

const hits = (a: { x0: number; x1: number; y0: number; y1: number }, b: typeof a) =>
  a.x0 < b.x1 && b.x0 < a.x1 && a.y0 < b.y1 && b.y0 < a.y1;

describe('route labels', () => {
  it('keep off text the chart always draws, however many there are', () => {
    const hub = node('hub', 100, 300);
    const others = Array.from({ length: 20 }, (_, i) => node('m' + i, 20 + (i % 5) * 190, 500 + Math.floor(i / 5) * 5));
    const avoid = [{ x: 60, y: 330, width: 80, height: 18 }];
    const routes = routeLines([hub, ...others], others.map((n) => line('hub', n.name)), 'hub', { ...area, avoid });
    const boxes = routes.map((r) => {
      const half = (`hub → ${r.line.to} · allowed`.length * 7.6) / 2;
      return { x0: r.labelX - half, x1: r.labelX + half, y0: r.labelY - 18, y1: r.labelY };
    });
    expect(boxes).toHaveLength(20);
    for (const [i, a] of boxes.entries()) {
      expect(hits(a, { x0: 60, x1: 140, y0: 330, y1: 348 })).toBe(false);
      for (const b of boxes.slice(i + 1)) expect(hits(a, b)).toBe(false);
    }
  });

  it('are still placed, and reserved, when the chart has no free room', () => {
    const hub = node('hub', 0, 40);
    const others = Array.from({ length: 6 }, (_, i) => node('m' + i, 150 * i, 40));
    const tiny = { width: 400, height: 60, avoid: [] };
    const routes = routeLines([hub, ...others], others.map((n) => line('hub', n.name)), 'hub', tiny);
    expect(routes).toHaveLength(6);
    for (const r of routes) expect(`${r.labelX} ${r.labelY}`).not.toMatch(/NaN|undefined/);
    expect(new Set(routes.map((r) => `${r.labelX},${r.labelY}`)).size).toBeGreaterThan(1);
  });

  it('stay inside the chart, arches included', () => {
    const edge = [node('left', 0, 46), node('right', 800, 46)];
    const routes = routeLines(edge, [line('left', 'right'), line('left', 'left'), line('right', 'right')], 'left', area);
    for (const r of routes) {
      const half = (focusLabelLength(r.line) * 7.6) / 2;
      expect(r.labelX - half).toBeGreaterThanOrEqual(0);
      expect(r.labelX + half).toBeLessThanOrEqual(area.width);
      expect(r.labelY - 18).toBeGreaterThanOrEqual(0);
    }
    const arch = routes.find((r) => r.line.to === 'right')!;
    const peak = Number(arch.path.split(',')[0].split(' ').at(-1));
    expect(peak).toBeGreaterThanOrEqual(46 - 60);
  });

  it('do not cover one another', () => {
    const many = [node('hub', 100, 100), ...Array.from({ length: 6 }, (_, i) => node('m' + i, 100 + i * 12, 400))];
    const routes = routeLines(many, many.slice(1).map((n) => line('hub', n.name)), 'hub', area);
    const boxes = routes.map((r) => {
      const half = (`hub → ${r.line.to} · allowed`.length * 7.6) / 2;
      return { x0: r.labelX - half, x1: r.labelX + half, y0: r.labelY - 18, y1: r.labelY };
    });
    for (const [i, a] of boxes.entries())
      for (const b of boxes.slice(i + 1)) expect(a.x0 < b.x1 && b.x0 < a.x1 && a.y0 < b.y1 && b.y0 < a.y1).toBe(false);
  });
});

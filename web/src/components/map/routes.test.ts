import { describe, expect, it } from 'vitest';
import type { MapNode } from './layout';
import type { MapLine } from './model';
import { routeLines } from './routes';

const node = (name: string, x: number, y: number): MapNode => ({ name, layer: '', volatility: 'low', x, y, width: 200, height: 64 });
const nodes = [node('api', 100, 100), node('db', 100, 400), node('ui', 400, 100), node('cache', 700, 100)];
const line = (from: string, to: string): MapLine => ({ kind: 'allowed', from, to });

describe('routeLines', () => {
  it('draws only the lines of the module in focus', () => {
    const routes = routeLines(nodes, [line('api', 'db'), line('ui', 'cache'), line('ui', 'api')], 'api');
    expect(routes.map((r) => `${r.line.from}>${r.line.to}`)).toEqual(['api>db', 'ui>api']);
  });

  it('leaves the lower side of a module toward one below and enters the upper side of that one', () => {
    const [route] = routeLines(nodes, [line('api', 'db')], 'api');
    expect(route.path.startsWith('M 200 164 ')).toBe(true);
    expect(route.path.endsWith(' 200 400')).toBe(true);
  });

  it('arches over a row for two modules side by side', () => {
    const [route] = routeLines(nodes, [line('api', 'ui')], 'api');
    expect(route.path.startsWith('M 200 100 ')).toBe(true);
    expect(route.labelY).toBeLessThan(100);
  });

  it('spreads lines that share a side of a module and orders them by the other end', () => {
    const routes = routeLines(nodes, [line('ui', 'api'), line('cache', 'api')], 'api');
    const starts = routes.map((r) => Number(r.path.split(' ').at(-2)));
    const xs = routes.map((r) => Number(/ (\d+(?:\.\d+)?) \d+$/.exec(r.path)![1]));
    expect(new Set(xs).size).toBe(2);
    expect(starts).toHaveLength(2);
    expect(xs[0]).toBeLessThan(xs[1]);
  });

  it('has no NaN, even for a line from a module to itself or to a missing one', () => {
    const routes = routeLines(nodes, [line('api', 'api'), line('api', 'missing')], 'api');
    expect(routes).toHaveLength(1);
    expect(routes[0].path).not.toMatch(/NaN|undefined/);
  });
});

describe('route labels', () => {
  it('do not cover one another', () => {
    const many = [node('hub', 100, 100), ...Array.from({ length: 6 }, (_, i) => node('m' + i, 100 + i * 12, 400))];
    const routes = routeLines(many, many.slice(1).map((n) => line('hub', n.name)), 'hub');
    const boxes = routes.map((r) => {
      const half = (`hub → ${r.line.to} · allowed`.length * 7.6) / 2;
      return { x0: r.labelX - half, x1: r.labelX + half, y0: r.labelY - 18, y1: r.labelY };
    });
    for (const [i, a] of boxes.entries())
      for (const b of boxes.slice(i + 1)) expect(a.x0 < b.x1 && b.x0 < a.x1 && a.y0 < b.y1 && b.y0 < a.y1).toBe(false);
  });
});

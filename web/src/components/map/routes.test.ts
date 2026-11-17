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

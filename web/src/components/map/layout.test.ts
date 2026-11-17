import ELK from 'elkjs/lib/elk.bundled.js';
import { describe, expect, it } from 'vitest';
import { bigMap } from './fixtures';
import { placeBands, type MapLayout } from './layout';
import type { MapModel } from './model';

const elk = new ELK();
const place = (model: MapModel) => placeBands(model, (g) => elk.layout(g));
const overlap = (a: MapLayout['nodes'][number], b: MapLayout['nodes'][number]) =>
  a.x < b.x + b.width && b.x < a.x + a.width && a.y < b.y + b.height && b.y < a.y + a.height;

describe('layer bands', () => {
  it('keeps disconnected modules in outer-first bands and joins layers with one line each', async () => {
    const model: MapModel = {
      layers: ['outer', 'inner', ''],
      modules: [
        { name: 'api', layer: 'outer', volatility: 'high' },
        { name: 'ui', layer: 'outer', volatility: 'low' },
        { name: 'db', layer: 'inner', volatility: 'low' },
        { name: 'unplaced', layer: '', volatility: 'high' },
      ],
      lines: [
        { kind: 'requested', from: 'api', to: 'db', request: 431 },
        { kind: 'allowed', from: 'api', to: 'db' },
        { kind: 'allowed', from: 'ui', to: 'db' },
        { kind: 'exception', from: 'api', to: 'ui', expires: '2026-11-19' },
        { kind: 'deny', from: 'db', to: 'db', rule: 'no-self' },
      ],
    };
    const result = await place(model);
    expect(result.bands.map((b) => b.name)).toEqual(['outer', 'inner', '']);
    expect(result.nodes.map((n) => n.name)).toEqual(['api', 'ui', 'db', 'unplaced']);
    for (const node of result.nodes) {
      const band = result.bands.find((b) => b.name === node.layer)!;
      expect(node.y).toBeGreaterThan(band.y);
      expect(node.y + node.height).toBeLessThan(band.y + band.height);
      expect(node.x + node.width).toBeLessThan(result.width);
    }
    for (let i = 1; i < result.bands.length; i++)
      expect(result.bands[i].y).toBeGreaterThan(result.bands[i - 1].y + result.bands[i - 1].height);
    // Same-layer lines are not links; two lines outer -> inner are one link with the strongest kind.
    expect(result.links.map((l) => [l.from, l.to, l.kind, l.count])).toEqual([['outer', 'inner', 'requested', 2]]);
    for (const link of result.links) expect(link.path).not.toMatch(/NaN|undefined/);
  });

  it('does not grow with the number of dependencies', async () => {
    const full = bigMap();
    const none = await place({ ...full, lines: [] });
    const all = await place(full);
    expect(all.height).toBe(none.height);
    expect(all.width - none.width).toBeLessThanOrEqual(3 * 30);
    // 24 modules in 3 layers fit about one page: names stay readable when the chart fits a 1280 px page.
    expect(all.width).toBeLessThan(1500);
    expect(all.height).toBeLessThan(1000);
    expect(all.links.length).toBeLessThanOrEqual(6);
    expect(all.links.reduce((n, l) => n + l.count, 0)).toBeLessThanOrEqual(full.lines.length);
  });

  it('places the 24 modules without overlap, each inside its band', async () => {
    const result = await place(bigMap());
    expect(result.nodes).toHaveLength(24);
    for (const [i, a] of result.nodes.entries())
      for (const b of result.nodes.slice(i + 1)) expect(overlap(a, b)).toBe(false);
    for (const node of result.nodes) {
      const band = result.bands.find((b) => b.name === node.layer)!;
      expect(node.y).toBeGreaterThanOrEqual(band.y);
      expect(node.y + node.height).toBeLessThanOrEqual(band.y + band.height);
    }
  });

  it('wraps a band of many modules into rows instead of one very wide row', async () => {
    const modules = Array.from({ length: 24 }, (_, i) => ({ name: `module-${i}`, layer: '', volatility: 'high' }));
    const result = await place({ layers: [''], modules, lines: [] });
    expect(new Set(result.nodes.map((n) => n.y)).size).toBeGreaterThan(1);
    expect(result.width).toBeLessThan(1500);
  });

  it('works with a two-module request model and no dependencies', async () => {
    const modules = ['from', 'to'].map((name) => ({ name, layer: '', volatility: 'high' }));
    const result = await place({ layers: [], modules, lines: [] });
    expect(result.nodes).toHaveLength(2);
    expect(result.bands.map((b) => b.name)).toEqual(['']);
    expect(result.links).toEqual([]);
  });

  it('refuses dependencies whose modules cannot be placed', async () => {
    await expect(
      place({
        layers: [''],
        modules: [{ name: 'a', layer: '', volatility: 'high' }],
        lines: [{ kind: 'allowed', from: 'a', to: 'missing' }],
      }),
    ).rejects.toThrow('A dependency has no module.');
  });
});

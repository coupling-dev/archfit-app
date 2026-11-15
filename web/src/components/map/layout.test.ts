import ELK from 'elkjs/lib/elk.bundled.js';
import { describe, expect, it } from 'vitest';
import { placeBands } from './layout';
import type { MapModel } from './model';

const elk = new ELK();
describe('layer bands', () => {
  it('keeps disconnected modules in outer-first bands and routes every winning dependency', async () => {
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
        { kind: 'exception', from: 'api', to: 'ui', expires: '2026-11-19' },
        { kind: 'deny', from: 'db', to: 'db', rule: 'no-self' },
      ],
    };
    const result = await placeBands(model, (g) => elk.layout(g));
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
    expect(result.edges.map((e) => e.line.kind)).toEqual(['requested', 'exception', 'deny']);
    for (const edge of result.edges) {
      expect(edge.path).not.toMatch(/NaN|undefined/);
      expect(edge.labelX).toBeLessThan(result.width);
    }
  });

  it('enters the target without crossing a neighboring module', async () => {
    const modules = [
      { name: 'src', layer: 'outer', volatility: 'high' },
      { name: 'target', layer: 'inner', volatility: 'low' },
      { name: 'neighbor', layer: 'inner', volatility: 'low' },
    ];
    const result = await placeBands({ layers: ['outer', 'inner'], modules,
      lines: [{ kind: 'requested', from: 'src', to: 'target', request: 431 }] }, (g) => elk.layout(g));
    const target = result.nodes.find((n) => n.name === 'target')!;
    const neighbor = result.nodes.find((n) => n.name === 'neighbor')!;
    expect(neighbor.x).toBeGreaterThan(target.x);
    expect(result.edges[0].path).toMatch(new RegExp(
      `H ${target.x + target.width / 2} V ${target.y + target.height}$`,
    ));
  });

  it('keeps each band on one row so vertical ports do not cross other modules', async () => {
    const modules = Array.from({ length: 24 }, (_, i) => ({ name: `module-${i}`, layer: '', volatility: 'high' }));
    const result = await placeBands({ layers: [''], modules, lines: [] }, (g) => elk.layout(g));
    expect(new Set(result.nodes.map((n) => n.y)).size).toBe(1);
  });

  it('works with a two-module request model and no dependencies', async () => {
    const modules = ['from', 'to'].map((name) => ({ name, layer: '', volatility: 'high' }));
    const result = await placeBands({ layers: [], modules, lines: [] }, (g) => elk.layout(g));
    expect(result.nodes).toHaveLength(2);
    expect(result.bands.map((b) => b.name)).toEqual(['']);
    expect(result.edges).toEqual([]);
  });

  it('refuses dependencies whose modules cannot be placed', async () => {
    await expect(
      placeBands(
        {
          layers: [''],
          modules: [{ name: 'a', layer: '', volatility: 'high' }],
          lines: [{ kind: 'allowed', from: 'a', to: 'missing' }],
        },
        (g) => elk.layout(g),
      ),
    ).rejects.toThrow('A dependency has no module.');
  });
});

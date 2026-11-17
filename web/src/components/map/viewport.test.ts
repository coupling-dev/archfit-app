import { describe, expect, it } from 'vitest';
import { centerOn, fitView, FLOOR, MAX_ZOOM, MIN_ZOOM, panBy, viewBox, visible, zoomAt } from './viewport';

const vp = { w: 1200, h: 600 };

describe('fitView', () => {
  it('fits to width when names stay at 11 px or more', () => {
    const view = fitView({ w: 1500, h: 4000 }, vp);
    expect(view).toEqual({ k: 1200 / 1500, x: 0, y: 0 });
    expect(view.k).toBeGreaterThanOrEqual(FLOOR);
  });

  it('starts at the floor, top-left, when fitting would shrink names below 11 px', () => {
    const view = fitView({ w: 5884, h: 4960 }, vp);
    expect(view).toEqual({ k: FLOOR, x: 0, y: 0 });
    expect(14 * view.k).toBeCloseTo(11);
  });

  it('never magnifies a small chart past 100% and centers it', () => {
    const view = fitView({ w: 360, h: 176 }, vp);
    expect(view.k).toBe(1);
    expect(view.x).toBe(-(1200 - 360) / 2);
  });
});

describe('zoomAt', () => {
  it('keeps the world point under the pointer fixed', () => {
    const world = { w: 5000, h: 5000 };
    const start = { k: 1, x: 300, y: 200 };
    const next = zoomAt(start, world, vp, 2, 400, 100);
    expect(next.k).toBe(2);
    const before = { x: start.x + 400 / start.k, y: start.y + 100 / start.k };
    const after = { x: next.x + 400 / next.k, y: next.y + 100 / next.k };
    expect(after).toEqual(before);
  });

  it('stays inside the zoom range', () => {
    const world = { w: 5000, h: 5000 };
    expect(zoomAt({ k: 1, x: 0, y: 0 }, world, vp, 99, 0, 0).k).toBe(MAX_ZOOM);
    expect(zoomAt({ k: 1, x: 0, y: 0 }, world, vp, 0.001, 0, 0).k).toBe(MIN_ZOOM);
  });
});

describe('panBy', () => {
  it('moves the chart with the pointer', () => {
    const view = panBy({ k: 2, x: 100, y: 100 }, { w: 5000, h: 5000 }, vp, 40, -20);
    expect(view).toEqual({ k: 2, x: 80, y: 110 });
  });

  it('cannot push the whole chart off screen', () => {
    const world = { w: 1000, h: 800 };
    const far = panBy({ k: 1, x: 0, y: 0 }, world, vp, -99999, -99999);
    expect(far.x).toBeLessThan(world.w);
    expect(far.y).toBeLessThan(world.h);
    const back = panBy({ k: 1, x: 0, y: 0 }, world, vp, 99999, 99999);
    expect(back.x + vp.w).toBeGreaterThan(0);
    expect(back.y + vp.h).toBeGreaterThan(0);
  });
});

describe('centerOn', () => {
  it('puts the box in the middle and keeps at least the floor', () => {
    const world = { w: 5000, h: 5000 };
    const box = { x: 2000, y: 1000, width: 200, height: 64 };
    const view = centerOn({ k: 0.2, x: 0, y: 0 }, world, vp, box);
    expect(view.k).toBe(FLOOR);
    expect(view.x + vp.w / view.k / 2).toBeCloseTo(2100);
    expect(view.y + vp.h / view.k / 2).toBeCloseTo(1032);
    expect(visible(view, vp, box)).toBe(true);
  });
});

describe('viewBox', () => {
  it('shows vp / k world units from the corner', () => {
    expect(viewBox({ k: 2, x: 10, y: 20 }, vp)).toBe('10 20 600 300');
  });
});

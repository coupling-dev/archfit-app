import { readFileSync } from 'node:fs';
import { expect, test, type Locator, type Page } from '@playwright/test';
import { bigMap } from '../src/components/map/fixtures';
import { docsClock, docsShot, expectClean, open } from './support';

const path = '/r/acme/app';
const api = 'GET /api/repos/acme/app/home';
const group = 'Modules by layer and their dependencies';
const home = {
  ...JSON.parse(readFileSync(new URL('../../internal/control/ui/testdata/home/clean.json', import.meta.url), 'utf8')),
  map: bigMap(),
};
const map = bigMap();

async function openDiagram(page: Page, width = 1280) {
  await page.setViewportSize({ width, height: 900 });
  await open(page, path, { [api]: () => [200, home] });
  await page.getByRole('button', { name: 'Diagram', exact: true }).click();
  await expect(page.getByRole('group', { name: group })).toBeVisible();
}
const viewport = (page: Page) => page.locator('.map-viewport');
// The view is the SVG viewBox; scale is screen pixels per world unit.
async function view(page: Page) {
  return page.locator('.map-viewport svg').evaluate((svg: SVGSVGElement) => {
    const box = svg.viewBox.baseVal;
    return { x: box.x, y: box.y, k: svg.getBoundingClientRect().width / box.width };
  });
}
const linesOf = (name: string) => map.lines.filter((l) => l.from === name || l.to === name);
async function center(el: Locator) {
  const box = (await el.boundingBox())!;
  return { x: box.x + box.width / 2, y: box.y + box.height / 2 };
}

test('diagram: opens readable, bounded, with the legend and caption above it', async ({ page }) => {
  await openDiagram(page);
  const frame = (await viewport(page).boundingBox())!;
  expect(frame.height).toBeLessThanOrEqual(720);
  const { k } = await view(page);
  expect(14 * k).toBeGreaterThanOrEqual(11);
  const legend = page.getByRole('list', { name: 'Map legend' });
  await expect(legend.getByRole('listitem')).toHaveText([
    'An allowed dependency',
    'A requested dependency',
    'One a rule denies',
    'An exception (with its date)',
    'Drift',
  ]);
  await expect(
    page.getByText('An arrow points at the module it depends on. Outer layer on top. Allowed dependencies, not observed imports.'),
  ).toBeVisible();
  expect((await legend.boundingBox())!.y).toBeLessThan(frame.y);
  expect(await page.evaluate(() => document.documentElement.scrollHeight)).toBeLessThan(4000);
  await expect(page.locator('.map-node')).toHaveCount(24);
  // 106 lines are drawn as one line per pair of layers until a module is in focus.
  await expect(page.locator('.map-edge')).toHaveCount(0);
  await expect(page.locator('.map-link')).toHaveCount(3);
  await expect(page.locator('.map-link path').first()).toHaveCSS('vector-effect', 'non-scaling-stroke');
  const svg = await page.locator('.map-viewport svg').boundingBox();
  const natural = await page.locator('.map-viewport svg').evaluate((el: SVGSVGElement) => el.viewBox.baseVal.width);
  expect(natural).toBeLessThan(frame.width / (11 / 14) + 1);
  expect(svg!.width).toBeLessThanOrEqual(frame.width);
  await docsClock(page);
  await docsShot(page, 'diagram-1280', ['Review workflow change']);
});

test('diagram: wheel scrolls the page, Ctrl or Cmd with wheel zooms at the pointer, double-click zooms in', async ({ page }) => {
  await openDiagram(page);
  const frame = (await viewport(page).boundingBox())!;
  const at = { x: frame.x + frame.width / 2, y: frame.y + frame.height / 2 };
  await page.mouse.move(at.x, at.y);
  const before = await view(page);
  const scrollY = await page.evaluate(() => window.scrollY);
  await page.mouse.wheel(0, 300);
  await expect.poll(() => page.evaluate(() => window.scrollY)).toBeGreaterThan(scrollY);
  expect(await view(page)).toEqual(before);
  await page.evaluate(() => window.scrollTo(0, 0));

  const pointer = { x: frame.x + 200, y: frame.y + 120 };
  const worldUnderPointer = (v: { x: number; y: number; k: number }) => ({
    x: v.x + (pointer.x - frame.x) / v.k,
    y: v.y + (pointer.y - frame.y) / v.k,
  });
  const was = await view(page);
  await page.mouse.move(pointer.x, pointer.y);
  await page.keyboard.down('Control');
  await page.mouse.wheel(0, -40);
  await page.keyboard.up('Control');
  await expect.poll(async () => (await view(page)).k).toBeGreaterThan(was.k);
  const now = await view(page);
  expect(worldUnderPointer(now).x).toBeCloseTo(worldUnderPointer(was).x, 0);
  expect(worldUnderPointer(now).y).toBeCloseTo(worldUnderPointer(was).y, 0);
  expect(await page.evaluate(() => window.scrollY)).toBe(0);

  await viewport(page).dblclick({ position: { x: 20, y: 20 } });
  await expect.poll(async () => (await view(page)).k).toBeGreaterThan(now.k * 1.5);
});

test('diagram: dragging pans, and a drag that starts on a module does not select it', async ({ page }) => {
  await openDiagram(page);
  const before = await view(page);
  const node = await center(page.locator('[data-module="handlers"]'));
  await page.mouse.move(node.x, node.y);
  await page.mouse.down();
  await page.mouse.move(node.x - 120, node.y + 40, { steps: 6 });
  await page.mouse.up();
  const after = await view(page);
  expect(after.k).toBe(before.k);
  expect(after.x).toBeGreaterThan(before.x);
  expect(after.y).toBeLessThanOrEqual(before.y);
  await expect(page.locator('.map-node.selected')).toHaveCount(0);
});

test('diagram: toolbar buttons zoom, fit and show 100%', async ({ page }) => {
  await openDiagram(page);
  const fit = await view(page);
  await page.getByRole('button', { name: 'Zoom in' }).click();
  const zoomed = await view(page);
  expect(zoomed.k).toBeGreaterThan(fit.k);
  await page.getByRole('button', { name: 'Zoom out' }).click();
  expect((await view(page)).k).toBeCloseTo(fit.k, 5);
  await page.getByRole('button', { name: '100%' }).click();
  expect((await view(page)).k).toBeCloseTo(1, 5);
  await page.getByRole('button', { name: 'Fit' }).click();
  expect(await view(page)).toEqual(fit);
  await expect(page.getByRole('button', { name: 'Full screen' })).toBeVisible();
});

test('diagram: the focused viewport answers to arrows, plus, minus and 0', async ({ page }) => {
  await openDiagram(page);
  const fit = await view(page);
  await viewport(page).focus();
  await page.keyboard.press('ArrowRight');
  const right = await view(page);
  expect(right.x).toBeGreaterThan(fit.x);
  await page.keyboard.press('ArrowDown');
  expect((await view(page)).y).toBeGreaterThan(fit.y);
  await page.keyboard.press('+');
  const zoomed = await view(page);
  expect(zoomed.k).toBeGreaterThan(fit.k);
  await page.keyboard.press('-');
  expect((await view(page)).k).toBeCloseTo(fit.k, 5);
  await page.keyboard.press('ArrowRight');
  await page.keyboard.press('0');
  expect(await view(page)).toEqual(fit);
});

test('diagram: a module in focus keeps its lines, dims the rest, and names both ends', async ({ page }) => {
  await openDiagram(page);
  const mine = linesOf('handlers');
  expect(mine.length).toBeGreaterThan(3);
  await page.locator('[data-module="handlers"]').click();
  await expect(page.locator('.map-node.selected')).toHaveCount(1);
  await expect(page.locator('.map-edge text')).toHaveCount(mine.length);
  await expect(page.locator('.map-edge')).toHaveCount(mine.length);
  await expect(page.locator('.map-link.dim')).toHaveCount(3);
  const neighbours = new Set(mine.flatMap((l) => [l.from, l.to]));
  await expect(page.locator('.map-node:not(.dim)')).toHaveCount(neighbours.size);
  const first = mine[0];
  const labels = await page.locator('.map-edge text').allTextContents();
  expect(labels.some((t) => t.startsWith(`${first.from} → ${first.to} · `))).toBe(true);
  // Clicking again clears; so does Escape.
  await page.locator('[data-module="handlers"]').click();
  await expect(page.locator('.map-edge')).toHaveCount(0);
  await page.locator('[data-module="handlers"]').click();
  await expect(page.locator('.map-edge')).toHaveCount(mine.length);
  await viewport(page).focus();
  await page.keyboard.press('Escape');
  await expect(page.locator('.map-edge')).toHaveCount(0);
  await expect(page.locator('.map-node.dim')).toHaveCount(0);
});

test('diagram: Tab to a module focuses it, Enter clears it', async ({ page }) => {
  await openDiagram(page);
  await viewport(page).focus();
  await page.keyboard.press('Tab');
  await expect(page.locator('.map-node.selected')).toHaveCount(1);
  const name = await page.locator('.map-node.selected').getAttribute('data-module');
  await expect(page.locator('.map-node.selected')).toBeFocused();
  await page.keyboard.press('Enter');
  await expect(page.locator('.map-node.selected')).toHaveCount(0);
  await page.keyboard.press('Enter');
  await expect(page.locator(`.map-node.selected[data-module="${name}"]`)).toHaveCount(1);
});

test('diagram: finding a module pans to it and focuses it', async ({ page }) => {
  await openDiagram(page);
  const target = 'gemini';
  await expect(page.locator('datalist option')).toHaveCount(24);
  await page.getByLabel('Find a module').fill(target);
  await expect(page.locator(`.map-node.selected[data-module="${target}"]`)).toHaveCount(1);
  const frame = (await viewport(page).boundingBox())!;
  const box = (await page.locator(`[data-module="${target}"] rect`).boundingBox())!;
  expect(box.x).toBeGreaterThanOrEqual(frame.x);
  expect(box.y).toBeGreaterThanOrEqual(frame.y);
  expect(box.x + box.width).toBeLessThanOrEqual(frame.x + frame.width);
  expect(box.y + box.height).toBeLessThanOrEqual(frame.y + frame.height);
  expect((await view(page)).k * 14).toBeGreaterThanOrEqual(11);
  await page.getByLabel('Find a module').fill('no-such-module');
  await expect(page.locator(`.map-node.selected[data-module="${target}"]`)).toHaveCount(1);
});

test('diagram: full screen toggles when the browser has it', async ({ page }) => {
  await openDiagram(page);
  const button = page.getByRole('button', { name: 'Full screen' });
  await button.click();
  await expect(button).toHaveAttribute('aria-pressed', 'true');
  expect(await page.evaluate(() => document.fullscreenElement?.className)).toContain('map-root');
  await button.click();
  await expect(button).toHaveAttribute('aria-pressed', 'false');
});

for (const width of [390, 1280]) {
  test(`diagram: clean in light and dark, no sideways scroll, touch targets at ${width}`, async ({ page }) => {
    await openDiagram(page, width);
    await page.locator('[data-module="handlers"]').click();
    expect(await page.evaluate(() => document.documentElement.scrollWidth)).toBeLessThanOrEqual(width);
    expect((await viewport(page).boundingBox())!.width).toBeLessThanOrEqual(width - 32);
    if (width < 720)
      for (const name of ['Zoom in', 'Zoom out', 'Fit', '100%']) {
        const box = (await page.getByRole('button', { name, exact: true }).boundingBox())!;
        expect(box.height).toBeGreaterThanOrEqual(44);
        expect(box.width).toBeGreaterThanOrEqual(44);
      }
    await page.getByRole('button', { name: 'Dark', exact: true }).click();
    await expect(viewport(page)).toBeVisible();
    await page.screenshot({ path: `../tmp/diagram/focus-dark-${width}.png` });
    await page.getByRole('button', { name: 'Auto', exact: true }).click();
    await expectClean(page, ['Review workflow change']);
  });
}

test('diagram: a module without lines stays bright when it is the one in focus', async ({ page }) => {
  await page.setViewportSize({ width: 1280, height: 900 });
  const alone = {
    ...home,
    map: { layers: [''], modules: [...map.modules.slice(0, 2), { name: 'alone', layer: '', volatility: 'low' }], lines: [] },
  };
  await open(page, path, { [api]: () => [200, alone] });
  await page.getByRole('button', { name: 'Diagram', exact: true }).click();
  await page.locator('[data-module="alone"]').click();
  await expect(page.locator('.map-node.selected')).toHaveCount(1);
  await expect(page.locator('.map-node:not(.dim)')).toHaveCount(1);
});

test('diagram: a right click on a module does not stop Tab from focusing modules', async ({ page }) => {
  await openDiagram(page);
  await page.locator('[data-module="handlers"]').click({ button: 'right' });
  await page.locator('[data-module="api"]').focus();
  await expect(page.locator('.map-node.selected[data-module="api"]')).toHaveCount(1);
});

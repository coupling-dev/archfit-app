import { expect, test } from '@playwright/test';
import { expectClean, open } from './support';
import type { MapModel } from '../src/components/map/model';

function fixture(count: number): { status: string; needs_you: []; map: MapModel } {
  const names = ['api', 'web', 'billing', 'orders', 'storage', 'auth', 'cache', 'events', 'jobs', 'search'];
  const modules = Array.from({ length: count }, (_, i) => ({
    name: names[i] ?? `module-${String(i).padStart(2, '0')}`,
    layer: i < 2 ? 'Entry' : i < 4 ? 'Domain' : 'Support',
    volatility: 'low',
  }));
  return {
    status: 'clean',
    needs_you: [],
    map: {
      layers: ['Entry', 'Domain', 'Support'],
      modules,
      lines: [
        { from: 'api', to: 'billing', kind: 'allowed' },
        { from: 'api', to: 'storage', kind: 'deny', rule: 'no-direct-storage' },
        { from: 'api', to: 'storage', kind: 'deny', rule: 'entry-boundary' },
        { from: 'api', to: 'storage', kind: 'requested', request: 42 },
        { from: 'api', to: 'storage', kind: 'requested', request: 43 },
        {
          from: 'api',
          to: 'storage',
          kind: 'exception',
          rule: 'no-direct-storage',
          expires: '2027-01-01',
          days_left: 0,
        },
        { from: 'api', to: 'storage', kind: 'drift', rule: 'graph' },
        { from: 'billing', to: 'storage', kind: 'allowed' },
        { from: 'orders', to: 'storage', kind: 'exception', expires: '2027-02-01', days_left: 31 },
      ],
    },
  };
}
const api = 'GET /api/repos/acme/app/home';
const tableName = 'Policy and review status matrix';

for (const count of [5, 10])
  for (const width of [390, 1280])
    for (const theme of ['light', 'dark'] as const) {
      test(`DSM layout: ${count} modules, ${width}px, ${theme}`, async ({ page }) => {
        await page.setViewportSize({ width, height: 844 });
        await page.emulateMedia({ colorScheme: theme });
        const errors: string[] = [];
        page.on('pageerror', (error) => errors.push(error.message));
        await open(page, '/r/acme/app', { [api]: () => [200, fixture(count)] });
        await expect(page.getByRole('button', { name: 'Matrix', exact: true })).toHaveAttribute('aria-pressed', 'true');
        await expect(page.getByRole('table', { name: tableName })).toBeVisible();
        await expect(page.locator('tbody tr')).toHaveCount(count);
        await expect(page.getByRole('group', { name: 'Modules by layer and their dependencies' })).toHaveCount(0);
        expect(await page.evaluate(() => document.documentElement.scrollWidth)).toBeLessThanOrEqual(width);
        await expectClean(page, ['Policy and review status']);
        expect(errors).toEqual([]);
      });
    }

test('DSM preserves all statuses, keyboard navigation and modal focus return', async ({ page }) => {
  await open(page, '/r/acme/app', { [api]: () => [200, fixture(5)] });
  const cell = page.getByRole('button', { name: /^api to storage:/ });
  await expect(cell).toContainText('××??◷!');
  await expect(cell).toHaveAttribute('aria-label', /entry-boundary.*request #42.*request #43.*0 days left.*Drift/);
  await page.getByRole('button', { name: /^api to web:/ }).focus();
  await page.keyboard.press('End');
  await expect(cell).toBeFocused();
  await page.keyboard.press('Enter');
  const detail = page.getByRole('dialog', { name: 'api → storage' });
  await expect(detail).toBeVisible();
  await expect(detail.getByRole('button', { name: 'Close details' })).toBeFocused();
  await expect(detail.getByRole('listitem')).toHaveCount(6);
  for (const request of [42, 43]) {
    await expect(detail.getByRole('link', { name: `Open request #${request}` })).toHaveAttribute(
      'href',
      `/r/acme/app/requests/${request}`,
    );
  }
  await expect(detail).toContainText('expires 2027-01-01 · 0 days left');
  await expect(detail).toContainText('not observed imports');
  await expectClean(page, ['Policy and review status', 'scan coverage']);
  await page.keyboard.press('Escape');
  await expect(detail).toBeHidden();
  await expect(cell).toBeFocused();
  await page.keyboard.press('Home');
  const blank = page.getByRole('button', { name: 'api to web: no rule' });
  await expect(blank).toBeFocused();
  await page.keyboard.press('Enter');
  await expect(page.getByRole('dialog')).toContainText('No explicit relationship in this payload.');
  await page.getByRole('button', { name: 'Close details' }).click();
  await expect(blank).toBeFocused();
});

test('DSM diagonal cells are shaded and skipped; blank cells have a short name', async ({ page }) => {
  await open(page, '/r/acme/app', { [api]: () => [200, fixture(5)] });
  await expect(page.getByRole('table', { name: tableName })).toBeVisible();
  await expect(page.locator('td.self')).toHaveCount(5);
  await expect(page.locator('td > button')).toHaveCount(20);
  await expect(page.getByRole('button', { name: /^api to api:/ })).toHaveCount(0);
  await expect(page.getByRole('button', { name: 'web to api: no rule' })).toHaveCount(1);
  await expect(page.getByRole('button', { name: /deny status not fully evaluated/ })).toHaveCount(0);
  // One tab stop; arrows step over the diagonal.
  await page.keyboard.press('Tab');
  await page.getByRole('button', { name: 'api to web: no rule' }).focus();
  await page.keyboard.press('ArrowDown');
  await expect(page.getByRole('button', { name: 'billing to web: no rule' })).toBeFocused();
  await page.keyboard.press('ArrowUp');
  await page.keyboard.press('ArrowUp');
  await expect(page.getByRole('button', { name: 'api to web: no rule' })).toBeFocused();
  await page.keyboard.press('ArrowLeft');
  await expect(page.getByRole('button', { name: 'api to web: no rule' })).toBeFocused();
  await expect(page.locator('td > button[tabindex="0"]')).toHaveCount(1);
  await expect(page.locator('td.highlight')).not.toHaveCount(0);
});

test('DSM moving the active cell touches only the old and new row and column', async ({ page }) => {
  const count = 12;
  await open(page, '/r/acme/app', { [api]: () => [200, fixture(count)] });
  await page.getByRole('button', { name: 'api to web: no rule' }).focus();
  await page.evaluate(() => {
    const touched = new Set<Node>();
    const w = window as unknown as { touched: Set<Node> };
    w.touched = touched;
    new MutationObserver((records) => records.forEach((r) => touched.add(r.target))).observe(document.querySelector('table')!, {
      attributes: true,
      subtree: true,
      childList: true,
    });
  });
  await page.keyboard.press('ArrowDown');
  await page.keyboard.press('ArrowRight');
  const touched = await page.evaluate(() => (window as unknown as { touched: Set<Node> }).touched.size);
  // Two moves: each clears one row and column and sets another (cells, headers), never all 144 cells.
  expect(touched).toBeGreaterThan(0);
  expect(touched).toBeLessThanOrEqual(2 * 2 * (2 * count + 2));
});

test('DSM keeps one tab stop after a click moves the active cell', async ({ page }) => {
  await open(page, '/r/acme/app', { [api]: () => [200, fixture(5)] });
  await expect(page.locator('td > button[tabindex="0"]')).toHaveCount(1);
  await page.getByRole('button', { name: 'storage to billing: no rule' }).click();
  await page.keyboard.press('Escape');
  await expect(page.locator('td > button[tabindex="0"]')).toHaveCount(1);
  await expect(page.getByRole('button', { name: 'storage to billing: no rule' })).toHaveAttribute('tabindex', '0');
});

test('DSM keeps a self-pair that has a status focusable', async ({ page }) => {
  const data = fixture(5);
  data.map.lines.push({ from: 'web', to: 'web', kind: 'drift', rule: 'graph' });
  await open(page, '/r/acme/app', { [api]: () => [200, data] });
  await expect(page.locator('td.self')).toHaveCount(4);
  await expect(page.getByRole('button', { name: 'web to web: Drift · rule: graph' })).toBeVisible();
});

test('DSM says so when only one module is declared', async ({ page }) => {
  await open(page, '/r/acme/app', { [api]: () => [200, { ...fixture(1), map: { ...fixture(1).map, lines: [] } }] });
  await expect(page.getByText('One module is declared, so there is nothing to draw.')).toBeVisible();
  await expect(page.getByRole('table')).toHaveCount(0);
  await expect(page.getByRole('list', { name: 'Matrix legend' })).toHaveCount(0);
});

test('DSM large map filters retain square axes and bounded two-axis scrolling', async ({ page }) => {
  // Axe scans all 2,500 cells twice; allow its measured cost under full-suite concurrency.
  test.slow();
  await page.setViewportSize({ width: 390, height: 844 });
  await open(page, '/r/acme/app', { [api]: () => [200, fixture(50)] });
  await expect(page.getByRole('rowheader')).toHaveCount(50);
  const viewport = page.getByRole('region', { name: 'Scrollable relationship matrix' });
  expect(await viewport.evaluate((el) => el.scrollWidth > el.clientWidth && el.scrollHeight > el.clientHeight)).toBe(
    true,
  );
  await page.getByLabel('Search modules or layers').fill('Entry');
  await expect(page.getByRole('rowheader')).toHaveCount(2);
  await expect(page.getByRole('columnheader')).toHaveCount(3);
  await page.getByRole('button', { name: 'Reset filters' }).click();
  await page.getByLabel('Focus module').selectOption('storage');
  await page.getByLabel('Direction').selectOption('inbound');
  await expect(page.getByRole('rowheader')).toHaveCount(4);
  await page.getByLabel('Direction').selectOption('outbound');
  await expect(page.getByRole('rowheader')).toHaveCount(1);
  await page.getByRole('button', { name: 'Reset filters' }).click();
  await page.getByLabel('Needs attention').check();
  await expect(page.getByRole('rowheader')).toHaveCount(3);
  await page.getByRole('button', { name: /Support \(46 modules\)/ }).click();
  await expect(page.getByRole('rowheader')).toHaveCount(2);
  await page.getByRole('button', { name: /Support \(46 modules\)/ }).click();
  await expect(page.getByRole('rowheader')).toHaveCount(3);
  await page.getByLabel('Search modules or layers').fill('missing');
  await expect(page.getByText('No modules match these filters. Reset filters to see all modules.')).toBeVisible();
  await page.getByRole('button', { name: 'Reset filters' }).click();
  await expect(page.getByRole('rowheader')).toHaveCount(50);
  expect(await page.evaluate(() => document.documentElement.scrollWidth)).toBeLessThanOrEqual(390);
  await expectClean(page, ['Policy and review status', 'Needs attention']);
});

test('Matrix is the default without a worker; explicit Diagram choice survives reload and resize', async ({ page }) => {
  const workers: string[] = [];
  page.on('request', (request) => {
    if (/\/assets\/(?:layout\.worker|Diagram)-/.test(request.url())) workers.push(request.url());
  });
  await page.setViewportSize({ width: 390, height: 844 });
  await open(page, '/r/acme/app', { [api]: () => [200, fixture(5)] });
  await expect(page.getByRole('table', { name: tableName })).toBeVisible();
  expect(workers).toEqual([]);
  await page.getByRole('button', { name: 'Diagram', exact: true }).click();
  await expect(page.getByRole('group', { name: 'Modules by layer and their dependencies' })).toBeVisible();
  await expect(page.getByText('Show the map as a diagram', { exact: true })).toHaveCount(0);
  expect(workers.some((url) => url.includes('layout.worker-'))).toBe(true);
  await page.reload();
  await expect(page.getByRole('button', { name: 'Diagram', exact: true })).toHaveAttribute('aria-pressed', 'true');
  await page.setViewportSize({ width: 1280, height: 844 });
  await expect(page.getByRole('button', { name: 'Diagram', exact: true })).toHaveAttribute('aria-pressed', 'true');
  await page.getByRole('button', { name: 'Matrix', exact: true }).click();
  await page.reload();
  await expect(page.getByRole('table', { name: tableName })).toBeVisible();
});

test('Matrix works when browser storage is unavailable', async ({ page }) => {
  await page.addInitScript(() =>
    Object.defineProperty(window, 'localStorage', {
      get() {
        throw new Error('unavailable');
      },
    }),
  );
  await open(page, '/r/acme/app', { [api]: () => [200, fixture(5)] });
  await expect(page.getByRole('table', { name: tableName })).toBeVisible();
  await page.getByRole('button', { name: 'Diagram', exact: true }).click();
  await expect(page.getByRole('group', { name: 'Modules by layer and their dependencies' })).toBeVisible();
  await expect(page.getByText('Show the map as a diagram', { exact: true })).toHaveCount(0);
  await page.getByRole('button', { name: 'Matrix', exact: true }).click();
  await expect(page.getByRole('table', { name: tableName })).toBeVisible();
});

test('Diagram import failure reports an error and leaves Matrix available', async ({ page }) => {
  await page.route('**/assets/Diagram-*.js', (route) => route.abort());
  await open(page, '/r/acme/app', { [api]: () => [200, fixture(5)] });
  await expect(page.getByRole('table', { name: tableName })).toBeVisible();
  await page.getByRole('button', { name: 'Diagram', exact: true }).click();
  await expect(page.getByRole('alert')).toHaveText('The diagram could not load. Select Matrix for relationship details.');
  await expect(page.getByText('Show the map as a diagram', { exact: true })).toHaveCount(0);
  await page.getByRole('button', { name: 'Matrix', exact: true }).click();
  await expect(page.getByRole('table', { name: tableName })).toBeVisible();
  await expect(page.getByRole('alert')).toHaveCount(0);
});

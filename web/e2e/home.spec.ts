import { readFileSync, readdirSync } from 'node:fs';
import { expect, test } from '@playwright/test';
import { expectClean, expectNoNotUsedWord, expectPageText, open } from './support';
import { readHome } from '../src/lib/home';

const path = '/r/acme/app';
const api = 'GET /api/repos/acme/app/home';
const fixtures = new URL('../../internal/control/ui/testdata/home/', import.meta.url);
const names = readdirSync(fixtures)
  .filter((n) => n.endsWith('.json'))
  .sort();
const fixture = (name: string) => JSON.parse(readFileSync(new URL(name + '.json', fixtures), 'utf8'));

for (const file of names) {
  const name = file.replace(/\.json$/, '');
  test(`home: ${name} golden`, async ({ page }) => {
    const json = fixture(name);
    const body = readHome(json);
    const reads: string[] = [];
    page.on('request', (r) => {
      if (r.url().includes('/api/repos/')) reads.push(new URL(r.url()).pathname);
    });
    const writes = await open(page, path, { [api]: () => [200, json] });
    await expect(page.getByRole('heading', { name: 'Home', exact: true })).toBeVisible();
    await expect(page.locator('main > p').first()).toHaveText('● ' + body.status);
    await expect(page.getByRole('navigation', { name: 'Repository' }).getByRole('link')).toHaveText(['Home', 'Policy']);
    await expect(page.getByRole('link', { name: 'Home', exact: true })).toHaveAttribute('aria-current', 'page');
    if (body.map)
      await expect(page.getByRole('img', { name: 'Modules by layer and their dependencies' })).toBeVisible();
    if (body.setup) {
      await expect(page.getByRole('heading', { name: 'Needs you' })).toHaveCount(0);
      await expect(page.getByRole('heading', { name: 'The map' })).toHaveCount(0);
      await expect(page.getByRole('link', { name: 'register a passkey' })).toHaveAttribute('href', path + '/passkeys');
    } else {
      for (const need of body.needs_you)
        await expect(page.getByRole('link', { name: need.text, exact: true })).toHaveAttribute('href', need.href);
    }
    await expectPageText(page, 'home-' + name);
    await expectClean(page);
    if (body.map) {
      await page.getByText('Show the map as a list', { exact: true }).focus();
      await page.keyboard.press('Enter');
      for (const m of body.map.modules)
        await expect(page.locator('details li').filter({ hasText: m.name + ' · Volatility:' })).toBeVisible();
      await expectNoNotUsedWord(page);
    }
    expect(reads).toEqual(['/api/repos/acme/app/home']);
    expect(writes).toEqual([]);
  });
}

test('home: request opens its page and the record folds away', async ({ page }) => {
  await open(page, path, { [api]: () => [200, fixture('one-request')] });
  await expect(page.getByText('cmd/run.go:12:', { exact: false })).toBeHidden();
  await page.getByText('Show the record', { exact: true }).click();
  await expect(page.locator('pre')).toContainText('balance: 9/10 (min 7)');
  await expectNoNotUsedWord(page);
  await page.route('**/api/repos/acme/app/requests/431', (r) =>
    r.fulfill({ status: 404, json: { error: 'Not found.' } }),
  );
  await page.getByRole('link', { name: 'Request #431 waits.' }).click();
  await expect(page).toHaveURL(/\/requests\/431$/);
});

test('home: worker failure says so, and the list still works', async ({ page }) => {
  await page.route('**/assets/layout.worker-*.js', (r) => r.abort());
  await open(page, path, { [api]: () => [200, fixture('one-request')] });
  await expect(page.getByRole('alert')).toContainText('The map layout worker could not place the modules.');
  await expect(page.getByRole('img', { name: 'Modules by layer and their dependencies' })).toHaveCount(0);
  await page.getByText('Show the map as a list', { exact: true }).click();
  await expect(page.getByText('app → other: Requested dependency · #431', { exact: true })).toBeVisible();
  await expectPageText(page, 'home-worker-error');
  await expectClean(page);
});

for (const [name, response] of [
  ['api-error', { status: 502, body: { error: 'The Home answer could not be read.' } }],
  ['malformed', { status: 200, body: { status: 'clean', map: {} } }],
] as const) {
  test(`home: ${name} is not an empty map`, async ({ page }) => {
    await open(page, path, { [api]: () => [response.status, response.body] });
    await expect(page.getByRole('alert')).toContainText('The Home answer could not be read.');
    await expect(page.getByRole('heading', { name: 'Needs you' })).toHaveCount(0);
    await expectPageText(page, 'home-' + name);
    await expectClean(page);
  });
}

test('home: empty map is explicit', async ({ page }) => {
  const body = { ...fixture('clean'), map: { layers: [], modules: [], lines: [] } };
  await open(page, path, { [api]: () => [200, body] });
  await expect(page.getByText('No modules.', { exact: true })).toBeVisible();
  await expectPageText(page, 'home-empty');
  await expectClean(page);
});

test('home: scrolling stays inside the map at phone width, and bands stay in order', async ({ page }) => {
  await page.setViewportSize({ width: 390, height: 844 });
  await open(page, path, { [api]: () => [200, fixture('one-request')] });
  const scroll = page.getByRole('region', { name: 'Module map; scroll to see the whole map' });
  await expect(scroll).toBeVisible();
  expect(await scroll.evaluate((el) => el.scrollWidth)).toBeGreaterThan(await scroll.evaluate((el) => el.clientWidth));
  await scroll.focus();
  await page.keyboard.press('ArrowRight');
  await expect.poll(() => scroll.evaluate((el) => el.scrollLeft)).toBeGreaterThan(0);
  expect(await page.evaluate(() => document.documentElement.scrollWidth)).toBeLessThanOrEqual(390);
  const ys = await page.locator('.map-node rect').evaluateAll((nodes) => nodes.map((n) => Number(n.getAttribute('y'))));
  expect(ys[0]).toBeLessThan(ys[1]);
  expect(ys[1]).toBeLessThan(ys[2]);
});

test('home: SVG keeps request/date labels and only one kind per pair', async ({ page }) => {
  for (const [name, label, kind] of [
    ['one-request', 'Requested dependency · #431', 'requested'],
    ['exception-nine-days', 'Exception · 2026-11-19', 'exception'],
    ['deny-request', 'A rule denies this dependency · no-app-to-other', 'deny'],
    ['drift', 'Drift · graph', 'drift'],
  ]) {
    await page.unrouteAll();
    await open(page, path, { [api]: () => [200, fixture(name)] });
    await expect(page.locator('.map-edge.' + kind)).toContainText(label);
    await expect(page.locator('.map-edge')).toHaveCount(2);
    await expect(page.locator('.map-edge.allowed')).toHaveCount(1);
    await expectNoNotUsedWord(page);
  }
});

test('home: unknown and cross-repository links stay text, and API text is escaped', async ({ page }) => {
  const body = {
    ...fixture('clean'),
    needs_you: [
      { kind: 'request', text: '<img src=x onerror=alert(1)>', href: 'javascript:alert(1)' },
      { kind: 'request', text: 'Another repository', href: '/r/elsewhere/app/requests/431' },
      { kind: 'request', text: 'Unknown page', href: '/r/acme/app/elsewhere' },
    ],
  };
  await open(page, path, { [api]: () => [200, body] });
  await expect(page.getByText('<img src=x onerror=alert(1)>', { exact: true })).toBeVisible();
  await expect(page.getByRole('link', { name: 'Another repository' })).toHaveCount(0);
  await expect(page.getByRole('link', { name: 'Unknown page' })).toHaveCount(0);
  await expect(page.locator('main img')).toHaveCount(0);
  await expectClean(page);
});

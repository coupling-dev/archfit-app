import { readFileSync } from 'node:fs';
import { expect, test, type Page } from '@playwright/test';
import type { Policy } from '../src/lib/api';
import { open } from './support';

const fixture = (path: string) =>
  JSON.parse(readFileSync(new URL('../../internal/control/ui/testdata/' + path + '.json', import.meta.url), 'utf8'));
const pumba: Policy = fixture('policy/pumba');
const home = fixture('home/not-set-up');

// marker lives on the window: a document load clears it, so it survives only when the App moved without one.
async function markDocument(page: Page) {
  await page.evaluate(() => {
    (window as unknown as Record<string, unknown>).marker = 1;
  });
}
const pathOf = (page: Page) => new URL(page.url()).pathname;
const documentMarker = (page: Page) =>
  page.evaluate(() => (window as unknown as Record<string, unknown>).marker);

test('a link inside the App moves to its page without a document load', async ({ page }) => {
  await open(page, '/r/acme/app', {
    'GET /api/repos/acme/app/home': () => [200, home],
    'GET /api/repos/acme/app/policy': () => [200, pumba],
  });
  await expect(page.getByRole('link', { name: 'Policy', exact: true })).toBeVisible();
  await markDocument(page);

  await page.getByRole('link', { name: 'Policy', exact: true }).click();

  await expect.poll(() => pathOf(page)).toBe('/r/acme/app/policy');
  await expect(page.getByRole('heading', { name: 'Policy', exact: true })).toBeVisible();
  expect(await documentMarker(page)).toBe(1);
});

test('back and forward walk the App history without a document load', async ({ page }) => {
  await open(page, '/r/acme/app', {
    'GET /api/repos/acme/app/home': () => [200, home],
    'GET /api/repos/acme/app/policy': () => [200, pumba],
  });
  await page.getByRole('link', { name: 'Policy', exact: true }).click();
  await expect.poll(() => pathOf(page)).toBe('/r/acme/app/policy');
  await markDocument(page);

  await page.goBack();
  await expect.poll(() => pathOf(page)).toBe('/r/acme/app');
  await expect(page.getByRole('link', { name: 'Policy', exact: true })).toBeVisible();

  await page.goForward();
  await expect.poll(() => pathOf(page)).toBe('/r/acme/app/policy');
  await expect(page.getByRole('heading', { name: 'Policy', exact: true })).toBeVisible();
  expect(await documentMarker(page)).toBe(1);
});

test('a read still in flight from a page the user left does not replace the page shown', async ({ page }) => {
  let release!: () => void;
  const held = new Promise<void>((resolve) => (release = resolve));
  await open(page, '/r/acme/gone/pull/9', {
    'GET /api/repos/acme/gone/pulls/9': async () => {
      await held;
      return [404, { error: 'Repository not found.' }];
    },
    'GET /api/repos': () => [200, { repos: [], truncated: false }],
  });

  await page.getByRole('banner').getByRole('link', { name: 'Repositories', exact: true }).click();
  await expect.poll(() => pathOf(page)).toBe('/');
  await expect(page.getByRole('heading', { name: 'Repositories', exact: true })).toBeVisible();

  const answered = page.waitForResponse((response) => response.url().includes('/pulls/9'));
  release();
  await answered;
  await expect(page.getByRole('heading', { name: 'Repositories', exact: true })).toBeVisible();
  await expect(page.getByRole('heading', { name: 'Page not available', exact: true })).toHaveCount(0);
});

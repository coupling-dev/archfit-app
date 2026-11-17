import { readFileSync } from 'node:fs';
import { expect, test, type Locator, type Page } from '@playwright/test';
import type { Policy } from '../src/lib/api';
import { open as openSignedOut } from './fixtures';
import { open } from './support';

const fixture = (path: string) =>
  JSON.parse(readFileSync(new URL('../../internal/control/ui/testdata/' + path + '.json', import.meta.url), 'utf8'));
const pumba: Policy = fixture('policy/pumba');
const home = (page: Page, name: string) =>
  open(page, '/r/acme/app', { 'GET /api/repos/acme/app/home': () => [200, fixture('home/' + name)] });

const style = (locator: Locator, property: string) =>
  locator.first().evaluate((el, p) => getComputedStyle(el).getPropertyValue(p), property);

test('foundation: a link that looks like a button is never underlined, at rest or on hover', async ({ page }) => {
  await home(page, 'not-set-up');
  for (const link of await page.locator('main a.btn').all()) {
    await expect(link).toBeVisible();
    expect(await style(link, 'text-decoration-line')).toBe('none');
    await link.hover();
    expect(await style(link, 'text-decoration-line')).toBe('none');
  }
});

test('foundation: a link in a table or a list is plain, a link in a sentence is underlined', async ({ page }) => {
  await open(page, '/r/acme/app/policy', { 'GET /api/repos/acme/app/policy': () => [200, pumba] });
  const rules = page.getByRole('region', { name: 'Rules', exact: true });
  expect(await style(rules.getByRole('link', { name: 'View source' }), 'text-decoration-line')).toBe('none');
  await home(page, 'not-set-up');
  const sentence = page.locator('main p a').first();
  await expect(sentence).toBeVisible();
  expect(await style(sentence, 'text-decoration-line')).toBe('underline');
});

test('foundation: the focus ring is an outline and keeps the corners of the element', async ({ page }) => {
  await home(page, 'not-set-up');
  const button = page.locator('main a.btn').first();
  const radius = await style(button, 'border-top-left-radius');
  await button.focus();
  await expect(button).toBeFocused();
  expect(await style(button, 'outline-style')).toBe('solid');
  expect(await style(button, 'outline-width')).toBe('2px');
  expect(await style(button, 'border-top-left-radius')).toBe(radius);
  expect(radius).toBe('6px');
});

test('foundation: the policy search and the policy editor field are 32px controls', async ({ page }) => {
  await open(page, '/r/acme/app/policy', { 'GET /api/repos/acme/app/policy': () => [200, pumba] });
  const search = page.getByRole('searchbox', { name: 'Search policy' });
  expect((await search.boundingBox())?.height).toBe(32);
  const button = page.getByRole('button', { name: 'Copy digest' });
  expect((await button.boundingBox())?.height).toBe(28);
});

test('foundation: setup status text is not monospace and the sections keep one 24px gap', async ({ page }) => {
  await home(page, 'not-set-up');
  const chip = page.locator('.setup .chip').first();
  await expect(chip).toBeVisible();
  expect(await style(chip, 'font-family')).not.toMatch(/mono|Menlo|Consolas/i);
  const gap = await page.locator('.page-head').evaluate((head) => {
    const next = head.nextElementSibling as HTMLElement;
    return next.getBoundingClientRect().top - head.getBoundingClientRect().bottom;
  });
  expect(gap).toBe(24);
});

test('foundation: the primary button reads its colours from tokens in both schemes', async ({ page }) => {
  await openSignedOut(page, '/', { signedIn: false });
  const primary = page.locator('.btn.primary').first();
  for (const [scheme, background] of [
    ['light', 'rgb(31, 136, 61)'],
    ['dark', 'rgb(35, 134, 54)'],
  ] as const) {
    await page.emulateMedia({ colorScheme: scheme });
    expect(await style(primary, 'background-color')).toBe(background);
  }
});

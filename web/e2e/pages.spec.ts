import { expect, test } from '@playwright/test';
import { open, expectClean, everyPage, rendered } from './fixtures';


test('theme: the choice applies at once and survives a reload', async ({ page }) => {
  await open(page, '/');
  await page.getByRole('button', { name: 'Dark' }).click();
  await expect(page.locator('html')).toHaveAttribute('data-theme', 'dark');
  await page.reload();
  await expect(page.locator('html')).toHaveAttribute('data-theme', 'dark');
  await expect(page.getByRole('button', { name: 'Dark' })).toHaveAttribute('aria-pressed', 'true');
  await page.getByRole('button', { name: 'Auto' }).click();
  await expect(page.locator('html')).not.toHaveAttribute('data-theme', /./);
  await expectClean(page);
});


test('a page reads its data beside the session and shows its frame at once', async ({ page }) => {
  let release = () => {};
  const holdSession = new Promise<void>((resolve) => (release = resolve));
  const overview = page.waitForRequest('**/api/repos/acme/app/overview');
  await open(page, '/r/acme/app', { holdSession });
  await overview; // asked before the session answered
  await expect(page.getByRole('status', { name: 'Loading' })).toBeVisible();
  release();
  await expect(page.getByRole('heading', { name: 'acme/app' })).toBeVisible();
  await expect(page.getByRole('status', { name: 'Loading' })).toHaveCount(0);
  await expectClean(page);
});


test('every page: content at most 1280px and centred, the licences in the footer', async ({ page }) => {
  await page.setViewportSize({ width: 1600, height: 900 });
  for (const path of everyPage) {
    await page.unrouteAll();
    await open(page, path, { operator: path === '/admin' });
    await rendered(page, path);
    await expect(page.getByRole('link', { name: 'Third-party licences' })).toHaveAttribute('href', '/licenses.txt');
    await expect(page.getByRole('link', { name: 'Documentation' })).toHaveAttribute('href', /docs\/user-guide\.md$/);
    for (const box of [await page.locator('main').boundingBox(), await page.locator('footer').boundingBox()]) {
      expect(box!.width, path).toBeLessThanOrEqual(1280);
      expect(Math.abs(box!.x - (1600 - box!.x - box!.width)), path).toBeLessThanOrEqual(1);
    }
  }
});


test('every page: no sideways scroll at phone width', async ({ page }) => {
  await page.setViewportSize({ width: 390, height: 844 });
  for (const path of everyPage) {
    await page.unrouteAll();
    await open(page, path, { operator: path === '/admin' });
    await rendered(page, path);
    const width = await page.evaluate(() => document.documentElement.scrollWidth);
    expect(width, path).toBeLessThanOrEqual(390);
  }
});

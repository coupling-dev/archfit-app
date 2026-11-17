import { readFileSync } from 'node:fs';
import { expect, test } from '@playwright/test';
import { expectClean, open } from './support';

// A page head with three wide actions must wrap them on a phone instead of pushing the page wider than the screen.
test('page head actions wrap at phone width', async ({ page }) => {
  await page.setViewportSize({ width: 390, height: 844 });
  const body = JSON.parse(readFileSync(new URL('../../internal/control/ui/testdata/pull/unknown-workflow.json', import.meta.url), 'utf8')) as object;
  await open(page, '/r/acme/app/pull/4', { 'GET /api/repos/acme/app/pulls/4': () => [200, body] });
  await expect(page.locator('.page-actions')).toBeVisible();
  expect(await page.evaluate(() => document.documentElement.scrollWidth)).toBeLessThanOrEqual(390);
  const box = await page.locator('.page-actions').boundingBox();
  expect(box?.width ?? Infinity).toBeLessThanOrEqual(390);
  await expectClean(page, ['Review workflow change', 'Review first policy', 'An architecture owner named in the approved policy can review this.']);
});

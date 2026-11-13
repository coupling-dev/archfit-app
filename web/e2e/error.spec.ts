import { expectNoNotUsedWord } from './support';
import { expect, test } from '@playwright/test';
import { open, expectClean } from './fixtures';


test("a repository the user cannot see reads as one that does not exist: the API's Not found, no tabs", async ({
  page,
}) => {
  const shown: string[] = [];
  for (const path of ['/r/acme/hidden', '/r/acme/missing/pull/4']) {
    await open(page, path);
    await expect(page.getByRole('heading', { name: 'This page cannot be shown' })).toBeVisible();
    await expect(page.getByText('Not found.')).toBeVisible();
    await expect(page.getByRole('navigation', { name: 'Repository' })).toHaveCount(0);
    await expect(page).toHaveTitle('Error · Archfit');
    shown.push(await page.locator('main').innerHTML());
  }
  expect(shown[0]).toBe(shown[1]);
  await expectClean(page);
  await expectNoNotUsedWord(page);
});


test('error page: the server sentence, no repository tabs, under the CSP', async ({ page }) => {
  await open(page, '/r/acme/hidden', { serverError: { status: 404, sentence: 'Not found.' } });
  await expect(page.getByRole('heading', { name: 'This page cannot be shown' })).toBeVisible();
  await expect(page.getByText('Not found.')).toBeVisible();
  await expect(page.getByRole('navigation', { name: 'Repository' })).toHaveCount(0);
  await expect(page).toHaveTitle('Error · Archfit');
  await expect(page.getByRole('link', { name: 'Third-party licences' })).toHaveAttribute('href', '/licenses.txt');
  await expectClean(page);
  await expectNoNotUsedWord(page);
});

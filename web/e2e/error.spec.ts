import { docsShot, expectNoNotUsedWord } from './support';
import { expect, test } from '@playwright/test';
import { open, expectClean } from './fixtures';


test("a repository the user cannot see reads as one that does not exist: the API's Not found, no tabs", async ({
  page,
}) => {
  const shown: string[] = [];
  for (const path of ['/r/acme/hidden', '/r/acme/missing/pull/4']) {
    await open(page, path);
    await expect(page.getByRole('heading', { name: 'Page not available' })).toBeVisible();
    await expect(page.getByText('This page does not exist or is not available to your account.')).toBeVisible();
    await expect(page.getByRole('navigation', { name: 'Repository' })).toHaveCount(0);
    await expect(page).toHaveTitle('Page not available · Archfit');
    shown.push(await page.locator('main').innerHTML());
  }
  expect(shown[0]).toBe(shown[1]);
  await expectClean(page);
  await expectNoNotUsedWord(page);
});


test('error page: the server sentence, no repository tabs, under the CSP', async ({ page }) => {
  await open(page, '/r/acme/hidden', { serverError: { status: 404, sentence: 'Not found.' } });
  await expect(page.getByRole('heading', { name: 'Page not available' })).toBeVisible();
  await expect(page.getByText('This page does not exist or is not available to your account.')).toBeVisible();
  await expect(page.getByRole('navigation', { name: 'Repository' })).toHaveCount(0);
  await expect(page).toHaveTitle('Page not available · Archfit');
  await expect(page.getByRole('link', { name: 'Third-party licences' })).toHaveAttribute('href', '/licenses.txt');
  await expectClean(page);
  await expectNoNotUsedWord(page);
});

test('a missing pull request on a repository the user can see names it and keeps the repository tabs', async ({
  page,
}) => {
  await open(page, '/r/acme/app/pull/999999', { repos: { repos: [{ full_name: 'acme/app', name: 'app', private: false }] } });
  await expect(page.getByRole('heading', { name: 'Page not available' })).toBeVisible();
  await expect(page.getByText('Pull request #999999 was not found in acme/app.', { exact: true })).toBeVisible();
  await expect(page.getByRole('navigation', { name: 'Repository' })).toBeVisible();
  await expect(page.getByRole('link', { name: 'Back to acme/app' })).toHaveAttribute('href', '/r/acme/app');
  await expect(page).toHaveTitle('Page not available · Archfit');
  await expectClean(page);
  await expectNoNotUsedWord(page);
});

test('a missing pull request on a repository the user cannot see stays generic, with no repository tabs', async ({
  page,
}) => {
  await open(page, '/r/acme/app/pull/999999');
  await expect(page.getByText('This page does not exist or is not available to your account.')).toBeVisible();
  await expect(page.getByText('was not found in', { exact: false })).toHaveCount(0);
  await expect(page.getByRole('navigation', { name: 'Repository' })).toHaveCount(0);
  await expectClean(page);
});

test('/repos points to the repositories on the home page', async ({ page }) => {
  await open(page, '/repos');
  await expect(page.getByRole('heading', { name: 'Page not available' })).toBeVisible();
  await expect(page.getByText('Repositories are listed on the home page.')).toBeVisible();
  await expect(page.locator('main').getByRole('link', { name: 'Repositories' })).toHaveAttribute('href', '/');
  await expectClean(page);
});

test('about this App: the lead says what it is, and only rows with a value show', async ({ page }) => {
  await open(page, '/', {
    appBuild: { source_revision: '7111039f10af3c5559638354e4d8073cc9f4b790', built_at: '2026-11-16T13:56:20Z' },
  });
  await page.getByText('About this App', { exact: false }).click();
  await expect(page.getByText('This is the build of the App. The engine and Action versions are on each pull request.')).toBeVisible();
  const labels = await page.locator('.app-about dt').allInnerTexts();
  expect(labels).toEqual(['Source revision', 'Built (UTC)']);
  await expect(page.getByText('Not recorded')).toHaveCount(0);
  await expectClean(page);
});

test('a repository the user can see is matched whatever the letter case of the address', async ({ page }) => {
  await open(page, '/r/Acme/APP/pull/999999', { repos: { repos: [{ full_name: 'acme/app', name: 'app', private: false }] } });
  await expect(page.getByText('Pull request #999999 was not found in Acme/APP.', { exact: true })).toBeVisible();
  await expect(page.getByRole('navigation', { name: 'Repository' })).toBeVisible();
  await expectClean(page);
});

test('a missing page shows the generic message at once, and names the repository only when the list answers @docs', async ({
  page,
}) => {
  await page.setViewportSize({ width: 1280, height: 900 });
  let release = () => {};
  const held = new Promise<void>((resolve) => {
    release = resolve;
  });
  await open(page, '/r/acme/app/pull/999999', {
    holdRepos: held,
    repos: { repos: [{ full_name: 'acme/app', name: 'app', private: false }] },
  });
  await expect(page.getByRole('heading', { name: 'Page not available' })).toBeVisible();
  await expect(page.getByText('This page does not exist or is not available to your account.')).toBeVisible();
  await expect(page.getByText('was not found in', { exact: false })).toHaveCount(0);
  await docsShot(page, 'unknown/missing-pull-pending-1280');
  release();
  await expect(page.getByText('Pull request #999999 was not found in acme/app.', { exact: true })).toBeVisible();
  await expect(page.getByRole('navigation', { name: 'Repository' })).toBeVisible();
  await expectClean(page);
  await docsShot(page, 'unknown/missing-pull-visible-1280');
});

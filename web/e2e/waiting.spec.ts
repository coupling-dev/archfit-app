import { expectClean, expectNoNotUsedWord } from './support';
import { expect, test } from '@playwright/test';
import { open } from './fixtures';


test('waiting: a repository of an account no operator approved shows the pitch and its status, no tabs', async ({
  page,
}) => {
  await open(page, '/r/octo/tool');
  await expect(page.getByRole('heading', { name: 'Check pull requests against your architecture policy' })).toBeVisible();
  await expect(page.getByRole('status').getByText('Archfit is in a closed beta. Ask for access:', { exact: false })).toBeVisible();
  await expect(page.getByRole('navigation', { name: 'Repository' })).toHaveCount(0);
  await expect(page.getByRole('link', { name: 'Sign in with GitHub' })).toHaveCount(0);
  await expect(page.getByRole('link', { name: /beta-access.yml/ })).toHaveAttribute('href', 'https://github.com/coupling-dev/archfit/issues/new?template=beta-access.yml');
  await expect(page).toHaveTitle('Waiting to be admitted · Archfit');
  await expectClean(page);
  await expectNoNotUsedWord(page);
});

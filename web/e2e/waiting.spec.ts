import { expectNoNotUsedWord } from './support';
import { expect, test } from '@playwright/test';
import { open, expectClean } from './fixtures';


test('waiting: a repository of an account no operator approved shows the pitch and its status, no tabs', async ({
  page,
}) => {
  await open(page, '/r/octo/tool');
  await expect(page.getByRole('heading', { name: 'Architecture checks on every pull request' })).toBeVisible();
  await expect(page.getByRole('status').getByText('Your installation on octo is waiting for approval.')).toBeVisible();
  await expect(page.getByRole('navigation', { name: 'Repository' })).toHaveCount(0);
  await expect(page.getByRole('link', { name: 'Sign in with GitHub' })).toHaveCount(0);
  await expect(page.getByRole('link', { name: 'How approval works' })).toHaveAttribute('href', /#who-the-app-serves$/);
  await expect(page).toHaveTitle('Waiting for approval · Archfit');
  await expectClean(page);
  await expectNoNotUsedWord(page);
});

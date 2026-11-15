import { expectNoNotUsedWord } from './support';
import { expect, test } from '@playwright/test';
import { open, expectClean } from './fixtures';


test('signed out: the landing page says what Archfit does, signs in and comes back', async ({ page }) => {
  await open(page, '/', { signedIn: false });
  await expect(page.getByRole('heading', { name: 'Architecture checks on every pull request' })).toBeVisible();
  await expect(page.getByText('Your code never leaves your CI.')).toBeVisible();
  await expect(page.getByText('Waiting for approval')).toHaveCount(0);
  await expect(page.getByRole('link', { name: 'Sign in with GitHub' })).toHaveAttribute(
    'href',
    '/auth/login?return=%2F',
  );
  await expectClean(page);
  await expectNoNotUsedWord(page);
});


test('a token GitHub rejects sends the user to sign in, not to an error', async ({ page }) => {
  await open(page, '/', { tokenRejected: true });
  await page.waitForURL('/auth/login?return=%2F');
});

import { expectNoNotUsedWord } from './support';
import { expect, test } from '@playwright/test';
import { open, expectClean } from './fixtures';


test('signed out: the landing page says what Archfit does, signs in and comes back', async ({ page }) => {
  await open(page, '/', { signedIn: false });
  await expect(page.getByRole('heading', { name: 'Check pull requests against your architecture policy' })).toBeVisible();
  await expect(page.getByText('CI checks your code. The App reads reports and policy files to show results and owner requests.')).toBeVisible();
  await expect(page.getByText('No verdict', { exact: true })).toBeVisible();
  await expect(page.getByText(/never leaves/)).toHaveCount(0);
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

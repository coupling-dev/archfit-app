import { expect, test } from '@playwright/test';
import { session, open, expectClean } from './fixtures';


test('repositories: each default branch as a dot and a word', async ({ page }) => {
  await open(page, '/');
  await expect(page.getByRole('heading', { name: 'Repositories' })).toBeVisible();
  await expect(page.getByRole('link', { name: 'acme/web' })).toHaveAttribute('href', '/r/acme/web');
  await expect(page.getByText('violations found', { exact: true })).toHaveClass(/status danger/);
  await expect(page.getByText('@alice')).toBeVisible();
  await expect(page.locator('form[action="/auth/logout"] input[name="csrf"]')).toHaveValue(session.csrf);
  await expectClean(page);
});


test('repositories: an account waiting for approval says so in its row', async ({ page }) => {
  await open(page, '/');
  await expect(page.getByRole('row', { name: /octo\/tool/ }).getByText('waiting for approval')).toHaveClass(
    /status attention/,
  );
  await expect(page.getByRole('link', { name: 'Admin' })).toHaveCount(0);
});

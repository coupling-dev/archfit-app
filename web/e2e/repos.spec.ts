import { expect, test } from '@playwright/test';
import { session, open } from './fixtures';
import { expectClean } from './support';

test('repositories: stored states are dots and words; only expired has fail colour', async ({ page }) => {
  await open(page, '/');
  await expect(page.getByRole('heading', { name: 'Repositories' })).toBeVisible();
  await expect(page.getByRole('link', { name: 'acme/expired' })).toHaveAttribute('href', '/r/acme/expired');
  for (const state of ['clean', 'drift', 'expired', 'policy changed outside the flow', 'not set up', 'unknown']) {
    const word = page.getByText(state, { exact: true });
    await expect(word).toHaveClass(state === 'expired' ? 'status danger' : 'status neutral');
    expect(await word.evaluate((el) => getComputedStyle(el, '::before').borderRadius)).toBe('50%');
  }
  await expect(page.getByText('@alice')).toBeVisible();
  await expect(page.locator('form[action="/auth/logout"] input[name="csrf"]')).toHaveValue(session.csrf);
  await expectClean(page);
});

test('repositories: an account waiting for approval says so without reading its state', async ({ page }) => {
  await open(page, '/');
  await expect(page.getByRole('row', { name: /octo\/tool/ }).getByText('waiting for approval')).toHaveClass(
    'status neutral',
  );
  await expect(page.getByRole('link', { name: 'Admin' })).toHaveCount(0);
});

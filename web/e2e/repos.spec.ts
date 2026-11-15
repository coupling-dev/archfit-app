import { expect, test } from '@playwright/test';
import { session, open } from './fixtures';
import { expectClean } from './support';

test('repositories: stored states are dots and words; only expired has fail colour', async ({ page }) => {
  await open(page, '/');
  await expect(page.getByRole('heading', { name: 'Repositories' })).toBeVisible();
  await expect(page.getByRole('link', { name: 'acme/expired' })).toHaveAttribute('href', '/r/acme/expired');
  for (const state of ['clean', 'drift', 'expired', 'policy changed outside the flow', 'no policy', 'unknown']) {
    const word = page.getByText(state, { exact: true });
    const klass = state === 'expired' ? 'danger' : state === 'no policy' ? 'attention' : 'neutral';
    await expect(word).toHaveClass(`status ${klass}`);
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

test('repositories: onboarding stages use distinct status colours', async ({ page }) => {
  const repos = {
    repos: ['no policy', 'needs workflow', 'awaiting approval', 'active'].map((state, i) => ({
      full_name: `acme/stage-${i}`,
      state,
    })),
    truncated: false,
  };
  await open(page, '/', { repos });
  for (const [state, klass] of [
    ['no policy', 'attention'],
    ['needs workflow', 'attention'],
    ['awaiting approval', 'accent'],
    ['active', 'success'],
  ]) {
    await expect(page.getByText(state, { exact: true })).toHaveClass(`status ${klass}`);
  }
});

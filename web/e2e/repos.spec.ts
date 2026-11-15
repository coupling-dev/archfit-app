import { expect, test } from '@playwright/test';
import { session, open, reposApprovedCopy } from './fixtures';
import { expectClean, docsShot } from './support';

for (const width of [1280, 390]) {
  test(`repositories: real stages and next destinations at ${width}px @docs`, async ({ page }) => {
    await page.setViewportSize({ width, height: width === 390 ? 844 : 900 });
    await open(page, '/');
    await expect(page.getByRole('heading', { name: 'Repositories' })).toBeVisible();
    await expect(page.getByRole('status', { name: 'Loading' })).toHaveCount(0);
    for (const word of [
      'Not started',
      'First approval',
      'Workflow review',
      'Policy approved',
      'Required',
      'Not required',
    ]) {
      await expect(page.getByText(word, { exact: true }).first()).toBeVisible();
    }
    for (const word of ['unknown', 'clean', 'no policy', 'active']) {
      await expect(page.getByText(word, { exact: true })).toHaveCount(0);
    }
    await expect(page.getByRole('link', { name: 'Review first policy' })).toHaveAttribute(
      'href',
      '/r/acme/drift/owner/first?pull=8',
    );
    await expect(page.getByRole('link', { name: 'Review workflow PR' })).toHaveAttribute(
      'href',
      '/r/acme/unknown/pull/203',
    );
    await expect(page.getByText('PR #203', { exact: true })).toBeVisible();
    await expect(page.getByText('The policy changed outside the approval flow.')).toBeVisible();
    await expect(page.getByText('0 requests', { exact: true })).toHaveCount(0);
    await expect(page.getByText('@alice')).toBeVisible();
    await expect(page.locator('form[action="/auth/logout"] input[name="csrf"]')).toHaveValue(session.csrf);
    if (width === 390) {
      for (const card of await page.locator('tbody tr').all()) {
        await expect(card.getByRole('link')).toHaveCount(1);
        await expect(card.getByText('Setup', { exact: true })).toBeVisible();
        await expect(card.getByText('Needs attention', { exact: true })).toBeVisible();
        await expect(card.getByText('Next step', { exact: true })).toBeVisible();
      }
    }
    await page.emulateMedia({ colorScheme: 'light' });
    await docsShot(page, `repos-stages-${width}`, reposApprovedCopy);
    await expectClean(page, reposApprovedCopy);
  });
}

test('repositories: installation approval does not imply setup readiness', async ({ page }) => {
  await open(page, '/');
  const row = page.getByRole('row', { name: /octo\/tool/ });
  await expect(row.getByText('Waiting for installation approval')).toBeVisible();
  await expect(row.getByText('Policy approved', { exact: true })).toHaveCount(0);
  await expect(page.getByRole('link', { name: 'Admin' })).toHaveCount(0);
});

test('repositories: unread setup explains Check and offers Home without a false stage', async ({ page }) => {
  await open(page, '/', {
    repos: {
      repos: [
        {
          full_name: 'acme/app',
          state: 'clean',
          setup_reason: 'The App could not read setup; open Home to try again.',
          enforcement: 'check',
          enforcement_reason: 'The App could not read the required-check settings.',
          next_step: { text: 'Open Home', href: '/r/acme/app' },
        },
      ],
      truncated: false,
    },
  });
  await expect(page.getByText('Check', { exact: true })).toHaveCount(2);
  await expect(page.getByText('The App could not read setup; open Home to try again.')).toBeVisible();
  await expect(page.getByRole('link', { name: 'Open Home', exact: true })).toHaveAttribute('href', '/r/acme/app');
  await expect(page.getByText('Policy approved', { exact: true })).toHaveCount(0);
  await expectClean(page, reposApprovedCopy);
});

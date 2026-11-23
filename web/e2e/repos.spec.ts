import { expect, test } from '@playwright/test';
import { session, open } from './fixtures';
import { expectClean, docsShot } from './support';

for (const width of [1280, 390]) {
  test(`repositories: real stages and next destinations at ${width}px @docs`, async ({ page }) => {
    await page.setViewportSize({ width, height: width === 390 ? 844 : 900 });
    await open(page, '/repositories');
    await expect(page.getByRole('heading', { name: 'Repositories' })).toBeVisible();
    await expect(page.getByRole('status', { name: 'Loading' })).toHaveCount(0);
    for (const word of ['not set up', 'drift', 'unknown', 'clean', 'changed outside the flow', 'required', 'not required', 'not confirmed']) {
      await expect(page.getByText(word, { exact: true }).first()).toBeVisible();
    }
    for (const word of ['Needs attention', 'Required check', 'Not started', 'First approval', 'Workflow review', 'Policy approved', 'Check', 'active']) {
      await expect(page.getByText(word, { exact: true })).toHaveCount(0);
    }
    await expect(page.getByRole('link', { name: 'Review first policy' })).toHaveAttribute('href', '/r/acme/drift/pull/8');
    await expect(page.getByRole('link', { name: 'Review workflow change' })).toHaveAttribute('href', '/r/acme/unknown/pull/203');
    await expect(page.getByText('0 requests', { exact: true })).toHaveCount(0);
    await expect(page.locator('main a[href*="/owner/"]')).toHaveCount(0);
    // No row links the same page twice: the name goes to Home, a next step only to its own page.
    for (const row of await page.locator('tbody tr').all()) {
      const hrefs = await row.getByRole('link').evaluateAll((links) => links.map((a) => a.getAttribute('href')));
      expect(new Set(hrefs).size).toBe(hrefs.length);
    }
    await expect(page.getByText(/archfit\/gate is required when GitHub requires pull requests/)).toHaveCount(1);
    await expect(page.getByText('@alice')).toBeVisible();
    await expect(page.locator('form[action="/auth/logout"] input[name="csrf"]')).toHaveValue(session.csrf);
    if (width === 390) {
      for (const card of await page.locator('tbody tr').all()) {
        const hrefs = await card.getByRole('link').evaluateAll((links) => links.map((a) => a.getAttribute('href')));
      expect(new Set(hrefs).size).toBe(hrefs.length);
        await expect(card.getByText('State', { exact: true })).toBeVisible();
        await expect(card.getByText('archfit/gate', { exact: true })).toBeVisible();
      }
    }
    await page.emulateMedia({ colorScheme: 'light' });
    await docsShot(page, `repos-stages-${width}`);
    await expectClean(page);
  });
}

test('repositories: installation approval does not imply setup readiness', async ({ page }) => {
  await open(page, '/repositories');
  const row = page.getByRole('row', { name: /octo\/tool/ });
  await expect(row.getByText('not admitted', { exact: true })).toBeVisible();
  await expect(row.getByText('Policy approved', { exact: true })).toHaveCount(0);
  await expect(page.getByRole('link', { name: 'Admin' })).toHaveCount(0);
});

test('repositories: unread setup says not confirmed and names no stage', async ({ page }) => {
  await open(page, '/repositories', {
    repos: {
      repos: [
        {
          full_name: 'acme/app',
          state: 'clean',
          enforcement: 'check',
          enforcement_reason: 'The App could not read the required-check settings.',
          next_step: { text: 'Open Home', href: '/r/acme/app' },
        },
      ],
      truncated: false,
    },
  });
  await expect(page.getByText('not confirmed', { exact: true })).toHaveCount(1);
  await expect(page.getByText('Policy approved', { exact: true })).toHaveCount(0);
  await expect(page.getByText('The App could not read the required-check settings.', { exact: true })).toHaveCount(0);
  await expect(page.getByRole('row', { name: /acme\/app/ }).getByRole('link')).toHaveCount(1);
  await expect(page.getByText(/Ask a repository administrator/)).toBeVisible();
  await expectClean(page);
});

test('repositories: rows show before the checks return, then fill in', async ({ page }) => {
  let release = () => {};
  const holdProbe = new Promise<void>((resolve) => (release = resolve));
  await open(page, '/repositories', { holdProbe });
  const row = page.getByRole('row', { name: /acme\/clean/ });
  await expect(row.getByRole('link', { name: 'acme/clean' })).toBeVisible();
  await expect(row.getByText('clean', { exact: true })).toBeVisible();
  // The probe is still held: no row claims a result it does not have.
  await expect(page.getByRole('status', { name: 'Loading' })).toHaveCount(0);
  await expect(row.getByText('Checking…')).toBeVisible();
  for (const word of ['required', 'not required']) await expect(page.getByText(word, { exact: true })).toHaveCount(0);
  await expect(page.getByText('not confirmed', { exact: true })).toHaveCount(1); // the row no operator admitted
  await expect(page.getByRole('row', { name: /octo\/tool/ }).getByText('Checking…')).toHaveCount(0);
  await expectClean(page);
  release();
  await expect(row.getByText('required', { exact: true })).toBeVisible();
  await expect(page.getByText('Checking…')).toHaveCount(0);
  await expect(page.getByRole('link', { name: 'Review first policy' })).toHaveAttribute('href', '/r/acme/drift/pull/8');
  await expectClean(page);
});

test('repositories: when the checks fail the rows stay and say so', async ({ page }) => {
  await open(page, '/repositories', { probeError: true });
  await expect(page.getByRole('link', { name: 'acme/clean' })).toBeVisible();
  await expect(page.getByRole('alert')).toContainText('The App did not answer');
  await expect(page.getByText('Checking…')).toHaveCount(0);
  await expect(page.getByText('not confirmed', { exact: true }).first()).toBeVisible();
  await expectClean(page);
});

test('repositories: a failed check that answers before the list does not hide the list', async ({ page }) => {
  let release = () => {};
  const holdList = new Promise<void>((resolve) => (release = resolve));
  const failed = page.waitForResponse((r) => r.url().includes('probe=1') && r.status() === 502);
  await open(page, '/repositories', { holdList, probeError: true });
  await failed;
  await expect(page.getByRole('status', { name: 'Loading' })).toBeVisible();
  release();
  await expect(page.getByRole('link', { name: 'acme/clean' })).toBeVisible();
  await expect(page.getByRole('alert')).toContainText('The App did not answer');
  await expect(page.getByText('Checking…')).toHaveCount(0);
  await expectClean(page);
});

for (const width of [1280, 390]) {
  test(`repositories: search and filters at ${width}px`, async ({ page }) => {
    await page.setViewportSize({ width, height: 900 });
    await open(page, '/repositories');
    const search = page.getByLabel('Search repositories');
    await expect(page.getByRole('button', { name: 'All', exact: true })).toHaveAttribute('aria-pressed', 'true');
    await search.fill('ACME/DRIFT');
    await expect(page.getByText('Showing 1 of 7 repositories.')).toBeVisible();
    await expect(page.getByRole('link', { name: 'acme/drift' })).toBeVisible();
    await search.fill('no-such-repository');
    await expect(page.getByText('No repository matches these filters.')).toBeVisible();
    await page.getByRole('button', { name: 'Clear filters' }).click();
    await expect(search).toHaveValue('');
    for (const [filter, repo] of [['Not set up', 'acme/not-set-up'], ['Unknown', 'acme/unknown'], ['archfit/gate not required', 'acme/drift']]) {
      const button = page.getByRole('button', { name: filter, exact: true });
      await button.click();
      await expect(button).toHaveAttribute('aria-pressed', 'true');
      await expect(page.getByText('Showing 1 of 7 repositories.')).toBeVisible();
      await expect(page.getByRole('link', { name: repo })).toBeVisible();
    }
    await page.getByRole('button', { name: 'All', exact: true }).click();
    await expect(page.getByText('Showing 1 of 7 repositories.')).toHaveCount(0);
  });
}

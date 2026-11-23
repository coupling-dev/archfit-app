import { readFileSync } from 'node:fs';
import { expect, test } from '@playwright/test';
import { open as openMock } from './fixtures';
import { docsClock, expectClean, open } from './support';

// Owner steps that wait, as /api/owner-actions answers: the longest wait first, a step with no recorded time last.
const label = 'Review the workflow change';
const ownerActions = {
  actions: [
    { repo: 'alexei-led/pumba', kind: 'workflow', text: 'Workflow change in pull request #419 waits for an owner.', label, pull_request: 419, since: '2026-11-11T12:00:00Z', href: '/r/alexei-led/pumba/owner/workflow?pull=419' },
    { repo: 'alexei-led/ccgram', kind: 'workflow', text: 'Workflow change in pull request #413 waits for an owner.', label, pull_request: 413, since: '2026-11-13T09:00:00Z', href: '/r/alexei-led/ccgram/owner/workflow?pull=413' },
    { repo: 'acme/app', kind: 'confirm', text: 'Confirm the policy changed outside the flow.', label: 'Confirm the policy', href: '/r/acme/app/owner/confirm' },
  ],
  unread: 0,
  truncated: false,
};

for (const width of [1280, 390]) {
  test(`repositories: Needs you lists each owner step with its link at ${width}px`, async ({ page }) => {
    await page.setViewportSize({ width, height: width === 390 ? 844 : 900 });
    await docsClock(page);
    await openMock(page, '/', { ownerActions });
    const needs = page.getByRole('region', { name: 'Needs you' });
    await expect(needs.getByRole('heading', { name: 'Needs you' })).toBeVisible();
    const rows = needs.locator('.box-row');
    await expect(rows).toHaveCount(3);
    await expect(rows.nth(0)).toContainText('Review the workflow change #419');
    await expect(rows.nth(0)).toContainText('alexei-led/pumba');
    await expect(rows.nth(0)).toContainText('2 days');
    if (width === 390) await expect(rows.nth(0).locator('a.btn')).toBeVisible();
    await expect(rows.nth(1)).toContainText('Review the workflow change #413');
    await expect(rows.nth(1)).toContainText('3 h');
    await expect(rows.nth(2)).toContainText('Confirm the policy');
    await expect(rows.nth(0).getByRole('link', { name: 'Review the workflow change #419' })).toHaveAttribute(
      'href',
      '/r/alexei-led/pumba/owner/workflow?pull=419',
    );
    await expect(rows.nth(2).getByRole('link', { name: 'Confirm the policy' })).toHaveAttribute('href', '/r/acme/app/owner/confirm');
    // The steps come before the repository table.
    const before = await needs.evaluate((el) => !!(el.compareDocumentPosition(document.querySelector('table')!) & Node.DOCUMENT_POSITION_FOLLOWING));
    expect(before).toBe(true);
    await expectClean(page);
  });
}

test('repositories: new waits have one action each even when destinations coincide', async ({ page }) => {
  const repo = 'acme/app';
  await openMock(page, '/', { ownerActions: { actions: [
    { repo, kind: 'request', text: 'Request #431 waits.', label: 'Answer request #431', since: '2026-11-11T12:00:00Z', href: '/r/acme/app/requests/431' },
    { repo, kind: 'exception', text: '1 exception expires today', label: 'Open the policy', href: '/r/acme/app/policy' },
    { repo, kind: 'drift', text: 'Drift on the default branch.', label: 'Open the policy', href: '/r/acme/app/policy' },
  ], unread: 0, truncated: false } });
  const rows = page.getByRole('region', { name: 'Needs you' }).locator('.box-row');
  await expect(rows).toHaveCount(3);
  for (const [i, label] of ['Answer request #431', 'Open the policy', 'Open the policy'].entries()) {
    await expect(rows.nth(i).locator('a.btn')).toHaveCount(1);
    await expect(rows.nth(i).locator('a.btn')).toHaveText(label);
    await expect(rows.nth(i)).toContainText(repo);
  }
  await expect(rows.nth(0).locator('a.btn')).toHaveClass(/primary/);
  await expect(rows.nth(1).locator('a.btn')).not.toHaveClass(/primary/);
  await expect(rows.nth(2).locator('a.btn')).not.toHaveClass(/primary/);
  await expect(rows.nth(0)).toContainText('Request #431 waits.');
  await expect(rows.nth(1)).toContainText('1 exception expires today');
  await expect(rows.nth(2)).toContainText('Drift on the default branch.');
  await expectClean(page);
});

test('repositories: no owner step shows no Needs you', async ({ page }) => {
  await openMock(page, '/');
  await expect(page.getByRole('heading', { name: 'Repositories' })).toBeVisible();
  await expect(page.getByRole('heading', { name: 'Needs you' })).toHaveCount(0);
  await expect(page.getByRole('status').filter({ hasText: 'owner step' })).toHaveCount(0);
});

test('repositories: owner steps the App could not read are said, not left out', async ({ page }) => {
  await openMock(page, '/', { ownerActions: { actions: [], unread: 2, truncated: false } });
  await expect(page.getByText('The App could not read the owner steps of 2 repositories.')).toBeVisible();
  await expect(page.getByRole('heading', { name: 'Needs you' })).toHaveCount(0);
});

test('repository Home: an owner step is the first row, with its link and wait', async ({ page }) => {
  await docsClock(page);
  const home = JSON.parse(readFileSync(new URL('../../internal/control/ui/testdata/home/workflow-change.json', import.meta.url), 'utf8'));
  home.needs_you[0].since = '2026-11-11T12:00:00Z';
  await open(page, '/r/acme/app', { 'GET /api/repos/acme/app/home': () => [200, home] });
  const row = page.locator('section[aria-labelledby="home-needs"] .box-row').first();
  await expect(row.getByRole('link', { name: 'Review workflow change · pull request #4' })).toHaveAttribute(
    'href',
    '/r/acme/app/owner/workflow?pull=4',
  );
  await expect(row).toContainText('Waiting 2 days');
  await expect(page.locator('.page-head .btn.primary')).toHaveAttribute('href', '/r/acme/app/owner/workflow?pull=4');
});

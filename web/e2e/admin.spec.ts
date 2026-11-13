import { expectNoNotUsedWord } from './support';
import { expect, test } from '@playwright/test';
import { session, open, expectClean } from './fixtures';


test('admin: an operator approves a waiting account and revokes one after asking once', async ({ page }) => {
  const writes = await open(page, '/admin', { operator: true });
  await expect(page.getByRole('link', { name: 'Admin 1 waiting' })).toHaveAttribute('href', '/admin');
  await expect(page).toHaveTitle('Installations · Archfit');
  await expect(page.getByRole('heading', { name: 'Waiting 1' })).toBeVisible();
  await expect(page.getByText('ledger · private')).toBeVisible();
  await expect(page.getByText('Approved by vladikk on 18 October 2026')).toBeVisible();
  await expect(page.getByRole('button', { name: 'Revoke alexei-led' })).toHaveCount(0);
  await expectClean(page);
  await expectNoNotUsedWord(page);

  await page.getByRole('button', { name: 'Approve octo' }).click();
  await expect(page.getByText('Approved installation 7.')).toBeVisible();
  await expectNoNotUsedWord(page);
  await page.getByRole('button', { name: 'Revoke acme' }).click();
  await expect(page.getByText('Revoke acme? Checks stop on its next event.')).toBeVisible();
  await expectNoNotUsedWord(page);
  await page.getByRole('button', { name: 'Revoke', exact: true }).click();
  await expect(page.getByText('Revoked installation 3.')).toBeVisible();
  await expectNoNotUsedWord(page);
  expect(writes.map((w) => new URL(w.url()).pathname)).toEqual([
    '/api/admin/installations/7/approve',
    '/api/admin/installations/3/revoke',
  ]);
  for (const w of writes) expect(w.headers()['x-csrf-token']).toBe(session.csrf);
});


test("admin: anyone else gets the API's Not found", async ({ page }) => {
  await open(page, '/admin');
  await expect(page.getByRole('heading', { name: 'This page cannot be shown' })).toBeVisible();
  await expect(page.getByRole('link', { name: 'Admin' })).toHaveCount(0);
  await expectNoNotUsedWord(page);
});

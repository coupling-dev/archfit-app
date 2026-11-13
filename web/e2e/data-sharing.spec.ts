import { expect, test } from '@playwright/test';
import { session, open, expectClean } from './fixtures';


test('data sharing: what leaves, who allowed it, and Allow', async ({ page }) => {
  const writes = await open(page, '/r/acme/app/data-sharing');
  await expect(page.getByRole('heading', { name: 'Data sharing', level: 1 })).toBeVisible();
  await expect(page.getByText('every repository of acme that the Archfit App can access').first()).toBeVisible();
  const ai = page.getByRole('region', { name: 'Managed AI' });
  await expect(ai.getByText('Not allowed', { exact: true })).toBeVisible();
  await expect(ai.getByText('(to do)')).toBeAttached();
  const discovery = page.getByRole('region', { name: 'Managed discovery' });
  // read-only state as a cross or a tick, never an empty circle that looks like a radio input
  await expect(ai.locator('.checks li.not_met .mark')).toHaveText('✗');
  await expect(discovery.locator('.checks li.met .mark')).toHaveText('✓');
  await expect(discovery.getByText('Allowed by @carol on 1 September 2026')).toBeVisible();
  await expect(discovery.getByText('The App does not start managed discovery yet.', { exact: false })).toBeVisible();
  // a feature the App does not run is never offered for allowing; a consent given earlier can still be revoked
  await expect(discovery.getByRole('button', { name: /Allow/ })).toHaveCount(0);
  await expect(discovery.getByRole('button', { name: 'Revoke' })).toBeVisible();
  await expect(page.getByRole('link', { name: 'Home', exact: true })).not.toHaveAttribute('aria-current', 'page');
  await expectClean(page);

  await ai.getByRole('button', { name: 'Allow Managed AI' }).click();
  await expect(page.getByRole('status')).toHaveText('Managed AI is allowed.');
  await expect(ai.getByText('Allowed by @alice on 20 October 2026')).toBeVisible();
  await expect(ai.getByRole('button', { name: 'Revoke' })).toBeVisible();
  expect(writes).toHaveLength(1);
  const [w] = writes;
  expect(new URL(w.url()).pathname).toBe('/api/repos/acme/app/data-sharing');
  expect(w.headers()['x-csrf-token']).toBe(session.csrf);
  expect(w.postDataJSON()).toEqual({ kind: 'managed_llm', action: 'allow' });
  await expectClean(page);
});


test('data sharing: a consent that cannot be read is an error, never "not allowed"', async ({ page }) => {
  await open(page, '/r/acme/app/data-sharing', { sharingDown: true });
  await expect(page.getByRole('alert')).toContainText('Nothing was changed');
  await expect(page.getByText('Not allowed')).toHaveCount(0);
  await expect(page.getByRole('button', { name: /Allow/ })).toHaveCount(0);
  await expectClean(page);
});


test('data sharing: a change saved but not read back shows the state read again', async ({ page }) => {
  await open(page, '/r/acme/app/data-sharing', { savedUnread: true });
  const ai = page.getByRole('region', { name: 'Managed AI' });
  await ai.getByRole('button', { name: 'Allow Managed AI' }).click();
  await expect(page.getByRole('alert')).toContainText('The change was saved');
  await expect(ai.getByText('Allowed by @alice on 20 October 2026')).toBeVisible();
  await expect(ai.getByRole('button', { name: 'Revoke' })).toBeVisible();
  await expectClean(page);
});

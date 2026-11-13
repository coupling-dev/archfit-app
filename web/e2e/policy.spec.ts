import { readFileSync } from 'node:fs';
import { expect, test } from '@playwright/test';
import { expectClean, expectPageText, open } from './support';

const path = '/r/acme/app/policy';
const api = 'GET /api/repos/acme/app/policy';
const typical = JSON.parse(readFileSync(new URL('../../internal/control/ui/testdata/policy/typical.json', import.meta.url), 'utf8'));
const pumba = JSON.parse(readFileSync(new URL('../../internal/control/ui/testdata/policy/pumba.json', import.meta.url), 'utf8'));

for (const [name, fixture] of [['typical', typical], ['pumba', pumba]] as const) {
  test(`policy: ${name} golden`, async ({ page }) => {
    const writes = await open(page, path, { [api]: () => [200, fixture] });
    await expect(page.getByRole('heading', { name: 'Policy', level: 1 })).toBeVisible();
    await expect(page.getByRole('heading', { name: 'Modules' })).toBeVisible();
    await expect(page.getByRole('link', { name: 'Policy', exact: true })).toHaveAttribute('aria-current', 'page');
    await expect(page.getByRole('link', { name: 'Edit', exact: true })).toHaveAttribute('href', path + '?edit=1');
    await expect(page.getByText(fixture.yaml, { exact: true })).toBeHidden();
    await expectPageText(page, `policy-${name}`);
    await expectClean(page);
    await page.getByText('Show raw YAML').focus();
    await page.keyboard.press('Enter');
    await expect(page.getByText(fixture.yaml, { exact: true })).toBeVisible();
    await page.setViewportSize({ width: 390, height: 844 });
    expect(await page.evaluate(() => document.documentElement.scrollWidth)).toBeLessThanOrEqual(390);
    expect(writes).toEqual([]);
  });
}

test('policy: Edit navigates to the read view until the editor is built', async ({ page }) => {
  await open(page, path, { [api]: () => [200, typical] });
  await page.getByRole('link', { name: 'Edit', exact: true }).click();
  await expect(page).toHaveURL(/\/policy\?edit=1$/);
  await expect(page.getByRole('heading', { name: 'Modules' })).toBeVisible();
});

test('policy: edit URL remains a read view and exceptions link only to supplied requests', async ({ page }) => {
  const body = {
    ...typical,
    exceptions: [
      { rule: 'graph', from: '<img src=x onerror=alert(1)>', to: 'util', why: 'Approved.', expires: '2026-11-19', days_left: 0, request: 431, request_href: '/r/acme/app/requests/431' },
      { rule: 'cycle', from: 'util', why: 'Old.', expires: '2026-11-18', days_left: -1 },
      { rule: 'graph', from: 'next', why: 'A line added by edit.', request: 7, request_href: 'javascript:alert(1)' },
    ],
    edit: { available: false, reason: 'Change control has not started.' },
  };
  await open(page, path + '?edit=1', { [api]: () => [200, body] });
  await expect(page.getByText('Change control has not started.')).toBeVisible();
  await expect(page.getByRole('link', { name: 'Edit', exact: true })).toHaveCount(0);
  await expect(page.getByText('Expires: 2026-11-19 (0 days left)')).toBeVisible();
  await expect(page.getByText('Expires: 2026-11-18 (expired 1 day ago)')).toBeVisible();
  await expect(page.getByRole('link', { name: 'Request #431' })).toHaveAttribute('href', '/r/acme/app/requests/431');
  await expect(page.getByRole('link', { name: 'Request #7' })).toHaveCount(0);
  await expect(page.locator('main img')).toHaveCount(0);
  await expectClean(page);
});

test('policy: API failure is shown rather than an empty policy', async ({ page }) => {
  await open(page, path, { [api]: () => [409, { error: 'The policy could not be read.' }] });
  await expect(page.getByRole('alert')).toContainText('The policy could not be read.');
  await expect(page.getByRole('heading', { name: 'Modules' })).toHaveCount(0);
});

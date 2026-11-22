import { readFileSync } from 'node:fs';
import { expect, test } from '@playwright/test';
import { open } from './support';

const fixture = () => JSON.parse(readFileSync(new URL('../../internal/control/ui/testdata/home/not-set-up.json', import.meta.url), 'utf8'));
for (const weekly of [
  { known: true, checked: 3, quiet: 2, asked: 1, caveat: 'Counts include checks Archfit recorded; some checks may be missing' },
  { known: true, checked: 0, quiet: 0, asked: 0, caveat: 'Counts include checks Archfit recorded; some checks may be missing' },
  { known: false },
]) {
  test(`weekly setup ${JSON.stringify(weekly)}`, async ({ page }) => {
    const body = fixture();
    body.setup.weekly = weekly;
    await open(page, '/r/acme/app', { 'GET /api/repos/acme/app/home': () => [200, body] });
    if (weekly.known) {
      await expect(page.getByText(`This week: ${weekly.checked} checked, ${weekly.quiet} quiet, ${weekly.asked} asked`)).toBeVisible();
      await expect(page.getByText(weekly.caveat!)).toBeVisible();
    } else await expect(page.getByText('This week: counts unavailable')).toBeVisible();
  });
}

test('weekly counts remain visible for an open setup pull request', async ({ page }) => {
  const body = JSON.parse(readFileSync(new URL('../../internal/control/ui/testdata/home/setup-open.json', import.meta.url), 'utf8'));
  body.setup.weekly = { known: true, checked: 2, quiet: 1, asked: 1, caveat: 'Partial evidence' };
  await open(page, '/r/acme/app', { 'GET /api/repos/acme/app/home': () => [200, body] });
  await expect(page.getByText('This week: 2 checked, 1 quiet, 1 asked')).toBeVisible();
  await expect(page.getByText('Partial evidence')).toBeVisible();
});

test('weekly counts remain visible on a complete Home', async ({ page }) => {
  const body = JSON.parse(readFileSync(new URL('../../internal/control/ui/testdata/home/clean.json', import.meta.url), 'utf8'));
  body.weekly = { known: true, checked: 2, quiet: 1, asked: 1, caveat: 'Partial evidence' };
  await open(page, '/r/acme/app', { 'GET /api/repos/acme/app/home': () => [200, body] });
  await expect(page.getByText('This week: 2 checked, 1 quiet, 1 asked')).toBeVisible();
  await expect(page.getByText('Partial evidence')).toBeVisible();
});

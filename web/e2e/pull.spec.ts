import { readFileSync } from 'node:fs';
import { expect, test } from '@playwright/test';
import { expectClean, expectPageText, open } from './support';

const states = ['pass', 'fail-advisory', 'fail-advisory-request', 'required-request', 'fail-fix', 'fail-ask', 'fail-copy', 'fail-expired-exception', 'unknown', 'unknown-workflow', 'unknown-outside-flow', 'unknown-missing-evidence', 'waiting', 'unpublished', 'no-result', 'missing-artifact', 'mismatch', 'pending'];
for (const state of states) {
  test(`pull request: ${state}`, async ({ page }) => {
    const body = JSON.parse(readFileSync(new URL(`../../internal/control/ui/testdata/pull/${state}.json`, import.meta.url), 'utf8')) as object;
    await open(page, '/r/acme/app/pull/4', { 'GET /api/repos/acme/app/pulls/4': () => [200, body] });
    await expect(page.locator('main section, main h1').last()).toBeVisible();
    await expectPageText(page, `pull-${state}`);
    await expectClean(page);
    if (state === 'waiting') await expect(page.getByRole('link', { name: '#431' }).first()).toHaveAttribute('href', '/r/acme/app/requests/431');
    if (state === 'fail-advisory-request') await expect(page.locator('main h1')).toHaveText('Archfit: fail, 1 error');
    if (state === 'required-request') await expect(page.locator('main h1')).toHaveText('Archfit: waiting, request #431');
    if (state === 'unknown-outside-flow') await expect(page.locator('main a[href*="/requests/"]')).toHaveCount(0);
  });
}

test('pull page and comment use the same lines', async ({ page }) => {
  const body = JSON.parse(readFileSync(new URL('../../internal/control/ui/testdata/pull/waiting.json', import.meta.url), 'utf8')) as object;
  const comment = readFileSync(new URL('../../internal/gate/feedback/testdata/golden4/waiting.comment.md', import.meta.url), 'utf8');
  await open(page, '/r/acme/app/pull/4', { 'GET /api/repos/acme/app/pulls/4': () => [200, body] });
  await expect(page.locator('section[aria-label="Pull request"] h1')).toBeVisible();
  const lines = await page.locator('section[aria-label="Pull request"]').evaluate((section) =>
    Array.from(section.querySelectorAll('h1, .pull-record > div, p')).map((el) => (el as HTMLElement).innerText),
  );
  const expected = comment.split('\n').filter((line) => line !== '').map((line) =>
    line.replace(/^## /, '').replace(/^- /, '').replaceAll('**', '').replaceAll('`', '').replaceAll('&lt;', '<'),
  );
  expect(lines).toEqual(expected);
});

test('pull request: API failure is not a result', async ({ page }) => {
  const body = JSON.parse(readFileSync(new URL('../../internal/control/ui/testdata/pull/infrastructure-error.json', import.meta.url), 'utf8')) as object;
  await open(page, '/r/acme/app/pull/4', { 'GET /api/repos/acme/app/pulls/4': () => [502, body] });
  await expect(page.getByRole('alert')).toBeVisible();
  await expect(page.locator('section[aria-label="Pull request"]')).toHaveCount(0);
  await expectPageText(page, 'pull-infrastructure-error');
  await expectClean(page);
});

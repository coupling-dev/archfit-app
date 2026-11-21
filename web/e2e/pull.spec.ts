import { readFileSync } from 'node:fs';
import { expect, test } from '@playwright/test';
import { expectClean, expectPageText, open, docsClock, docsShot } from './support';

const states = ['pass', 'fail-advisory', 'fail-advisory-request', 'required-request', 'fail-fix', 'fail-ask', 'fail-surface', 'fail-copy', 'next-backticks', 'fail-expired-exception', 'unknown', 'unknown-first-policy', 'unknown-workflow', 'unknown-workflow-viewer', 'unknown-outside-flow', 'unknown-missing-evidence', 'waiting', 'unpublished', 'superseded', 'no-result', 'missing-artifact', 'mismatch', 'pending'];
for (const state of states) {
  test(`pull request: ${state}${state === 'waiting' ? ' @docs' : ''}`, async ({ page }) => {
    await docsClock(page);
    const body = JSON.parse(readFileSync(new URL(`../../internal/control/ui/testdata/pull/${state}.json`, import.meta.url), 'utf8')) as object;
    await open(page, '/r/acme/app/pull/4', { 'GET /api/repos/acme/app/pulls/4': () => [200, body] });
    await expect(page.locator('.page-status')).toBeVisible();
    await expectPageText(page, `pull-${state}`);
    if (state === 'waiting') await docsShot(page, 'pull');
    
    await expectClean(page);
    for (const width of [390, 1280]) {
      await page.setViewportSize({ width, height: width === 390 ? 844 : 900 });
      await docsShot(page, `pull-states/${state}-${width}`);
    }
    if (state === 'unknown-workflow' || state === 'unknown-first-policy') {
      const first = state === 'unknown-first-policy';
      const link = page.getByRole('link', { name: first ? 'Review first policy' : 'Review workflow change', exact: true });
      await expect(link).toHaveAttribute('href', '/r/acme/app/owner/' + (first ? 'first' : 'workflow') + '?pull=4');
      await expect(link).toHaveClass(/primary/);
      await link.focus();
      await expect(link).toBeFocused();
    }
    if (state === 'unknown-workflow-viewer') {
      await expect(page.locator('main a[href*="/owner/"]')).toHaveCount(0);
      await expect(page.getByText('An architecture owner named in the approved policy can review this.', { exact: true })).toBeVisible();
    }
    if (state === 'waiting') await expect(page.getByRole('link', { name: '#431' }).first()).toHaveAttribute('href', '/r/acme/app/requests/431');
    if (state === 'fail-advisory-request') await expect(page.locator('.page-status')).toContainText('Archfit: fail, 1 error');
    if (state === 'required-request') await expect(page.locator('.page-status')).toContainText('Archfit: waiting, request #431');
    if (state === 'next-backticks') await expect(page.getByText('next: git show `git rev-parse HEAD`')).toBeVisible();
    if (state === 'unknown-outside-flow') await expect(page.locator('main a[href*="/requests/"]')).toHaveCount(0);
  });
}

for (const [state, comment] of [
  ['waiting', 'waiting.comment.md'],
  ['unknown', 'unknown.comment.md'],
]) {
  test(`pull page and comment use the same lines: ${state}`, async ({ page }) => {
    const body = JSON.parse(readFileSync(new URL(`../../internal/control/ui/testdata/pull/${state}.json`, import.meta.url), 'utf8')) as object;
    const text = readFileSync(new URL(`../../internal/gate/feedback/testdata/golden_result/${comment}`, import.meta.url), 'utf8');
    await open(page, '/r/acme/app/pull/4', { 'GET /api/repos/acme/app/pulls/4': () => [200, body] });
    await expect(page.locator('.page-status')).toBeVisible();
    const lines = await page.locator('.page-status p, section[aria-label="Pull request"] .pull-record p, section[aria-label="Pull request"] > p').allInnerTexts();
    const expected = text.split('\n').filter((line) => line !== '').map((line) =>
      line.replace(/^## (?:(?:✅|❌|⏳|⚠️) )?/u, '').replace(/^- /, '').replaceAll('**', '').replaceAll('`', '').replaceAll('&lt;', '<'),
    );
    expect(lines).toEqual(expected);
  });
}

test('pull request: API failure is not a result', async ({ page }) => {
  const body = JSON.parse(readFileSync(new URL('../../internal/control/ui/testdata/pull/infrastructure-error.json', import.meta.url), 'utf8')) as object;
  await open(page, '/r/acme/app/pull/4', { 'GET /api/repos/acme/app/pulls/4': () => [502, body] });
  await expect(page.getByRole('alert')).toBeVisible();
  await expect(page.locator('section[aria-label="Pull request"]')).toHaveCount(0);
  await expectPageText(page, 'pull-infrastructure-error');
  await expectClean(page);
});

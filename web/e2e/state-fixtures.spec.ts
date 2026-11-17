import { expect, test } from '@playwright/test';
import { stateFixtures } from './state-fixtures';
import { docsClock, docsShot, expectClean, open } from './support';

for (const width of [390, 1280]) {
  for (const state of stateFixtures) {
    test(`read-only state: ${state.name} at ${width} @docs`, async ({ page }) => {
      await page.setViewportSize({ width, height: width === 390 ? 844 : 900 });
      await docsClock(page);
      const writes = await open(
        page,
        state.path,
        {
          ['GET ' + state.api]: () => state.reply,
          ...(state.name === 'expired-session'
            ? {
                'GET /api/repos/acme/app/pulls/4': () => [401, { error: 'Sign in to continue.' }] as [number, object],
              }
            : {}),
        },
        true,
      );
      await expect(page.getByRole('heading', { name: state.heading, exact: true, level: 1 })).toBeVisible();
      await expect(
        page
          .locator('main')
          .getByText(state.text, { exact: state.name !== 'closed-request' })
          .first(),
      ).toBeVisible();
      await expect(page.getByRole('status', { name: 'Loading' })).toHaveCount(0);
      if (state.name === 'expired-session') {
        await expect(page.getByRole('link', { name: 'Sign in with GitHub' })).toHaveAttribute(
          'href',
          '/auth/login?return=%2Fr%2Facme%2Fapp%2Fpull%2F4',
        );
      }
      if (state.name === 'waiting-installation' || state.name === 'expired-session') {
        await expect(page.getByRole('navigation', { name: 'Repository' })).toHaveCount(0);
      }
      if (['closed-request', 'stale-owner-action', 'denied-role', 'missing-report'].includes(state.name)) {
        await expect(page.locator('main button')).toHaveCount(0);
        await expect(page.locator('main a[href*="/owner/"]')).toHaveCount(0);
      }
      if (state.name === 'stale-owner-action') {
        await expect(page.getByRole('heading', { level: 2, name: 'This approval is not needed' })).toBeVisible();
        await expect(page.getByRole('link', { name: 'View current setup', exact: true })).toHaveAttribute('href', '/r/acme/app');
        await expect(page.getByRole('link', { name: 'View policy', exact: true })).toHaveAttribute('href', '/r/acme/app/policy');
        await expect(page.getByRole('link', { name: 'Try again', exact: true })).toHaveCount(0);
        await expect(page.getByRole('alert')).toHaveCount(0);
      }
      if (state.name === 'unknown-ruleset') {
        await expect(page.getByLabel('2 of 4 steps done')).toBeVisible();
        await page.locator('.setup .steps > li').nth(3).locator('summary').first().click();
        await expect(page.getByText('verify in GitHub', { exact: true }).first()).toBeVisible();
      }
      if (state.name === 'long-names') {
        await page.getByRole('region', { name: 'Modules', exact: true }).locator('td summary').click();
      }
      if (state.name === 'closed-request' && width === 1280) {
        await expect(page.getByText('Show the map as a diagram', { exact: true })).toBeVisible();
        await expect(page.getByRole('img', { name: 'Modules by layer and their dependencies' })).toHaveCount(0);
      }
      const text = (await page.locator('main').innerText()).replace(/[ \t]+$/gm, '');
      expect(text).toMatchSnapshot(`state-${state.name}-${width}.txt`);
      expect(await page.evaluate(() => document.documentElement.scrollWidth)).toBeLessThanOrEqual(width);
      await expectClean(page, state.approvedCopy);
      await docsShot(page, `states/${state.name}-${width}`, state.approvedCopy);
      expect(writes).toEqual([]);
    });
  }
}

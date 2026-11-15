import { readFileSync } from 'node:fs';
import { expect, test } from '@playwright/test';
import type { Policy } from '../src/lib/api';
import { docsClock, docsShot, expectClean, expectPageText, open } from './support';

const path = '/r/acme/app/policy';
const api = 'GET /api/repos/acme/app/policy';
const pumba: Policy = JSON.parse(
  readFileSync(new URL('../../internal/control/ui/testdata/policy/pumba.json', import.meta.url), 'utf8'),
);

test('policy reading: search filters rules, exceptions and modules without changing totals', async ({ page }) => {
  await open(page, path, { [api]: () => [200, pumba] });
  const counts = page.getByLabel('Policy counts');
  await expect(counts).toContainText('9Blocking rules');
  const search = page.getByRole('searchbox', { name: 'Search policy' });
  await search.fill('container.Client');
  await expect(page.getByRole('status')).toHaveText('1 matching items');
  const rule = page.getByRole('region', { name: 'Rules', exact: true });
  await expect(rule.locator('details')).toHaveCount(1);
  await rule.locator('summary').click();
  await expect(rule.getByText(pumba.rules[1]?.instead ?? '', { exact: true })).toBeVisible();
  await expect(counts).toContainText('9Blocking rules');

  await search.fill('no-such-module');
  await expect(page.getByRole('status')).toHaveText('No matches. Try a module name or rule ID.');
  await expect(page.getByRole('region', { name: 'Modules', exact: true }).locator('tbody tr')).toHaveCount(0);
  await search.fill(' PKG/TC ');
  await expect(page.getByRole('region', { name: 'Modules', exact: true }).locator('tbody tr')).toHaveCount(1);
  await expectClean(page);
});

test('policy reading: graph is secondary, uses declared dependencies and selects module details @docs', async ({
  page,
}) => {
  await docsClock(page);
  await open(page, path, { [api]: () => [200, pumba] });
  const graph = page.getByRole('group', { name: 'Allowed module dependencies' });
  await expect(graph).toBeHidden();
  await page.setViewportSize({ width: 390, height: 844 });
  await expect(graph).toBeHidden();
  if (process.env.ARCHFIT_DOCS_SHOTS) {
    await docsShot(page, 'policy-mobile');
    await page.emulateMedia({ colorScheme: 'dark' });
    await page.screenshot({
      path: new URL('../../docs/images/policy-mobile-dark.png', import.meta.url).pathname,
      fullPage: true,
    });
  }
  await page.getByText('Show graph', { exact: false }).click();
  await expect(graph.getByRole('link')).toHaveCount(6);
  await expect(graph.locator('path.dependency')).toHaveCount(7);
  await page.getByRole('searchbox').fill('no-such-module');
  await graph.getByRole('link', { name: 'Inspect pkg_container', exact: true }).focus();
  await page.keyboard.press('Enter');
  const detail = page.locator('#policy-module-2');
  await expect(detail).toHaveAttribute('open', '');
  await expect(detail.locator('summary')).toBeFocused();
  await expect(page.getByRole('searchbox')).toHaveValue('');
  await expectClean(page);
  if (process.env.ARCHFIT_DOCS_SHOTS) {
    await docsShot(page, 'policy-graph-mobile');
    await page.setViewportSize({ width: 1280, height: 900 });
    await docsShot(page, 'policy-graph');
  }
});

test('policy reading: expiry counts, unknown facts and server dates remain explicit @docs', async ({ page }) => {
  await docsClock(page);
  const body: Policy = {
    ...pumba,
    owners: null,
    layers: null,
    exceptions: [
      {
        rule: 'graph',
        from: 'pkg_chaos',
        to: 'pkg_runtime',
        why: 'Move to container.Client.',
        days_left: 12,
        expires: '2026-11-25',
        request: 431,
        request_href: '/r/acme/app/requests/431',
      },
      {
        rule: 'duplication',
        from: 'pkg_tc',
        to: 'pkg_util',
        why: 'Remove the copy.',
        days_left: -1,
        expires: '2026-11-12',
      },
      {
        rule: 'cycle',
        from: 'pkg_util',
        to: 'pkg_chaos',
        why: 'Remove the cycle.',
        days_left: 0,
        expires: '2026-11-13',
      },
      { rule: 'graph', from: 'cmd', to: 'pkg_chaos', why: 'No expiry recorded.' },
    ],
  };
  await open(page, path, { [api]: () => [200, body] });
  const counts = page.getByLabel('Policy counts');
  await expect(counts).toContainText('1 already expired');
  await expect(counts).toContainText('2Expiring in 30 days');
  const facts = page.getByRole('region', { name: 'Policy summary' });
  await expect(facts.locator('dd')).toHaveText(['Not provided', 'Not provided', 'required', 'Not provided']);
  const exceptions = page.getByRole('region', { name: 'Exceptions', exact: true });
  await expect(exceptions.getByText('Expires in 12 days', { exact: true })).toBeVisible();
  await expect(exceptions.getByText('Expired 1 day ago', { exact: true })).toBeVisible();
  await expect(exceptions.getByText('Expires today', { exact: true })).toBeVisible();
  await expect(exceptions.getByRole('progressbar')).toHaveCount(3);
  await expect(exceptions.getByRole('progressbar').nth(0)).toHaveAttribute('value', '12');
  await expect(exceptions.getByRole('progressbar').nth(1)).toHaveAttribute('value', '0');
  await expect(exceptions.getByRole('link', { name: 'Request #431' })).toHaveAttribute(
    'href',
    '/r/acme/app/requests/431',
  );
  await expectPageText(page, 'policy-expiry');
  await docsShot(page, 'policy-expiry');
  await expectClean(page);
});

test('policy reading: source and digest stay reachable, including clipboard refusal', async ({ page, context }) => {
  const writes = await open(page, path, { [api]: () => [200, pumba] });
  await page.locator('.page-head').getByRole('link', { name: 'View source', exact: true }).click();
  await expect(page.getByText(pumba.yaml, { exact: true })).toBeVisible();
  await context.grantPermissions(['clipboard-read', 'clipboard-write']);
  await page.getByRole('button', { name: 'Copy digest', exact: true }).click();
  await expect(page.getByText('Digest copied.', { exact: true })).toBeVisible();
  expect(await page.evaluate(() => navigator.clipboard.readText())).toBe(pumba.digest);
  await page.evaluate(() => {
    navigator.clipboard.writeText = async () => {
      throw new Error('refused');
    };
  });
  await page.getByRole('button', { name: 'Copy digest', exact: true }).click();
  await expect(page.getByText('Clipboard unavailable. Select the digest to copy it.', { exact: true })).toBeVisible();
  expect(writes).toEqual([]);
});

test('policy reading: empty module and exception lists remain usable', async ({ page }) => {
  await open(page, path, { [api]: () => [200, { ...pumba, modules: [], rules: [], exceptions: [] }] });
  await expect(page.getByText('No modules.', { exact: true })).toBeVisible();
  await expect(page.getByText('No exceptions.', { exact: true })).toBeVisible();
  await expect(page.getByText('Show graph', { exact: false })).toHaveCount(0);
  await expectClean(page);
});

test('policy reading: long identifiers and expanded details fit a phone in both themes', async ({ page }) => {
  const name = 'module_'.repeat(30);
  const layer = 'layer_'.repeat(30);
  const module = pumba.modules[0];
  if (!module) throw new Error('fixture has no module');
  const body: Policy = {
    ...pumba,
    modules: [{ ...module, name, layer, paths: [name + '/**'], depends_on: [name] }],
    layers: [layer],
  };
  await page.setViewportSize({ width: 390, height: 844 });
  await open(page, path, { [api]: () => [200, body] });
  await page.getByText('Show graph', { exact: false }).click();
  await page.getByRole('region', { name: 'Modules', exact: true }).locator('td summary').click();
  await expectClean(page);
});

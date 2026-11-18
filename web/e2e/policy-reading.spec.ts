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
  await expect(counts).toContainText('9Ask and deny rules');
  const search = page.getByRole('searchbox', { name: 'Search policy' });
  await search.fill('container.Client');
  await expect(page.getByRole('status')).toHaveText('1 matching items');
  const rule = page.getByRole('region', { name: 'Rules', exact: true });
  await expect(rule.locator('details')).toHaveCount(1);
  await rule.locator('summary').click();
  await expect(rule.getByText(pumba.rules[1]?.instead ?? '', { exact: true })).toBeVisible();
  await expect(counts).toContainText('9Ask and deny rules');

  await search.fill('no-such-module');
  await expect(page.getByRole('status')).toHaveText('No matches. Try a module name or rule ID.');
  await expect(page.getByRole('region', { name: 'Modules', exact: true }).locator('tbody tr')).toHaveCount(0);
  await search.fill(' PKG/TC ');
  await expect(page.getByRole('region', { name: 'Modules', exact: true }).locator('tbody tr')).toHaveCount(1);
  await expectClean(page);
});

test('policy reading: rules carry their ids and notes are explained once', async ({ page }) => {
  const body: Policy = {
    ...pumba,
    gate: 'advisory',
    rules: [{ id: 'use-runner', note: 'Run jobs through the runner.', not_checked: true, why: '' }],
  };
  await open(page, path, { [api]: () => [200, body] });
  const facts = page.getByRole('region', { name: 'Policy summary' });
  await expect(facts.locator('dd').nth(2)).toHaveText('advisory — rules are reported and archfit/gate does not fail');
  const rules = page.getByRole('region', { name: 'Rules', exact: true });
  await expect(rules.locator('summary strong')).toHaveText([
    'graph',
    'coupling',
    'duplication',
    'cycle',
    'layers',
    'unowned',
    'use-runner',
  ]);
  await expect(rules.getByText('A note is not checked. It tells the reader what to do.', { exact: true })).toHaveCount(1);
  await rules.locator('summary').last().click();
  await expect(rules.getByText('Run jobs through the runner.', { exact: true })).toBeVisible();
  await expect(rules.getByText('Instead', { exact: true })).toHaveCount(0);
  await expectClean(page);
});

test('policy reading: a repository without a policy keeps its tabs and points to setup', async ({ page }) => {
  const writes = await open(page, path, { [api]: () => [409, { error: 'No policy on the default branch yet.' }] });
  await expect(page.getByRole('heading', { name: 'No policy on the default branch yet.' })).toBeVisible();
  await expect(page.getByRole('link', { name: 'Continue setup', exact: true })).toHaveAttribute('href', '/r/acme/app');
  await expect(page.getByRole('navigation', { name: 'Repository' }).getByRole('link', { name: 'Policy' })).toHaveAttribute(
    'aria-current',
    'page',
  );
  await expect(page.getByRole('link', { name: 'Edit', exact: true })).toHaveCount(0);
  expect(writes).toEqual([]);
  await expectClean(page);
});

test('policy reading: a repository the user cannot see stays the generic page', async ({ page }) => {
  await open(page, path, { [api]: () => [404, { error: 'Not found.' }] });
  await expect(page.getByRole('heading', { name: 'Page not available' })).toBeVisible();
  await expect(page.getByRole('navigation', { name: 'Repository' })).toHaveCount(0);
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
  await expect(graph.locator('.map-node')).toHaveCount(6);
  // A six-module graph is shorter than the 70vh cap, so its viewport shrinks to the chart instead of leaving a blank area.
  const viewport = await page.locator('.map-viewport').boundingBox();
  const frame = page.viewportSize()!;
  expect(viewport!.height).toBeGreaterThanOrEqual(240);
  expect(viewport!.height).toBeLessThan(frame.height * 0.7);
  await page.getByRole('searchbox').fill('no-such-module');
  await graph.locator('[data-module="pkg_container"]').click();
  const panel = page.getByRole('region', { name: 'Selected module' });
  await expect(panel).toContainText('Depends on:');
  await expect(panel).toContainText('Used by:');
  await panel.getByRole('button', { name: 'Show in the table', exact: true }).click();
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
  await expect(facts.locator('dd')).toHaveText(['None', 'None', 'required — a failing rule makes archfit/gate fail']);
  await expect(facts.getByRole('link', { name: 'Home', exact: true })).toHaveAttribute('href', '/r/acme/app');
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

test('policy reading: exceptions group by rule, show twenty at a time and drop empty columns', async ({ page }) => {
  const many = Array.from({ length: 25 }, (_, i) => ({
    rule: i < 22 ? 'coupling' : 'duplication',
    from: `pkg_${i}`,
    why: 'Migration.',
  }));
  await open(page, path, { [api]: () => [200, { ...pumba, exceptions: many }] });
  const exceptions = page.getByRole('region', { name: 'Exceptions', exact: true });
  await expect(exceptions.getByText('25 exceptions have no end date.', { exact: true })).toBeVisible();
  await expect(exceptions.locator('thead th').first()).toHaveText('Rule / boundary');
  await expect(exceptions.getByRole('columnheader', { name: 'Owner', exact: true })).toHaveCount(0);
  await expect(exceptions.getByText('coupling · 22', { exact: true })).toBeVisible();
  await expect(exceptions.getByText('duplication · 3', { exact: true })).toBeVisible();
  await exceptions.getByText('duplication · 3', { exact: true }).click();
  await expect(exceptions.getByText('Show all to see these exceptions.', { exact: true })).toBeVisible();
  await expect(exceptions.locator('tbody tr')).toHaveCount(20);
  await expect(exceptions.getByText('pkg_24', { exact: true })).toHaveCount(0);
  await expect(exceptions.getByText('Showing 20 of 25', { exact: true })).toBeVisible();
  await expect(exceptions.getByText('Bars show', { exact: false })).toHaveCount(0);
  await exceptions.getByRole('button', { name: 'Show all', exact: true }).click();
  await expect(exceptions.locator('tbody tr')).toHaveCount(25);
  await expect(exceptions.getByRole('button', { name: 'Show all', exact: true })).toHaveCount(0);
  await expectClean(page);
});

test('policy reading: exception screenshots for the docs @docs', async ({ page }) => {
  if (!process.env.ARCHFIT_DOCS_SHOTS) test.skip();
  const rules: [string, number][] = [
    ['coupling', 62],
    ['duplication', 41],
    ['graph', 19],
    ['cycle', 8],
  ];
  const undated: Policy = {
    ...pumba,
    exceptions: rules.flatMap(([rule, n]) =>
      Array.from({ length: n }, (_, i) => ({ rule, from: `pkg_${rule}_${i}`, to: 'pkg_util', why: 'Migration.' })),
    ),
  };
  const dated: Policy = {
    ...pumba,
    exceptions: [
      { rule: 'coupling', from: 'pkg_chaos', to: 'pkg_runtime', why: 'Move to container.Client.', days_left: 12, expires: '2026-11-29', request: 431, request_href: '/r/acme/app/requests/431' },
      { rule: 'coupling', from: 'pkg_tc', to: 'pkg_util', why: 'Remove the copy.', days_left: -1, expires: '2026-11-16' },
      { rule: 'duplication', from: 'cmd', to: 'pkg_chaos', why: 'Shared flag.', days_left: 45, expires: '2027-01-31' },
    ],
  };
  for (const [name, body, width] of [
    ['policy-exceptions-130-1280', undated, 1280],
    ['policy-exceptions-130-390', undated, 390],
    ['policy-exceptions-dated-1280', dated, 1280],
  ] as const) {
    await page.setViewportSize({ width, height: 900 });
    await open(page, path, { [api]: () => [200, body] });
    const exceptions = page.getByRole('region', { name: 'Exceptions', exact: true });
    await exceptions.scrollIntoViewIfNeeded();
    await expect(exceptions.locator('thead th').first()).toBeVisible();
    await page.screenshot({
      path: new URL(`../../docs/images/${name}.png`, import.meta.url).pathname,
      fullPage: true,
      animations: 'disabled',
    });
  }
});

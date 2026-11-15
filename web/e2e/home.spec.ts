import { readFileSync, readdirSync } from 'node:fs';
import { expect, test } from '@playwright/test';
import { expectClean, expectNoNotUsedWord, expectPageText, open, docsClock, docsShot } from './support';
import { readHome } from '../src/lib/home';

const path = '/r/acme/app';
const api = 'GET /api/repos/acme/app/home';
const fixtures = new URL('../../internal/control/ui/testdata/home/', import.meta.url);
const names = readdirSync(fixtures)
  .filter((n) => n.endsWith('.json'))
  .sort();
const fixture = (name: string) => JSON.parse(readFileSync(new URL(name + '.json', fixtures), 'utf8'));
const setupApprovedCopy = [
  'Rules reach the default branch through review, so the approval binds to exact file contents.',
  'Require the gate',
  'Next: review the pull request',
  'Review PR #203',
  'PR #203 adds the workflow — review it.',
  'A password or a GitHub review can be replayed by a compromised account',
  'In review',
  'Review workflow change',
  'Review first policy',
  'Review needed',
  'A passkey is needed to approve, but not to read the review.',
];

for (const file of names) {
  const name = file.replace(/\.json$/, '');
  test(`home: ${name} golden${name === 'one-request' || name === 'workflow-review' ? ' @docs' : ''}`, async ({ page }) => {
    await docsClock(page);
    const json = fixture(name);
    const body = readHome(json);
    const reads: string[] = [];
    page.on('request', (r) => {
      if (r.url().includes('/api/repos/')) reads.push(new URL(r.url()).pathname);
    });
    const writes = await open(page, path, { [api]: () => [200, json] });
    await expect(page.getByRole('heading', { name: 'Home', exact: true })).toBeVisible();
    await expect(page.locator('.page-head .status')).toHaveText(body.status);
    await expect(page.getByRole('navigation', { name: 'Repository' }).getByRole('link')).toHaveText(['Home', 'Policy']);
    await expect(page.getByRole('link', { name: 'Home', exact: true })).toHaveAttribute('aria-current', 'page');
    if (body.map)
      await expect(page.getByRole('img', { name: 'Modules by layer and their dependencies' })).toBeVisible();
    if (body.setup) {
      await expect(page.getByRole('heading', { name: 'Needs you' })).toHaveCount(0);
      await expect(page.getByRole('heading', { name: 'The map' })).toHaveCount(0);
      if (body.setup.passkey) {
        await expect(page.getByRole('link', { name: 'Manage passkeys' })).toHaveAttribute('href', path + '/passkeys');
        await expect(page.getByRole('link', { name: 'Manage passkeys' })).toHaveClass(/ghost/);
        await expect(page.getByRole('link', { name: 'Register a passkey' })).toHaveCount(0);
      } else {
        await expect(page.getByRole('link', { name: 'Register a passkey' })).toHaveAttribute('href', path + '/passkeys');
      }
      await expect(page.getByRole('heading', { name: 'Set up Archfit' })).toBeVisible();
      await expect(page.getByRole('listitem')).toHaveCount(4);
      const chips = page.locator('.setup .chip');
      for (const chip of await chips.all()) {
        const status = await chip.innerText();
        const tone = ['Done', 'On'].includes(status)
          ? 'good'
          : ['In review', 'Waiting', 'Not available'].includes(status)
            ? 'neutral'
            : 'attention';
        await expect(chip).toHaveClass(new RegExp(tone));
      }
      await expect(
        page.getByText(
          'The App cannot change repository rulesets without Administration permission. An administrator must turn on the required check in the repository settings.',
        ),
      ).toBeVisible();
      await expect(
        page.getByText(
          'This step turns green once archfit/gate is required and the applicable rules below are checked.',
        ),
      ).toBeVisible();
      await expect(page.getByText('The owner signs the policy with a passkey. Change one byte and the approval no longer counts.')).toBeVisible();
      if (body.setup.open_pull > 0) {
        await expect(page.getByRole('listitem').nth(1).getByText('In review', { exact: true })).toBeVisible();
        await expect(page.getByText(`PR #${body.setup.open_pull} adds the workflow — review it.`)).toBeVisible();
        await expect(page.getByRole('link', { name: `PR #${body.setup.open_pull}`, exact: true })).toHaveAttribute('href', path + '/pull/' + body.setup.open_pull);
        await expect(page.getByRole('link', { name: `Review PR #${body.setup.open_pull}`, exact: true })).toHaveAttribute('href', `https://github.com/acme/app/pull/${body.setup.open_pull}`);
      }
      if (body.setup.open_pull > 0) await expect(page.getByText('This opens GitHub to read the change; nothing needs to be created here.')).toBeVisible();
      if (body.setup.first_pull) {
        await expect(page.getByRole('listitem').nth(1).getByText('Done', { exact: true })).toBeVisible();
        await expect(page.getByText(`First policy: #${body.setup.first_pull}, open and waiting for approval. The workflow is present in this PR.`)).toBeVisible();
      }
      if (body.setup.owner_action?.href) {
        const review = page.getByRole('link', { name: body.setup.owner_action.label, exact: true });
        await expect(review).toHaveAttribute('href', body.setup.owner_action.href);
        await expect(review).toHaveClass(/primary/);
        await expect(review.locator('..').getByText('This needs your passkey.', { exact: true })).toBeVisible();
      }
      if (!body.setup.workflow && body.setup.open_pull === 0 && !body.setup.first_pull)
        await expect(page.getByText('The pull request should also add .github/workflows/archfit.yaml.')).toBeVisible();
      if (body.setup.open_pull > 0 || body.setup.first_pull)
        await expect(page.getByText('The pull request should also add .github/workflows/archfit.yaml.')).toHaveCount(0);
      await expect(page.getByText('ok', { exact: true })).toHaveCount(0);
      await expect(page.getByText('unknown', { exact: true })).toHaveCount(0);
      if (body.setup.rules.length)
        await expect(page.getByRole('link', { name: 'Open ruleset settings' })).toHaveAttribute('href', 'https://github.com/acme/app/settings/rules');
      if (name === 'workflow-review') await docsShot(page, 'home-setup', setupApprovedCopy);
    } else {
      for (const need of body.needs_you)
        await expect(page.getByRole('link', { name: need.text, exact: true })).toHaveAttribute('href', need.href);
    }
    await expectPageText(page, 'home-' + name);
    if (name === 'one-request') await docsShot(page, 'home');
    if (body.map) {
      await page.getByText('Show the map as a list', { exact: true }).focus();
      await page.keyboard.press('Enter');
      for (const m of body.map.modules)
        await expect(page.locator('details li').filter({ hasText: m.name + ' · Volatility:' })).toBeVisible();
      await expectNoNotUsedWord(page, ['Review workflow change']);
    }
    await expectClean(page, body.setup ? setupApprovedCopy : ['Review workflow change']);
    expect(reads).toEqual(['/api/repos/acme/app/home']);
    expect(writes).toEqual([]);
  });
}

test('home: request opens its page and the record folds away', async ({ page }) => {
  await open(page, path, { [api]: () => [200, fixture('one-request')] });
  await expect(page.getByText('cmd/run.go:12:', { exact: false })).toBeHidden();
  await page.getByText('Show the record', { exact: true }).click();
  await expect(page.locator('pre')).toContainText('balance: 9/10 (min 7)');
  await expectNoNotUsedWord(page, ['Review workflow change']);
  await page.route('**/api/repos/acme/app/requests/431', (r) =>
    r.fulfill({ status: 404, json: { error: 'Not found.' } }),
  );
  await page.getByRole('link', { name: 'Request #431 waits.' }).click();
  await expect(page).toHaveURL(/\/requests\/431$/);
});

test('home: worker failure says so, and the list still works', async ({ page }) => {
  await page.route('**/assets/layout.worker-*.js', (r) => r.abort());
  await open(page, path, { [api]: () => [200, fixture('one-request')] });
  await expect(page.getByRole('alert')).toContainText('The map layout worker could not place the modules.');
  await expect(page.getByRole('img', { name: 'Modules by layer and their dependencies' })).toHaveCount(0);
  await page.getByText('Show the map as a list', { exact: true }).click();
  await expect(page.getByText('app → other: Requested dependency · #431', { exact: true })).toBeVisible();
  await expectPageText(page, 'home-worker-error');
  await expectClean(page);
});

for (const [name, response] of [
  ['api-error', { status: 502, body: { error: 'The Home answer could not be read.' } }],
  ['malformed', { status: 200, body: { status: 'clean', map: {} } }],
] as const) {
  test(`home: ${name} is not an empty map`, async ({ page }) => {
    await open(page, path, { [api]: () => [response.status, response.body] });
    await expect(page.getByRole('alert')).toContainText('The Home answer could not be read.');
    await expect(page.getByRole('heading', { name: 'Needs you' })).toHaveCount(0);
    await expectPageText(page, 'home-' + name);
    await expectClean(page);
  });
}

test('home: empty map is explicit', async ({ page }) => {
  const body = { ...fixture('clean'), map: { layers: [], modules: [], lines: [] } };
  await open(page, path, { [api]: () => [200, body] });
  await expect(page.getByText('No modules.', { exact: true })).toBeVisible();
  await expectPageText(page, 'home-empty');
  await expectClean(page);
});

test('home: phone map starts as a list, the diagram fits, and bands stay in order', async ({ page }) => {
  await page.setViewportSize({ width: 390, height: 844 });
  await open(page, path, { [api]: () => [200, fixture('one-request')] });
  const diagram = page.getByRole('img', { name: 'Modules by layer and their dependencies' });
  await expect(page.getByText('app → other: Requested dependency · #431', { exact: true })).toBeVisible();
  await expect(diagram).toBeHidden();
  await page.getByText('Show the map as a diagram', { exact: true }).click();
  await expect(diagram).toBeVisible();
  expect(await diagram.evaluate((el) => el.getBoundingClientRect().width)).toBeLessThan(390);
  expect(await page.evaluate(() => document.documentElement.scrollWidth)).toBeLessThanOrEqual(390);
  const ys = await page.locator('.map-node rect').evaluateAll((nodes) => nodes.map((n) => Number(n.getAttribute('y'))));
  expect(ys[0]).toBeLessThan(ys[1]);
  expect(ys[1]).toBeLessThan(ys[2]);
});

test('home: SVG keeps request/date labels and only one kind per pair', async ({ page }) => {
  for (const [name, label, kind] of [
    ['one-request', 'Requested dependency · #431', 'requested'],
    ['exception-nine-days', 'Exception · 2026-11-19', 'exception'],
    ['deny-request', 'A rule denies this dependency · no-app-to-other', 'deny'],
    ['drift', 'Drift · graph', 'drift'],
  ]) {
    await page.unrouteAll();
    await open(page, path, { [api]: () => [200, fixture(name)] });
    await expect(page.locator('.map-edge.' + kind)).toContainText(label);
    await expect(page.locator('.map-edge')).toHaveCount(2);
    await expect(page.locator('.map-edge.allowed')).toHaveCount(1);
    await expectNoNotUsedWord(page, ['Review workflow change']);
  }
});

test('home: unknown and cross-repository links stay text, and API text is escaped', async ({ page }) => {
  const body = {
    ...fixture('clean'),
    needs_you: [
      { kind: 'request', text: '<img src=x onerror=alert(1)>', href: 'javascript:alert(1)' },
      { kind: 'request', text: 'Another repository', href: '/r/elsewhere/app/requests/431' },
      { kind: 'request', text: 'Unknown page', href: '/r/acme/app/elsewhere' },
    ],
  };
  await open(page, path, { [api]: () => [200, body] });
  await expect(page.getByText('<img src=x onerror=alert(1)>', { exact: true })).toBeVisible();
  await expect(page.getByRole('link', { name: 'Another repository' })).toHaveCount(0);
  await expect(page.getByRole('link', { name: 'Unknown page' })).toHaveCount(0);
  await expect(page.locator('main img')).toHaveCount(0);
  await expectClean(page);
});

for (const first of [true, false]) {
  test(`home: ${first ? 'first policy' : 'workflow'} review does not require an existing passkey`, async ({ page }) => {
    const body = fixture(first ? 'not-set-up' : 'workflow-review');
    body.setup.passkey = false;
    await open(page, path, { [api]: () => [200, body] });
    const link = page.getByRole('link', { name: body.setup.owner_action.label, exact: true });
    await expect(link).toHaveAttribute('href', body.setup.owner_action.href);
    await expect(link).not.toHaveAttribute('aria-disabled');
    await expect(page.getByRole('link', { name: 'Register a passkey' })).toBeVisible();
    await link.focus();
    await expect(link).toBeFocused();
    await expectClean(page, setupApprovedCopy);
  });
}

test('home: ineligible viewer gets a role handoff without an owner-action link', async ({ page }) => {
  const body = fixture('not-set-up');
  body.setup.owner_action = { label: 'A repository administrator named as an architecture owner in the proposed policy can review this.' };
  await open(page, path, { [api]: () => [200, body] });
  await expect(page.getByText(body.setup.owner_action.label, { exact: true })).toBeVisible();
  await expect(page.locator('main a[href*="/owner/"]')).toHaveCount(0);
  await expect(page.getByText('This needs your passkey.', { exact: true })).toHaveCount(0);
  await expectPageText(page, 'home-first-policy-viewer');
  await expectClean(page, [...setupApprovedCopy, body.setup.owner_action.label]);
});

test('home: an unavailable rule does not block setup completion, an unknown rule does', async ({ page }) => {
  const body = fixture('not-set-up');
  body.setup.rules = [{ code: 'gate_check', state: 'ok', text: '' }, { code: 'policy_push', state: 'not_applicable', text: '' }];
  await open(page, path, { [api]: () => [200, body] });
  await expect(page.getByLabel('3 of 4 steps done')).toBeVisible();
  await expect(page.getByRole('listitem').nth(3).getByText('Action needed', { exact: true })).toHaveCount(0);
  await expect(page.getByText('Not available', { exact: true })).toBeVisible();
  body.setup.rules.push({ code: 'administrator_bypass', state: 'unknown', text: '' });
  await page.reload();
  await expect(page.getByLabel('2 of 4 steps done')).toBeVisible();
  await expect(page.getByText('Check with an admin', { exact: true })).toBeVisible();
  await expectClean(page, setupApprovedCopy);
});

for (const [name, setup, next] of [
  ['missing-policy', { policy: false, open_pull: 0, owner_action: undefined }, 'Next: add the policy'],
  ['review', {}, 'Next: review the pull request'],
  ['approval', { first_pull: 12, open_pull: 0 }, 'Next: approve the workflow with your passkey'],
  ['required-check', { first_pull: 12, open_pull: 0, state: 'approved', owner_action: undefined }, 'Next: turn on the required check'],
  ['complete', { first_pull: 12, open_pull: 0, state: 'approved', owner_action: undefined, rules: [{ code: 'gate_check', state: 'ok', text: '' }] }, 'Setup is complete.'],
] as const) {
  test('home: setup next action for ' + name, async ({ page }) => {
    const body = fixture('workflow-review');
    Object.assign(body.setup, setup);
    await open(page, path, { [api]: () => [200, body] });
    await expect(page.locator('.setup .box-head p').first()).toHaveText(next);
  });
}

test('home: setup chips use semantic colours in both themes', async ({ page }) => {
  const body = fixture('workflow-review');
  body.setup.rules.push({ code: 'administrator_bypass', state: 'unknown', text: '' });
  await open(page, path, { [api]: () => [200, body] });
  for (const [theme, good, attention, neutral] of [
    ['light', 'rgb(218, 251, 225)', 'rgb(255, 248, 197)', 'rgb(239, 242, 245)'],
    ['dark', 'rgba(46, 160, 67, 0.15)', 'rgba(187, 128, 9, 0.15)', 'rgba(101, 108, 118, 0.2)'],
  ] as const) {
    await page.emulateMedia({ colorScheme: theme });
    await expect(page.locator('.setup .chip').filter({ hasText: /^Done$/ })).toHaveCSS('background-color', good);
    await expect(page.locator('.setup .chip').filter({ hasText: /^Review needed$/ })).toHaveCSS('background-color', attention);
    await expect(page.locator('.setup .chip').filter({ hasText: /^Check$/ })).toHaveCSS('background-color', attention);
    await expect(page.locator('.setup .chip').filter({ hasText: /^In review$/ })).toHaveCSS('background-color', neutral);
  }
  delete body.setup.owner_action;
  await page.reload();
  await expect(page.locator('.setup .chip').filter({ hasText: /^Waiting$/ })).toHaveClass(/neutral/);
});

import { readFileSync, readdirSync } from 'node:fs';
import { expect, test } from '@playwright/test';
import { expectClean, expectNoNotUsedWord, expectPageText, open, docsClock, docsShot } from './support';
import { homeAction, homeStateLabel, readHome, ownerReviewLabel } from '../src/lib/home';

const path = '/r/acme/app';
const api = 'GET /api/repos/acme/app/home';
const fixtures = new URL('../../internal/control/ui/testdata/home/', import.meta.url);
const names = readdirSync(fixtures)
  .filter((n) => n.endsWith('.json'))
  .sort();
const fixture = (name: string) => JSON.parse(readFileSync(new URL(name + '.json', fixtures), 'utf8'));
const setupApprovedCopy = [
  'Policy and review status',
  'Rules reach the default branch through review, so the approval binds to exact file contents.',
  'Require the gate',
  'Ready for review',
  'A password or a GitHub review can be replayed by a compromised account',
  'Review workflow change',
  'Review first policy',
  'Review needed',
  'A passkey is needed to approve, but not to read the review.',
  'Next: review owner approval',
  'Review and approve',
  'Review proposed files',
  'Review the proposed files before approving.',
  'PR #203 changed after approval. Review the new head.',
  'Could not read your passkeys. Review is still available; try again before approving.',
];

for (const file of names) {
  const name = file.replace(/\.json$/, '');
  test(`home: ${name} golden${name === 'one-request' || name === 'workflow-review' ? ' @docs' : ''}`, async ({
    page,
  }) => {
    await docsClock(page);
    const json = fixture(name);
    const body = readHome(json);
    const reads: string[] = [];
    page.on('request', (r) => {
      if (r.url().includes('/api/repos/')) reads.push(new URL(r.url()).pathname);
    });
    const writes = await open(page, path, { [api]: () => [200, json] });
    await expect(page.getByRole('heading', { level: 1, name: 'acme/app', exact: true })).toBeVisible();
    await expect(page.locator('.page-head .status-label')).toHaveText(homeStateLabel(body.state));
    await expect(page.locator('.page-head .status-line p')).toHaveText(body.status);
    await expect(page.getByText('Continue setup')).toHaveCount(0);
    const action = homeAction(body, { owner: 'acme', name: 'app', fullName: 'acme/app' });
    const head = page.locator('.page-head');
    if (action) {
      const primary = head.locator('.btn.primary');
      await expect(primary).toHaveCount(1);
      await expect(primary).toContainText(action.label);
      await expect(primary).toHaveAttribute('href', action.href);
    } else await expect(head.locator('.btn.primary')).toHaveCount(0);
    await expect(head.getByRole('link', { name: 'View policy' })).toHaveCount(!body.setup || body.setup.policy ? 1 : 0);
    await expect(page.getByRole('navigation', { name: 'Repository' }).getByRole('link')).toHaveText(['Home', 'Policy', 'Passkeys']);
    await expect(page.getByRole('link', { name: 'Home', exact: true })).toHaveAttribute('aria-current', 'page');
    if (body.map) await expect(page.getByRole('table', { name: 'Policy and review status matrix' })).toBeVisible();
    if (body.setup) {
      if (!['approved', 'outside_flow'].includes(body.setup.state)) {
        await expect(page.getByRole('heading', { name: 'The map' })).toHaveCount(0);
      }
      if (body.setup.passkey) {
        await expect(page.getByRole('link', { name: 'Manage passkeys' })).toHaveAttribute('href', path + '/passkeys');
        await expect(page.getByRole('link', { name: 'Manage passkeys' })).not.toHaveClass(/btn/);
        await expect(page.getByRole('link', { name: 'Register a passkey to approve' })).toHaveCount(0);
      } else {
        await expect(page.getByRole('link', { name: 'Register a passkey to approve' })).toHaveAttribute(
          'href',
          path + '/passkeys',
        );
      }
      await expect(page.getByRole('heading', { name: 'Set up Archfit' })).toBeVisible();
      await expect(page.getByRole('heading', { name: '3 · Owner approval', exact: true })).toBeVisible();
      const approval = body.setup.approval;
      const gateStep = page.locator('.steps > li').nth(3);
      if (name.startsWith('approval-') || name === 'workflow-review') {
        await expect(gateStep).toHaveClass(/done/);
        await expect(gateStep.locator(':scope > .chip')).toHaveText('On');
        await expect(page.getByText('Next: turn on the required check', { exact: true })).toHaveCount(0);
      }
      if (name === 'not-set-up' || name === 'rules-incomplete') {
        await expect(gateStep).not.toHaveClass(/done/);
        await expect(gateStep.locator(':scope > .chip')).toHaveText('Action needed');
      }
      if (approval.status === 'approved_pending_merge' || approval.status === 'unknown') {
        await expect(page.locator('main a[href*="/owner/"]')).toHaveCount(0);
        await expect(page.getByRole('link', { name: 'Review and approve', exact: true })).toHaveCount(0);
      }
      if (approval.status === 'approved_pending_merge') {
        await expect(page.locator('.steps > li').nth(2)).toHaveClass(/done/);
        await expect(
          page.getByText(
            approval.merged
              ? 'Approval recorded; checking activation'
              : `Approved for PR #${approval.pull_request}. Waiting for merge.`,
            { exact: true },
          ),
        ).toBeVisible();
      }
      if (approval.status === 'stale') {
        await expect(
          page.getByText(`PR #${approval.pull_request} changed after approval. Review the new head.`, { exact: true }),
        ).toBeVisible();
        await expect(page.locator('.steps > li').nth(2)).not.toHaveClass(/done/);
      }
      if (approval.status === 'unknown') await expect(page.locator('.steps > li').nth(2)).not.toHaveClass(/done/);
      await expect(page.locator('.steps > li')).toHaveCount(4);
      const chips = page.locator('.setup .chip');
      for (const chip of await chips.all()) {
        const status = await chip.innerText();
        const tone = ['Done', 'On', 'Signed', 'Ready for review'].includes(status)
          ? 'good'
          : ['Waiting', 'Does not apply'].includes(status)
            ? 'neutral'
            : 'attention';
        await expect(chip).toHaveClass(new RegExp(tone));
      }
      await expect(
        page.getByText(
          'In GitHub, require archfit/gate on the default branch. The App reads these settings; an administrator changes them.',
        ),
      ).toBeVisible();
      await expect(
        page.getByText(
          'This step turns green when GitHub requires pull requests and archfit/gate on the default branch. Bypass and policy-push advice does not block this step; Verify yourself means the App has not checked that setting.',
        ),
      ).toBeVisible();
      await expect(
        page.getByText('The owner signs the policy with a passkey. Change one byte and the approval no longer counts.'),
      ).toBeVisible();
      if (body.setup.open_pull > 0) {
        await expect(page.getByRole('listitem').nth(1).getByText('Ready for review', { exact: true })).toBeVisible();
        await expect(page.getByText(`PR #${body.setup.open_pull} adds the workflow.`)).toBeVisible();
        await expect(page.getByRole('link', { name: `PR #${body.setup.open_pull}`, exact: true })).toHaveAttribute(
          'href',
          path + '/pull/' + body.setup.open_pull,
        );
        await expect(
          page.getByRole('link', { name: `View PR #${body.setup.open_pull} on GitHub`, exact: true }),
        ).toHaveAttribute('href', `https://github.com/acme/app/pull/${body.setup.open_pull}`);
      }
      if (body.setup.first_pull) {
        await expect(page.getByRole('listitem').nth(1).getByText('Ready for review', { exact: true })).toBeVisible();
        await expect(
          page.getByText(`PR #${body.setup.first_pull} proposes the first policy and workflow.`),
        ).toBeVisible();
      }
      if (body.setup.owner_action?.href) {
        const review = page.getByRole('link', { name: ownerReviewLabel(body.setup), exact: true });
        await expect(review).toHaveAttribute('href', body.setup.owner_action.href);
        await expect(review).toHaveClass(/primary/);
        await expect(review).toHaveAccessibleDescription('Approval needs your passkey.');
      }
      if (!body.setup.workflow && body.setup.open_pull === 0 && !body.setup.first_pull)
        await expect(page.getByText('The pull request should also add .github/workflows/archfit.yaml.')).toBeVisible();
      if (body.setup.open_pull > 0 || body.setup.first_pull)
        await expect(page.getByText('The pull request should also add .github/workflows/archfit.yaml.')).toHaveCount(0);
      await expect(page.getByText('ok', { exact: true })).toHaveCount(0);
      await expect(page.getByText('unknown', { exact: true })).toHaveCount(0);
      if (body.setup.rules.length)
        await expect(page.getByRole('link', { name: 'Open ruleset settings' })).toHaveAttribute(
          'href',
          'https://github.com/acme/app/settings/rules',
        );
      if (name === 'workflow-review') await docsShot(page, 'home-setup', setupApprovedCopy);
    } else {
      await expect(page.getByRole('link', { name: 'See what needs you' })).toHaveCount(0);
    }
    await expect(page.getByRole('heading', { name: 'Needs you' })).toHaveCount(body.needs_you.length ? 1 : 0);
    if (!body.setup) {
      for (const need of body.needs_you)
        await expect(page.getByRole('link', { name: need.text })).toHaveAttribute('href', need.href);
    }
    // HTML tables add trailing cell separators to innerText; keep text goldens whitespace-clean.
    if (body.map)
      expect((await page.locator('main').innerText()).replace(/[ \t]+$/gm, '').trimEnd()).toMatchSnapshot(
        'home-' + name + '.txt',
      );
    else await expectPageText(page, 'home-' + name);
    if (name === 'one-request') await docsShot(page, 'home', ['Policy and review status']);
    if (body.map) {
      for (const m of body.map.modules)
        await expect(page.getByRole('rowheader').filter({ hasText: m.name })).toBeVisible();
      await expectNoNotUsedWord(
        page,
        body.setup ? setupApprovedCopy : ['Review workflow change', 'Policy and review status'],
      );
    }
    await expectClean(page, body.setup ? setupApprovedCopy : ['Review workflow change', 'Policy and review status']);
    expect(reads).toEqual(['/api/repos/acme/app/home']);
    expect(writes).toEqual([]);
  });
}

test('home: request opens its page and the record folds away', async ({ page }) => {
  await open(page, path, { [api]: () => [200, fixture('one-request')] });
  await expect(page.getByText('cmd/run.go:12:', { exact: false })).toBeHidden();
  await page.getByText('Show the record', { exact: true }).click();
  await expect(page.locator('pre')).toContainText('balance: 9/10 (min 7)');
  await expectNoNotUsedWord(page, ['Review workflow change', 'Policy and review status']);
  await page.route('**/api/repos/acme/app/requests/431', (r) =>
    r.fulfill({ status: 404, json: { error: 'Not found.' } }),
  );
  await page.getByRole('link', { name: 'Request #431 waits.' }).click();
  await expect(page).toHaveURL(/\/requests\/431$/);
});

test('home: worker failure says so, and Matrix still works', async ({ page }) => {
  await page.route('**/assets/layout.worker-*.js', (r) => r.abort());
  await open(page, path, { [api]: () => [200, fixture('one-request')] });
  await page.getByRole('radio', { name: 'Diagram', exact: true }).click();
  await expect(page.getByRole('alert')).toContainText('The map layout worker could not place the modules.');
  await expect(page.getByRole('group', { name: 'Modules by layer and their dependencies' })).toHaveCount(0);
  await expectPageText(page, 'home-worker-error');
  await page.getByRole('radio', { name: 'Matrix', exact: true }).click();
  await page.getByRole('button', { name: /^app to other:/ }).click();
  await expect(page.getByRole('link', { name: 'Open request #431' })).toHaveAttribute(
    'href',
    '/r/acme/app/requests/431',
  );
  await page.getByRole('button', { name: 'Close details' }).click();
  await expectClean(page, ['Policy and review status']);
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
  await expectClean(page, ['Policy and review status']);
});

test('home: phone map starts as a matrix, the diagram fits, and bands stay in order', async ({ page }) => {
  const workers: string[] = [];
  page.on('request', (request) => {
    if (/\/assets\/layout\.worker-.*\.js/.test(request.url())) workers.push(request.url());
  });

  await page.setViewportSize({ width: 390, height: 844 });
  await open(page, path, { [api]: () => [200, fixture('one-request')] });
  const diagram = page.getByRole('group', { name: 'Modules by layer and their dependencies' });
  await expect(page.getByRole('table', { name: 'Policy and review status matrix' })).toBeVisible();
  await expect(diagram).toBeHidden();
  expect(workers).toEqual([]);
  const workerRequest = page.waitForRequest(/\/assets\/layout\.worker-.*\.js/);
  await page.getByRole('radio', { name: 'Diagram', exact: true }).click();
  await workerRequest;
  await expect(page.getByText('Show the map as a diagram', { exact: true })).toHaveCount(0);
  await expect(diagram).toBeVisible();
  expect(workers).toHaveLength(1);
  expect(await diagram.evaluate((el) => el.getBoundingClientRect().width)).toBeLessThan(390);
  expect(await page.evaluate(() => document.documentElement.scrollWidth)).toBeLessThanOrEqual(390);
  const ys = await page.locator('.map-node rect').evaluateAll((nodes) => nodes.map((n) => Number(n.getAttribute('y'))));
  expect(ys[0]).toBeLessThan(ys[1]);
  expect(ys[1]).toBeLessThan(ys[2]);
});

test('home: SVG names both ends of a line when its module is in focus, and draws one kind per pair', async ({ page }) => {
  for (const [name, label, kind] of [
    ['one-request', 'app → other · requested #431', 'requested'],
    ['exception-nine-days', 'app → other · exception until 2026-11-19', 'exception'],
    ['deny-request', 'app → other · a rule denies it · no-app-to-other', 'deny'],
    ['drift', 'app → other · drift · graph', 'drift'],
  ]) {
    await page.unrouteAll();
    await open(page, path, { [api]: () => [200, fixture(name)] });
    await page.getByRole('radio', { name: 'Diagram', exact: true }).click();
    await expect(page.locator('.map-link')).toHaveCount(2);
    await expect(page.locator('.map-edge')).toHaveCount(0);
    await page.locator('[data-module="app"]').click();
    await expect(page.locator('.map-edge')).toHaveCount(2);
    await expect(page.locator('.map-label.' + kind)).toHaveText(label);
    await expect(page.locator('.map-label.allowed')).toHaveText('app → util · allowed');
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
  await expectClean(page, ['Policy and review status']);
});

for (const first of [true, false]) {
  test(`home: ${first ? 'first policy' : 'workflow'} review does not require an existing passkey`, async ({ page }) => {
    const body = fixture(first ? 'not-set-up' : 'workflow-review');
    body.setup.passkey = false;
    await open(page, path, { [api]: () => [200, body] });
    const link = page.getByRole('link', { name: ownerReviewLabel(body.setup), exact: true });
    await expect(link).toHaveAttribute('href', body.setup.owner_action.href);
    await expect(link).not.toHaveAttribute('aria-disabled');
    await expect(page.getByRole('link', { name: 'Register a passkey to approve' })).toBeVisible();
    await link.focus();
    await expect(link).toBeFocused();
    await expectClean(page, setupApprovedCopy);
  });
}

test('home: ineligible viewer gets a role handoff without an owner-action link', async ({ page }) => {
  const body = fixture('not-set-up');
  body.setup.rules = [
    { code: 'pull_requests', state: 'ok', text: '' },
    { code: 'gate_check', state: 'ok', text: '' },
  ];
  body.setup.owner_action = {
    label: 'A repository administrator named as an architecture owner in the proposed policy can review this.',
  };
  await open(page, path, { [api]: () => [200, body] });
  await expect(page.getByText(body.setup.owner_action.label, { exact: true })).toBeVisible();
  await expect(page.locator('main a[href*="/owner/"]')).toHaveCount(0);
  await expect(page.getByText('Approval needs your passkey.', { exact: true })).toHaveCount(0);
  const gateStep = page.locator('.steps > li').nth(3);
  await expect(gateStep).toHaveClass(/done/);
  await expect(gateStep.locator(':scope > .chip')).toHaveText('On');
  await expectPageText(page, 'home-first-policy-viewer');
  await expectClean(page, [...setupApprovedCopy, body.setup.owner_action.label]);
});

test('home: advisory findings do not block enforcement, unreadable rulesets do', async ({ page }) => {
  const body = fixture('not-set-up');
  body.setup.rules = [
    { code: 'pull_requests', state: 'ok', text: '' },
    { code: 'gate_check', state: 'ok', text: '' },
    { code: 'policy_push', state: 'not_applicable', text: '' },
  ];
  await open(page, path, { [api]: () => [200, body] });
  await expect(page.getByLabel('3 of 4 steps done')).toBeVisible();
  await expect(page.getByRole('listitem').nth(3).getByText('Action needed', { exact: true })).toHaveCount(0);
  await expect(page.getByText('Does not apply', { exact: true })).toBeVisible();
  body.setup.rules.push({ code: 'administrator_bypass', state: 'unknown', text: '' });
  await page.getByRole('button', { name: 'Check', exact: true }).click();
  await expect(page.getByLabel('3 of 4 steps done')).toBeVisible();
  await expect(page.getByText('Verify yourself', { exact: true })).toBeVisible();
  body.setup.rules.push({ code: 'rulesets', state: 'unknown', text: '' });
  await page.getByRole('button', { name: 'Check', exact: true }).click();
  await expect(page.getByLabel('2 of 4 steps done')).toBeVisible();
  await expect(page.getByText('Check with an admin', { exact: true })).toBeVisible();
  await expectClean(page, setupApprovedCopy);
});

for (const [name, setup, next] of [
  ['missing-policy', { policy: false, open_pull: 0, owner_action: undefined }, 'Next: add the policy'],
  ['create', { open_pull: 0, owner_action: undefined }, 'Next: create the pull request'],
  ['existing-workflow-pr', {}, 'Next: review owner approval'],
  ['approval', { first_pull: 12, open_pull: 0 }, 'Next: review owner approval'],
  [
    'required-check',
    {
      first_pull: 12,
      open_pull: 0,
      state: 'approved',
      approval: { status: 'active', kind: 'first' },
      owner_action: undefined,
    },
    'Next: turn on the required check',
  ],
  [
    'complete',
    {
      first_pull: 12,
      open_pull: 0,
      state: 'approved',
      approval: { status: 'active', kind: 'first' },
      owner_action: undefined,
      rules: [
        { code: 'pull_requests', state: 'ok', text: '' },
        { code: 'gate_check', state: 'ok', text: '' },
      ],
    },
    'Setup is complete.',
  ],
] as const) {
  test('home: setup next action for ' + name, async ({ page }) => {
    const body = fixture('workflow-review');
    Object.assign(body.setup, setup);
    if (name === 'required-check') body.setup.rules = fixture('rules-incomplete').setup.rules;
    await open(page, path, { [api]: () => [200, body] });
    await expect(page.locator('.setup .box-head p').first()).toHaveText(next);
  });
}

for (const [name, setup, chip, completed, copy, action, href] of [
  [
    'workflow PR',
    { policy: true, workflow: false, open_pull: 203 },
    'Ready for review',
    2,
    'PR #203 adds the workflow.',
    'View PR #203 on GitHub',
    'https://github.com/acme/app/pull/203',
  ],
  [
    'first-policy PR',
    { policy: false, workflow: false, open_pull: 0, first_pull: 12 },
    'Ready for review',
    2,
    'PR #12 proposes the first policy and workflow.',
    'View PR #12',
    '/r/acme/app/pull/12',
  ],
  [
    'default-branch files',
    { policy: true, workflow: true, open_pull: 0 },
    'Done',
    2,
    'The policy and workflow are present on the default branch. No setup pull request is needed.',
    'View policy',
    '/r/acme/app/policy',
  ],
  [
    'missing workflow',
    { policy: true, workflow: false, open_pull: 0 },
    'Action needed',
    1,
    'Open one pull request that adds the missing policy or workflow.',
    'Create a pull request on GitHub',
    'https://github.com/acme/app/compare',
  ],
] as const) {
  test('home: pull-request stage describes ' + name, async ({ page }) => {
    const body = fixture('workflow-review');
    Object.assign(body.setup, setup, { owner_action: undefined, rules: fixture('rules-incomplete').setup.rules });
    await open(page, path, { [api]: () => [200, body] });
    const step = page.locator('.setup .steps > li').nth(1);
    await expect(step.getByRole('heading')).toHaveText('2 · Policy and workflow pull request');
    await expect(step.locator('.chip')).toHaveText(chip);
    await expect(step.locator('.number')).toHaveText(completed === 2 ? '✓' : '2');
    if (completed === 2) await expect(step).toHaveClass(/done/);
    else await expect(step).not.toHaveClass(/done/);
    await expect(page.getByLabel(`${completed} of 4 steps done`)).toBeVisible();
    await expect(step.getByText(copy, { exact: true })).toBeVisible();
    // The page head carries the step's own action, so the step does not repeat it.
    const where = action === 'Create a pull request on GitHub' ? page.locator('.page-head') : step;
    await expect(where.getByRole('link', { name: action })).toHaveAttribute('href', href);
    if (action === 'Create a pull request on GitHub') await expect(step.getByRole('link', { name: action })).toHaveCount(0);
    if (completed === 2)
      await expect(page.getByRole('link', { name: 'Create a pull request on GitHub' })).toHaveCount(0);
    await expectClean(page, setupApprovedCopy);
  });
}

test('home: setup chips use semantic colours in both themes', async ({ page }) => {
  const body = fixture('workflow-review');
  body.setup.rules.push({ code: 'administrator_bypass', state: 'unknown', text: '' });
  await open(page, path, { [api]: () => [200, body] });
  for (const [theme, good, attention] of [
    ['light', 'rgb(218, 251, 225)', 'rgb(255, 248, 197)'],
    ['dark', 'rgba(46, 160, 67, 0.15)', 'rgba(187, 128, 9, 0.15)'],
  ] as const) {
    await page.emulateMedia({ colorScheme: theme });
    await expect(page.locator('.setup .chip').filter({ hasText: /^Done$/ })).toHaveCSS('background-color', good);
    await expect(page.locator('.setup .chip').filter({ hasText: /^Review needed$/ })).toHaveCSS(
      'background-color',
      attention,
    );
    await expect(page.locator('.setup .chip').filter({ hasText: /^Verify yourself$/ })).toHaveCSS(
      'background-color',
      attention,
    );
    await expect(page.locator('.setup .chip').filter({ hasText: /^Ready for review$/ })).toHaveCSS(
      'background-color',
      good,
    );
  }
  delete body.setup.owner_action;
  await page.reload();
  await expect(
    page
      .locator('.setup .chip')
      .filter({ hasText: /^Action needed$/ })
      .first(),
  ).toHaveClass(/attention/);
});

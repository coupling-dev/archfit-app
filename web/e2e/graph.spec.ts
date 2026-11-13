import { expect, test } from '@playwright/test';
import { view, repoGraph, pullGraph, open, expectClean } from './fixtures';


test('graph: the canvas, the inspector and the matrix', async ({ page }) => {
  await open(page, '/r/acme/app/graph');
  await expect(page.getByRole('heading', { name: 'Architecture', level: 1 })).toBeVisible();
  await expect(page.getByRole('link', { name: 'Architecture' })).toHaveAttribute('aria-current', 'page');
  await expect(page.locator('.g-node')).toHaveCount(4);
  await expect(page.locator('.g-edge')).toHaveCount(5);
  // width is integration strength; a dependency with no measured strength is dashed
  await expect(
    page.getByRole('button', { name: /^api to db, 12 imports, violation, intrusive coupling/ }).locator('.line'),
  ).toHaveAttribute('stroke-width', '5');
  await expect(page.locator('.g-edge.unmeasured')).toHaveCount(1);
  await expect(page.locator('.g-node.undeclared')).toHaveText('legacy');
  // what the report's policy declares, never a seam's volatility
  await expect(page.locator('.g-node', { hasText: 'db' })).toContainText('volatility high');
  await expect(page.locator('.g-node', { hasText: 'api' })).toContainText('volatility undeclared');
  await expect(page.locator('.g-node', { hasText: 'ui' })).toContainText('subdomain core');
  await expect(page.locator('#incomplete')).toBeVisible();
  await expect(page.getByRole('button', { name: /added by this PR/ })).toHaveCount(0);
  // the legend is one line: colours and line width; the rarer marks fold under More
  const legend = page.getByRole('group', { name: 'Legend' });
  await expect(legend).toContainText('line width = integration strength');
  await expect(legend.getByText('not in the policy')).toBeHidden();
  await legend.getByText('More').click();
  await expect(legend.getByText('not in the policy')).toBeVisible();
  await legend.getByText('More').click();
  const problems = page.getByRole('region', { name: /Problems/ });
  // one unit: a dependency is one module → another, so the cycle is two rows and the chip says the same number
  await expect(page.locator('#g-unit')).toHaveText('5 dependencies:');
  await expect(page.getByRole('button', { name: '2 with a violation' })).toBeVisible();
  await expect(problems.getByRole('heading', { name: /Problems/ })).toContainText('2');
  const active = problems.locator('.g-rows').first().getByRole('button');
  await expect(active).toHaveCount(2);
  await expect(active.first()).toContainText('cycle');
  // accepted debt is no problem: its own group, collapsed
  const acceptedGroup = problems.locator('details.g-accepted');
  await expect(acceptedGroup.locator('summary')).toContainText('Accepted earlier');
  await expect(acceptedGroup.locator('summary')).toContainText('1');
  await expect(acceptedGroup).not.toHaveAttribute('open', '');
  await expect(page.locator('.g-edge.accepted .line')).toHaveAttribute('stroke-width', '1.5');
  await expectClean(page);

  await acceptedGroup.locator('summary').click();
  await acceptedGroup.getByRole('button').click();
  const accepted = page.getByRole('complementary');
  await expect(accepted.locator('.g-tag')).toHaveText('accepted advisory');
  await expect(accepted).toContainText('high (cascade)');

  await active.first().click();
  const inspector = page.getByRole('complementary');
  await expect(inspector.getByRole('heading', { name: 'Dependency' })).toBeVisible();
  await expect(inspector).toContainText('intrusive coupling');
  await expect(inspector).toContainText('imbalanced · medium');
  await expect(inspector).toContainText(
    'Flagged: intrusive coupling across the module boundary at high volatility (container system).',
  );
  await expect(inspector).toContainText('A rule violation covers this pair: follow its Fix.');
  await expect(inspector).toContainText('tight: strong coupling across a boundary');
  await expect(inspector).toContainText('the module boundary, within system');
  await expect(page.locator('#flagged')).toHaveText(
    '1 dependency couples strongly across a module boundary at high volatility.',
  );
  await expect(inspector).toContainText('api and db depend on each other.');
  await expect(inspector.locator('.g-finding')).toContainText('no-module-cycles · high');
  await expect(inspector).toContainText('Other direction · a cycle');
  await expect(inspector.getByRole('link', { name: /Open api in the policy/ })).toHaveAttribute(
    'href',
    'https://github.com/acme/app/blob/main/.archfit.yaml#L3',
  );

  // Selecting a dependency re-renders the nodes it dims: focus until it holds, or Enter reaches the problem row.
  const db = page.locator('.g-node', { hasText: 'db' });
  await expect(async () => {
    await db.focus();
    await expect(db).toBeFocused({ timeout: 200 });
  }).toPass();
  await page.keyboard.press('Enter');
  await expect(inspector.getByRole('heading', { name: 'Module' })).toBeVisible();
  await expect(inspector).toContainText('volatility high');
  await expect(inspector.getByRole('link', { name: 'Open in the policy' })).toHaveAttribute(
    'href',
    'https://github.com/acme/app/blob/main/.archfit.yaml#L9',
  );

  await page.getByRole('button', { name: 'Matrix' }).click();
  await expect(page.getByRole('button', { name: 'api to db, 12 imports, violation' })).toBeVisible();
  await page.getByRole('button', { name: '2 with a violation' }).click();
  await expect(page.getByRole('button', { name: 'api to db, 12 imports, violation' })).toHaveCount(0);
  await expectClean(page);

  // a module the report's own policy does not name: no claim that the policy is stale
  await page.getByRole('button', { name: 'Graph', exact: true }).click();
  await page.locator('.g-node', { hasText: 'legacy' }).click();
  await expect(inspector).toContainText('nothing: not in the policy');
  await expect(inspector).not.toContainText('not the one the report used');

  await page.getByText('Modules and dependencies as tables').click();
  await expect(page.getByRole('table', { name: 'Dependencies' })).toContainText('not measured');
});


test('graph: the incomplete banner shows only when the report says it is', async ({ page }) => {
  await open(page, '/r/acme/app/graph', { graph: { ...repoGraph, view: { ...view, incomplete: false } } });
  await expect(page.locator('.g-node')).toHaveCount(4);
  await expect(page.locator('#incomplete')).toHaveCount(0);
  await expect(page.getByRole('button', { name: '2 with a violation' })).toBeVisible(); // a failed gate is the violations
  // no banner, no empty block: an empty one would add a second gap to the page's flow
  const empty = await page
    .locator('main > *')
    .evaluateAll((els) => els.filter((e) => e.getBoundingClientRect().height === 0).length);
  expect(empty).toBe(0);
});


test('graph: a repository with only accepted debt has no problems and draws it faint', async ({ page }) => {
  const calm = {
    ...view,
    incomplete: false,
    links: view.links.filter((l) => l.status === 'accepted' || l.status === 'allowed'),
  };
  await open(page, '/r/acme/app/graph', { graph: { ...repoGraph, view: calm } });
  const problems = page.getByRole('region', { name: /Problems/ });
  await expect(problems.getByText('No violation or advisory is shown.')).toBeVisible();
  await expect(problems.locator('details.g-accepted summary')).toContainText('1');
  await expect(page.locator('.g-node.violation, .g-node.advisory, .g-node.accepted')).toHaveCount(0);
});


test('pull request graph: the header, and a halo only on what the PR added', async ({ page }) => {
  await open(page, '/r/acme/app/pull/4/graph');
  await expect(page.getByRole('heading', { name: 'feat: read options #4', level: 1 })).toBeVisible();
  await expect(page.getByRole('link', { name: 'Graph' })).toHaveAttribute('aria-current', 'page');
  await expect(page.getByRole('button', { name: '1 with introduced findings' })).toHaveAttribute(
    'aria-pressed',
    'true',
  );
  await expect(page.locator('.g-edge .halo')).toHaveCount(1);
  await expect(
    page.getByRole('button', { name: /^api to db, .*, contains introduced findings; source-site origin unknown$/ }),
  ).toBeVisible();
  await expect(page.getByRole('button', { name: /^db to api, .*violation, contract coupling$/ })).toBeVisible();
  await expectClean(page);

  await page.getByRole('button', { name: '1 with introduced findings' }).click();
  await expect(page.locator('.g-edge .halo')).toHaveCount(0);
  await page.locator('.g-node', { hasText: 'db' }).click();
  await expect(page.getByRole('complementary').getByRole('link', { name: 'Open in the policy' })).toHaveAttribute(
    'href',
    'https://github.com/acme/app/blob/a1b2c3d/.archfit.yaml#L9',
  );
});


test('graph: a policy link outside the App or github.com is not rendered', async ({ page }) => {
  await open(page, '/r/acme/app/graph', { graph: { ...repoGraph, policy_href: 'javascript:alert(1)//' } });
  await page.locator('.g-node', { hasText: 'db' }).click();
  await expect(page.getByRole('complementary').getByText('not in the policy')).toBeVisible();
  await expect(page.locator('a[href^="javascript:"]')).toHaveCount(0);
});


test('graph: no report says why', async ({ page }) => {
  await open(page, '/r/acme/app/graph', { graph: { ...repoGraph, view: null, unavailable: 'No report available.' } });
  await expect(page.getByText('No report available.')).toBeVisible();
  await expect(page.locator('.g-canvas')).toHaveCount(0);
  await expectClean(page);
});


test('pull request graph: a withdrawn result sends the reader to the next step, not to a re-run', async ({ page }) => {
  await open(page, '/r/acme/app/pull/4/graph', {
    pullGraph: {
      ...pullGraph,
      view: null,
      unavailable: 'This result asks for action before its findings mean anything.',
    },
  });
  await expect(page.getByText('This result asks for action before its findings mean anything.')).toBeVisible();
  await expect(page.getByText('Follow the next step in PR analysis above.')).toBeVisible();
  await expect(page.getByText('re-run the workflow')).toHaveCount(0);
  await expectClean(page);
});


test('graph: a layout worker that fails to load says so instead of laying out forever', async ({ page }) => {
  await page.route('**/assets/layout.worker-*.js', (route) => route.fulfill({ status: 404, body: 'gone' }));
  await open(page, '/r/acme/app/graph');
  await expect(page.getByRole('alert')).toHaveText('The layout failed: the layout worker stopped');
  await expect(page.locator('.g-node')).toHaveCount(0);
});

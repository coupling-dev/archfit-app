import { readFileSync } from 'node:fs';
import AxeBuilder from '@axe-core/playwright';
import { expect, test, type Page, type Request } from '@playwright/test';

const csp = readFileSync(new URL('../csp.txt', import.meta.url), 'utf8').trim();
const session = { login: 'alice', csrf: 'csrf-token-1' };

const repos = {
  repos: [
    { full_name: 'acme/app', state: 'healthy', tone: 'success', reason: 'No new findings' },
    { full_name: 'acme/web', state: 'violations found', tone: 'danger', reason: 'Architecture violations found' },
    { full_name: 'acme/new', state: 'no state yet', tone: 'neutral', reason: '' },
  ],
  truncated: false,
};

const steps = (current: number) =>
  ['Installed', 'Workflow merged', 'First report', 'Policy merged', 'Advisory', 'Required'].map((label, i) => ({
    label,
    done: i < current,
    current: i === current,
  }));

const reporting = {
  default_branch: 'main',
  main_state: 'attention',
  main_tone: 'attention',
  main_reason: 'Architecture needs attention',
  pulls: [
    {
      number: 4,
      title: 'feat: read options',
      state: 'healthy',
      tone: 'success',
      reason: 'No new findings',
      href: '/r/acme/app/pull/4',
    },
  ],
  pulls_more: 2,
  pulls_error: false,
  steps: steps(4),
  next: 'Rehearse the gate on a pull request.',
  stage: 'Gate is advisory',
  diagnostic: '',
  draft_url: '',
  draft_state: '',
  banner: 'The trial ends on 1 November 2026.',
  admin: true,
  can_start: false,
  can_discover: true,
  pulls_url: 'https://github.com/acme/app/pulls',
  checks_url: 'https://github.com/acme/app/commits/main',
};

const installed = {
  ...reporting,
  main_state: '',
  main_tone: 'neutral',
  main_reason: '',
  pulls: null,
  pulls_more: 0,
  steps: steps(0),
  next: 'Open the onboarding pull request.',
  stage: 'App installed',
  banner: '',
  can_start: true,
  can_discover: false,
};

const direction = (from: string, to: string, origin: string, n: number) => ({
  rule: 'no-module-cycles',
  rule_id: 'no-module-cycles',
  meta: 'high',
  from,
  to,
  origin,
  why: '',
  fix: '',
  alternatives: null,
  locations: Array.from({ length: n }, (_, i) => `internal/${from}/file${i}.go:${i + 3}`),
  strength: 'model',
  distance: 'across modules',
  volatility: 'high',
});

const pull = {
  number: 4,
  title: 'feat: read options',
  author: 'alice',
  state: 'open',
  head_ref: 'feat/options',
  base_ref: 'main',
  head_sha: 'a1b2c3d',
  url: 'https://github.com/acme/app/pull/4',
  checks_url: 'https://github.com/acme/app/pull/4/checks',
  graph_href: '/r/acme/app/pull/4/graph',
  result: 'violations found',
  tone: 'danger',
  headline: 'Architecture violations found · advisory',
  required: false,
  unavailable: '',
  run_id: 88,
  no_baseline: true,
  compared: true,
  violation_count: 3,
  cycles: [
    {
      modules: ['pkg_chaos', 'pkg_container'],
      rule: 'no-module-cycles',
      rule_id: 'no-module-cycles',
      meta: 'high',
      violation: true,
      why: 'Modules depend on each other.',
      fixes: ['Remove one direction.'],
      alternatives: null,
      directions: [direction('pkg_chaos', 'pkg_container', 'added', 2), direction('pkg_container', 'pkg_chaos', 'existing', 37)],
    },
  ],
  other: [
    {
      ...direction('api', 'db', 'added', 5),
      rule: 'Imbalanced coupling',
      rule_id: 'bc/imbalanced_coupling',
      why: 'API reads the database model.',
      fix: 'Go through the repository.',
    },
  ],
  advisory_count: 1,
  advisories: [{ ...direction('ui', 'store', '', 1), rule: 'layering', rule_id: 'layering', why: 'UI skips a layer.' }],
  accepted_count: 1,
  accepted: [{ ...direction('x', 'y', '', 1), meta: 'low, baseline' }],
  omitted: 0,
  dimensions: [{ key: 'change_locality', name: 'Change locality', status: 'partial', coverage: '3 / 4 modules', confidence: 'medium', gate: 'warn' }],
  unknowns: [{ dimension: 'Coupling', fact: 'coupling volatility', reason: 'volatility is undeclared' }],
  tasks: '[\n  {"id": "t1"}\n]',
  task_count: 1,
};

interface Mock {
  signedIn?: boolean;
  tokenRejected?: boolean; // the session stands, but GitHub rejects the user's token on the first repository call
  overview?: object;
  pull?: object;
}

// open answers /api in the browser from the mock, opens the page, and returns the writes the page sent. Each page records the CSP violations
// it sees, and every test fails on one.
async function open(page: Page, path: string, mock: Mock = {}) {
  const writes: Request[] = [];
  await page.route('**/api/**', async (route) => {
    const req = route.request();
    const url = new URL(req.url());
    const json = (status: number, body: object) => route.fulfill({ status, json: body });
    if (req.method() === 'POST') {
      writes.push(req);
      return json(200, { notice: 'The onboarding pull request is open.' });
    }
    if (mock.signedIn === false) return json(401, { error: 'Sign in to continue.' });
    if (mock.tokenRejected && url.pathname !== '/api/session') return json(401, { error: 'Sign in to continue.' });
    switch (url.pathname) {
      case '/api/session':
        return json(200, session);
      case '/api/repos':
        return json(200, repos);
      case '/api/repos/acme/app/overview':
        return json(200, mock.overview ?? reporting);
      case '/api/repos/acme/app/pulls/4':
        return json(200, mock.pull ?? pull);
    }
    return json(404, { error: 'Not found.' });
  });
  await page.addInitScript(() => {
    const seen: string[] = [];
    (window as unknown as { cspViolations: string[] }).cspViolations = seen;
    document.addEventListener('securitypolicyviolation', (e) => seen.push(`${e.violatedDirective} ${e.blockedURI}`));
  });
  const res = await page.goto(path);
  expect(res?.headers()['content-security-policy']).toBe(csp);
  return writes;
}

async function expectClean(page: Page) {
  const violations = await page.evaluate(() => (window as unknown as { cspViolations: string[] }).cspViolations);
  expect(violations).toEqual([]);
  for (const scheme of ['light', 'dark'] as const) {
    await page.emulateMedia({ colorScheme: scheme });
    const axe = await new AxeBuilder({ page }).analyze();
    expect(axe.violations.map((v) => `${scheme}: ${v.id} ${v.nodes.map((n) => n.target).join(', ')}`)).toEqual([]);
  }
}

test('signed out: the landing page signs in and comes back', async ({ page }) => {
  await open(page, '/', { signedIn: false });
  await expect(page.getByRole('link', { name: 'Sign in with GitHub' })).toHaveAttribute(
    'href',
    '/auth/login?return=%2F',
  );
  await expectClean(page);
});

test('repositories: each default branch as a dot and a word', async ({ page }) => {
  await open(page, '/');
  await expect(page.getByRole('heading', { name: 'Repositories' })).toBeVisible();
  await expect(page.getByRole('link', { name: 'acme/web' })).toHaveAttribute('href', '/r/acme/web');
  await expect(page.getByText('violations found', { exact: true })).toHaveClass(/status danger/);
  await expect(page.getByText('@alice')).toBeVisible();
  await expect(page.locator('form[action="/auth/logout"] input[name="csrf"]')).toHaveValue(session.csrf);
  await expectClean(page);
});

test('a token GitHub rejects sends the user to sign in, not to an error', async ({ page }) => {
  await open(page, '/', { tokenRejected: true });
  await page.waitForURL('/auth/login?return=%2F');
});

test('overview: state, pull requests, setup and history', async ({ page }) => {
  const writes = await open(page, '/r/acme/app');
  await expect(page.getByRole('heading', { name: 'acme/app', level: 1 })).toBeVisible();
  await expect(page.locator('.state.attention')).toHaveText('attention');
  await expect(page.getByRole('link', { name: 'Overview' })).toHaveAttribute('aria-current', 'page');
  await expect(page.getByRole('link', { name: 'Policy' })).toHaveAttribute('href', '/r/acme/app/policy');
  await expect(page.getByRole('link', { name: '#4 feat: read options' })).toHaveAttribute('href', '/r/acme/app/pull/4');
  await expect(page.getByText('2 more open pull requests')).toBeVisible();
  await expect(page.getByText('The trial ends')).toBeVisible();
  await expect(page.getByText('Advisory (current step)')).toBeAttached();
  await expectClean(page);

  await page.getByRole('button', { name: 'Run discovery in your workflow' }).click();
  await expect(page.getByRole('status')).toBeVisible();
  expect(writes.map((w) => w.url())).toEqual([expect.stringMatching(/\/api\/repos\/acme\/app\/discover$/)]);
});

test('overview: an admin opens the onboarding pull request', async ({ page }) => {
  const writes = await open(page, '/r/acme/app', { overview: installed });
  await page.getByLabel('Policy owners').fill('alice\nacme/platform');
  await expectClean(page);
  await page.getByRole('button', { name: 'Open the onboarding pull request' }).click();
  await expect(page.getByRole('status')).toHaveText('The onboarding pull request is open.');

  expect(writes).toHaveLength(1);
  const [w] = writes;
  expect(new URL(w.url()).pathname).toBe('/api/repos/acme/app/onboarding');
  expect(w.headers()['x-csrf-token']).toBe(session.csrf);
  expect(w.headers()['content-type']).toBe('application/json');
  expect(w.postDataJSON()).toEqual({ mode: 'customer', owners: 'alice\nacme/platform' });
});

test('pull request: the cycle drawn as on the comment, then the other findings', async ({ page, context }) => {
  await context.grantPermissions(['clipboard-read', 'clipboard-write']);
  await open(page, '/r/acme/app/pull/4');
  await expect(page.getByRole('heading', { name: 'feat: read options #4', level: 1 })).toBeVisible();
  await expect(page.getByRole('link', { name: '#4' })).toHaveAttribute('aria-current', 'page');
  await expect(page.locator('#result')).toHaveText('violations found');
  await expect(page.getByText('gate: advisory · does not block merging')).toBeVisible();
  const cycle = page.getByRole('article').filter({ hasText: 'Dependency cycle between pkg_chaos and pkg_container' });
  await expect(cycle.locator('svg path.arrow')).toHaveCount(2);
  await expect(cycle.locator('svg path.arrow.added')).toHaveCount(1);
  await expect(cycle.getByText('37 locations', { exact: true })).toBeAttached();
  await expect(cycle.getByText('All 37 locations')).toBeVisible();
  await expect(cycle.getByText('Fix:')).toBeVisible();
  await expect(page.getByText('Go through the repository.')).toBeVisible();
  await expect(page.getByRole('link', { name: 'Graph' })).toHaveAttribute('href', '/r/acme/app/pull/4/graph');
  await page.getByText('Advisories · never block').click();
  await expect(page.getByText('UI skips a layer.')).toBeVisible();
  await page.getByText('To measure more').click();
  await expectClean(page);

  await page.getByRole('button', { name: 'Copy 1 task' }).click();
  await expect(page.getByRole('status')).toHaveText('Copied 1 task.');
  expect(await page.evaluate(() => navigator.clipboard.readText())).toBe(pull.tasks);
});

test('pull request: a link that is neither the App nor github.com is not rendered', async ({ page }) => {
  await open(page, '/r/acme/app/pull/4', { pull: { ...pull, url: 'javascript:alert(1)', graph_href: '//evil.example/g' } });
  await expect(page.getByRole('heading', { level: 1 })).toBeVisible();
  await expect(page.getByRole('link', { name: 'View on GitHub' })).toHaveCount(0);
  await expect(page.getByRole('link', { name: 'Graph' })).toHaveCount(0);
  await expect(page.locator('a[href^="javascript:"], a[href^="//"]')).toHaveCount(0);
});

test('pull request: no report says why and links the checks', async ({ page }) => {
  await open(page, '/r/acme/app/pull/4', {
    pull: { ...pull, unavailable: 'GitHub no longer keeps the report of this run.', cycles: null, other: null, violation_count: 0 },
  });
  await expect(page.getByText('GitHub no longer keeps the report')).toBeVisible();
  await expect(page.getByRole('link', { name: 'Checks on GitHub' })).toHaveAttribute('href', pull.checks_url);
  await expectClean(page);
});

test('theme: the choice applies at once and survives a reload', async ({ page }) => {
  await open(page, '/');
  await page.getByRole('button', { name: 'Dark' }).click();
  await expect(page.locator('html')).toHaveAttribute('data-theme', 'dark');
  await page.reload();
  await expect(page.locator('html')).toHaveAttribute('data-theme', 'dark');
  await expect(page.getByRole('button', { name: 'Dark' })).toHaveAttribute('aria-pressed', 'true');
  await page.getByRole('button', { name: 'Auto' }).click();
  await expect(page.locator('html')).not.toHaveAttribute('data-theme', /./);
  await expectClean(page);
});

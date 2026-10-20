import { readFileSync } from 'node:fs';
import AxeBuilder from '@axe-core/playwright';
import { expect, test, type Page, type Request } from '@playwright/test';

const csp = readFileSync(new URL('../csp.txt', import.meta.url), 'utf8').trim();
const session = { login: 'alice', csrf: 'csrf-token-1' };

const repos = {
  repos: [
    { full_name: 'acme/app', state: 'healthy', tone: 'success', reason: 'No new findings' },
    {
      full_name: 'acme/web',
      state: 'violations found',
      tone: 'danger',
      reason: 'Architecture violations found',
    },
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
  managed_ai: 'not_allowed',
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

const aiChecks = (allowed: boolean) => [
  {
    label: 'Allowed for acme',
    state: allowed ? 'met' : 'not_met',
  },
  { label: "This repository's settings say data_mode: managed", state: 'met' },
];

const sharing = (allowed: boolean) => ({
  owner: 'acme',
  features: [
    {
      kind: 'managed_llm',
      title: 'Managed AI',
      leaves:
        'When someone asks for a policy proposal in managed mode, the draft policy text and the instruction are sent to the AI provider.',
      allowed,
      ...(allowed ? { by: 'alice', at: '2026-10-20T09:00:00Z' } : {}),
      checks: aiChecks(allowed),
    },
    {
      kind: 'source_processing',
      title: 'Managed discovery',
      leaves: 'Archfit clones the repository into a short-lived sandbox to propose a first policy.',
      note: 'The App does not start managed discovery yet. Onboarding runs discovery in your own workflow.',
      allowed: true,
      by: 'carol',
      at: '2026-09-01T00:00:00Z',
      checks: [
        {
          label: 'Allowed for acme',
          state: 'met',
        },
      ],
    },
  ],
});

const direction = (from: string, to: string, origin: string, n: number) => ({
  rule: 'no-module-cycles',
  rule_id: 'no-module-cycles',
  meta: 'high',
  violation: true,
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
      directions: [
        direction('pkg_chaos', 'pkg_container', 'added', 2),
        direction('pkg_container', 'pkg_chaos', 'existing', 37),
      ],
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
  dimensions: [
    {
      key: 'change_locality',
      name: 'Change locality',
      status: 'partial',
      observed: 3,
      measurable: 4,
      basis: 'modules',
      confidence: 'medium',
      gate: 'warn',
    },
  ],
  unknowns: [{ dimension: 'Coupling', fact: 'coupling volatility', reason: 'volatility is undeclared' }],
  tasks: '[\n  {"id": "t1"}\n]',
  task_count: 1,
};

const findings = { ...pull, unavailable: '', branch: 'main', compared: false };

const gnode = (name: string, layer: string, policy_line: number, volatility = '', subdomain = '') => ({
  name,
  layer,
  policy_line,
  volatility,
  subdomain,
});
const glink = (from: string, to: string, status: string, count: number, more: object = {}) => ({
  from,
  to,
  status,
  count,
  origin: '',
  strength: 'functional',
  distance: 'across modules',
  volatility: 'undeclared',
  volatility_source: 'undeclared',
  imbalance: '',
  accepted: '',
  hypothesis: '',
  quadrant: '',
  crossing: '',
  container: '',
  distributed_monolith: false,
  ...more,
});

const view = {
  nodes: [
    gnode('api', 'app', 3, 'undeclared'),
    gnode('db', 'infra', 9, 'high'),
    gnode('ui', 'app', 6, '', 'core'),
    gnode('legacy', '', 0),
  ],
  links: [
    glink('api', 'db', 'violation', 12, {
      strength: 'intrusive',
      imbalance: 'medium',
      volatility: 'high',
      hypothesis: 'follow_rule',
      quadrant: 'tight',
      crossing: 'module_boundary',
      container: 'system',
      distributed_monolith: true,
    }),
    glink('db', 'api', 'violation', 2, { strength: 'contract' }),
    glink('ui', 'api', 'allowed', 4, { strength: 'model' }),
    glink('ui', 'db', 'accepted', 3, { volatility: 'high', volatility_source: 'cascade', accepted: 'advisory' }),
    glink('legacy', 'db', 'observed', 0, { strength: '', distance: '', volatility: '' }),
  ],
  findings: [
    {
      from: 'api',
      to: 'db',
      rule_id: 'no-module-cycles',
      kind: 'gate',
      status: 'new',
      why: 'api and db depend on each other.',
      more: 0,
    },
  ],
  stale: false,
  incomplete: true,
};

const repoGraph = {
  unavailable: '',
  view,
  stale_reason: '',
  policy_href: '/r/acme/app/policy',
  branch: 'main',
  pull: null,
  added: 0,
};

const pullHead = Object.fromEntries(
  Object.entries(pull).filter(([k]) =>
    [
      'number',
      'title',
      'author',
      'state',
      'head_ref',
      'base_ref',
      'head_sha',
      'url',
      'checks_url',
      'result',
      'tone',
      'headline',
      'required',
      'unavailable',
      'run_id',
      'no_baseline',
      'compared',
    ].includes(k),
  ),
);
const pullGraph = {
  ...repoGraph,
  policy_href: 'https://github.com/acme/app/blob/a1b2c3d/.archfit.yaml',
  pull: pullHead,
  added: 1,
  view: {
    ...view,
    links: view.links.map((l) =>
      l.from === 'api' && l.to === 'db'
        ? { ...l, origin: 'added' }
        : l.from === 'db'
          ? { ...l, origin: 'existing' }
          : l,
    ),
  },
};

const head = 'a'.repeat(40);
const policy = (managedAllowed: boolean, over: object = {}) => ({
  branch: 'archfit/edit-alice',
  head_sha: head,
  content: 'version: 2\nmodlues: {}\nrules: []\n',
  draft: 'draft',
  draft_state: 'draft',
  draft_url: 'https://github.com/acme/app/pull/9',
  checks: [
    { name: 'archfit/state', state: 'healthy', tone: 'success', reason: 'No new findings' },
    { name: 'archfit/gate', state: 'in progress', tone: 'accent', reason: '' },
  ],
  diagnostics: [{ line: 2, column: 1, message: 'Unknown key `modlues`. Did you mean `modules`?' }],
  can_write: true,
  can_ready: true,
  managed: managedAllowed
    ? { offer: true }
    : {
        offer: false,
        off: 'Managed mode is off: managed AI is not allowed for acme.',
        data_sharing: '/r/acme/app/data-sharing',
      },
  ...over,
});

interface Mock {
  signedIn?: boolean;
  tokenRejected?: boolean; // the session stands, but GitHub rejects the user's token on the first repository call
  overview?: object;
  sharingDown?: boolean; // the consent store cannot be read
  savedUnread?: boolean; // the write is saved, but the App cannot read it back in the same answer
  pull?: object;
  graph?: object;
  pullGraph?: object;
  policy?: object; // fields of the policy page that differ from policy()
  refuseSave?: boolean; // the save is refused with the problems of the text
  holdAssist?: Promise<void>; // the AI request answers once this settles
  serverError?: { status: number; sentence: string }; // the server answers the page as its error page
}

// open answers /api in the browser from the mock, opens the page, and returns the writes the page sent. Each page records the CSP violations
// it sees, and every test fails on one.
async function open(page: Page, path: string, mock: Mock = {}) {
  const writes: Request[] = [];
  let allowed = false;
  await page.route('**/api/**', async (route) => {
    const req = route.request();
    const url = new URL(req.url());
    const json = (status: number, body: object) => route.fulfill({ status, json: body });
    if (req.method() === 'POST') {
      writes.push(req);
      const sent = req.postDataJSON();
      switch (url.pathname) {
        case '/api/repos/acme/app/policy/check':
          return json(200, { diagnostics: [], notice: 'The check found no problems.' });
        case '/api/repos/acme/app/policy':
          if (mock.refuseSave)
            return json(422, {
              error: 'The policy has problems, listed above the editor. Nothing was saved.',
              diagnostics: [{ line: 1, column: 1, message: 'Missing required key `version`.' }],
            });
          return json(200, {
            ...policy(allowed, mock.policy),
            content: sent.content,
            head_sha: 'b'.repeat(40),
            diagnostics: [],
            notice: 'Saved as a commit on the draft pull request.',
          });
        case '/api/repos/acme/app/assist':
          await mock.holdAssist;
          return json(200, {
            ...policy(allowed, mock.policy),
            content: sent.content,
            head_sha: sent.head_sha,
            diagnostics: [],
            since: head,
            run: { state: 'pending', url: 'https://github.com/acme/app/actions/workflows/archfit-assist.yaml' },
            notice: 'The assist workflow is running in your CI.',
          });
      }
      if (url.pathname === '/api/repos/acme/app/data-sharing') {
        allowed = true;
        if (mock.savedUnread)
          return json(502, { error: 'The change was saved, but it could not be read back. Reload the page.' });
        return json(200, { ...sharing(true), notice: 'Managed AI is allowed.' });
      }
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
      case '/api/repos/acme/app/data-sharing':
        if (mock.sharingDown)
          return json(502, {
            error: 'GitHub or an Archfit service failed. Nothing was changed. Try again.',
          });
        return json(200, sharing(allowed));
      case '/api/repos/acme/app/pulls/4':
        return json(200, mock.pull ?? pull);
      case '/api/repos/acme/app/findings':
        return json(200, findings);
      case '/api/repos/acme/app/graph':
        return json(200, mock.graph ?? repoGraph);
      case '/api/repos/acme/app/pulls/4/graph':
        return json(200, mock.pullGraph ?? pullGraph);
      case '/api/repos/acme/app/policy':
        return json(200, policy(allowed, mock.policy));
    }
    return json(404, { error: 'Not found.' });
  });
  if (mock.serverError) {
    // The server's error page is the shell with its status and the sentence in the app-error meta tag.
    const { status, sentence } = mock.serverError;
    await page.route(`**${path}`, async (route) => {
      const res = await route.fetch({ url: new URL('/', route.request().url()).href });
      await route.fulfill({
        status,
        headers: res.headers(),
        body: (await res.text()).replace('__APP_ERROR__', sentence),
      });
    });
  }
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
  const counts = page.getByRole('group', { name: 'Counts on main' });
  await expect(counts).toContainText('3 Rule violations');
  await expect(counts).toContainText('1 Advisory never block');
  await expect(counts).toContainText('0 Open pull requests need attention');
  await expect(page.getByRole('heading', { name: 'Findings on main' })).toBeVisible();
  await expect(page.getByText('Go through the repository.')).toBeVisible();
  await expect(page.getByRole('link', { name: 'Open the graph' })).toHaveAttribute('href', '/r/acme/app/graph');
  await expect(page.getByRole('link', { name: 'Data sharing' })).toHaveAttribute('href', '/r/acme/app/data-sharing');
  await expect(page.getByText('Managed AI: not allowed')).toBeVisible();
  await expect(page.getByRole('link', { name: 'Consent' })).toHaveCount(0);
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
  await open(page, '/r/acme/app/pull/4', {
    pull: { ...pull, url: 'javascript:alert(1)', checks_url: '//evil.example/c' },
  });
  await expect(page.getByRole('heading', { level: 1 })).toBeVisible();
  await expect(page.getByRole('link', { name: 'View on GitHub' })).toHaveCount(0);
  await expect(page.getByRole('link', { name: 'Graph' })).toHaveAttribute('href', '/r/acme/app/pull/4/graph');
  await expect(page.locator('a[href^="javascript:"], a[href^="//"]')).toHaveCount(0);
});

test('pull request: no report says why and links the checks', async ({ page }) => {
  await open(page, '/r/acme/app/pull/4', {
    pull: {
      ...pull,
      unavailable: 'GitHub no longer keeps the report of this run.',
      cycles: null,
      other: null,
      violation_count: 0,
    },
  });
  await expect(page.getByText('GitHub no longer keeps the report')).toBeVisible();
  await expect(page.getByRole('link', { name: 'Checks on GitHub' })).toHaveAttribute('href', pull.checks_url);
  await expectClean(page);
});

test('graph: the canvas, the inspector and the matrix', async ({ page }) => {
  await open(page, '/r/acme/app/graph');
  await expect(page.getByRole('heading', { name: 'Architecture', level: 1 })).toBeVisible();
  await expect(page.getByRole('link', { name: 'Architecture' })).toHaveAttribute('aria-current', 'page');
  await expect(page.locator('.g-node')).toHaveCount(4);
  await expect(page.locator('.g-edge')).toHaveCount(5);
  // width is integration strength; a dependency with no measured strength is dashed
  await expect(
    page.getByRole('button', { name: /^api to db, 12 dependencies, violation, intrusive coupling/ }).locator('.line'),
  ).toHaveAttribute('stroke-width', '5');
  await expect(page.locator('.g-edge.unmeasured')).toHaveCount(1);
  await expect(page.locator('.g-node.undeclared')).toHaveText('legacy');
  // what the report's policy declares, never a seam's volatility
  await expect(page.locator('.g-node', { hasText: 'db' })).toContainText('volatility high');
  await expect(page.locator('.g-node', { hasText: 'api' })).toContainText('volatility undeclared');
  await expect(page.locator('.g-node', { hasText: 'ui' })).toContainText('subdomain core');
  await expect(page.locator('#incomplete')).toBeVisible();
  await expect(page.getByRole('button', { name: /added in this PR/ })).toHaveCount(0);
  const problems = page.getByRole('region', { name: /Problems/ });
  await expect(problems.getByRole('button')).toHaveCount(2); // the cycle as one row, then accepted
  await expect(problems.getByRole('button').first()).toContainText('⇄');
  await expect(problems.getByRole('button').last()).toContainText('accepted advisory');
  await expectClean(page);

  await problems.getByRole('button').last().click();
  const accepted = page.getByRole('complementary');
  await expect(accepted.locator('.g-tag')).toHaveText('accepted advisory');
  await expect(accepted).toContainText('high (cascade)');

  await problems.getByRole('button').first().click();
  const inspector = page.getByRole('complementary');
  await expect(inspector.getByRole('heading', { name: 'Dependency' })).toBeVisible();
  await expect(inspector).toContainText('intrusive coupling');
  await expect(inspector).toContainText('imbalanced · medium');
  await expect(inspector).toContainText(
    'Flagged: intrusive coupling across the module boundary at high volatility (container system).',
  );
  await expect(inspector).toContainText('Fix the rule violation on this dependency.');
  await expect(inspector).toContainText('tight: strong coupling across a boundary');
  await expect(inspector).toContainText('the module boundary, within system');
  await expect(page.locator('#flagged')).toHaveText(
    '1 dependency couples strongly across a module boundary at high volatility.',
  );
  await expect(inspector).toContainText('api and db depend on each other.');
  await expect(inspector).toContainText('Other direction · a cycle');
  await expect(inspector.getByRole('link', { name: /Open api in the policy/ })).toHaveAttribute(
    'href',
    '/r/acme/app/policy#L3',
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
    '/r/acme/app/policy#L9',
  );

  await page.getByRole('button', { name: 'Matrix' }).click();
  await expect(page.getByRole('button', { name: 'api to db, 12, violation' })).toBeVisible();
  await page.getByRole('button', { name: 'violation', exact: false }).filter({ hasText: '2' }).first().click();
  await expect(page.getByRole('button', { name: 'api to db, 12, violation' })).toHaveCount(0);
  await expectClean(page);

  // a module the report's own policy does not name: no claim that the policy is stale
  await page.getByRole('button', { name: 'Graph', exact: true }).click();
  await page.locator('.g-node', { hasText: 'legacy' }).click();
  await expect(inspector).toContainText('nothing: not in the policy');
  await expect(inspector).not.toContainText('not the one the report used');

  await page.getByText('Modules and dependencies as tables').click();
  await expect(page.getByRole('table', { name: 'Dependencies' })).toContainText('not measured');
});

test('pull request graph: the header, and a halo only on what the PR added', async ({ page }) => {
  await open(page, '/r/acme/app/pull/4/graph');
  await expect(page.getByRole('heading', { name: 'feat: read options #4', level: 1 })).toBeVisible();
  await expect(page.getByRole('link', { name: 'Graph' })).toHaveAttribute('aria-current', 'page');
  await expect(page.getByRole('button', { name: '1 added in this PR' })).toHaveAttribute('aria-pressed', 'true');
  await expect(page.locator('.g-edge .halo')).toHaveCount(1);
  await expect(page.getByRole('button', { name: /^api to db, .*, added by this PR$/ })).toBeVisible();
  await expect(page.getByRole('button', { name: /^db to api, .*violation, contract coupling$/ })).toBeVisible();
  await expectClean(page);

  await page.getByRole('button', { name: '1 added in this PR' }).click();
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

test('graph: a layout worker that fails to load says so instead of laying out forever', async ({ page }) => {
  await page.route('**/assets/layout.worker-*.js', (route) => route.fulfill({ status: 404, body: 'gone' }));
  await open(page, '/r/acme/app/graph');
  await expect(page.getByRole('alert')).toHaveText('The layout failed: the layout worker stopped');
  await expect(page.locator('.g-node')).toHaveCount(0);
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

test('data sharing: what leaves, who allowed it, and Allow', async ({ page }) => {
  const writes = await open(page, '/r/acme/app/data-sharing');
  await expect(page.getByRole('heading', { name: 'Data sharing', level: 1 })).toBeVisible();
  await expect(page.getByText('every repository of acme that the Archfit App can access').first()).toBeVisible();
  const ai = page.getByRole('region', { name: 'Managed AI' });
  await expect(ai.getByText('Not allowed', { exact: true })).toBeVisible();
  await expect(ai.getByText('(to do)')).toBeAttached();
  const discovery = page.getByRole('region', { name: 'Managed discovery' });
  await expect(discovery.getByText('Allowed by @carol on 1 September 2026')).toBeVisible();
  await expect(discovery.getByText('The App does not start managed discovery yet.', { exact: false })).toBeVisible();
  await expect(page.getByRole('link', { name: 'Overview' })).not.toHaveAttribute('aria-current', 'page');
  await expectClean(page);

  await ai.getByRole('button', { name: 'Allow Managed AI' }).click();
  await expect(page.getByRole('status')).toHaveText('Managed AI is allowed.');
  await expect(ai.getByText('Allowed by @alice on 20 October 2026')).toBeVisible();
  await expect(ai.getByRole('button', { name: 'Revoke' })).toBeVisible();
  expect(writes).toHaveLength(1);
  const [w] = writes;
  expect(new URL(w.url()).pathname).toBe('/api/repos/acme/app/data-sharing');
  expect(w.headers()['x-csrf-token']).toBe(session.csrf);
  expect(w.postDataJSON()).toEqual({ kind: 'managed_llm', action: 'allow' });
  await expectClean(page);
});

test('data sharing: a consent that cannot be read is an error, never "not allowed"', async ({ page }) => {
  await open(page, '/r/acme/app/data-sharing', { sharingDown: true });
  await expect(page.getByRole('alert')).toContainText('Nothing was changed');
  await expect(page.getByText('Not allowed')).toHaveCount(0);
  await expect(page.getByRole('button', { name: /Allow/ })).toHaveCount(0);
  await expectClean(page);
});

test('data sharing: a change saved but not read back shows the state read again', async ({ page }) => {
  await open(page, '/r/acme/app/data-sharing', { savedUnread: true });
  const ai = page.getByRole('region', { name: 'Managed AI' });
  await ai.getByRole('button', { name: 'Allow Managed AI' }).click();
  await expect(page.getByRole('alert')).toContainText('The change was saved');
  await expect(ai.getByText('Allowed by @alice on 20 October 2026')).toBeVisible();
  await expect(ai.getByRole('button', { name: 'Revoke' })).toBeVisible();
  await expectClean(page);
});

test('policy: the editor under the CSP, with the problems as a list and as marks', async ({ page }) => {
  await open(page, '/r/acme/app/policy#L3');
  await expect(page.getByRole('heading', { name: 'Policy', level: 1 })).toBeVisible();
  await expect(page.getByRole('link', { name: 'Policy' })).toHaveAttribute('aria-current', 'page');
  const editor = page.getByRole('textbox', { name: 'Policy text, .archfit.yaml' });
  await expect(editor).toContainText('modlues: {}');
  // CodeMirror's styles are a <style> element that only the shell's nonce lets through.
  await expect(page.locator('style[nonce="__CSP_NONCE__"]')).not.toHaveCount(0);
  await expect(page.locator('.cm-activeLine')).toHaveText('rules: []');
  const problems = page.getByRole('region', { name: '1 problem' });
  await expect(problems).toContainText('Unknown key `modlues`. Did you mean `modules`?');
  await expect(page.locator('.cm-lintRange-error')).toHaveText('modlues: {}');
  await problems.getByRole('button', { name: 'Line 2, column 1' }).click();
  await expect(page.locator('.cm-activeLine')).toHaveText('modlues: {}');
  await expect(page.getByRole('link', { name: 'Draft pull request' })).toHaveAttribute(
    'href',
    'https://github.com/acme/app/pull/9',
  );
  await expect(page.getByText('Checked by Archfit (current step)')).toBeAttached();
  await expectClean(page);
});

test('policy: Check and Save send the text, the head and the CSRF header', async ({ page }) => {
  const writes = await open(page, '/r/acme/app/policy');
  const editor = page.getByRole('textbox', { name: 'Policy text, .archfit.yaml' });
  await expect(editor).toContainText('rules: []');
  await editor.click();
  await page.keyboard.press('ControlOrMeta+End');
  await page.keyboard.type('# edited');
  await expect(page.getByText('unsaved edits')).toBeVisible();
  await expect(page.getByRole('button', { name: 'Ready for review' })).toBeDisabled();
  const text = 'version: 2\nmodlues: {}\nrules: []\n# edited';

  await page.getByRole('button', { name: 'Check' }).click();
  await expect(page.getByRole('status')).toHaveText('The check found no problems.');
  await expect(page.getByRole('region', { name: /problem/ })).toHaveCount(0);

  await page.getByRole('button', { name: 'Save to draft PR' }).click();
  await expect(page.getByRole('status')).toHaveText('Saved as a commit on the draft pull request.');
  await expect(page.getByText('unsaved edits')).toHaveCount(0);
  await expect(page.getByRole('button', { name: 'Ready for review' })).toBeEnabled();

  expect(writes.map((w) => new URL(w.url()).pathname)).toEqual([
    '/api/repos/acme/app/policy/check',
    '/api/repos/acme/app/policy',
  ]);
  for (const w of writes) expect(w.headers()['x-csrf-token']).toBe(session.csrf);
  expect(writes[0].postDataJSON()).toEqual({ content: text });
  expect(writes[1].postDataJSON()).toEqual({ branch: 'archfit/edit-alice', head_sha: head, content: text });
  await expectClean(page);
});

test('policy: a refused save lists the problems and keeps the text', async ({ page }) => {
  await open(page, '/r/acme/app/policy', { refuseSave: true });
  const editor = page.getByRole('textbox', { name: 'Policy text, .archfit.yaml' });
  await expect(editor).toContainText('rules: []');
  await page.getByRole('button', { name: 'Save to draft PR' }).click();
  await expect(page.getByRole('alert')).toContainText('Nothing was saved.');
  await expect(page.getByRole('region', { name: '1 problem' })).toContainText('Missing required key `version`.');
  await expect(editor).toContainText('modlues: {}');
  await expectClean(page);
});

test('policy: a reader can check but is offered no write', async ({ page }) => {
  await open(page, '/r/acme/app/policy', { policy: { can_write: false, can_ready: false, managed: { offer: false } } });
  await expect(page.getByText('Saving needs write access to acme/app.')).toBeVisible();
  await expect(page.getByRole('button', { name: 'Check' })).toBeVisible();
  await expect(page.getByRole('button', { name: /Save|Ready|Propose/ })).toHaveCount(0);
  await expect(page.getByRole('heading', { name: 'Ask AI for a proposal' })).toHaveCount(0);
  await expectClean(page);
});

test('policy: managed mode is off with its reason, and an administrator allows it in place', async ({ page }) => {
  const writes = await open(page, '/r/acme/app/policy');
  const managed = page.getByRole('radio', { name: /Managed/ });
  await expect(managed).toBeDisabled();
  await expect(page.getByText('Managed mode is off: managed AI is not allowed for acme.')).toBeVisible();
  await expect(page.getByRole('link', { name: 'Data sharing' })).toHaveAttribute('href', '/r/acme/app/data-sharing');
  await expectClean(page);

  await page.getByRole('button', { name: 'Allow managed AI' }).click();
  await expect(page.getByRole('status')).toHaveText('Managed AI is allowed.');
  await expect(managed).toBeEnabled();
  expect(writes.map((w) => w.postDataJSON())).toEqual([{ kind: 'managed_llm', action: 'allow' }]);
});

test('policy: a customer-mode request follows the run in your CI', async ({ page }) => {
  const writes = await open(page, '/r/acme/app/policy');
  await expect(page.getByRole('textbox', { name: 'Policy text, .archfit.yaml' })).toContainText('rules: []');
  await page.getByRole('textbox', { name: 'Instruction', exact: true }).fill('split module a');
  await expect(page.getByRole('radio', { name: /Customer/ })).toBeChecked();
  await page.getByRole('button', { name: 'Propose' }).click();
  await expect(page.getByText('AI proposal in your CI: queued or running')).toBeVisible();
  await expect(page.getByRole('link', { name: 'workflow runs' })).toHaveAttribute(
    'href',
    'https://github.com/acme/app/actions/workflows/archfit-assist.yaml',
  );
  await expect(page).toHaveURL(`/r/acme/app/policy?since=${head}`);
  expect(writes.map((w) => w.postDataJSON())).toEqual([
    {
      branch: 'archfit/edit-alice',
      head_sha: head,
      content: 'version: 2\nmodlues: {}\nrules: []\n',
      instruction: 'split module a',
      mode: 'customer',
    },
  ]);
  await expectClean(page);
});

test('policy: Propose saves the edits first and keeps text typed while it runs', async ({ page }) => {
  let release = () => {};
  const writes = await open(page, '/r/acme/app/policy', { holdAssist: new Promise((r) => (release = r)) });
  const editor = page.getByRole('textbox', { name: 'Policy text, .archfit.yaml' });
  await expect(editor).toContainText('rules: []');
  await editor.click();
  await page.keyboard.press('ControlOrMeta+End');
  await page.keyboard.type('# mine');
  await page.getByRole('textbox', { name: 'Instruction', exact: true }).fill('split module a');
  await page.getByRole('button', { name: 'Propose' }).click();
  await expect.poll(() => writes.length).toBe(2);
  await editor.click();
  await page.keyboard.press('ControlOrMeta+End');
  await page.keyboard.type(' and more');
  release();
  await expect(page.getByText('AI proposal in your CI: queued or running')).toBeVisible();
  await expect(editor).toContainText('# mine and more');
  await expect(page.getByText('unsaved edits')).toBeVisible();
  const mine = 'version: 2\nmodlues: {}\nrules: []\n# mine';
  expect(writes.map((w) => [new URL(w.url()).pathname, w.postDataJSON()])).toEqual([
    ['/api/repos/acme/app/policy', { branch: 'archfit/edit-alice', head_sha: head, content: mine }],
    [
      '/api/repos/acme/app/assist',
      {
        branch: 'archfit/edit-alice',
        head_sha: 'b'.repeat(40),
        content: mine,
        instruction: 'split module a',
        mode: 'customer',
      },
    ],
  ]);
  await expectClean(page);
});

test('policy: a CRLF file is not an edit, and saves with CRLF', async ({ page }) => {
  const writes = await open(page, '/r/acme/app/policy', { policy: { content: 'version: 2\r\nrules: []\r\n' } });
  const editor = page.getByRole('textbox', { name: 'Policy text, .archfit.yaml' });
  await expect(editor).toContainText('rules: []');
  await expect(page.getByText('unsaved edits')).toHaveCount(0);
  await editor.click();
  await page.keyboard.press('ControlOrMeta+End');
  await page.keyboard.type('# edited');
  await page.getByRole('button', { name: 'Save to draft PR' }).click();
  await expect(page.getByRole('status')).toHaveText('Saved as a commit on the draft pull request.');
  expect(writes[0].postDataJSON().content).toBe('version: 2\r\nrules: []\r\n# edited');
  await expectClean(page);
});

test('error page: the server sentence, no repository tabs, under the CSP', async ({ page }) => {
  await open(page, '/r/acme/hidden', { serverError: { status: 404, sentence: 'Not found.' } });
  await expect(page.getByRole('heading', { name: 'This page cannot be shown' })).toBeVisible();
  await expect(page.getByText('Not found.')).toBeVisible();
  await expect(page.getByRole('navigation', { name: 'Repository' })).toHaveCount(0);
  await expect(page).toHaveTitle('Error · Archfit');
  await expect(page.getByRole('link', { name: 'Third-party licences' })).toHaveAttribute('href', '/licenses.txt');
  await expectClean(page);
});

test('every page links the third-party licences', async ({ page }) => {
  await open(page, '/r/acme/app');
  await expect(page.getByRole('link', { name: 'Third-party licences' })).toHaveAttribute('href', '/licenses.txt');
});

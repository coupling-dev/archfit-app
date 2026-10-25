import { readFileSync } from 'node:fs';
import AxeBuilder from '@axe-core/playwright';
import { expect, test, type Locator, type Page, type Request } from '@playwright/test';

const csp = readFileSync(new URL('../csp.txt', import.meta.url), 'utf8').trim();
const appBuild = {
  version: 'v0.12.0',
  source_revision: 'b'.repeat(40),
  built_at: '2026-10-25T10:00:00Z',
  runtime_revision: 'archfit-public-123',
  modified: false,
};
const session = { login: 'alice', csrf: 'csrf-token-1', build: appBuild };

const analysisCheck = {
  run_id: 88,
  run_attempt: 1,
  head_sha: 'a'.repeat(40),
  check_started_at: '2026-10-20T10:00:00Z',
  report_digest: 'sha256:' + 'c'.repeat(64),
  reason: 'blocking_findings (architecture)',
  next: 'Fix the rule violations, then re-run.',
  detail: '',
  also: [],
  producer: 'engine v3.0.1, image sha256:' + 'd'.repeat(64) + ', linux/amd64',
  policy_digest: 'e'.repeat(64),
  baseline_digest: 'no baseline',
  labels_digest: 'no labels file',
  profile: 'version v1, settings hash ' + 'f'.repeat(64),
};

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
    { full_name: 'octo/tool', waiting: true, state: 'waiting for approval', tone: 'attention', reason: '' },
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
    {
      number: 5,
      title: 'chore: bump deps',
      state: 'no state yet',
      tone: 'neutral',
      reason: '',
      href: '/r/acme/app/pull/5',
    },
  ],
  pulls_more: 2,
  pulls_error: false,
  steps: steps(4),
  next: 'Make the gate required. Rehearse it first on one forbidden and one legitimate change.',
  next_how: 'A forbidden change must show a rule violation on archfit/state. Then merge the settings pull request.',
  stage: 'Gate is advisory',
  diagnostic: '',
  draft_url: '',
  draft_state: '',
  banner: 'The trial ends on 1 November 2026.',
  admin: true,
  managed_ai: 'not_allowed',
  can_start: false,
  can_discover: false, // discovery is no step once the policy is merged
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

const workflowMerged = {
  ...installed,
  steps: steps(1),
  next: 'Run discovery: it proposes a first policy as a pull request.',
  next_how: 'Discovery runs in your own workflow.',
  stage: 'Workflow merged',
  diagnostic: 'Run discovery (workflow_dispatch with `mode` set to `discovery`)',
  can_start: false,
  can_discover: true,
};

const workflowYAML = 'name: archfit\non:\n  pull_request:\n';

const workflowNeeded = {
  ...installed,
  diagnostic: 'Add `.github/workflows/archfit.yaml` to PR #1',
  next: '',
  draft_url: 'https://github.com/acme/app/pull/1',
  draft_state: 'draft',
  can_start: false,
  workflow: {
    path: '.github/workflows/archfit.yaml',
    branch: 'archfit/onboarding',
    content: workflowYAML,
    add_url:
      'https://github.com/acme/app/new/archfit/onboarding?filename=.github%2Fworkflows%2Farchfit.yaml&value=name%3A+archfit',
  },
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
      offered: true,
      allowed,
      ...(allowed ? { by: 'alice', at: '2026-10-20T09:00:00Z' } : {}),
      checks: aiChecks(allowed),
    },
    {
      kind: 'source_processing',
      title: 'Managed discovery',
      leaves: 'Archfit clones the repository into a short-lived sandbox to propose a first policy.',
      note: 'The App does not start managed discovery yet. Onboarding runs discovery in your own workflow.',
      offered: false,
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
  move: '',
  metric: '',
  owner_decision: false,
  also: null as string[] | null,
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
  check: analysisCheck,
  no_baseline: true,
  compared: true,
  decision: {
    verdict: 'blocked',
    detail: 'hard gates fail, 3 active blockers, 1 required rule not evaluated',
    hard_gates: 'fail',
    blockers: 3,
    unevaluated: [{ rule_id: 'layers', reason: 'selector matches no module' }],
    origin_reasons: [],
  },
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
      alternatives: ['pkg_shared'],
      directions: [
        { ...direction('pkg_chaos', 'pkg_container', 'added', 2), also: ['Imbalanced coupling · critical'] },
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
  advisory_count: 2,
  advisories_elsewhere: true,
  advisories: [
    {
      ...direction('ui', 'store', '', 1),
      rule: 'layering',
      rule_id: 'layering',
      why: 'UI skips a layer.',
      move: 'Move the functionality: lower the distance, so the coupled code lives in one module',
      owner_decision: true,
    },
    {
      ...direction('', '', '', 0),
      rule: 'metric/fan_out',
      rule_id: 'metric/fan_out',
      meta: 'medium',
      metric: 'metric fan_out: 3 → 5 (at most 4)',
    },
  ],
  accepted_count: 1,
  accepted: [
    {
      ...direction('x', 'y', '', 1),
      meta: 'low, accepted in the baseline',
      strength: 'model',
      distance: 'across modules',
      volatility: 'low',
      move: 'Accept it: the target rarely changes, so the strength costs little',
    },
  ],
  fixed_count: 1,
  fixed: [{ ...direction('old', 'gone', '', 1), rule: 'no-module-cycles', why: 'Was a cycle.' }],
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
    {
      key: 'testability',
      name: 'Testability',
      status: 'partial',
      observed: 3,
      measurable: 3,
      basis: 'classified source files',
      confidence: 'medium',
      gate: 'pass',
    },
    {
      key: 'drift',
      name: 'Drift',
      status: 'unmeasured',
      observed: 0,
      measurable: 0,
      basis: '',
      confidence: 'low',
      gate: 'not_applicable',
    },
  ],
  unknowns: [{ dimension: 'Coupling', fact: 'coupling volatility', reason: 'volatility is undeclared' }],
  tasks: '[\n  {"id": "t1"}\n]',
  task_count: 1,
};

const findings = {
  ...pull,
  unavailable: '',
  branch: 'main',
  compared: false,
  advisory_count: 1,
  advisories: pull.advisories.slice(0, 1),
  attention_reason: '1 advisory; testability and operations are partially measured',
};

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
  move: '',
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
      move: 'A rule violation covers this pair: follow its Fix',
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
      rule: 'no-module-cycles',
      meta: 'high',
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

// installations is the operators' page: one account waiting, one approved by hand, one by the App's configuration.
const installations = {
  waiting: [
    {
      id: 7,
      account: 'octo',
      type: 'Organization',
      installed_at: '2026-10-21T09:00:00Z',
      repos: [
        { name: 'tool', private: false },
        { name: 'ledger', private: true },
      ],
      repos_error: false,
      auto: false,
    },
  ],
  approved: [
    {
      id: 3,
      account: 'acme',
      type: 'Organization',
      installed_at: '2026-09-01T09:00:00Z',
      repos: [{ name: 'app', private: true }],
      repos_error: false,
      auto: false,
      approved_by: 'vladikk',
      approved_at: '2026-10-18T10:00:00Z',
    },
    {
      id: 1,
      account: 'alexei-led',
      type: 'User',
      installed_at: '2026-08-01T09:00:00Z',
      repos: [{ name: 'ccgram', private: false }],
      repos_error: false,
      auto: true,
    },
  ],
  unreadable: 0,
};

interface Mock {
  signedIn?: boolean;
  operator?: boolean; // the signed-in user approves installations
  tokenRejected?: boolean; // the session stands, but GitHub rejects the user's token on the first repository call
  overview?: object;
  sharingDown?: boolean; // the consent store cannot be read
  savedUnread?: boolean; // the write is saved, but the App cannot read it back in the same answer
  appBuild?: object;
  pull?: object;
  graph?: object;
  pullGraph?: object;
  policy?: object; // fields of the policy page that differ from policy()
  refuseSave?: boolean; // the save is refused with the problems of the text
  saveNotFound?: boolean; // the save answers 404, as when write access went away
  holdAssist?: Promise<void>; // the AI request answers once this settles
  serverError?: { status: number; sentence: string }; // the server answers the page as its error page
  holdSession?: Promise<void>; // /api/session answers once this settles
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
          if (mock.saveNotFound) return json(404, { error: 'Not found.' });
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
      const admin = /^\/api\/admin\/installations\/(\d+)\/(approve|revoke)$/.exec(url.pathname);
      if (admin) {
        if (!mock.operator) return json(404, { error: 'Not found.' });
        return json(200, { notice: `${admin[2] === 'approve' ? 'Approved' : 'Revoked'} installation ${admin[1]}.` });
      }
      if (url.pathname === '/api/repos/acme/app/data-sharing') {
        allowed = true;
        if (mock.savedUnread)
          return json(502, { error: 'The change was saved, but it could not be read back. Reload the page.' });
        return json(200, { ...sharing(true), notice: 'Managed AI is allowed.' });
      }
      return json(200, { notice: 'The onboarding pull request is open. Next: add the workflow file to it.' });
    }
    if (mock.signedIn === false) return json(401, { error: 'Sign in to continue.' });
    if (mock.tokenRejected && url.pathname !== '/api/session') return json(401, { error: 'Sign in to continue.' });
    if (url.pathname.startsWith('/api/repos/octo/'))
      return json(403, { error: 'This account is waiting for approval.', waiting: 'octo' });
    switch (url.pathname) {
      case '/api/session':
        await mock.holdSession;
        return json(200, {
          ...session,
          build: mock.appBuild ?? appBuild,
          ...(mock.operator ? { operator: true, waiting: 1 } : {}),
        });
      case '/api/admin/installations':
        if (!mock.operator) return json(404, { error: 'Not found.' });
        return json(200, installations);
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

test('signed out: the landing page says what Archfit does, signs in and comes back', async ({ page }) => {
  await open(page, '/', { signedIn: false });
  await expect(page.getByRole('heading', { name: 'Architecture checks on every pull request' })).toBeVisible();
  await expect(page.getByText('Your code never leaves your CI.')).toBeVisible();
  await expect(page.getByText('Waiting for approval')).toHaveCount(0);
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
  await expect(page.locator('.hero .state.attention')).toHaveText('attention');
  await expect(page.locator('#attention-reason')).toHaveText(
    'In the latest report: 1 advisory; testability and operations are partially measured.',
  );
  await expect(page.getByRole('link', { name: 'Overview' })).toHaveAttribute('aria-current', 'page');
  await expect(page.getByRole('link', { name: 'Policy' })).toHaveAttribute('href', '/r/acme/app/policy');
  await expect(page.getByRole('link', { name: '#4 feat: read options' })).toHaveAttribute('href', '/r/acme/app/pull/4');
  await expect(page.getByText('2 more open pull requests')).toBeVisible();
  await expect(page.getByText('The trial ends')).toBeVisible();
  await expect(page.getByText('Advisory (current step)')).toBeAttached();
  const needsYou = page.getByRole('region', { name: 'Needs you' });
  await expect(needsYou).toContainText('Make the gate required.');
  await expect(needsYou.getByText('Then merge the settings pull request.')).toBeHidden();
  await needsYou.getByText('How', { exact: true }).click();
  await expect(needsYou.getByText('Then merge the settings pull request.')).toBeVisible();
  const counts = page.getByRole('group', { name: 'Counts on main' });
  await expect(counts).toContainText('3 Rule violations');
  await expect(counts).toContainText('1 Advisory never block');
  // a pull request with no report yet is waiting, not unhealthy
  await expect(counts).toContainText('0 Open pull requests not healthy among the 2 listed; 1 waiting for a report');
  await expect(page.getByRole('heading', { name: 'Findings on main' })).toBeVisible();
  await expect(page.getByText('Go through the repository.')).toBeVisible();
  await expect(page.getByRole('link', { name: 'Open the graph' })).toHaveAttribute('href', '/r/acme/app/graph');
  await expect(page.getByRole('link', { name: 'Data sharing' })).toHaveAttribute('href', '/r/acme/app/data-sharing');
  await expect(page.getByText('Managed AI: not allowed')).toBeVisible();
  await expect(page.getByRole('link', { name: 'Consent' })).toHaveCount(0);
  // the next step is the gate, so no button for another step: one sentence, one action
  const needs = page.getByRole('region', { name: 'Needs you' });
  await expect(needs.getByRole('button')).toHaveCount(0);
  await expectClean(page);
  expect(writes).toEqual([]);
});

test('overview: discovery is offered at its own step and runs in the workflow', async ({ page }) => {
  const writes = await open(page, '/r/acme/app', { overview: workflowMerged });
  const needs = page.getByRole('region', { name: 'Needs you' });
  await expect(needs).toContainText('Run discovery: it proposes a first policy');
  await expect(needs).not.toContainText('workflow_dispatch with'); // the diagnostic yields to the one-sentence step
  await expectClean(page);
  await needs.getByRole('button', { name: 'Run discovery in your workflow' }).click();
  await expect(page.getByRole('status')).toBeVisible();
  expect(writes.map((w) => w.url())).toEqual([expect.stringMatching(/\/api\/repos\/acme\/app\/discover$/)]);
});

test('overview: an admin opens the onboarding pull request', async ({ page }) => {
  const writes = await open(page, '/r/acme/app', { overview: installed });
  await page.getByLabel('Policy owners').fill('alice\nacme/platform');
  await expectClean(page);
  await page.getByRole('button', { name: 'Open the onboarding pull request' }).click();
  await expect(page.getByRole('status')).toHaveText(
    'The onboarding pull request is open. Next: add the workflow file to it.',
  );

  expect(writes).toHaveLength(1);
  const [w] = writes;
  expect(new URL(w.url()).pathname).toBe('/api/repos/acme/app/onboarding');
  expect(w.headers()['x-csrf-token']).toBe(session.csrf);
  expect(w.headers()['content-type']).toBe('application/json');
  expect(w.postDataJSON()).toEqual({ mode: 'customer', owners: 'alice\nacme/platform' });
});

test('overview: the owner adds the workflow file the App does not write', async ({ page, context }) => {
  await context.grantPermissions(['clipboard-read', 'clipboard-write']);
  const writes = await open(page, '/r/acme/app', { overview: workflowNeeded });
  const needs = page.getByRole('region', { name: 'Needs you' });
  await expect(needs).toContainText('Add the Archfit workflow.');
  await expect(needs).not.toContainText('to PR #1'); // the card says the step; the raw diagnostic stays out
  await expect(needs.getByRole('link', { name: 'Add the file on GitHub' })).toHaveAttribute(
    'href',
    workflowNeeded.workflow.add_url,
  );
  await expect(needs).toContainText('Commit directly to the archfit/onboarding branch');
  await expect(needs.getByLabel('Policy owners')).toHaveCount(0); // onboarding is started: no second form
  await expectClean(page);

  await needs.getByRole('button', { name: 'Copy the file' }).click();
  await expect(needs.getByRole('status')).toContainText('Copied.');
  expect(await page.evaluate(() => navigator.clipboard.readText())).toBe(workflowYAML);

  const reread = page.waitForRequest('**/api/repos/acme/app/overview');
  await needs.getByRole('button', { name: 'Check again' }).click();
  await reread;
  expect(writes).toEqual([]);
});

test('pull request: the cycle drawn as on the comment, then the other findings', async ({ page, context }) => {
  await context.grantPermissions(['clipboard-read', 'clipboard-write']);
  await open(page, '/r/acme/app/pull/4');
  await expect(page.getByRole('heading', { name: 'feat: read options #4', level: 1 })).toBeVisible();
  await expect(page.getByRole('link', { name: '#4' })).toHaveAttribute('aria-current', 'page');
  await expect(page.locator('#result')).toHaveText('violations found');
  await expect(page.getByText('Advisory: does not block merging')).toBeVisible();
  await expect(page.locator('.result .box-foot')).toContainText('customer-attested');
  const cycle = page.getByRole('article').filter({ hasText: 'Dependency cycle between pkg_chaos and pkg_container' });
  await expect(cycle.locator('svg path.arrow')).toHaveCount(2);
  await expect(cycle.locator('svg path.arrow.added')).toHaveCount(1);
  await expect(cycle.getByText('37 locations', { exact: true })).toBeAttached();
  await expect(cycle.getByText('All 37 locations')).toBeVisible();
  await expect(cycle.getByText('Fix:')).toBeVisible();
  // an advisory on a violation's dependency joins its row, as on the comment
  await expect(cycle.getByRole('columnheader', { name: 'Also reported as' })).toBeVisible();
  await expect(cycle.getByRole('row', { name: /pkg_chaos → pkg_container/ })).toContainText(
    'Imbalanced coupling · critical',
  );
  await expect(page.getByText('Go through the repository.')).toBeVisible();
  await expect(page.getByRole('link', { name: 'Graph' })).toHaveAttribute('href', '/r/acme/app/pull/4/graph');
  await expect(page.locator('#decision')).toContainText(
    'Engine verdict blocked: hard gates fail, 3 active blockers, 1 required rule not evaluated',
  );
  await expect(cycle.getByText('Allowed alternatives:')).toBeVisible();
  await expect(cycle.locator('.cycle-legend')).toHaveText(/added by this PR\s*existed before/);
  await expect(page.locator('#decision')).toContainText('layers: selector matches no module');
  await page.getByText('Advisories on other dependencies · never block').click();
  await expect(page.getByText('UI skips a layer.')).toBeVisible();
  const advisories = page.locator('#advisories');
  await expect(advisories).toContainText(
    'layering · Suggested move: Move the functionality: lower the distance, so the coupled code lives in one module.',
  );
  await expect(advisories.getByText('owner decision')).toBeVisible();
  await expect(advisories).toContainText('metric fan_out: 3 → 5 (at most 4)');
  await expect(advisories).not.toContainText('(no module)');
  // known debt: its coupling, and no move to make
  await page.getByText('Known issues accepted earlier').click();
  const accepted = page.locator('#accepted');
  await expect(accepted.getByRole('columnheader', { name: 'Volatility' })).toBeVisible();
  await expect(accepted).toContainText('model');
  await expect(accepted).not.toContainText('Suggested move');
  await page.getByText('Known issues fixed').click();
  await expect(page.locator('#fixed')).toContainText('old → gone');
  await page.getByText('To measure more').click();
  const evidence = page.getByRole('table');
  await expect(evidence.getByRole('columnheader', { name: 'Result' })).toBeVisible();
  await expect(evidence.getByRole('columnheader', { name: 'Gate' })).toHaveCount(0);
  await expect(evidence.getByRole('row', { name: /Testability/ })).toContainText('3 of 3 ("classified source files")');
  await expect(evidence.getByRole('row', { name: /Drift/ })).toContainText('nothing to measure');
  await expect(evidence.getByRole('row', { name: /Drift/ })).toContainText('not applicable');
  await expectClean(page);

  // a fold first in its box draws no rule above it; a dimension that warns stands out
  await expect(page.locator('#tasks').locator('xpath=ancestor::section').locator('details.disclosure')).toHaveCSS(
    'border-top-width',
    '0px',
  );
  await expect(
    evidence
      .getByRole('row', { name: /Change locality/ })
      .getByRole('cell')
      .last(),
  ).toHaveCSS('font-weight', '600');
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

for (const graph of [false, true]) {
  for (const reason of ['unknown_engine_identity', 'missing_required_coverage', 'stale_inputs']) {
    test(`PR diagnostics: ${reason} stays actionable on ${graph ? 'Graph' : 'Findings'} without admitted data`, async ({
      page,
    }, info) => {
      const next =
        reason === 'unknown_engine_identity'
          ? 'Update the workflow producer and re-run.'
          : reason === 'stale_inputs'
            ? 'Restore authorization and re-run.'
            : 'Install the missing tool and re-run.';
      const check = {
        ...analysisCheck,
        reason: reason + ' (evidence)',
        next,
        detail: '<img src=x onerror=alert(1)> & missing evidence',
        also: ['missing_required_coverage: six rules not measured. Next: Supply the evidence.'],
        approved:
          reason === 'unknown_engine_identity'
            ? {
                engine_version: 'v3.2.0',
                image_digest: 'sha256:' + '9'.repeat(64),
                platform: 'linux/amd64',
                action_ref: 'cadf35c0d4885a49a3a1482b876f2a456d20de06',
              }
            : undefined,
      };
      const blocked = {
        ...pull,
        check,
        run_id: 0,
        result: 'action required',
        tone: 'attention',
        headline: 'Action required',
        unavailable: 'This result asks for action before its findings mean anything.',
        decision: undefined,
        cycles: null,
        other: null,
        tasks: '',
        violation_count: 0,
      };
      await open(page, '/r/acme/app/pull/4' + (graph ? '/graph' : ''), {
        pull: blocked,
        pullGraph: { ...pullGraph, pull: blocked, view: null, unavailable: blocked.unavailable },
      });
      const analysis = page.getByRole('region', { name: 'PR analysis', exact: true });
      await expect(analysis.getByText(reason + ' (evidence)', { exact: false })).toBeVisible();
      await expect(analysis.getByText(next, { exact: false })).toBeVisible();
      await expect(analysis.getByText(check.detail, { exact: true })).toBeVisible();
      await expect(analysis.getByText('Analysis time (UTC): Not recorded', { exact: true })).toBeVisible();
      await expect(page.getByRole('link', { name: 'Run 88 · attempt 1' })).toHaveAttribute(
        'href',
        'https://github.com/acme/app/actions/runs/88/attempts/1',
      );
      await expect(
        page.getByRole('heading', { name: graph ? 'Graph unavailable' : 'Findings unavailable', exact: true }),
      ).toBeVisible();
      await expect(
        page.getByRole('heading', { name: graph ? 'Findings unavailable' : 'Graph unavailable', exact: true }),
      ).toHaveCount(0);
      await expect(page.locator('.svelte-flow__node')).toHaveCount(0);
      await expect(page.getByRole('heading', { name: /^Rule violations/ })).toHaveCount(0);
      await expect(page.getByRole('heading', { name: 'For coding agents' })).toHaveCount(0);
      await expect(analysis.locator('img, script')).toHaveCount(0);
      if (check.approved) {
        await expect(
          analysis.getByText('Current approved workflow pins — not the producer of this analysis'),
        ).toBeVisible();
        await expect(analysis.getByText('Engine v3.2.0 · linux/amd64', { exact: true })).toBeVisible();
      }
      await expectClean(page);
      if (reason === 'unknown_engine_identity') {
        await page.screenshot({
          path: info.outputPath(graph ? 'blocked-graph.png' : 'blocked-findings.png'),
          fullPage: true,
        });
      }
    });
  }
}

test('PR diagnostics: historical producer stays fixed across App upgrades; partial evidence remains qualified', async ({
  page,
}, info) => {
  for (const version of ['v0.12.0', 'v0.13.0']) {
    for (const graph of [false, true]) {
      await page.unrouteAll();
      const partial = {
        ...pull,
        check: { ...analysisCheck, also: ['missing_required_coverage: layers. Next: Supply the missing evidence.'] },
        decision: {
          ...pull.decision,
          hard_gates: 'unmeasured',
          detail: 'hard gates unmeasured, 3 active blockers, 1 required rule not evaluated',
        },
      };
      await open(page, '/r/acme/app/pull/4' + (graph ? '/graph' : ''), {
        appBuild: { ...appBuild, version },
        pull: partial,
        pullGraph: { ...pullGraph, pull: partial, view: { ...pullGraph.view, incomplete: true } },
      });
      await expect(page.getByText(analysisCheck.producer, { exact: false })).toBeVisible();
      await expect(page.getByText('Action version / SHA: Not recorded', { exact: true })).toBeVisible();
      await expect(page.getByText('Analysis time (UTC): Not recorded', { exact: true })).toBeVisible();
      await page.getByText('About this App · ' + version, { exact: true }).click();
      await expect(page.locator('.app-about').getByText(appBuild.source_revision, { exact: true })).toBeVisible();
      await expect(page.locator('.app-about').getByText(appBuild.built_at, { exact: true })).toBeVisible();
      await expect(
        page
          .locator('.app-about')
          .getByText('Serving App build, not the Engine or Action that produced a PR analysis.'),
      ).toBeVisible();
      if (graph) {
        await expect(page.locator('#incomplete')).toBeVisible();
        await expect(page.locator('.svelte-flow__node').first()).toBeVisible();
      } else {
        await expect(
          page.getByText('Partial evidence: findings cover only what was measured.', { exact: false }),
        ).toBeVisible();
        await expect(page.getByRole('heading', { name: 'Evidence', exact: true })).toBeVisible();
      }
      await expectClean(page);
      if (version === 'v0.13.0')
        await page.screenshot({
          path: info.outputPath(graph ? 'partial-graph-about.png' : 'partial-findings-about.png'),
          fullPage: true,
        });
    }
  }
});

test('PR diagnostics: missing historical metadata and unsafe links stay explicit and escaped', async ({ page }) => {
  await open(page, '/r/acme/app/pull/4', {
    pull: {
      ...pull,
      check: { ...analysisCheck, producer: '', head_sha: '', run_id: 0, next: '[unsafe](javascript:alert(1))' },
      url: 'https://github.com.evil.example/acme/app/pull/4',
      checks_url: 'javascript:alert(1)',
    },
    appBuild: {},
  });
  await expect(page.getByText('Analysis producer (claimed): Not recorded', { exact: true })).toBeVisible();
  await expect(page.getByText('[unsafe](javascript:alert(1))', { exact: false })).toBeVisible();
  await expect(page.getByText('About this App · build not recorded', { exact: true })).toBeVisible();
  await expect(page.locator('a[href^="javascript:"], a[href*="evil.example"]')).toHaveCount(0);
  await expectClean(page);
});

test('PR diagnostics: run links preserve owners and repositories named pull', async ({ page }) => {
  for (const repository of ['acme/app', 'acme/pull', 'pull/app', 'pull/pull']) {
    await page.unrouteAll();
    await open(page, '/r/acme/app/pull/4', {
      pull: { ...pull, url: 'https://github.com/' + repository + '/pull/4' },
    });
    await expect(page.getByRole('link', { name: 'Run 88 · attempt 1' })).toHaveAttribute(
      'href',
      'https://github.com/' + repository + '/actions/runs/88/attempts/1',
    );
  }
});

test('PR diagnostics: an explicit no-upload producer is not missing history', async ({ page }) => {
  await open(page, '/r/acme/app/pull/4', {
    pull: {
      ...pull,
      check: { ...analysisCheck, producer: 'no upload', run_id: 0, run_attempt: 0 },
      unavailable: 'This result came without a report, so there is nothing to show here.',
      run_id: 0,
    },
  });
  await expect(page.getByText('Analysis producer (claimed): no upload', { exact: true })).toBeVisible();
  await expect(page.getByRole('heading', { name: 'Findings unavailable', exact: true })).toBeVisible();
  await expectClean(page);
});

test('PR diagnostics: keyboard disclosures and long metadata fit a phone viewport', async ({ page }) => {
  await page.setViewportSize({ width: 390, height: 844 });
  await open(page, '/r/acme/app/pull/4');
  const details = page.getByText('Analysis details', { exact: true });
  await details.press('Enter');
  await expect(details).toBeFocused();
  await expect(page.getByText(analysisCheck.report_digest, { exact: true })).toBeVisible();
  const about = page.getByText('About this App · ' + appBuild.version, { exact: true });
  await about.press('Enter');
  await expect(about).toBeFocused();
  await expect(page.locator('.app-about').getByText(appBuild.source_revision, { exact: true })).toBeVisible();
  expect(await page.evaluate(() => document.documentElement.scrollWidth)).toBeLessThanOrEqual(390);
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
  await expect(page.getByRole('button', { name: '1 added by this PR' })).toHaveAttribute('aria-pressed', 'true');
  await expect(page.locator('.g-edge .halo')).toHaveCount(1);
  await expect(page.getByRole('button', { name: /^api to db, .*, added by this PR$/ })).toBeVisible();
  await expect(page.getByRole('button', { name: /^db to api, .*violation, contract coupling$/ })).toBeVisible();
  await expectClean(page);

  await page.getByRole('button', { name: '1 added by this PR' }).click();
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
  // read-only state as a cross or a tick, never an empty circle that looks like a radio input
  await expect(ai.locator('.checks li.not_met .mark')).toHaveText('✗');
  await expect(discovery.locator('.checks li.met .mark')).toHaveText('✓');
  await expect(discovery.getByText('Allowed by @carol on 1 September 2026')).toBeVisible();
  await expect(discovery.getByText('The App does not start managed discovery yet.', { exact: false })).toBeVisible();
  // a feature the App does not run is never offered for allowing; a consent given earlier can still be revoked
  await expect(discovery.getByRole('button', { name: /Allow/ })).toHaveCount(0);
  await expect(discovery.getByRole('button', { name: 'Revoke' })).toBeVisible();
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
  // the AI box is under the editor: the toolbar takes the user to it
  await expect(page.locator('#policy-hint')).toHaveText(
    'Check validates the schema. The Archfit check on the draft pull request validates the rest.',
  );
  await page.getByRole('button', { name: 'Ask AI for a proposal' }).click();
  await expect(page.getByRole('textbox', { name: 'Instruction' })).toBeFocused();
  await expectClean(page);
});

test('policy: Check and Save send the text, the head and the CSRF header', async ({ page }) => {
  const writes = await open(page, '/r/acme/app/policy');
  const editor = page.getByRole('textbox', { name: 'Policy text, .archfit.yaml' });
  await expect(editor).toContainText('rules: []');
  await editor.click();
  await page.keyboard.press('ControlOrMeta+End');
  await page.keyboard.insertText('# edited');
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

test("policy: a save's 404 is a sentence on the page, and the editor keeps its text", async ({ page }) => {
  await open(page, '/r/acme/app/policy', { saveNotFound: true });
  const editor = page.getByRole('textbox', { name: 'Policy text, .archfit.yaml' });
  await editor.click();
  await page.keyboard.press('End');
  await page.keyboard.type(' # kept');
  await page.getByRole('button', { name: 'Save to draft PR' }).click();
  await expect(page.getByRole('alert')).toContainText('Not found.');
  await expect(page.getByRole('heading', { name: 'This page cannot be shown' })).toHaveCount(0);
  await expect(editor).toContainText('# kept');
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
  // One input event per string: CodeMirror reads typed keys back from the DOM, and fast keys on a slow runner can land
  // out of order ("mreo"). insertText is one change at the cursor, as a paste or an IME commit.
  await page.keyboard.insertText('# mine');
  await expect(editor).toContainText('# mine');
  await page.getByRole('textbox', { name: 'Instruction', exact: true }).fill('split module a');
  await page.getByRole('button', { name: 'Propose' }).click();
  await expect.poll(() => writes.length).toBe(2);
  await editor.click();
  await page.keyboard.press('ControlOrMeta+End');
  await page.keyboard.insertText(' and more');
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
  await page.keyboard.insertText('# edited');
  await page.getByRole('button', { name: 'Save to draft PR' }).click();
  await expect(page.getByRole('status')).toHaveText('Saved as a commit on the draft pull request.');
  expect(writes[0].postDataJSON().content).toBe('version: 2\r\nrules: []\r\n# edited');
  await expectClean(page);
});

test("a repository the user cannot see reads as one that does not exist: the API's Not found, no tabs", async ({
  page,
}) => {
  const shown: string[] = [];
  for (const path of ['/r/acme/hidden', '/r/acme/missing/pull/4']) {
    await open(page, path);
    await expect(page.getByRole('heading', { name: 'This page cannot be shown' })).toBeVisible();
    await expect(page.getByText('Not found.')).toBeVisible();
    await expect(page.getByRole('navigation', { name: 'Repository' })).toHaveCount(0);
    await expect(page).toHaveTitle('Error · Archfit');
    shown.push(await page.locator('main').innerHTML());
  }
  expect(shown[0]).toBe(shown[1]);
  await expectClean(page);
});

test('waiting: a repository of an account no operator approved shows the pitch and its status, no tabs', async ({
  page,
}) => {
  await open(page, '/r/octo/tool');
  await expect(page.getByRole('heading', { name: 'Architecture checks on every pull request' })).toBeVisible();
  await expect(page.getByRole('status').getByText('Your installation on octo is waiting for approval.')).toBeVisible();
  await expect(page.getByRole('navigation', { name: 'Repository' })).toHaveCount(0);
  await expect(page.getByRole('link', { name: 'Sign in with GitHub' })).toHaveCount(0);
  await expect(page.getByRole('link', { name: 'How approval works' })).toHaveAttribute('href', /#who-the-app-serves$/);
  await expect(page).toHaveTitle('Waiting for approval · Archfit');
  await expectClean(page);
});

test('repositories: an account waiting for approval says so in its row', async ({ page }) => {
  await open(page, '/');
  await expect(page.getByRole('row', { name: /octo\/tool/ }).getByText('waiting for approval')).toHaveClass(
    /status attention/,
  );
  await expect(page.getByRole('link', { name: 'Admin' })).toHaveCount(0);
});

test('admin: an operator approves a waiting account and revokes one after asking once', async ({ page }) => {
  const writes = await open(page, '/admin', { operator: true });
  await expect(page.getByRole('link', { name: 'Admin 1 waiting' })).toHaveAttribute('href', '/admin');
  await expect(page).toHaveTitle('Installations · Archfit');
  await expect(page.getByRole('heading', { name: 'Waiting 1' })).toBeVisible();
  await expect(page.getByText('ledger · private')).toBeVisible();
  await expect(page.getByText('Approved by vladikk on 18 October 2026')).toBeVisible();
  await expect(page.getByRole('button', { name: 'Revoke alexei-led' })).toHaveCount(0);
  await expectClean(page);

  await page.getByRole('button', { name: 'Approve octo' }).click();
  await expect(page.getByText('Approved installation 7.')).toBeVisible();
  await page.getByRole('button', { name: 'Revoke acme' }).click();
  await expect(page.getByText('Revoke acme? Checks stop on its next event.')).toBeVisible();
  await page.getByRole('button', { name: 'Revoke', exact: true }).click();
  await expect(page.getByText('Revoked installation 3.')).toBeVisible();
  expect(writes.map((w) => new URL(w.url()).pathname)).toEqual([
    '/api/admin/installations/7/approve',
    '/api/admin/installations/3/revoke',
  ]);
  for (const w of writes) expect(w.headers()['x-csrf-token']).toBe(session.csrf);
});

test("admin: anyone else gets the API's Not found", async ({ page }) => {
  await open(page, '/admin');
  await expect(page.getByRole('heading', { name: 'This page cannot be shown' })).toBeVisible();
  await expect(page.getByRole('link', { name: 'Admin' })).toHaveCount(0);
});

test('a page reads its data beside the session and shows its frame at once', async ({ page }) => {
  let release = () => {};
  const holdSession = new Promise<void>((resolve) => (release = resolve));
  const overview = page.waitForRequest('**/api/repos/acme/app/overview');
  await open(page, '/r/acme/app', { holdSession });
  await overview; // asked before the session answered
  await expect(page.getByRole('status', { name: 'Loading' })).toBeVisible();
  release();
  await expect(page.getByRole('heading', { name: 'acme/app' })).toBeVisible();
  await expect(page.getByRole('status', { name: 'Loading' })).toHaveCount(0);
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

const everyPage = [
  '/',
  '/r/acme/app',
  '/r/acme/app/pull/4',
  '/r/acme/app/graph',
  '/r/acme/app/pull/4/graph',
  '/r/acme/app/data-sharing',
  '/r/acme/app/policy',
  '/r/octo/tool',
  '/admin',
];

// rendered waits for the page's own content: its heading, nothing still loading, and the graph laid out.
async function rendered(page: Page, path: string) {
  await expect(page.locator('main h1').first(), path).toBeVisible();
  await expect(page.locator('main').getByText('Loading…'), path).toHaveCount(0);
  if (path.endsWith('/graph')) await expect(page.locator('.svelte-flow__node').first(), path).toBeVisible();
}

test('every page: content at most 1280px and centred, the licences in the footer', async ({ page }) => {
  await page.setViewportSize({ width: 1600, height: 900 });
  for (const path of everyPage) {
    await page.unrouteAll();
    await open(page, path, { operator: path === '/admin' });
    await rendered(page, path);
    await expect(page.getByRole('link', { name: 'Third-party licences' })).toHaveAttribute('href', '/licenses.txt');
    await expect(page.getByRole('link', { name: 'Documentation' })).toHaveAttribute('href', /docs\/user-guide\.md$/);
    for (const box of [await page.locator('main').boundingBox(), await page.locator('footer').boundingBox()]) {
      expect(box!.width, path).toBeLessThanOrEqual(1280);
      expect(Math.abs(box!.x - (1600 - box!.x - box!.width)), path).toBeLessThanOrEqual(1);
    }
  }
});

test('every page: no sideways scroll at phone width', async ({ page }) => {
  await page.setViewportSize({ width: 390, height: 844 });
  for (const path of everyPage) {
    await page.unrouteAll();
    await open(page, path, { operator: path === '/admin' });
    await rendered(page, path);
    const width = await page.evaluate(() => document.documentElement.scrollWidth);
    expect(width, path).toBeLessThanOrEqual(390);
  }
});

test('pull request: on a phone the cycle stacks, and its labels stay readable', async ({ page }) => {
  await page.setViewportSize({ width: 390, height: 844 });
  await open(page, '/r/acme/app/pull/4');
  const label = page.locator('.cycle-drawing .arrow-label').first();
  await expect(label).toBeVisible();
  // the rendered height of 12px text: at least 11px, so the drawing is not scaled down
  expect((await label.boundingBox())!.height).toBeGreaterThanOrEqual(11);
  const boxes = await page.locator('.cycle-drawing .module rect').evaluateAll((rs) =>
    rs.map((r) => {
      const b = r.getBoundingClientRect();
      return Math.round(b.x + b.width / 2);
    }),
  );
  expect(new Set(boxes).size).toBe(1);

  // the findings tables read as one block per dependency: nothing off-screen, no row taller than its cells
  await page.getByText('Known issues accepted earlier').click();
  for (const wrap of await page.locator('.table-wrap:visible').all()) {
    const fit = await wrap.evaluate((w) => w.scrollWidth <= w.clientWidth);
    expect(fit).toBe(true);
  }
  // a cell as tall as its row but not its content is the empty block: each cell's box against what it renders, with
  // room for its padding and its label's line
  const slack = await page.locator('table.cards tbody td:visible').evaluateAll((cells) =>
    cells.map((td) => {
      const content = document.createRange();
      content.selectNodeContents(td);
      const rects = [...content.getClientRects()].filter((r) => r.width > 0 && r.height > 0);
      const used = Math.max(...rects.map((r) => r.bottom)) - Math.min(...rects.map((r) => r.top));
      return Math.round(td.getBoundingClientRect().height - used);
    }),
  );
  expect(slack.length).toBeGreaterThan(0);
  for (const s of slack) expect(s).toBeLessThanOrEqual(32);
  await expect(page.locator('table.cards td.sdv').first()).toBeVisible();
});

test('policy: a long policy scrolls inside the editor, and a line link scrolls to it', async ({ page }) => {
  await page.setViewportSize({ width: 1280, height: 800 });
  const content = ['version: 2', 'rules: []', ...Array.from({ length: 200 }, (_, i) => `# note ${i}`)].join('\n');
  await open(page, '/r/acme/app/policy#L150', { policy: { content } });
  const editor = page.locator('.editor .cm-editor');
  await expect(page.locator('.cm-activeLine')).toHaveText('# note 147');
  await expect(page.locator('.cm-activeLine')).toBeInViewport();
  expect((await editor.boundingBox())!.height).toBeLessThanOrEqual(800 * 0.6 + 1);
  const scroller = await page.locator('.editor .cm-scroller').evaluate((e) => [e.scrollHeight, e.clientHeight]);
  expect(scroller[0]).toBeGreaterThan(scroller[1]);
});

// The screenshots the user guide shows. Regenerate them with `npm run docs:shots` after a change to the pages they show;
// they run only then, so the smoke suite stays fast.
const shotsDir = new URL('../../docs/images/', import.meta.url);

// callouts numbers each element and labels it in the margin beside the content, drawn over the page for the screenshot.
async function callouts(page: Page, marks: { at: Locator; text: string }[]) {
  const main = await page.locator('main').boundingBox();
  const items = await Promise.all(
    marks.map(async (m, i) => ({ n: i + 1, text: m.text, box: (await m.at.boundingBox())! })),
  );
  await page.evaluate(
    ({ items, labelX }) => {
      const layer = document.createElement('div');
      layer.style.cssText = 'position:absolute;inset:0;pointer-events:none;z-index:9999;font:13px system-ui,sans-serif';
      for (const it of items) {
        const y = it.box.y + window.scrollY;
        const dot = document.createElement('div');
        dot.textContent = String(it.n);
        dot.style.cssText = `position:absolute;left:${it.box.x - 34}px;top:${y - 12}px;width:24px;height:24px;border-radius:50%;background:#0969da;color:#fff;font-weight:700;display:grid;place-items:center;box-shadow:0 0 0 3px #fff`;
        const box = document.createElement('div');
        box.textContent = `${it.n}  ${it.text}`;
        box.style.cssText = `position:absolute;left:${labelX}px;top:${y - 8}px;width:200px;padding:6px 10px;border-radius:6px;background:#fff;color:#1f2328;border:1px solid #0969da;box-shadow:0 2px 8px rgba(0,0,0,.15)`;
        layer.append(dot, box);
      }
      document.body.append(layer);
    },
    { items: items.map((it) => ({ n: it.n, text: it.text, box: it.box })), labelX: main!.x + main!.width + 16 },
  );
}

test('docs: the screenshots the user guide shows', async ({ page }) => {
  test.skip(!process.env.ARCHFIT_DOCS_SHOTS, 'set ARCHFIT_DOCS_SHOTS=1 to regenerate the guide screenshots');
  await page.setViewportSize({ width: 1800, height: 900 });

  await open(page, '/r/octo/tool');
  await expect(page.getByRole('status').getByText('waiting for approval')).toBeVisible();
  await callouts(page, [
    { at: page.locator('.pill'), text: 'Shown at once: this account waits for an operator.' },
    { at: page.getByRole('status'), text: 'Nothing runs and nothing is posted until it is approved.' },
    { at: page.getByRole('link', { name: 'How approval works' }), text: 'The approval rules, in this guide.' },
  ]);
  await page.screenshot({ path: new URL('waiting-page.png', shotsDir).pathname, fullPage: true });

  await page.unrouteAll();
  await open(page, '/r/acme/app', { overview: installed });
  await expect(page.getByRole('heading', { name: 'Needs you' })).toBeVisible();
  await callouts(page, [
    { at: page.getByLabel(/Policy owners/), text: 'One GitHub login per line. Owners approve changes to the policy.' },
    {
      at: page.getByRole('button', { name: 'Open the onboarding pull request' }),
      text: 'Opens a pull request with the settings.',
    },
    { at: page.locator('ol.track'), text: 'Setup: each step is marked done or current.' },
  ]);
  await page.screenshot({
    path: new URL('onboarding-page.png', shotsDir).pathname,
    fullPage: true,
    clip: { x: 0, y: 0, width: 1800, height: 820 },
  });
});

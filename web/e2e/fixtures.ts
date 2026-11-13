import { readFileSync } from 'node:fs';
import AxeBuilder from '@axe-core/playwright';
import { expect, type Locator, type Page, type Request } from '@playwright/test';



export const csp = readFileSync(new URL('../csp.txt', import.meta.url), 'utf8').trim();

export const appBuild = {
  version: 'v0.12.0',
  source_revision: 'b'.repeat(40),
  built_at: '2026-10-25T10:00:00Z',
  runtime_revision: 'archfit-public-123',
  modified: false,
};

export const session = { login: 'alice', csrf: 'csrf-token-1', build: appBuild };


export const analysisCheck = {
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


export const repos = {
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


export const steps = (current: number) =>
  ['Installed', 'Workflow merged', 'First report', 'Policy merged', 'Advisory', 'Required'].map((label, i) => ({
    label,
    done: i < current,
    current: i === current,
  }));


export const reporting = {
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
  main_sha: '',
  main_at: '',
  pulls_to_update: 0,
  pulls_unread: 0,
  steps: steps(4),
  next: 'Make the gate required. Rehearse it first on one forbidden and one legitimate change.',
  next_how: 'A forbidden change must show a rule violation on archfit/state. Then merge the settings pull request.',
  stage: 'Gate is advisory',
  diagnostic: '',
  draft_url: '',
  draft_state: '',
  banner: 'The trial ends on 1 November 2026.',
  admin: true,
  can_start: false,
  can_discover: false, // discovery is no step once the policy is merged
  pulls_url: 'https://github.com/acme/app/pulls',
  checks_url: 'https://github.com/acme/app/commits/main',
};


// ago is a time that long before now, so the page words its age the same on every run.
export const hour = 3600e3;

export const ago = (ms: number) => new Date(Date.now() - ms).toISOString();


export const movedPull = {
  number: 6,
  title: 'refactor: split orders',
  state: 'action required',
  tone: 'attention',
  reason: 'Action required',
  href: '/r/acme/app/pull/6',
  rerun:
    'Pull request author: The target branch moved, and with it this pull request closes a module cycle: orders, billing. Update the branch (Update branch on GitHub, gh pr update-branch, or merge the target) to measure it again.',
};


// watching is a repository whose gate runs with agents: the main branch was checked, a pull request waits for an update
// after the target moved.
export const watching = {
  ...reporting,
  main_state: 'healthy',
  main_tone: 'success',
  main_reason: 'Architecture healthy',
  main_sha: '9f3a2c1',
  main_at: ago(2 * hour),
  pulls: [...reporting.pulls, movedPull],
  pulls_to_update: 1,
  pulls_unread: 0,
};


export const installed = {
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


export const workflowMerged = {
  ...installed,
  steps: steps(1),
  next: 'Run discovery: it proposes a first policy as a pull request.',
  next_how: 'Discovery runs in your own workflow.',
  stage: 'Workflow merged',
  diagnostic: 'Run discovery (workflow_dispatch with `mode` set to `discovery`)',
  can_start: false,
  can_discover: true,
};


export const workflowYAML = 'name: archfit\non:\n  pull_request:\n';


export const workflowNeeded = {
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


export const aiChecks = (allowed: boolean) => [
  {
    label: 'Allowed for acme',
    state: allowed ? 'met' : 'not_met',
  },
  { label: "This repository's settings say data_mode: managed", state: 'met' },
];


export const sharing = (allowed: boolean) => ({
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


export const direction = (from: string, to: string, origin: string, n: number) => ({
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


export const pull = {
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


export const findings = {
  ...pull,
  unavailable: '',
  branch: 'main',
  compared: false,
  advisory_count: 1,
  advisories: pull.advisories.slice(0, 1),
  attention_reason: '1 advisory; testability and operations are partially measured',
};


export const gnode = (name: string, layer: string, policy_line: number, volatility = '', subdomain = '') => ({
  name,
  layer,
  policy_line,
  volatility,
  subdomain,
});

export const glink = (from: string, to: string, status: string, count: number, more: object = {}) => ({
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


export const view = {
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


export const repoGraph = {
  unavailable: '',
  view,
  stale_reason: '',
  policy_href: 'https://github.com/acme/app/blob/main/.archfit.yaml',
  branch: 'main',
  pull: null,
  added: 0,
};


export const pullHead = Object.fromEntries(
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

export const pullGraph = {
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


// installations is the operators' page: one account waiting, one approved by hand, one by the App's configuration.
export const installations = {
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


export interface Mock {
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
  serverError?: { status: number; sentence: string }; // the server answers the page as its error page
  holdSession?: Promise<void>; // /api/session answers once this settles
}


// open answers /api in the browser from the mock, opens the page, and returns the writes the page sent. Each page records the CSP violations
// it sees, and every test fails on one.
export async function open(page: Page, path: string, mock: Mock = {}) {
  const writes: Request[] = [];
  let allowed = false;
  await page.route('**/api/**', async (route) => {
    const req = route.request();
    const url = new URL(req.url());
    const json = (status: number, body: object) => route.fulfill({ status, json: body });
    if (req.method() === 'POST') {
      writes.push(req);
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
      case '/api/repos/acme/app/home':
        return json(200, JSON.parse(readFileSync(new URL('../../internal/control/ui/testdata/home/clean.json', import.meta.url), 'utf8')));
      case '/api/repos/acme/app/overview':
        return json(200, mock.overview ?? reporting);
      case '/api/repos/acme/app/data-sharing':
        if (mock.sharingDown)
          return json(502, {
            error: 'GitHub or an Archfit service failed. Nothing was changed. Try again.',
          });
        return json(200, sharing(allowed));
      case '/api/repos/acme/app/pulls/4':
        return json(200, mock.pull ?? JSON.parse(readFileSync(new URL('../../internal/control/ui/testdata/pull/pass.json', import.meta.url), 'utf8')));
      case '/api/repos/acme/app/findings':
        return json(200, findings);
      case '/api/repos/acme/app/graph':
        return json(200, mock.graph ?? repoGraph);
      case '/api/repos/acme/app/pulls/4/graph':
        return json(200, mock.pullGraph ?? pullGraph);
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


export async function expectClean(page: Page) {
  const violations = await page.evaluate(() => (window as unknown as { cspViolations: string[] }).cspViolations);
  expect(violations).toEqual([]);
  for (const scheme of ['light', 'dark'] as const) {
    await page.emulateMedia({ colorScheme: scheme });
    const axe = await new AxeBuilder({ page }).analyze();
    expect(axe.violations.map((v) => `${scheme}: ${v.id} ${v.nodes.map((n) => n.target).join(', ')}`)).toEqual([]);
  }
}


export const everyPage = [
  '/',
  '/r/acme/app',
  '/r/acme/app/pull/4',
  '/r/acme/app/graph',
  '/r/acme/app/pull/4/graph',
  '/r/acme/app/data-sharing',
  '/r/octo/tool',
  '/admin',
];


// rendered waits for the page's own content: its heading, nothing still loading, and the graph laid out.
export async function rendered(page: Page, path: string) {
  await expect(page.locator('main h1').first(), path).toBeVisible();
  await expect(page.locator('main').getByText('Loading…'), path).toHaveCount(0);
  if (path.endsWith('/graph')) await expect(page.locator('.svelte-flow__node').first(), path).toBeVisible();
}


// The screenshots the user guide shows. Regenerate them with `npm run docs:shots` after a change to the pages they show;
// they run only then, so the smoke suite stays fast.
export const shotsDir = new URL('../../docs/images/', import.meta.url);


// callouts numbers each element and labels it in the margin beside the content, drawn over the page for the screenshot.
export async function callouts(page: Page, marks: { at: Locator; text: string }[]) {
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

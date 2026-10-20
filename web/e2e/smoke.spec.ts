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

interface Mock {
  signedIn?: boolean;
  tokenRejected?: boolean; // the session stands, but GitHub rejects the user's token on the first repository call
  overview?: object;
  sharingDown?: boolean; // the consent store cannot be read
  savedUnread?: boolean; // the write is saved, but the App cannot read it back in the same answer
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

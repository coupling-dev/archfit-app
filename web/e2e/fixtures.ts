import { readFileSync } from 'node:fs';
import AxeBuilder from '@axe-core/playwright';
import { expect, type Page, type Request } from '@playwright/test';



export const csp = readFileSync(new URL('../csp.txt', import.meta.url), 'utf8').trim();

export const appBuild = {
  version: 'v0.12.0',
  source_revision: 'b'.repeat(40),
  built_at: '2026-10-25T10:00:00Z',
  runtime_revision: 'archfit-public-123',
  modified: false,
};

export const session = { login: 'alice', csrf: 'csrf-token-1', build: appBuild };


export const repos = JSON.parse(readFileSync(new URL('../../internal/control/ui/testdata/repos/all-states.json', import.meta.url), 'utf8'));


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
  repos?: object;
  passkeys?: object;
  appBuild?: object;
  serverError?: { status: number; sentence: string }; // the server answers the page as its error page
  holdSession?: Promise<void>; // /api/session answers once this settles
}


// open answers /api in the browser from the mock, opens the page, and returns the writes the page sent. Each page records the CSP violations
// it sees, and every test fails on one.
export async function open(page: Page, path: string, mock: Mock = {}) {
  const writes: Request[] = [];
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
      return json(200, { notice: 'The change was saved.' });
    }
    if (mock.signedIn === false) return json(401, { error: 'Sign in to continue.' });
    if (mock.tokenRejected && url.pathname !== '/api/session') return json(401, { error: 'Sign in to continue.' });
    if (url.pathname.startsWith('/api/repos/octo/'))
      return json(403, { error: 'This account is waiting for approval.', waiting: 'octo' });
    const fixture = (name: string) => JSON.parse(readFileSync(new URL('../../internal/control/ui/testdata/' + name + '.json', import.meta.url), 'utf8'));
    const owner = /^\/api\/repos\/acme\/app\/owner\/(first|workflow|confirm|revert)\/view$/.exec(url.pathname);
    if (owner) return json(200, fixture('owner/' + owner[1]));
    switch (url.pathname) {
      case '/api/repos/acme/app/policy':
        return json(200, fixture('policy/typical'));
      case '/api/repos/acme/app/requests/431':
        return json(200, fixture('request/open'));
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
        return json(200, mock.repos ?? repos);
      case '/api/repos/acme/app/passkeys':
        return json(200, mock.passkeys ?? { passkeys: [] });
      case '/api/repos/acme/app/home':
        return json(200, JSON.parse(readFileSync(new URL('../../internal/control/ui/testdata/home/clean.json', import.meta.url), 'utf8')));
      case '/api/repos/acme/app/pulls/4':
        return json(200, JSON.parse(readFileSync(new URL('../../internal/control/ui/testdata/pull/pass.json', import.meta.url), 'utf8')));
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
  '/r/octo/tool',
  '/admin',
];


// rendered waits for the page's own content: its heading, nothing still loading, and the graph laid out.
export async function rendered(page: Page, path: string) {
  await expect(page.locator('main h1').first(), path).toBeVisible();
  await expect(page.locator('main').getByText('Loading…'), path).toHaveCount(0);
}

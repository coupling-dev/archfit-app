import { expect, test, type Page } from '@playwright/test';
import { assertion, authenticator, ceremony, expectClean, open, origin, type Handlers } from './support';

const path = '/r/acme/app/requests/431';
const api = '/api/repos/acme/app';
const req = `${api}/requests/431`;
const digest = 'd'.repeat(64);

// request is GET /api/repos/acme/app/requests/431 in the shape of requestJSON (internal/control/ui/requests.go).
const request = {
  number: 431,
  state: 'open',
  reason: 'netem needs the retry helper of pkg_util to apply a rule again after a container restart.',
  rule: 'graph',
  from: 'pkg_tc',
  to: 'pkg_util',
  expires: '2026-12-01',
  record: 'pkg/tc/netem.go:12 graph pkg_tc -> pkg_util is not an allowed dependency\n  balance: 9/10 (min 7)',
  verified: true,
  balance: { value: 9, max: 10, min: 7 },
  policy: true,
  modules: [
    { name: 'pkg_tc', layer: 'adapters', volatility: 'high', depends_on: ['pkg_container'] },
    { name: 'pkg_util', volatility: 'low', subdomain: 'generic', depends_on: [] },
  ],
  allowed: false,
  earlier: [
    {
      kind: 'exception',
      rule: 'graph',
      from: 'pkg_tc',
      to: 'pkg_util',
      why: 'Until the port lands.',
      expires: '2026-10-01',
    },
  ],
};

// preview is GET …/preview in the shape of previewJSON: what each answer writes and which pull requests change.
function preview(url: URL): [number, object] {
  const answer = url.searchParams.get('answer');
  const pulls = [
    { number: 12, status: 'waiting', waits: true, clears: 1, left: 0 },
    { number: 15, status: 'fail', waits: false, clears: 1, left: 2 },
  ];
  const hunk = '--- a/.archfit.yaml\n+++ b/.archfit.yaml\n@@ -4,3 +4,4 @@\n';
  switch (answer) {
    case 'approve':
      return [
        200,
        {
          answer,
          diff: hunk + '   pkg_tc:\n     depends_on:\n       - pkg_container\n+      - pkg_util # request #431\n',
          policy_digest: digest,
          loosens: true,
          pulls,
        },
      ];
    case 'approve-until': {
      const until = url.searchParams.get('until') ?? request.expires;
      return [
        200,
        {
          answer,
          until,
          diff: `${hunk}+  - rule: graph # request #431\n+    from: pkg_tc\n+    to: pkg_util\n+    expires: ${until}\n`,
          policy_digest: digest,
          loosens: true,
          pulls,
        },
      ];
    }
    case 'reject':
      return [
        200,
        {
          answer,
          diff: `${hunk}+  - id: no-tc-to-util # request #431\n+    instead: ${url.searchParams.get('instead')}\n`,
          policy_digest: digest,
          loosens: false,
          pulls: [{ number: 12, status: 'waiting', waits: true, clears: 0, left: 1 }],
        },
      ];
  }
  return [422, { error: 'Choose an answer.' }];
}

// owner is the server side of an answer: the request, its previews, the digest, the step-up and the answer. It records
// the previews read and checks that a token is redeemed for the answer, the request and the digest it was made for.
function owner(answered: object, passkey = '') {
  const reads: string[] = [];
  let state = 'open';
  let challenge = '';
  let purpose: object = {};
  const handlers: Handlers = {
    [`GET ${req}`]: () => [200, { ...request, state }],
    [`GET ${req}/preview`]: (_, url) => {
      reads.push(url.search);
      return preview(url);
    },
    [`POST ${req}/answer/digest`]: () => [200, { digest }],
    [`POST ${api}/passkeys/step-up`]: (body) => {
      purpose = body;
      const o = assertion([passkey]);
      challenge = o.publicKey.challenge;
      return [200, { options: o }];
    },
    [`POST ${api}/passkeys/step-up/finish`]: (body) => {
      const c = ceremony(body.credential);
      expect(c.client).toMatchObject({ type: 'webauthn.get', challenge, origin });
      expect(c.verified).toBe(true);
      expect(c.rawId).toBe(passkey);
      return [200, { token: 'step-up-token' }];
    },
    [`POST ${req}/answer`]: (body) => {
      if (body.token) expect(purpose).toEqual({ action: body.answer, request: 431, digest });
      state = 'answered';
      return [200, answered];
    },
  };
  return { handlers, reads };
}

const paths = (writes: { url(): string }[]) => writes.map((w) => new URL(w.url()).pathname);
const answerBox = (p: Page) => p.getByRole('region', { name: 'Answer', exact: true });

test('request: the reason, the record with its balance, the two modules, the preview of each answer, earlier answers', async ({
  page,
}) => {
  const { handlers, reads } = owner({});
  const writes = await open(page, path, handlers);
  await expect(page.getByRole('heading', { name: 'Request #431', level: 1 })).toBeVisible();
  await expect(page.locator('.lede')).toHaveText('waiting Request #431 waits for an owner to answer.');
  await expect(page.getByRole('region', { name: 'Reason' })).toContainText(request.reason);
  await expect(page.getByRole('region', { name: 'Reason' })).toContainText(
    'Rule graph · pkg_tc -> pkg_util · asked until 2026-12-01',
  );
  await expect(page.getByRole('link', { name: 'Request #431 on GitHub' })).toHaveAttribute(
    'href',
    'https://github.com/acme/app/issues/431',
  );
  const record = page.getByRole('region', { name: 'Record' });
  await expect(record).toContainText('Balance: 9/10 (min 7)');
  await expect(record.locator('pre')).toHaveText(request.record);
  const modules = page.getByRole('region', { name: 'Modules' });
  await expect(modules).toContainText('Layer adapters · Volatility high');
  await expect(modules).toContainText('Allowed dependencies: pkg_container');
  await expect(modules).toContainText('Volatility low · subdomain generic');
  await expect(modules).toContainText('Allowed dependencies: none');

  const shown = page.getByRole('region', { name: 'Preview' });
  await expect(shown.getByLabel('Approve: the change to .archfit.yaml')).toContainText(
    '+      - pkg_util # request #431',
  );
  await expect(shown.getByLabel('Approve until a date: the change to .archfit.yaml')).toContainText(
    'expires: 2026-12-01',
  );
  await expect(shown.getByLabel('Approve: open pull requests').getByRole('listitem')).toHaveText([
    '#12 (waiting), waits on this request: clears 1 error, 0 errors left',
    '#15 (fail): clears 1 error, 2 errors left',
  ]);
  await expect(shown).toContainText('Write what to do instead to see what this answer writes.');
  await expect(page.getByRole('region', { name: /^Earlier answers/ })).toContainText(
    'Exception to graph for pkg_tc -> pkg_util until 2026-10-01. Why: Until the port lands.',
  );
  expect(reads.sort()).toEqual(['?answer=approve', '?answer=approve-until']);
  expect(writes).toEqual([]);
  await expectClean(page);
});

test('request: Approve and merge takes the digest, a passkey step-up for it, then the answer with its token', async ({
  page,
}) => {
  const { id } = await authenticator(page, true);
  const notice = 'Answered. The policy change merged in #77.';
  const writes = await open(
    page,
    path,
    owner({ pull_request: 77, state: 'merged', merge: 'c'.repeat(40), notice }, id).handlers,
  );
  await answerBox(page).getByRole('button', { name: 'Approve and merge' }).click();
  await expect(answerBox(page).locator('.flash.success')).toContainText(notice);
  await expect(answerBox(page).getByRole('link', { name: 'Policy pull request #77' })).toHaveAttribute(
    'href',
    'https://github.com/acme/app/pull/77',
  );
  expect(paths(writes)).toEqual([
    `${req}/answer/digest`,
    `${api}/passkeys/step-up`,
    `${api}/passkeys/step-up/finish`,
    `${req}/answer`,
  ]);
  expect(writes[0].postDataJSON()).toEqual({ answer: 'approve' });
  expect(writes[1].postDataJSON()).toEqual({ action: 'approve', request: 431, digest });
  expect(writes[3].postDataJSON()).toEqual({ answer: 'approve', token: 'step-up-token' });
  // The page reads the request again: answered, so no answer is offered.
  await expect(page.locator('.lede')).toHaveText('answered Request #431 is answered.');
  await expect(answerBox(page).getByRole('button')).toHaveCount(0);
  await expect(page.getByRole('region', { name: 'Preview' })).toHaveCount(0);
  await expectClean(page);
});

test('request: Approve until a date sends the date and "merge later", and the preview shows that date', async ({
  page,
}) => {
  const { id } = await authenticator(page, true);
  const notice = 'Answered. The policy change is in #77; it takes effect when #77 merges.';
  const { handlers, reads } = owner({ pull_request: 77, state: 'later', notice }, id);
  const writes = await open(page, path, handlers);
  const box = answerBox(page);
  await expect(box.getByRole('button', { name: 'Approve until a date' })).toBeDisabled();
  await box.getByLabel('The exception ends on').fill('2027-01-15');
  await box.getByLabel('The exception ends on').blur();
  await expect(page.getByLabel('Approve until a date: the change to .archfit.yaml')).toContainText(
    'expires: 2027-01-15',
  );
  expect(reads).toContain('?answer=approve-until&until=2027-01-15');
  await box.getByLabel(/Merge later/).check();
  await box.getByRole('button', { name: 'Approve until a date' }).click();
  await expect(box.locator('.flash.success')).toContainText(notice);
  expect(writes[0].postDataJSON()).toEqual({ answer: 'approve-until', until: '2027-01-15', merge_later: true });
  expect(writes[1].postDataJSON()).toEqual({ action: 'approve-until', request: 431, digest });
  expect(writes[3].postDataJSON()).toEqual({
    answer: 'approve-until',
    until: '2027-01-15',
    merge_later: true,
    token: 'step-up-token',
  });
});

test('request: Reject sends what to do instead and needs no passkey', async ({ page }) => {
  const { handlers, reads } = owner({
    pull_request: 77,
    state: 'merged',
    notice: 'Answered. The policy change merged in #77.',
  });
  const writes = await open(page, path, handlers);
  const box = answerBox(page);
  await expect(box.getByRole('button', { name: 'Reject' })).toBeDisabled();
  await box.getByLabel('What to do instead').fill('Add a retry method to container.Client.');
  await box.getByLabel('What to do instead').blur();
  await expect(page.getByLabel('Reject: the change to .archfit.yaml')).toContainText(
    'instead: Add a retry method to container.Client.',
  );
  await expect(page.getByRole('region', { name: 'Preview' })).toContainText('Tightens the policy: no passkey needed.');
  expect(reads).toContain('?answer=reject&instead=Add+a+retry+method+to+container.Client.');
  await box.getByRole('button', { name: 'Reject' }).click();
  await expect(box.locator('.flash.success')).toBeVisible();
  expect(paths(writes)).toEqual([`${req}/answer`]);
  expect(writes[0].postDataJSON()).toEqual({ answer: 'reject', instead: 'Add a retry method to container.Client.' });
});

test('request: a refused step-up is the server sentence, nothing is answered, and the answers stay', async ({
  page,
}) => {
  const { handlers } = owner({});
  handlers[`POST ${api}/passkeys/step-up`] = () => [409, { error: 'Register a passkey first.' }];
  const writes = await open(page, path, handlers);
  const box = answerBox(page);
  await box.getByRole('button', { name: 'Approve and merge' }).click();
  await expect(box.getByRole('alert')).toHaveText('Register a passkey first.');
  await expect(box.getByRole('link', { name: 'Your passkeys' })).toHaveAttribute('href', '/r/acme/app/passkeys');
  expect(paths(writes)).toEqual([`${req}/answer/digest`, `${api}/passkeys/step-up`]);
  await expect(box.getByRole('button', { name: 'Approve and merge' })).toBeEnabled();
  await expectClean(page);
});

test('request: numbers as reported say so; a closed request offers no answer', async ({ page }) => {
  await open(page, path, { [`GET ${req}`]: () => [200, { ...request, verified: false }] });
  await expect(page.locator('.lede')).toContainText('Its numbers are as reported: no pull request confirmed them yet.');
  await expect(page.getByRole('region', { name: 'Record' })).toContainText('Balance: 9/10 (min 7), as reported');

  await page.unrouteAll();
  await open(page, path, { [`GET ${req}`]: () => [200, { ...request, state: 'closed' }] });
  await expect(page.locator('.lede')).toHaveText('closed Request #431 is closed.');
  await expect(answerBox(page)).toContainText('No answer is open.');
  await expect(answerBox(page).getByRole('button')).toHaveCount(0);
  await expectClean(page);
});

test('request: a request that does not exist is the API sentence, with no repository tabs', async ({ page }) => {
  await open(page, '/r/acme/app/requests/999', {});
  await expect(page.getByRole('heading', { name: 'This page cannot be shown' })).toBeVisible();
  await expect(page.getByRole('navigation', { name: 'Repository' })).toHaveCount(0);
});

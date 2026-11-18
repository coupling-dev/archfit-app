import { expect, test, type Page } from '@playwright/test';
import {
  assertion,
  authenticator,
  ceremony,
  expectClean,
  expectPageText,
  open,
  origin,
  docsClock,
  docsShot,
  type Handlers,
} from './support';

const path = '/r/acme/app/requests/431';
const api = '/api/repos/acme/app';
const req = `${api}/requests/431`;
const digest = 'd'.repeat(64);

// request is GET /api/repos/acme/app/requests/431 in the shape of requestJSON (internal/control/ui/requests.go).
const request = {
  issue_href: 'https://github.com/acme/app/issues/431',
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
  dependencies: [],
  layers: ['adapters'],
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
  await expect(page.locator('.lede')).toHaveText('waiting Waiting for an owner to answer.');
  await expect(page.getByRole('region', { name: 'Reason' })).toContainText(request.reason);
  await expect(page.getByRole('region', { name: 'Reason' })).toContainText(
    'Rule graph · pkg_tc -> pkg_util · asked until 2026-12-01',
  );
  await expect(page.getByRole('link', { name: 'Request #431 on GitHub' })).toHaveAttribute(
    'href',
    'https://github.com/acme/app/issues/431',
  );
  const record = page.getByRole('region', { name: 'Record', exact: true });
  const balanceWhy = page.getByRole('region', { name: 'Why the balance', exact: true });
  await expect(balanceWhy).toContainText('Balance: 9/10 (min 7)');
  await expect(record.locator('pre')).toHaveText(request.record);
  const map = page.getByRole('region', { name: 'The map' });
  await expect(map.locator('.map-node')).toHaveCount(0);
  await map.getByText('Show the map as a diagram', { exact: true }).click();
  await expect(map.locator('.map-node')).toHaveCount(2);
  await map.locator('[data-module]').first().click();
  await expect(map.locator('.map-label.requested')).toContainText('requested #431');
  const why = page.getByRole('region', { name: 'Why the balance' });
  await expect(why).toContainText('Layer adapters · Volatility high');
  await expect(why).toContainText('Volatility low · subdomain generic');

  const shown = page.getByRole('region', { name: /: preview$/ });
  await expect(shown.getByLabel('Approve: the change to .archfit.yaml')).toContainText(
    '+      - pkg_util # request #431',
  );
  await expect(answerBox(page).getByRole('button', { name: 'Approve and merge policy PR' })).toBeVisible();
  await expect(shown.getByLabel('Approve: open pull requests').getByRole('listitem').locator('p')).toHaveText([
    '#12 (waiting), waits on this request: clears 1 error, 0 errors left',
    '#15 (fail): clears 1 error, 2 errors left',
  ]);
  await expect(shown.getByRole('link', { name: 'PR #12 in Archfit' })).toHaveAttribute('href', '/r/acme/app/pull/12');
  await expect(page.getByRole('region', { name: /^Earlier answers/ })).toContainText(
    'Exception to graph for pkg_tc -> pkg_util until 2026-10-01. Why: Until the port lands.',
  );
  expect(reads).toEqual(['?answer=approve']);
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
  await answerBox(page).getByRole('button', { name: 'Approve and merge policy PR' }).click();
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
  await expect(page.locator('.lede')).toHaveText('answered An owner answered this request.');
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
  await box.getByRole('radio', { name: 'Approve until a date', exact: true }).check();
  await expect(box.getByRole('button', { name: 'Preview approve until a date' })).toBeDisabled();
  await box.getByLabel('The exception ends on').fill('2027-01-15');
  await box.getByRole('button', { name: 'Preview approve until a date' }).click();
  await expect(page.getByLabel('Approve until a date: the change to .archfit.yaml')).toContainText(
    'expires: 2027-01-15',
  );
  expect(reads).toContain('?answer=approve-until&until=2027-01-15');
  await box.getByLabel('Keep the policy pull request open').check();
  await box.getByRole('button', { name: 'Approve until date and keep policy PR open' }).click();
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

test('request: Reject sends what to do instead and "merge later", and needs no passkey', async ({ page }) => {
  const { handlers, reads } = owner({
    pull_request: 77,
    state: 'merged',
    notice: 'Answered. The policy change merged in #77.',
  });
  const writes = await open(page, path, handlers);
  const box = answerBox(page);
  await box.getByRole('radio', { name: 'Reject', exact: true }).check();
  await expect(box.getByRole('button', { name: 'Preview reject' })).toBeDisabled();
  await box.getByLabel('What to do instead').fill('Add a retry method to container.Client.');
  await box.getByRole('button', { name: 'Preview reject' }).click();
  await expect(page.getByLabel('Reject: the change to .archfit.yaml')).toContainText(
    'instead: Add a retry method to container.Client.',
  );
  await expect(page.getByRole('region', { name: /: preview$/ })).toContainText('Tightens the policy: no passkey needed.');
  expect(reads).toContain('?answer=reject&instead=Add+a+retry+method+to+container.Client.');
  // Merge later holds the policy pull request open for a reject too: the server merges at once without it.
  await box.getByLabel('Keep the policy pull request open').check();
  await box.getByRole('button', { name: 'Reject and keep policy PR open' }).click();
  await expect(box.locator('.flash.success')).toBeVisible();
  expect(paths(writes)).toEqual([`${req}/answer`]);
  expect(writes[0].postDataJSON()).toEqual({
    answer: 'reject',
    instead: 'Add a retry method to container.Client.',
    merge_later: true,
  });
});

test('request: a refused step-up is the server sentence, nothing is answered, and the answers stay', async ({
  page,
}) => {
  const { handlers } = owner({});
  handlers[`POST ${api}/passkeys/step-up`] = () => [409, { error: 'Register a passkey first.' }];
  const writes = await open(page, path, handlers);
  const box = answerBox(page);
  await box.getByRole('button', { name: 'Approve and merge policy PR' }).click();
  await expect(box.getByRole('alert')).toHaveText('Register a passkey first.');
  await expect(box.getByRole('link', { name: 'Your passkeys' })).toHaveAttribute(
    'href',
    '/r/acme/app/passkeys?return=' + encodeURIComponent(path),
  );
  expect(paths(writes)).toEqual([`${req}/answer/digest`, `${api}/passkeys/step-up`]);
  await expect(box.getByRole('button', { name: 'Approve and merge policy PR' })).toBeEnabled();
  await expectClean(page);
});

test('request: numbers as reported say so; a closed request offers no answer', async ({ page }) => {
  await open(page, path, { [`GET ${req}`]: () => [200, { ...request, verified: false }] });
  await expect(page.locator('.lede')).toContainText('Its numbers are as reported: no pull request confirmed them yet.');
  await expect(page.getByRole('region', { name: 'Why the balance', exact: true })).toContainText(
    'Balance: 9/10 (min 7), as reported',
  );

  await page.unrouteAll();
  await open(page, path, { [`GET ${req}`]: () => [200, { ...request, state: 'closed' }] });
  await expect(page.locator('.lede')).toHaveText('closed Request #431 is closed.');
  await expect(answerBox(page)).toContainText('No answer is open.');
  await expect(answerBox(page).getByRole('button')).toHaveCount(0);
  await expectClean(page);
});

test('request: a request that does not exist is the API sentence, with no repository tabs', async ({ page }) => {
  await open(page, '/r/acme/app/requests/999', {});
  await expect(page.getByRole('heading', { name: 'Page not available' })).toBeVisible();
  await expect(page.getByRole('navigation', { name: 'Repository' })).toHaveCount(0);
});

for (const [name, data] of [
  ['open', request],
  ['as-reported', { ...request, verified: false }],
  [
    'joined',
    {
      ...request,
      state: 'answered',
      dependencies: [{ kind: 'exception', from: 'pkg_tc', to: 'pkg_util', rule: 'graph', expires: '2027-01-15' }],
      answer: { kind: 'approve-until', until: '2027-01-15' },
    },
  ],
  [
    'answered',
    {
      ...request,
      state: 'answered',
      modules: request.modules.map((m) => (m.name === 'pkg_tc' ? { ...m, depends_on: ['pkg_util'] } : m)),
      dependencies: [{ kind: 'deny', from: 'pkg_tc', to: 'pkg_util', rule: 'no-tc-to-util' }],
      answer: { kind: 'reject', instead: 'Use a port.' },
    },
  ],
] as const) {
  test(`request: ${name} text golden on direct load${name === 'open' ? ' @docs' : ''}`, async ({ page }) => {
    await docsClock(page);
    await open(page, path, { [`GET ${req}`]: () => [200, data], [`GET ${req}/preview`]: (_, url) => preview(url) });
    await expect(page.getByRole('heading', { name: 'Request #431' })).toBeVisible();
    await page.getByText('Show the map as a diagram', { exact: true }).click();
    await expect(page.getByRole('group', { name: 'Modules by layer and their dependencies' })).toBeVisible();
    if (!('answer' in data)) await expect(page.getByLabel('Approve: the change to .archfit.yaml')).toBeVisible();
    else {
      await expect(answerBox(page).getByRole('button')).toHaveCount(0);
      const module = page.locator('[data-module]').first();
      await module.click();
      await expect(page.locator('.map-edge.' + (data.answer.kind === 'reject' ? 'deny' : 'exception'))).toHaveCount(1);
      await expect(page.locator('.map-edge.allowed')).toHaveCount(0);
      await module.click();
      await expect(page.locator('.map-edge')).toHaveCount(0);
      await expect(answerBox(page)).toContainText(
        data.answer.kind === 'reject' ? 'Rejected. What to do instead: Use a port.' : 'Approved until 2027-01-15.',
      );
      await expect(page.getByRole('link', { name: 'Request #431 on GitHub' })).toHaveAttribute(
        'href',
        'https://github.com/acme/app/issues/431',
      );
    }
    await expectPageText(page, 'request-' + name);
    if (name === 'open') await docsShot(page, 'request');
    await expectClean(page);
  });
}

test('request: old answered record links to issue without inventing an answer', async ({ page }) => {
  await open(page, path, { [`GET ${req}`]: () => [200, { ...request, state: 'answered' }] });
  await expect(answerBox(page)).toContainText('names the owner who answered and the passkey used.');
  await expect(answerBox(page)).not.toContainText('Approved.');
  await expect(answerBox(page).getByRole('button')).toHaveCount(0);
  await expect(page.getByRole('link', { name: 'Request #431 on GitHub' })).toBeVisible();
});

test('request: the page reads status, reason, record, map, balance, preview, earlier answers, then the answer', async ({
  page,
}) => {
  await open(page, path, owner({}).handlers);
  await expect(page.getByRole('region', { name: /: preview$/ })).toBeVisible();
  const heads = await page.locator('main section.box h2').allTextContents();
  expect(heads.map((h) => h.split(' ')[0])).toEqual(['Reason', 'Record', 'The', 'Why', 'Preview:', 'Earlier', 'Answer']);
  await expect(page.locator('main p', { hasText: /^Balance:/ })).toHaveCount(1);
});

for (const width of [390, 1280]) {
  test(`request: approved answer at ${width} @docs`, async ({ page }) => {
    await page.setViewportSize({ width, height: 844 });
    await docsClock(page);
    await open(page, path, {
      [`GET ${req}`]: () => [200, { ...request, state: 'answered', answer: { kind: 'approve' } }],
    });
    await expect(page.locator('.lede')).toContainText('approved');
    await docsShot(page, `request-approved-${width}`);
    await expectClean(page);
  });
}

test('request: an approved answer shows approved, the record as checked, and no need-a-request note', async ({ page }) => {
  await open(page, path, {
    [`GET ${req}`]: () => [200, { ...request, state: 'answered', answer: { kind: 'approve' } }],
  });
  await expect(page.locator('.lede')).toContainText('approved');
  await expect(page.getByRole('heading', { name: 'As checked when the request was made' })).toBeVisible();
  await expect(page.getByText('Dependencies not allowed by this policy need a request.')).toHaveCount(0);
  await expectClean(page);
});

for (const width of [390, 1280]) {
  for (const [kind, title, verb] of [
    ['approve', 'Approve', 'Approve'],
    ['approve-until', 'Approve until a date', 'Approve until date'],
    ['reject', 'Reject', 'Reject'],
  ] as const) {
    test(`request: decision ${kind} at ${width} @docs`, async ({ page }) => {
      await page.setViewportSize({ width, height: 844 });
      await docsClock(page);
      const writes = await open(page, path, owner({}).handlers);
      const box = answerBox(page);
      await box.getByRole('radio', { name: title, exact: true }).check();
      if (kind === 'approve-until') await box.getByLabel('The exception ends on').fill('2027-01-15');
      if (kind === 'reject') await box.getByLabel('What to do instead').fill('Use a port.');
      if (kind !== 'approve') await box.getByRole('button', { name: 'Preview ' + title.toLowerCase() }).click();
      const decision = page.getByRole('region', { name: title + ': preview' });
      await expect(decision.getByLabel(title + ': the change to .archfit.yaml')).toBeVisible();
      await expect(box.getByRole('button', { name: verb + ' and merge policy PR' })).toBeVisible();
      await expect(box.locator('button.primary')).toHaveCount(1);
      await expect(decision.getByRole('link', { name: 'PR #12 in Archfit' })).toHaveAttribute(
        'href',
        '/r/acme/app/pull/12',
      );
      await expect(decision.getByRole('link', { name: 'PR #12 on GitHub' })).toHaveAttribute(
        'href',
        'https://github.com/acme/app/pull/12',
      );
      await expect(page.getByRole('link', { name: 'Request #431 on GitHub' })).toHaveAttribute(
        'href',
        request.issue_href,
      );
      await box.getByLabel('Keep the policy pull request open').check();
      await expect(box.getByRole('button', { name: verb + ' and keep policy PR open' })).toBeVisible();
      await expect(box).toContainText('The change takes effect only when that pull request merges.');
      await expect(decision).toContainText('These code pull requests are not merged by this answer.');
      const target = await box.locator('button.primary').boundingBox();
      if (width === 390) expect(target?.height).toBeGreaterThanOrEqual(44);
      await expect(page.locator('.map-node')).toHaveCount(0);
      await docsShot(page, `request-${kind}-${width}`);
      await page.getByText('Show the map as a diagram', { exact: true }).click();
      await expect(page.locator('.map-node')).toHaveCount(2);
      await expectClean(page);
      expect(writes).toEqual([]);
      await decision.getByRole('link', { name: 'PR #12 in Archfit' }).click();
      await expect(page).toHaveURL(/\/pull\/12$/);
      expect(writes).toEqual([]);
    });
  }
}

test('request: editing discards the preview and a late read cannot restore it', async ({ page }) => {
  const { handlers } = owner({});
  let release: (() => void) | undefined;
  handlers[`GET ${req}/preview`] = async (_, url) => {
    if (url.searchParams.get('answer') === 'reject')
      await new Promise<void>((resolve) => {
        release = resolve;
      });
    return preview(url);
  };
  const writes = await open(page, path, handlers);
  const box = answerBox(page);
  await box.getByRole('radio', { name: 'Reject', exact: true }).check();
  await box.getByLabel('What to do instead').fill('Old alternative');
  await box.getByRole('button', { name: 'Preview reject' }).click();
  await expect.poll(() => !!release).toBe(true);
  await box.getByLabel('What to do instead').fill('New alternative');
  const completed = page.waitForResponse((response) => response.url().includes('instead=Old'));
  release?.();
  await (await completed).finished();
  await expect(page.getByLabel('Reject: the change to .archfit.yaml')).toHaveCount(0);
  await expect(box.locator('button.primary')).toHaveCount(0);
  handlers[`GET ${req}/preview`] = (_, url) => preview(url);
  await box.getByRole('button', { name: 'Preview reject' }).click();
  await expect(page.getByLabel('Reject: the change to .archfit.yaml')).toContainText('New alternative');
  await box.getByLabel('What to do instead').fill('');
  await expect(box.locator('button.primary')).toHaveCount(0);
  expect(writes).toEqual([]);
});

test('request: no policy change hides merge control and does not submit its stale value', async ({ page }) => {
  const { handlers } = owner({ notice: 'Answered. The policy needs no new line for it.' });
  handlers[`GET ${req}/preview`] = (_, url) =>
    url.searchParams.get('answer') === 'reject'
      ? [200, { answer: 'reject', diff: '', policy_digest: digest, loosens: false, pulls: [] }]
      : preview(url);
  const writes = await open(page, path, handlers);
  const box = answerBox(page);
  await box.getByLabel('Keep the policy pull request open').check();
  await box.getByRole('radio', { name: 'Reject', exact: true }).check();
  await box.getByLabel('What to do instead').fill('Use a port.');
  await box.getByRole('button', { name: 'Preview reject' }).click();
  await expect(box.getByLabel('Keep the policy pull request open')).toHaveCount(0);
  await expect(box).toContainText('This records your answer without changing .archfit.yaml.');
  await expectClean(page);
  await box.getByRole('button', { name: 'Reject without a passkey' }).click();
  await expect(box.locator('.flash.success')).toBeVisible();
  expect(writes.map((write) => write.postDataJSON())).toEqual([{ answer: 'reject', instead: 'Use a port.' }]);
});

test('request: missing or unsafe issue destination is not invented', async ({ page }) => {
  await open(page, path, {
    [`GET ${req}`]: () => [200, { ...request, state: 'closed', issue_href: 'https://other.example/issues/431' }],
  });
  await expect(page.getByRole('link', { name: 'Request #431 on GitHub' })).toHaveCount(0);
});

test('request: failed preview cannot confirm; retry and a changed date require a fresh preview', async ({ page }) => {
  const { handlers } = owner({});
  handlers[`GET ${req}/preview`] = () => [503, { error: 'Preview unavailable.' }];
  const writes = await open(page, path, handlers);
  const box = answerBox(page);
  await expect(box.getByRole('alert')).toContainText('Preview unavailable.');
  await expect(box.locator('button.primary')).toHaveCount(0);
  handlers[`GET ${req}/preview`] = (_, url) => preview(url);
  await box.getByRole('button', { name: 'Preview approve', exact: true }).click();
  await expect(box.getByRole('button', { name: 'Approve and merge policy PR' })).toBeVisible();
  await box.getByRole('radio', { name: 'Approve until a date', exact: true }).check();
  await box.getByLabel('The exception ends on').fill('2027-01-15');
  await box.getByRole('button', { name: 'Preview approve until a date' }).click();
  await expect(page.getByLabel('Approve until a date: the change to .archfit.yaml')).toContainText('2027-01-15');
  await box.getByLabel('The exception ends on').fill('2027-02-15');
  await expect(box.locator('button.primary')).toHaveCount(0);
  await expect(page.getByLabel('Approve until a date: the change to .archfit.yaml')).toHaveCount(0);
  expect(writes).toEqual([]);
});

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
  waiting: [12],
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
    case 'declare-public':
    case 'declare-model':
      return [
        200,
        {
          answer,
          diff: `${hunk}+      - pkg/util/retry # request #431\n`,
          policy_digest: digest,
          loosens: true,
          pulls,
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
const answerBox = (p: Page) => p.getByRole('region', { name: /^(Your answer|Answer)$/ });
const cardTitle = (title: string) => new RegExp(`^${title.replace(/[.*+?^${}()|[\]\\]/g, '\\$&')}\\s*$`);
// choose picks an answer card by its title; the card holds the radio, its effect lines and its fields.
const choose = (p: Page, title: string) =>
  p.locator('label.answer').filter({ has: p.locator('.name', { hasText: cardTitle(title) }) }).getByRole('radio').check();
const confirmButton = (p: Page, name: 'Answer' | 'Answer with passkey') => p.getByRole('button', { name, exact: true });
const dateField = (p: Page) => p.getByRole('textbox', { name: 'The exception ends on (yyyy-mm-dd)' });
const insteadField = (p: Page) => p.getByRole('textbox', { name: 'What to do instead', exact: true });
const changeDisclosure = (p: Page) => p.getByText('Show the change to the policy and the pull requests', { exact: true });
const bodyText = (p: Page) => p.evaluate(() => document.body.textContent ?? '');
// The page offers 90 days from today for an "Allow until a date" answer; docsClock fixes today at 2026-11-13.
const defaultDate = '2027-02-11';

const approved = { pull_request: 77, state: 'merged', notice: 'Answered. The policy change merged in #77.' } as const;

test('request: the question, the evidence and the answers, with each effect read before any click', async ({ page }) => {
  await docsClock(page);
  const { handlers, reads } = owner({});
  const writes = await open(page, path, handlers);
  await expect(page.getByRole('heading', { name: 'May pkg_tc depend on pkg_util?', level: 1 })).toBeVisible();
  await expect(page.locator('.lede')).toHaveText(
    `Request #431. The author says: “${request.reason}” Pull request #12 waits for your answer.`,
  );
  await expect(page.locator('.page-head .small')).toHaveCount(0);
  const evidence = page.locator('dl.evidence');
  await expect(evidence.locator('dt')).toHaveText(['Where', 'The rule', 'Balance', 'Asked until', 'On GitHub']);
  await expect(evidence.locator('dd')).toHaveText([
    'pkg/tc/netem.go:12 graph pkg_tc -> pkg_util is not an allowed dependency',
    'The policy does not allow pkg_tc → pkg_util (rule graph).',
    '9/10 (min 7)',
    '2026-12-01',
    'Request #431',
  ]);
  await expect(evidence.getByRole('link', { name: 'Request #431', exact: true })).toHaveAttribute(
    'href',
    'https://github.com/acme/app/issues/431',
  );
  const answers = page.getByRole('radiogroup', { name: 'Choose an answer' });
  await expect(answers.locator('.name')).toHaveText(['Allow', 'Allow until', 'Reject, and say what to do instead']);
  await expect(answers.getByRole('radio')).toHaveCount(3);
  await expect(answers.getByRole('radio', { checked: true })).toHaveCount(0);
  await expect(answers).toContainText('Adds 1 line to the policy: pkg_tc may depend on pkg_util.');
  await expect(answers).toContainText(`Adds an exception for this one place that ends on ${defaultDate}.`);
  await expect(answers).toContainText('The author sees what you say to do instead.');
  await expect(page.getByText('Pull request #12 passes when the policy is updated.').first()).toBeVisible();
  await expect(page.getByText('Pull request #15 still has 2 errors.').first()).toBeVisible();
  await expect(page.getByText('Choose an answer. Nothing changes until you confirm it.')).toBeVisible();
  await expect(confirmButton(page, 'Answer with passkey')).toBeDisabled();
  await expect(page.getByRole('link', { name: 'Advanced: edit the policy file' })).toHaveAttribute('href', '/r/acme/app/policy');
  expect(reads.sort()).toEqual(['?answer=approve', `?answer=approve-until&until=${defaultDate}`]);
  expect(writes).toEqual([]);
  await expectClean(page);
});

test('request: the more-about disclosures follow the answer: map, balance, earlier answers, record', async ({ page }) => {
  await open(page, path, owner({}).handlers);
  await expect(page.locator('main section.box h2')).toHaveText(['Your answer']);
  await expect(page.locator('section[aria-label="More about this request"] .box-body > details.disclosure-ui > summary')).toHaveText([
    'The map',
    'Why the balance',
    'Earlier answers about these modules (1)',
    'The record',
  ]);
  const more = page.getByRole('region', { name: 'More about this request' });
  await expect(more).toContainText('Balance: 9/10 (min 7). The balance is the value the policy checks against its minimum.');
  await expect(more).toContainText('pkg_tc: Layer adapters · Volatility high');
  await expect(more).toContainText('pkg_util: Volatility low · subdomain generic');
  await expect(more).toContainText('Exception to graph for pkg_tc -> pkg_util until 2026-10-01. Why: Until the port lands.');
  await expect(more.locator('pre')).toHaveText(request.record);
  const map = page.locator('details', { hasText: 'Show the map as a diagram' }).first();
  await expect(page.locator('.map-node')).toHaveCount(0);
  await page.getByText('The map', { exact: true }).click();
  await page.getByText('Show the map as a diagram', { exact: true }).click();
  await expect(page.locator('.map-node')).toHaveCount(2);
  await page.locator('[data-module]').first().click();
  await expect(page.locator('.map-label.requested')).toContainText('requested #431');
  await expect(map).toBeVisible();
  await expectClean(page);
});

test('request: the story row names the six steps and marks "ask #431" as now', async ({ page }) => {
  await open(page, path, owner({}).handlers);
  const story = page.getByRole('list', { name: 'Story of the change' });
  const steps = story.getByRole('listitem');
  await expect(steps).toHaveCount(6);
  await expect(steps).toHaveText([/change/, /check/, /ask #431/, /answer/, /policy/, /re-check/]);
  await expect(steps).toHaveClass([/\bdone\b/, /\bfailed\b/, /\bcurrent\b/, /^(?!.*\b(done|failed|current|skipped)\b)/, /^(?!.*\b(done|failed|current|skipped)\b)/, /^(?!.*\b(done|failed|current|skipped)\b)/]);
  await expect(story.locator('[aria-current="step"]')).toHaveCount(1);
  await expect(story.locator('[aria-current="step"]')).toContainText('ask #431');
  await expect(story.locator('[aria-current="step"]')).toContainText('now');
  await expect(steps.nth(1)).toContainText('failed');
});

test('request: Allow takes the digest, a passkey step-up for it, then the answer with its token', async ({ page }) => {
  const { id } = await authenticator(page, true);
  const writes = await open(page, path, owner(approved, id).handlers);
  await choose(page, 'Allow');
  await expect(answerBox(page).getByText('Your passkey confirms this one answer.')).toBeVisible();
  await confirmButton(page, 'Answer with passkey').click();
  const banner = answerBox(page).locator('.banner');
  await expect(banner).toContainText('Answered. The policy is updated.');
  await expect(banner.getByRole('link', { name: 'policy pull request #77' })).toHaveAttribute(
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
  // The page reads the request again: answered, so no answer is offered, and the owner's own answer is not "while you looked".
  await expect(page.locator('.lede')).toHaveText(`answered Request #431. The author says: “${request.reason}”`);
  await expect(page.locator('.page-head .small')).toHaveText('An owner answered this request.');
  await expect(answerBox(page).getByRole('radio')).toHaveCount(0);
  await expect(page.getByRole('button', { name: /^Answer/ })).toHaveCount(0);
  await expect(page.getByText('was answered while you looked')).toHaveCount(0);
  await expectClean(page);
});

test('request: Allow until a date sends the date, and the effect line shows that date', async ({ page }) => {
  await docsClock(page);
  const { id } = await authenticator(page, true);
  const { handlers, reads } = owner(approved, id);
  const writes = await open(page, path, handlers);
  await choose(page, 'Allow until');
  await expect(dateField(page)).toHaveValue(defaultDate);
  await dateField(page).fill('2027-01-15');
  await expect(page.getByText('Adds an exception for this one place that ends on 2027-01-15.')).toBeVisible();
  await expect.poll(() => reads).toContain('?answer=approve-until&until=2027-01-15');
  await changeDisclosure(page).click();
  await expect(page.getByLabel('The change to .archfit.yaml')).toContainText('expires: 2027-01-15');
  await confirmButton(page, 'Answer with passkey').click();
  await expect(answerBox(page).locator('.banner')).toContainText('Answered. The policy is updated.');
  expect(writes[0].postDataJSON()).toEqual({ answer: 'approve-until', until: '2027-01-15' });
  expect(writes[1].postDataJSON()).toEqual({ action: 'approve-until', request: 431, digest });
  expect(writes[3].postDataJSON()).toEqual({ answer: 'approve-until', until: '2027-01-15', token: 'step-up-token' });
});

test('request: Reject sends what to do instead, and needs no passkey', async ({ page }) => {
  const { handlers, reads } = owner(approved);
  const writes = await open(page, path, handlers);
  await choose(page, 'Reject, and say what to do instead');
  await expect(confirmButton(page, 'Answer')).toBeDisabled();
  await expect(answerBox(page)).toContainText('Write what to do instead.');
  await insteadField(page).fill('Add a retry method to container.Client.');
  await expect(answerBox(page)).toContainText('No passkey: a reject does not loosen the policy.');
  await expect.poll(() => reads).toContain('?answer=reject&instead=Add+a+retry+method+to+container.Client.');
  await expect(page.getByText('Adds a rule: pkg_tc may not depend on pkg_util')).toBeVisible();
  await changeDisclosure(page).click();
  await expect(page.getByLabel('The change to .archfit.yaml')).toContainText('instead: Add a retry method to container.Client.');
  await confirmButton(page, 'Answer').click();
  await expect(answerBox(page).locator('.banner')).toContainText('Answered. The policy is updated.');
  expect(paths(writes)).toEqual([`${req}/answer`]);
  expect(writes[0].postDataJSON()).toEqual({ answer: 'reject', instead: 'Add a retry method to container.Client.' });
});

test('request: no answer ever sends merge_later, and no merge checkbox is offered', async ({ page }) => {
  await docsClock(page);
  const { id } = await authenticator(page, true);
  const writes = await open(page, path, owner(approved, id).handlers);
  await expect(page.getByRole('checkbox')).toHaveCount(0);
  await choose(page, 'Allow until');
  await confirmButton(page, 'Answer with passkey').click();
  await expect(answerBox(page).locator('.banner')).toBeVisible();
  expect(writes.length).toBe(4);
  for (const w of writes) expect(JSON.stringify(w.postDataJSON())).not.toContain('merge_later');
  expect(await bodyText(page)).not.toMatch(/merge later|keep the policy pull request open/i);
});

for (const [name, answered, title, line, tone] of [
  ['merged', approved, 'Answered. The policy is updated.', 'Merged in policy pull request #77.', 'success'],
  [
    'not merged',
    { pull_request: 77, state: 'later', notice: 'Answered. The policy change is in #77.' },
    'Answered. Nothing else for you to do.',
    'The App merges policy pull request #77 when its checks pass, and the policy updates by itself.',
    'info',
  ],
  ['no policy pull request', { notice: 'Answered. The policy needs no new line for it.' }, 'Answered.', 'The policy does not change.', 'success'],
  [
    'void',
    { pull_request: 77, state: 'void', notice: 'The policy moved, so the pull request cannot merge.' },
    'Answered, but the policy pull request cannot merge.',
    'The policy moved, so the pull request cannot merge.',
    'attention',
  ],
] as const) {
  test(`request: answered banner, ${name} @docs`, async ({ page }) => {
    await docsClock(page);
    const { id } = await authenticator(page, true);
    await open(page, path, owner(answered, id).handlers);
    await choose(page, 'Allow');
    await confirmButton(page, 'Answer with passkey').click();
    const banner = answerBox(page).locator(`.banner.${tone}`);
    await expect(banner.locator('.title')).toHaveText(title);
    await expect(banner).toContainText(line);
    await expect(page.locator('.banner')).toHaveCount(1);
    if (name === 'merged') await docsShot(page, 'answer-page-answered');
    await expectClean(page);
  });
}

test('request: a refused step-up is the server sentence, nothing is answered, and the answers stay', async ({ page }) => {
  const { handlers } = owner({});
  handlers[`POST ${api}/passkeys/step-up`] = () => [409, { error: 'Register a passkey first.' }];
  const writes = await open(page, path, handlers);
  const box = answerBox(page);
  await choose(page, 'Allow');
  await confirmButton(page, 'Answer with passkey').click();
  await expect(box.getByRole('alert')).toHaveText('Register a passkey first.');
  await expect(box.getByRole('link', { name: 'Your passkeys' })).toHaveAttribute(
    'href',
    '/r/acme/app/passkeys?return=' + encodeURIComponent(path),
  );
  expect(paths(writes)).toEqual([`${req}/answer/digest`, `${api}/passkeys/step-up`]);
  await expect(box.locator('.banner')).toHaveCount(0);
  await expect(confirmButton(page, 'Answer with passkey')).toBeEnabled();
  await expect(answerBox(page).getByRole('radio')).toHaveCount(3);
  await expectClean(page);
});

test('request: numbers as reported say so; a closed request offers no answer', async ({ page }) => {
  await open(page, path, { [`GET ${req}`]: () => [200, { ...request, verified: false }] });
  await expect(page.locator('.page-head .small')).toContainText('Its numbers are as reported: no pull request confirmed them yet.');
  await expect(page.locator('dl.evidence')).toContainText('9/10 (min 7), as reported');
  await expect(page.getByRole('region', { name: 'More about this request' })).toContainText('Balance: 9/10 (min 7), as reported.');

  await page.unrouteAll();
  await open(page, path, { [`GET ${req}`]: () => [200, { ...request, state: 'closed' }] });
  await expect(page.locator('.lede')).toHaveText(`closed Request #431. The author says: “${request.reason}”`);
  await expect(page.locator('.page-head .small')).toHaveText('Request #431 is closed.');
  await expect(answerBox(page)).toContainText('No answer is open.');
  await expect(answerBox(page).getByRole('radio')).toHaveCount(0);
  await expect(page.getByRole('button', { name: /^Answer/ })).toHaveCount(0);
  await expectClean(page);
});

test('request: a request that does not exist is the API sentence, with no repository tabs', async ({ page }) => {
  await open(page, '/r/acme/app/requests/999', {});
  await expect(page.getByRole('heading', { name: 'Page not available' })).toBeVisible();
  await expect(page.getByRole('navigation', { name: 'Repository' })).toHaveCount(0);
});

const surface = {
  ...request,
  rule: 'coupling',
  record:
    'pkg/tc/netem.go:12: coupling: pkg_tc -> pkg/util/retry is past the surface\n  balance: 2/10 (min 7): intrusive, pkg_util is high\n' +
    '  surface: pkg/util (public)\n  next: use the surface, or: archfit request -m "<why retry belongs to the surface>"',
  balance: { value: 2, max: 10, min: 7, strength: 'intrusive', volatility: 'high' },
  surface: { public: ['pkg/util'], model: [], package: 'pkg/util/retry' },
};

test('request: a surface request makes the package public, allows it until a date or rejects it; never approves it @docs', async ({ page }) => {
  await docsClock(page);
  const { id } = await authenticator(page, true);
  const { handlers } = owner(approved, id);
  handlers[`GET ${req}`] = () => [200, surface];
  const writes = await open(page, path, handlers);
  await expect(page.getByRole('heading', { name: 'Should pkg/util/retry be public in pkg_util?', level: 1 })).toBeVisible();
  const answers = page.getByRole('radiogroup', { name: 'Choose an answer' });
  await expect(answers.locator('.name')).toHaveText(['Make it public', 'Allow until', 'Reject, and say what to do instead']);
  await expect(page.getByRole('radio', { name: /Declare it as shared model/ })).toBeVisible();
  await expect(page.locator('dl.evidence')).toContainText('pkg/util/retry is past the public part of pkg_util.');
  await expect(page.locator('dl.evidence')).toContainText('2/10 (min 7): intrusive, pkg_util is high');
  await expect(answers).toContainText('Adds 1 line: pkg/util/retry joins the public part of pkg_util. Every module may then use it.');
  await expect(page.getByText('Declare it as shared model', { exact: true })).toBeVisible();
  expect(await page.locator('main').innerText()).not.toMatch(/approve/i);
  await expect(page.getByRole('button', { name: /approve/i })).toHaveCount(0);
  await expectPageText(page, 'request-surface');
  await docsShot(page, 'answer-page-surface');
  await expectClean(page);

  await choose(page, 'Make it public');
  await confirmButton(page, 'Answer with passkey').click();
  await expect(answerBox(page).locator('.banner')).toContainText('Answered. The policy is updated.');
  expect(writes[0].postDataJSON()).toEqual({ answer: 'declare-public' });
  expect(writes[1].postDataJSON()).toEqual({ action: 'declare-public', request: 431, digest });
  expect(writes[3].postDataJSON()).toEqual({ answer: 'declare-public', token: 'step-up-token' });

  await page.unrouteAll();
  await open(page, path, { [`GET ${req}`]: () => [200, { ...surface, surface: { ...surface.surface, package: undefined } }] });
  await expect(page.getByRole('heading', { name: 'May pkg_tc depend on pkg_util?', level: 1 })).toBeVisible();
  await expect(page.getByRole('radiogroup', { name: 'Choose an answer' }).locator('.name')).toHaveText([
    'Allow until',
    'Reject, and say what to do instead',
  ]);
  await expect(page.getByRole('radio', { name: /Declare/ })).toHaveCount(0);
});

for (const [name, data] of [
  ['open', request],
  ['as-reported', { ...request, verified: false }],
  // A balance names the inputs it was computed with; a request stored from an engine v4.0.0 result has none.
  ['balance-inputs', { ...request, balance: { value: 9, max: 10, min: 7, strength: 'contract', volatility: 'low' } }],
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
    await expect(page.getByRole('heading', { name: 'May pkg_tc depend on pkg_util?', level: 1 })).toBeVisible();
    if (!('answer' in data)) {
      await expect(page.getByText('Pull request #12 passes when the policy is updated.').first()).toBeVisible();
    } else {
      await expect(answerBox(page).getByRole('radio')).toHaveCount(0);
      await page.getByText('The map', { exact: true }).click();
      await page.getByText('Show the map as a diagram', { exact: true }).click();
      await expect(page.getByRole('group', { name: 'Modules by layer and their dependencies' })).toBeVisible();
      const module = page.locator('[data-module]').first();
      await module.click();
      await expect(page.locator('.map-edge.' + (data.answer.kind === 'reject' ? 'deny' : 'exception'))).toHaveCount(1);
      await expect(page.locator('.map-edge.allowed')).toHaveCount(0);
      await module.click();
      await expect(page.locator('.map-edge')).toHaveCount(0);
      await expect(answerBox(page)).toContainText(
        data.answer.kind === 'reject' ? 'Rejected. What to do instead: Use a port.' : 'Allowed until 2027-01-15.',
      );
      await expect(page.getByRole('link', { name: 'Request #431', exact: true })).toHaveAttribute(
        'href',
        'https://github.com/acme/app/issues/431',
      );
    }
    await expectPageText(page, 'request-' + name);
    if (name === 'open') {
      await docsShot(page, 'request');
      await docsShot(page, 'answer-page-open');
    }
    await expectClean(page);
  });
}

test('request: old answered record links to issue without inventing an answer', async ({ page }) => {
  await open(page, path, { [`GET ${req}`]: () => [200, { ...request, state: 'answered' }] });
  await expect(answerBox(page)).not.toContainText('Allowed');
  await expect(answerBox(page)).not.toContainText('Rejected');
  await expect(answerBox(page).getByRole('radio')).toHaveCount(0);
  await expect(page.locator('dl.evidence').getByRole('link', { name: 'Request #431', exact: true })).toBeVisible();
});

for (const width of [390, 1280]) {
  test(`request: approved answer at ${width} @docs`, async ({ page }) => {
    await page.setViewportSize({ width, height: 844 });
    await docsClock(page);
    await open(page, path, {
      [`GET ${req}`]: () => [200, { ...request, state: 'answered', answer: { kind: 'approve' } }],
    });
    await expect(page.locator('.page-head .small')).toHaveText('An owner approved this request.');
    await expect(page.getByRole('list', { name: 'Story of the change' }).locator('[aria-current="step"]')).toContainText('policy');
    await docsShot(page, `request-approved-${width}`);
    await expectClean(page);
  });
}

test('request: an approved answer shows approved and the record as checked, and offers no answer', async ({ page }) => {
  await open(page, path, {
    [`GET ${req}`]: () => [200, { ...request, state: 'answered', answer: { kind: 'approve' } }],
  });
  await expect(page.locator('.page-head .small')).toHaveText('An owner approved this request.');
  await expect(page.getByText('The record as checked when the request was made', { exact: true })).toBeVisible();
  await expect(answerBox(page)).toContainText('Allowed.');
  await expect(answerBox(page).getByRole('radio')).toHaveCount(0);
  await expectClean(page);
});

for (const width of [390, 1280]) {
  for (const [kind, title, confirm] of [
    ['approve', 'Allow', 'Answer with passkey'],
    ['approve-until', 'Allow until', 'Answer with passkey'],
    ['reject', 'Reject, and say what to do instead', 'Answer'],
  ] as const) {
    test(`request: decision ${kind} at ${width} @docs`, async ({ page }) => {
      await page.setViewportSize({ width, height: 844 });
      await docsClock(page);
      const writes = await open(page, path, owner({}).handlers);
      const box = answerBox(page);
      await choose(page, title);
      if (kind === 'approve-until') await dateField(page).fill('2027-01-15');
      if (kind === 'reject') await insteadField(page).fill('Use a port.');
      await expect(box.locator('button.primary')).toHaveText(confirm);
      await expect(box.locator('button.primary')).toBeEnabled();
      await expect(box.locator('button.primary')).toHaveCount(1);
      await changeDisclosure(page).click();
      const detail = page.locator('details', { has: page.getByLabel('The change to .archfit.yaml') });
      await expect(detail.getByLabel('The change to .archfit.yaml')).toBeVisible();
      await expect(detail.getByRole('link', { name: 'in Archfit', exact: true }).first()).toHaveAttribute(
        'href',
        '/r/acme/app/pull/12',
      );
      await expect(detail.getByRole('link', { name: 'on GitHub', exact: true }).first()).toHaveAttribute(
        'href',
        'https://github.com/acme/app/pull/12',
      );
      await expect(page.locator('dl.evidence').getByRole('link', { name: 'Request #431', exact: true })).toHaveAttribute(
        'href',
        request.issue_href,
      );
      const target = await box.locator('button.primary').boundingBox();
      if (width === 390) expect(target?.height).toBeGreaterThanOrEqual(44);
      await expect(page.locator('.map-node')).toHaveCount(0);
      await docsShot(page, `request-${kind}-${width}`);
      await page.getByText('The map', { exact: true }).click();
      await page.getByText('Show the map as a diagram', { exact: true }).click();
      await expect(page.locator('.map-node')).toHaveCount(2);
      await expectClean(page);
      expect(writes).toEqual([]);
      await detail.getByRole('link', { name: 'in Archfit', exact: true }).first().click();
      await expect(page).toHaveURL(/\/pull\/12$/);
      expect(writes).toEqual([]);
    });
  }
}

test('request: a late preview read of an old instead text cannot restore it', async ({ page }) => {
  const { handlers } = owner({});
  let release: () => void = () => {};
  const held = new Promise<void>((resolve) => {
    release = resolve;
  });
  handlers[`GET ${req}/preview`] = async (_, url) => {
    if (url.searchParams.get('instead') === 'Old alternative') await held;
    return preview(url);
  };
  const writes = await open(page, path, handlers);
  await choose(page, 'Reject, and say what to do instead');
  await insteadField(page).fill('Old alternative');
  await insteadField(page).fill('New alternative');
  await changeDisclosure(page).click();
  await expect(page.getByLabel('The change to .archfit.yaml')).toContainText('instead: New alternative');
  const completed = page.waitForResponse((response) => response.url().includes('instead=Old'));
  release();
  await (await completed).finished();
  await expect(page.getByLabel('The change to .archfit.yaml')).toContainText('instead: New alternative');
  await expect(page.getByLabel('The change to .archfit.yaml')).not.toContainText('Old alternative');
  await insteadField(page).fill('');
  await expect(confirmButton(page, 'Answer')).toBeDisabled();
  await expect(changeDisclosure(page)).toHaveCount(0);
  expect(writes).toEqual([]);
});

test('request: a late preview read of an old date cannot restore it', async ({ page }) => {
  await docsClock(page);
  const { handlers } = owner({});
  let release: () => void = () => {};
  const held = new Promise<void>((resolve) => {
    release = resolve;
  });
  handlers[`GET ${req}/preview`] = async (_, url) => {
    if (url.searchParams.get('until') === '2027-01-15') await held;
    return preview(url);
  };
  const writes = await open(page, path, handlers);
  await choose(page, 'Allow until');
  await dateField(page).fill('2027-01-15');
  await dateField(page).fill('2027-02-15');
  await changeDisclosure(page).click();
  await expect(page.getByLabel('The change to .archfit.yaml')).toContainText('expires: 2027-02-15');
  const completed = page.waitForResponse((response) => response.url().includes('until=2027-01-15'));
  release();
  await (await completed).finished();
  await expect(page.getByLabel('The change to .archfit.yaml')).toContainText('expires: 2027-02-15');
  await expect(page.getByLabel('The change to .archfit.yaml')).not.toContainText('2027-01-15');
  expect(writes).toEqual([]);
});

test('request: a date that is not a date cannot be confirmed; a valid one reads a fresh preview', async ({ page }) => {
  await docsClock(page);
  const { handlers, reads } = owner({});
  const writes = await open(page, path, handlers);
  await choose(page, 'Allow until');
  await expect(confirmButton(page, 'Answer with passkey')).toBeEnabled();
  await dateField(page).fill('2027-02-30');
  await expect(confirmButton(page, 'Answer with passkey')).toBeDisabled();
  await expect(answerBox(page)).toContainText('Choose the date the exception ends.');
  await expect(changeDisclosure(page)).toHaveCount(0);
  await dateField(page).fill('2027-02-15');
  await expect(confirmButton(page, 'Answer with passkey')).toBeEnabled();
  expect(reads).toContain('?answer=approve-until&until=2027-02-15');
  expect(reads).not.toContain('?answer=approve-until&until=2027-02-30');
  await changeDisclosure(page).click();
  await expect(page.getByLabel('The change to .archfit.yaml')).toContainText('expires: 2027-02-15');
  expect(writes).toEqual([]);
});

test('request: a failed preview cannot confirm; a new date reads again', async ({ page }) => {
  await docsClock(page);
  const { handlers } = owner({});
  handlers[`GET ${req}/preview`] = () => [503, { error: 'Preview unavailable.' }];
  const writes = await open(page, path, handlers);
  await choose(page, 'Allow');
  await expect(answerBox(page).getByRole('alert')).toContainText('Preview unavailable.');
  await expect(confirmButton(page, 'Answer with passkey')).toBeDisabled();
  handlers[`GET ${req}/preview`] = (_, url) => preview(url);
  await choose(page, 'Allow until');
  await dateField(page).fill('2027-01-15');
  await expect(answerBox(page).getByRole('alert')).toHaveCount(0);
  await expect(confirmButton(page, 'Answer with passkey')).toBeEnabled();
  await changeDisclosure(page).click();
  await expect(page.getByLabel('The change to .archfit.yaml')).toContainText('2027-01-15');
  expect(writes).toEqual([]);
});

test('request: no policy change says so and answers without a policy pull request', async ({ page }) => {
  const { handlers } = owner({ notice: 'Answered. The policy needs no new line for it.' });
  handlers[`GET ${req}/preview`] = (_, url) =>
    url.searchParams.get('answer') === 'reject'
      ? [200, { answer: 'reject', diff: '', policy_digest: digest, loosens: false, pulls: [] }]
      : preview(url);
  const writes = await open(page, path, handlers);
  await choose(page, 'Reject, and say what to do instead');
  await insteadField(page).fill('Use a port.');
  await expect(page.getByText('Changes nothing in the policy. The author sees what you say to do instead.')).toBeVisible();
  await changeDisclosure(page).click();
  await expect(answerBox(page)).toContainText('It writes no line in .archfit.yaml.');
  await expectClean(page);
  await confirmButton(page, 'Answer').click();
  await expect(answerBox(page).locator('.banner')).toContainText('Answered.');
  await expect(answerBox(page).locator('.banner .title')).toHaveText('Answered.');
  expect(writes.map((write) => write.postDataJSON())).toEqual([{ answer: 'reject', instead: 'Use a port.' }]);
});

test('request: missing or unsafe issue destination is not invented', async ({ page }) => {
  await open(page, path, {
    [`GET ${req}`]: () => [200, { ...request, state: 'closed', issue_href: 'https://other.example/issues/431' }],
  });
  await expect(page.getByRole('heading', { name: 'May pkg_tc depend on pkg_util?', level: 1 })).toBeVisible();
  await expect(page.getByRole('link', { name: 'Request #431', exact: true })).toHaveCount(0);
});

test('request: an answer given elsewhere shows the live update banner after 20 seconds', async ({ page }) => {
  await page.clock.install({ time: new Date('2026-11-13T12:00:00Z') });
  const { handlers } = owner({});
  let state = 'open';
  handlers[`GET ${req}`] = () => [200, { ...request, state, answer: state === 'answered' ? { kind: 'approve' } : undefined }];
  await open(page, path, handlers);
  await expect(answerBox(page).getByRole('radio')).toHaveCount(3);
  await expect(page.getByText('was answered while you looked')).toHaveCount(0);
  state = 'answered';
  await page.clock.runFor(20_000);
  await expect(page.getByText('Request #431 was answered while you looked. The page shows the answer.')).toBeVisible();
  await expect(page.locator('.page-head .small')).toHaveText('An owner approved this request.');
  await expect(answerBox(page).getByRole('radio')).toHaveCount(0);
});

test('request: a policy that moved on the default branch shows the new effects after 20 seconds', async ({ page }) => {
  await page.clock.install({ time: new Date('2026-11-13T12:00:00Z') });
  const { handlers } = owner({});
  let moved = false;
  handlers[`GET ${req}/preview`] = (_, url) => {
    const [status, body] = preview(url);
    return [status, moved ? { ...body, policy_digest: 'e'.repeat(64) } : body];
  };
  await open(page, path, handlers);
  await expect(page.getByText('Pull request #12 passes when the policy is updated.').first()).toBeVisible();
  await expect(page.getByText('The policy on the default branch changed while you looked.')).toHaveCount(0);
  moved = true;
  await page.clock.runFor(20_000);
  await expect(
    page.getByText('The policy on the default branch changed while you looked. The effects below are the new ones.'),
  ).toBeVisible();
  await expect(answerBox(page).getByRole('radio')).toHaveCount(3);
});

test('request: the page never asks the owner to reload', async ({ page }) => {
  await docsClock(page);
  const { id } = await authenticator(page, true);
  const { handlers } = owner(approved, id);
  await open(page, path, handlers);
  expect(await bodyText(page)).not.toMatch(/reload/i);
  await choose(page, 'Allow');
  await changeDisclosure(page).click();
  expect(await bodyText(page)).not.toMatch(/reload/i);
  await confirmButton(page, 'Answer with passkey').click();
  await expect(answerBox(page).locator('.banner')).toBeVisible();
  expect(await bodyText(page)).not.toMatch(/reload/i);

  await page.unrouteAll();
  await open(page, path, { [`GET ${req}`]: () => [200, { ...request, state: 'closed' }] });
  await expect(answerBox(page)).toContainText('No answer is open.');
  expect(await bodyText(page)).not.toMatch(/reload/i);
});

for (const [name, title, edit, until, instead] of [
  ['date', 'Allow until', 'date', '2027-01-15', ''],
  ['reject text', 'Reject, and say what to do instead', 'instead', '', 'Second alternative'],
] as const) {
  test(`request: editing the ${name} disables the answer until its new preview lands`, async ({ page }) => {
    await docsClock(page);
    const { handlers } = owner({});
    let release: () => void = () => {};
    const held = new Promise<void>((resolve) => {
      release = resolve;
    });
    let started = false;
    handlers[`GET ${req}/preview`] = async (_, url) => {
      const target = edit === 'date' ? url.searchParams.get('until') === until : url.searchParams.get('instead') === instead;
      if (target) {
        started = true;
        await held;
      }
      return preview(url);
    };
    await open(page, path, handlers);
    await choose(page, title);
    if (edit === 'instead') {
      await insteadField(page).fill('First alternative');
      await expect(confirmButton(page, 'Answer')).toBeEnabled();
      await insteadField(page).fill(instead);
      await expect.poll(() => started).toBe(true);
      await expect(confirmButton(page, 'Answer')).toBeDisabled();
      await expect(changeDisclosure(page)).toHaveCount(0);
      release();
      await expect(confirmButton(page, 'Answer')).toBeEnabled();
      await changeDisclosure(page).click();
      await expect(page.getByLabel('The change to .archfit.yaml')).toContainText('instead: Second alternative');
    } else {
      await expect(confirmButton(page, 'Answer with passkey')).toBeEnabled();
      await dateField(page).fill(until);
      await expect.poll(() => started).toBe(true);
      await expect(confirmButton(page, 'Answer with passkey')).toBeDisabled();
      await expect(changeDisclosure(page)).toHaveCount(0);
      release();
      await expect(confirmButton(page, 'Answer with passkey')).toBeEnabled();
      await changeDisclosure(page).click();
      await expect(page.getByLabel('The change to .archfit.yaml')).toContainText(`expires: ${until}`);
    }
  });
}

test('request: a refresh read held across the owner\'s answer cannot bring back the open request or its banner', async ({ page }) => {
  await page.clock.install({ time: new Date('2026-11-13T12:00:00Z') });
  const { handlers } = owner({ notice: 'Answered. The policy change merged in #77.', pull_request: 77, state: 'merged' });
  const answerHandler = handlers[`POST ${req}/answer`];
  let answeredNow = false;
  let holdNext = false;
  let heldStarted = false;
  let release: () => void = () => {};
  const held = new Promise<void>((resolve) => {
    release = resolve;
  });
  handlers[`POST ${req}/answer`] = async (body, url) => {
    answeredNow = true;
    return answerHandler(body, url);
  };
  handlers[`GET ${req}`] = async () => {
    if (holdNext) {
      holdNext = false;
      heldStarted = true;
      await held;
      return [200, { ...request, state: 'open' }];
    }
    return [200, { ...request, state: answeredNow ? 'answered' : 'open', answer: answeredNow ? { kind: 'reject', instead: 'Use a port.' } : undefined }];
  };
  const writes = await open(page, path, handlers);
  await choose(page, 'Reject, and say what to do instead');
  await insteadField(page).fill('Use a port.');
  await expect(confirmButton(page, 'Answer')).toBeEnabled();
  holdNext = true;
  await page.clock.runFor(20_000);
  await expect.poll(() => heldStarted).toBe(true);
  await confirmButton(page, 'Answer').click();
  await expect(answerBox(page).locator('.banner')).toContainText('Answered. The policy is updated.');
  await expect(page.locator('.page-head .small')).toHaveText('An owner rejected this request.');
  expect(paths(writes)).toEqual([`${req}/answer`]);
  const completed = page.waitForResponse((r) => new URL(r.url()).pathname === req);
  release();
  await (await completed).finished();
  await expect(page.getByText('was answered while you looked')).toHaveCount(0);
  await expect(answerBox(page).getByRole('radio')).toHaveCount(0);
  await expect(page.locator('.page-head .small')).toHaveText('An owner rejected this request.');
  await expect(answerBox(page).locator('.banner')).toHaveCount(1);
});

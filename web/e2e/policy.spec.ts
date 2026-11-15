import { readFileSync } from 'node:fs';
import { expect, test } from '@playwright/test';
import {
  assertion,
  authenticator,
  ceremony,
  expectClean,
  expectPageText,
  docsClock,
  docsShot,
  open,
  origin,
  type Handlers,
} from './support';

const path = '/r/acme/app/policy';
const api = 'GET /api/repos/acme/app/policy';
const root = '/api/repos/acme/app';
const baseDigest = 'sha256:' + 'b'.repeat(64);
const newDigest = 'sha256:' + 'd'.repeat(64);
const target = 'e'.repeat(40);
const proposalYAML = 'version: 3\nowners: [alice]\nmodules:\n  app:\n    paths: [app/**]\n# earlier answer\n';
const editedYAML = proposalYAML + '# owner edit <img src=x onerror=alert(1)>\n';
const diff = '--- a/.archfit.yaml\n+++ b/.archfit.yaml\n@@ -6,0 +7 @@\n+# owner edit <img src=x onerror=alert(1)>\n';
const typical = JSON.parse(
  readFileSync(new URL('../../internal/control/ui/testdata/policy/typical.json', import.meta.url), 'utf8'),
);
const pumba = JSON.parse(
  readFileSync(new URL('../../internal/control/ui/testdata/policy/pumba.json', import.meta.url), 'utf8'),
);

for (const [name, fixture] of [
  ['typical', typical],
  ['pumba', pumba],
] as const) {
  test(`policy: ${name} golden${name === 'pumba' ? ' @docs' : ''}`, async ({ page }) => {
    await docsClock(page);
    const writes = await open(page, path, { [api]: () => [200, fixture] });
    await expect(page.getByRole('heading', { name: 'Policy', level: 1 })).toBeVisible();
    await expect(page.getByRole('heading', { name: 'Modules' })).toBeVisible();
    await expect(page.getByRole('link', { name: 'Policy', exact: true })).toHaveAttribute('aria-current', 'page');
    await expect(page.getByRole('link', { name: 'Edit', exact: true })).toHaveAttribute('href', path + '?edit=1');
    await expect(page.getByText(fixture.yaml, { exact: true })).toBeHidden();
    await expectPageText(page, `policy-${name}`);
    if (name === 'pumba') await docsShot(page, 'policy');
    await expectClean(page);
    await page.getByText('Show raw YAML').focus();
    await page.keyboard.press('Enter');
    await expect(page.getByText(fixture.yaml, { exact: true })).toBeVisible();
    await page.setViewportSize({ width: 390, height: 844 });
    expect(await page.evaluate(() => document.documentElement.scrollWidth)).toBeLessThanOrEqual(390);
    expect(writes).toEqual([]);
  });
}

test('policy: Edit opens the whole-file editor from the proposal', async ({ page }) => {
  const body = { ...typical, edit: { available: true, reason: '', file: proposalYAML, digest: baseDigest } };
  await open(page, path, {
    [api]: (_, url) => {
      if (url.searchParams.get('edit') === '1') return [200, body];
      return [200, typical];
    },
  });
  await page.getByRole('link', { name: 'Edit', exact: true }).click();
  await expect(page).toHaveURL(/\/policy\?edit=1$/);
  await expect(page.getByLabel('Policy YAML')).toHaveValue(proposalYAML);
  await expect(page.getByRole('button', { name: 'Preview the edit' })).toBeVisible();
  await expectClean(page);
});

test('policy: unavailable edit URL remains read-only and exceptions link only to supplied requests', async ({
  page,
}) => {
  const body = {
    ...typical,
    exceptions: [
      {
        rule: 'graph',
        from: '<img src=x onerror=alert(1)>',
        to: 'util',
        why: 'Approved.',
        expires: '2026-11-19',
        days_left: 0,
        request: 431,
        request_href: '/r/acme/app/requests/431',
      },
      { rule: 'cycle', from: 'util', why: 'Old.', expires: '2026-11-18', days_left: -1 },
      { rule: 'graph', from: 'next', why: 'A line added by edit.', request: 7, request_href: 'javascript:alert(1)' },
    ],
    edit: { available: false, reason: 'Change control has not started.' },
  };
  await open(page, path + '?edit=1', { [api]: () => [200, body] });
  await expect(page.getByText('Change control has not started.')).toBeVisible();
  await expect(page.getByRole('link', { name: 'Edit', exact: true })).toHaveCount(0);
  await expect(page.getByText('Expires: 2026-11-19 (0 days left)')).toBeVisible();
  await expect(page.getByText('Expires: 2026-11-18 (expired 1 day ago)')).toBeVisible();
  await expect(page.getByRole('link', { name: 'Request #431' })).toHaveAttribute('href', '/r/acme/app/requests/431');
  await expect(page.getByRole('link', { name: 'Request #7' })).toHaveCount(0);
  await expect(page.locator('main img')).toHaveCount(0);
  await expectClean(page);
});

test('policy: API failure is shown rather than an empty policy', async ({ page }) => {
  await open(page, path, { [api]: () => [409, { error: 'The policy could not be read.' }] });
  await expect(page.getByRole('alert')).toContainText('The policy could not be read.');
  await expect(page.getByRole('heading', { name: 'Modules' })).toHaveCount(0);
});

test('policy: omitted public paths mean the whole module is public', async ({ page }) => {
  await open(page, path, { [api]: () => [200, typical] });
  const modules = page.getByRole('region', { name: 'Modules', exact: true });
  const util = modules
    .locator('.box-row')
    .filter({ has: page.getByRole('heading', { name: 'pkg_util', exact: true }) });
  const chaos = modules
    .locator('.box-row')
    .filter({ has: page.getByRole('heading', { name: 'pkg_chaos', exact: true }) });
  await expect(util.locator('dd').nth(1)).toHaveText('Whole module');
  await expect(chaos.locator('dd').nth(1)).toHaveText('pkg/chaos, pkg/chaos/cliflags');
});

test('policy: repeated custom rule IDs retain both rules', async ({ page }) => {
  const body = {
    ...typical,
    rules: [
      { id: 'shared-id', note: 'One checked declaration.', not_checked: true, why: 'First reason.' },
      { id: 'shared-id', deny: { from: ['pkg_util'], to: ['pkg_chaos'] }, not_checked: false, why: 'Second reason.' },
    ],
  };
  await open(page, path, { [api]: () => [200, body] });
  const rules = page.getByRole('region', { name: 'Custom rules and notes', exact: true });
  await expect(rules.getByRole('heading', { name: 'shared-id', exact: true })).toHaveCount(1);
  await expect(rules.getByRole('heading', { name: 'shared-id (not checked)', exact: true })).toHaveCount(1);
  await expect(rules.getByText('Why: First reason.')).toBeVisible();
  await expect(rules.getByText('Why: Second reason.')).toBeVisible();
  await expect(page.getByRole('heading', { name: 'Policy file' })).toBeVisible();
});

function editor(passkey = '', refused = false): Handlers {
  let challenge = '';
  return {
    [api]: () => [200, { ...typical, edit: { available: true, reason: '', file: proposalYAML, digest: baseDigest } }],
    ['POST ' + root + '/policy/edit/preview']: (body) => {
      expect(body).toEqual({ answer: 'edit', yaml: editedYAML, base_digest: baseDigest });
      return [200, { digest: newDigest, target, diff, errors: [] }];
    },
    ['POST ' + root + '/passkeys/step-up']: (body) => {
      expect(body).toEqual({ action: 'edit', digest: newDigest, target, base_digest: baseDigest, yaml: editedYAML });
      const options = assertion([passkey]);
      challenge = options.publicKey.challenge;
      return [200, { options }];
    },
    ['POST ' + root + '/passkeys/step-up/finish']: (body) => {
      const c = ceremony(body.credential);
      expect(c.client).toMatchObject({ type: 'webauthn.get', challenge, origin });
      expect(c.verified).toBe(true);
      expect(c.rawId).toBe(passkey);
      return [200, { token: 'edit-token' }];
    },
    ['POST ' + root + '/policy/edit']: (body) => {
      expect(body).toEqual({ answer: 'edit', yaml: editedYAML, base_digest: baseDigest, target, token: 'edit-token' });
      return refused
        ? [409, { error: 'the policy changed; reload' }]
        : [200, { pull_request: 77, state: 'merged', notice: 'Answered. The policy change merged in #77.' }];
    },
  };
}

test('policy edit: line errors appear before any passkey step', async ({ page }) => {
  const handlers = editor();
  handlers['POST ' + root + '/policy/edit/preview'] = () => [
    200,
    {
      diff: '',
      errors: [
        '.archfit.yaml:2: policy: owners: required: list at least one GitHub login',
        '.archfit.yaml:3: policy: modules: required: declare at least one module',
      ],
    },
  ];
  const writes = await open(page, path + '?edit=1', handlers);
  await page.getByLabel('Policy YAML').fill('version: 3\nowners: []\nmodules: {}\n');
  await page.getByRole('button', { name: 'Preview the edit' }).click();
  await expect(page.getByRole('alert')).toContainText('.archfit.yaml:2: policy:');
  await expect(page.getByRole('alert')).toContainText('.archfit.yaml:3: policy:');
  await expect(page.getByRole('button', { name: 'Approve and merge with passkey' })).toHaveCount(0);
  expect(writes.map((r) => new URL(r.url()).pathname)).toEqual([root + '/policy/edit/preview']);
  await expectPageText(page, 'policy-edit-errors');
  await expectClean(page);
});

test('policy edit: server diff, local preview command and a verified passkey precede merge @docs', async ({ page }) => {
  await docsClock(page);
  const device = await authenticator(page, true);
  const writes = await open(page, path + '?edit=1', editor(device.id));
  await page.getByLabel('Policy YAML').fill(editedYAML);
  await page.getByRole('button', { name: 'Preview the edit' }).click();
  await expect(page.getByLabel('The policy change')).toHaveText(diff);
  await expect(page.getByText('preview it:')).toContainText('archfit check --policy <file>');
  await expect(page.locator('main img')).toHaveCount(0);
  await expectPageText(page, 'policy-edit-preview');
  await docsShot(page, 'policy-edit');
  await expectClean(page);
  await page.getByRole('button', { name: 'Approve and merge with passkey' }).click();
  await expect(page.getByRole('status')).toHaveText('Answered. The policy change merged in #77.');
  expect(writes.map((r) => new URL(r.url()).pathname)).toEqual([
    root + '/policy/edit/preview',
    root + '/passkeys/step-up',
    root + '/passkeys/step-up/finish',
    root + '/policy/edit',
  ]);
  await expectClean(page);
});

test('policy edit: changing any text drops the preview and the passkey step', async ({ page }) => {
  await open(page, path + '?edit=1', editor());
  await page.getByLabel('Policy YAML').fill(editedYAML);
  await page.getByRole('button', { name: 'Preview the edit' }).click();
  await expect(page.getByRole('button', { name: 'Approve and merge with passkey' })).toBeVisible();
  await page.getByLabel('Policy YAML').fill(editedYAML + '# later\n');
  await expect(page.getByRole('button', { name: 'Approve and merge with passkey' })).toHaveCount(0);
  await expect(page.getByLabel('The policy change')).toHaveCount(0);
  await expect(page.getByRole('button', { name: 'Preview the edit' })).toBeVisible();
});

test('policy edit: a moved target refuses the confirmed edit with the reload sentence', async ({ page }) => {
  const device = await authenticator(page, true);
  await open(page, path + '?edit=1', editor(device.id, true));
  await page.getByLabel('Policy YAML').fill(editedYAML);
  await page.getByRole('button', { name: 'Preview the edit' }).click();
  await page.getByRole('button', { name: 'Approve and merge with passkey' }).click();
  await expect(page.getByRole('alert')).toHaveText('the policy changed; reload');
  await expect(page.getByRole('status')).toHaveCount(0);
  await expectClean(page);
});

test('policy edit: explicit reload restores the recorded proposal before preview', async ({ page }) => {
  const handlers = editor();
  handlers[api] = () => [
    200,
    { ...typical, edit: { available: true, reason: '', file: proposalYAML, digest: baseDigest, recovery: true } },
  ];
  handlers['POST ' + root + '/policy/edit/reload'] = () => [
    200,
    { available: true, reason: '', file: proposalYAML, digest: baseDigest },
  ];
  const writes = await open(page, path + '?edit=1', handlers);
  await expect(page.getByLabel('Policy YAML')).toBeDisabled();
  await expect(page.getByRole('button', { name: 'Preview the edit' })).toHaveCount(0);
  await page.getByRole('button', { name: 'Reload the policy' }).click();
  await expect(page.getByLabel('Policy YAML')).toBeEnabled();
  await expect(page.getByRole('button', { name: 'Preview the edit' })).toBeVisible();
  expect(writes.map((r) => new URL(r.url()).pathname)).toEqual([root + '/policy/edit/reload']);
  await expectClean(page);
});

test('policy edit: an unreadable preview cannot prompt for a passkey', async ({ page }) => {
  const handlers = editor();
  handlers['POST ' + root + '/policy/edit/preview'] = () => [200, { errors: [], diff: 'unchecked' }];
  const writes = await open(page, path + '?edit=1', handlers);
  await page.getByRole('button', { name: 'Preview the edit' }).click();
  await expect(page.getByRole('alert')).toHaveText('The edit preview could not be read.');
  await expect(page.getByRole('button', { name: 'Approve and merge with passkey' })).toHaveCount(0);
  expect(writes).toHaveLength(1);
});

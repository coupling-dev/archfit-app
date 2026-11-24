import { createHash } from 'node:crypto';
import { readFileSync } from 'node:fs';
import { expect, test } from '@playwright/test';
import {
  assertion,
  authenticator,
  ceremony,
  expectClean,
  expectNoNotUsedWord,
  expectPageText,
  open,
  origin,
} from './support';
import { ownerButton, readOwnerView, type OwnerFile, type OwnerKind } from '../src/lib/owner';

const root = '/r/acme/app/owner/';
const api = '/api/repos/acme/app/';
const fixtures = new URL('../../internal/control/ui/testdata/owner/', import.meta.url);
function fixture(name: string) {
  const action = (name === 'first-pull' ? 'first' : name === 'revert-answers' ? 'revert' : name) as OwnerKind;
  const pull = name === 'first-pull' || name === 'workflow' ? 7 : 0;
  return readOwnerView(JSON.parse(readFileSync(new URL(name + '.json', fixtures), 'utf8')), {
    owner: 'acme',
    name: 'app',
    fullName: 'acme/app',
    action,
    pull,
  });
}

for (const name of ['first', 'first-pull', 'workflow', 'confirm', 'revert', 'revert-answers']) {
  test(`owner: ${name} text and passkey-bound submission`, async ({ page }) => {
    await page.clock.install({ time: new Date('2026-11-13T12:00:00Z') });
    const body = fixture(name);
    const { id } = await authenticator(page, true);
    let challenge = '';
    const writes = await open(page, root + body.kind + (body.pull_request ? '?pull=' + body.pull_request : ''), {
      [`GET ${api}owner/${body.kind}/view`]: (_, url) => {
        expect(url.searchParams.get('pull')).toBe(body.pull_request ? String(body.pull_request) : null);
        return [200, body];
      },
      [`POST ${api}passkeys/step-up`]: (purpose) => {
        expect(purpose).toEqual({ action: body.kind, digest: body.digest });
        const options = assertion([id]);
        challenge = options.publicKey.challenge;
        return [200, { options }];
      },
      [`POST ${api}passkeys/step-up/finish`]: (sent) => {
        const c = ceremony(sent.credential);
        expect(c.client).toMatchObject({ type: 'webauthn.get', challenge, origin });
        expect(c.verified).toBe(true);
        return [200, { token: 'view-bound-token' }];
      },
      [`POST ${api}owner/${body.kind}`]: (sent) => {
        expect(sent).toEqual({
          pull_request: body.pull_request,
          digest: body.action_digest,
          view_digest: body.digest,
          token: 'view-bound-token',
        });
        return [
          200,
          {
            approval: 'a1',
            ...(body.kind === 'revert' ? { pull_request: 12, state: 'merged' } : { check: 'pending' }),
          },
        ];
      },
    });
    await expect(page.getByRole('heading', { level: 1, name: body.main_action })).toBeVisible();
    await expect(page.locator('.page-head .page-status')).toHaveText(body.status);
    await expect(page.getByRole('button', { name: ownerButton(body.kind), exact: true })).toHaveClass(/primary approval/);
    await expect(page.locator('main button')).toHaveCount(1);
    expect(writes).toEqual([]);
    await expectPageText(page, 'owner-' + name);
    await expectClean(page);
    await page.getByText('Full files and approval details', { exact: true }).click();
    const fingerprint = page.getByText(body.files[0].digest, { exact: true });
    await expect(fingerprint).toBeHidden();
    await fingerprint.locator('..').locator('..').getByText('Technical details', { exact: true }).click();
    await expect(fingerprint).toBeVisible();
    await expectNoNotUsedWord(page);
    await page.getByRole('button', { name: ownerButton(body.kind), exact: true }).focus();
    await page.keyboard.press('Enter');
    await expect(page.getByRole('status')).toContainText(
      body.kind === 'revert' ? 'Policy reverted.' : body.kind === 'confirm' ? 'Policy confirmed.' : 'approved',
    );
    await expect(page.locator('main button')).toHaveCount(0);
    expect(writes.map((w) => new URL(w.url()).pathname)).toEqual([
      api + 'passkeys/step-up',
      api + 'passkeys/step-up/finish',
      api + 'owner/' + body.kind,
    ]);
    await expectClean(page);
  });
}

for (const changed of [false, true]) {
  for (const width of [390, 1280]) {
    test(`owner: workflow with ${changed ? 'changed' : 'unchanged'} policy at ${width}px`, async ({
      page,
    }, testInfo) => {
      const body = fixture('workflow');
      body.tip = '1'.repeat(40);
      body.base = '2'.repeat(40);
      body.head = '3'.repeat(40);
      body.approved = '4'.repeat(40);
      const policy = '.archfit.yaml';
      const workflow = '.github/workflows/archfit.yaml';
      const file = (role: string, ref: string, path: string, text: string): OwnerFile => ({
        role,
        ref,
        path,
        text,
        missing: false,
        digest: 'sha256:' + createHash('sha256').update(text).digest('hex'),
      });
      const base = 'version: 1\nowners: [alice]\n';
      const head = changed ? base + '# proposed policy\n' : base;
      body.files = [
        file('approved', body.approved, policy, 'version: 1\nowners: [alice, bob]\n'),
        file('base', body.base, policy, base),
        file('head', body.head, policy, head),
        file('workflow-base', body.tip, workflow, 'name: old\n'),
        file('workflow-head', body.head, workflow, 'name: archfit\n'),
      ];
      body.diffs[1].text = changed
        ? '--- a/.archfit.yaml\n+++ b/.archfit.yaml\n@@ -1,2 +1,3 @@\n version: 1\n owners: [alice]\n+# proposed policy\n'
        : '';
      await page.setViewportSize({ width, height: 900 });
      const writes = await open(page, root + 'workflow?pull=7', {
        [`GET ${api}owner/workflow/view`]: () => [200, body],
      });
      await expect(page.locator('.page-head .page-status')).toHaveText(
        'unknown — no verdict until this workflow change is approved.',
      );
      await expect(
        page.getByText(
          'This pull request changes the workflow that runs Archfit. The App cannot use its result without your approval.',
          { exact: true },
        ),
      ).toBeVisible();
      await expect(page.getByRole('link', { name: 'Read the change' })).toHaveCount(0);
      await expect(page.locator('main a.btn')).toHaveCount(0);
      await expect(page.locator('main .primary')).toHaveCount(1);
      const confirm = page.getByRole('button', { name: 'Approve with passkey', exact: true });
      await expect(page.locator('main button')).toHaveCount(1);
      await expect(confirm).toBeEnabled();
      expect(await confirm.evaluate((el) => getComputedStyle(el).backgroundColor)).toBe('rgb(31, 136, 61)');
      await expect(page.getByRole('link', { name: 'Pull request #7 on GitHub' })).toHaveAttribute(
        'href',
        'https://github.com/acme/app/pull/7',
      );
      await expect(page.getByRole('link', { name: 'View policy' })).toHaveAttribute('href', '/r/acme/app/policy');
      await expect(page.getByRole('heading', { name: 'Workflow — ' + workflow })).toBeVisible();
      await expect(page.getByText('Compared with the default branch at 1111111.')).toBeVisible();
      const policyBlock = page
        .getByRole('heading', { name: 'Policy — ' + policy })
        .locator('..')
        .locator('..');
      await expect(policyBlock).toContainText(
        `${changed ? 'Compared with' : 'Unchanged from'} this pull request's base at 2222222. These policy bytes are included in this approval.`,
      );
      await expect(policyBlock.getByRole('region')).toHaveCount(changed ? 1 : 0);
      await expect(page.getByText('already approved', { exact: false })).toHaveCount(0);
      await expect(page.getByText('Approve the workflow and policy shown for pull request #7 at commit 3333333.')).toBeVisible();
      const workflowDiff = page.getByRole('region', { name: `Diff: The workflow change (${workflow})` });
      await expect(workflowDiff.locator('.hunk')).toHaveText('@@ -1,1 +1,1 @@\n');
      await expect(workflowDiff.locator('.add')).toHaveText('+name: archfit\n');
      await expect(workflowDiff.locator('.del')).toHaveText('-name: old\n');
      expect(await workflowDiff.textContent()).toBe(body.diffs[0].text);
      const lineTops = await workflowDiff
        .locator('.line')
        .evaluateAll((lines) => lines.map((line) => line.getBoundingClientRect().top));
      expect(lineTops.every((top, index) => index === 0 || top > lineTops[index - 1])).toBe(true);
      await workflowDiff.focus();
      await expect(workflowDiff).toBeFocused();
      await expectPageText(page, `owner-workflow-${changed ? 'changed' : 'unchanged'}-${width}`);
      for (const colorScheme of ['light', 'dark'] as const) {
        await page.emulateMedia({ colorScheme });
        await page.screenshot({ path: testInfo.outputPath(`owner-${colorScheme}.png`), fullPage: true });
      }
      await page.getByText('Full files and approval details', { exact: true }).click();
      await expect(page.getByRole('heading', { level: 2, name: policy, exact: true })).toHaveCount(1);
      await expect(page.getByRole('heading', { level: 2, name: workflow, exact: true })).toHaveCount(1);
      for (const version of ['Default branch', 'Pull request base', 'Pull request', 'Previously approved']) {
        await expect(
          page.getByRole('heading', { name: new RegExp('^' + version + ' — commit') }).first(),
        ).toBeVisible();
      }
      const policyHead = page.getByRole('region', { name: 'File: Pull request .archfit.yaml', exact: true });
      expect(await policyHead.textContent()).toBe(head);
      const policyHeadSection = policyHead.locator('..');
      await expect(policyHeadSection.getByText(body.files[2].digest, { exact: true })).toBeHidden();
      await policyHeadSection.getByText('Technical details', { exact: true }).click();
      await expect(policyHeadSection.getByText(body.files[2].digest, { exact: true })).toBeVisible();
      await page.getByText('Why a passkey?', { exact: true }).click();
      await expect(
        page.getByText(
          'GitHub sign-in identifies your account. An agent can also use that account. Your passkey adds a separate confirmation for these file versions. Read both files before you confirm. Approval does not mean the check passed.',
          { exact: true },
        ),
      ).toBeVisible();
      await expect(
        page.getByText('If your passkey is unavailable, use a second registered passkey.', { exact: true }),
      ).toBeVisible();
      expect(writes).toEqual([]);
      await expectClean(page);
    });
  }
}

for (const passkey of [true, false]) {
  test(`owner: passkey guidance with registered passkey = ${passkey}`, async ({ page }) => {
    const body = { ...fixture('workflow'), passkey };
    const path = root + 'workflow?pull=7';
    const requests: unknown[] = [];
    await open(page, path, {
      [`GET ${api}owner/workflow/view`]: () => [200, body],
      [`POST ${api}passkeys/confirmation`]: (sent) => {
        requests.push(sent);
        return [200, { attempt: 'attempt-one', installation: 7 }];
      },
    });
    const register = page.getByRole('link', { name: 'Register your passkey' });
    const manage = page.getByRole('link', { name: 'Manage your passkeys' });
    const notice = page.getByText('Approving needs a passkey. Your first passkey needs a one-time token from an Archfit operator: request it now.', { exact: true });
    if (passkey) {
      await expect(manage).toHaveAttribute('href', '/r/acme/app/passkeys');
      await expect(register).toHaveCount(0);
      await expect(notice).toHaveCount(0);
    } else {
      await expect(register).toHaveAttribute('href', '/r/acme/app/passkeys?return=' + encodeURIComponent(path));
      await expect(manage).toHaveCount(0);
      await expect(notice).toBeVisible();
      await page.getByRole('button', { name: 'Request the token' }).click();
      await expect(page.getByText('installation 7, attempt attempt-one', { exact: false })).toBeVisible();
      expect(requests).toEqual([{}]);
    }
    await expect(page.getByRole('button', { name: ownerButton(body.kind), exact: true })).toBeEnabled();
    await expectClean(page);
  });
}

for (const [name, status, sentence] of [
  ['passkey-refused', 403, 'Passkey confirmation could not start. Try again.'],
  ['moved', 409, 'The change moved since you looked. Reload it and confirm again with your passkey.'],
] as const) {
  test(`owner: ${name} is explicit, with no unseen approval`, async ({ page }) => {
    const body = fixture('workflow');
    const writes = await open(page, root + 'workflow?pull=7', {
      [`GET ${api}owner/workflow/view`]: () => [200, body],
      [`POST ${api}passkeys/step-up`]: () => [status, { error: sentence }],
    });
    await page.getByRole('button', { name: ownerButton(body.kind), exact: true }).click();
    await expect(page.getByRole('alert')).toHaveText(sentence);
    await expectPageText(page, 'owner-' + name);
    expect(writes).toHaveLength(1);
    if (status === 409) {
      await expect(page.getByRole('button', { name: ownerButton(body.kind), exact: true })).toBeDisabled();
      await expect(page.getByRole('link', { name: 'Reload the change' })).toHaveAttribute(
        'href',
        root + 'workflow?pull=7',
      );
    }
    await expectClean(page);
  });
}

test('owner: a malformed view gives no approval button', async ({ page }) => {
  await open(page, root + 'first', { [`GET ${api}owner/first/view`]: () => [200, { kind: 'first' }] });
  await expect(page.getByRole('alert')).toHaveText('The owner action could not be read. Reload the page.');
  await expect(page.locator('main button')).toHaveCount(0);
  await expectPageText(page, 'owner-malformed');
  await expectClean(page);
});

test('owner: unsafe policy text stays text, and phone width stays inside the page', async ({ page }) => {
  const body = fixture('first');
  body.diffs[0].text += '+# <img src=x onerror=alert(1)>\n';
  await page.setViewportSize({ width: 390, height: 844 });
  await open(page, root + 'first', { [`GET ${api}owner/first/view`]: () => [200, body] });
  await expect(page.getByRole('region', { name: 'Diff: The first policy (.archfit.yaml)' })).toContainText(
    '<img src=x onerror=alert(1)>',
  );
  await expect(page.locator('main img')).toHaveCount(0);
  await expectClean(page);
});

test('owner: unreadable file gives the GitHub fallback and no action', async ({ page }) => {
  const sentence =
    'Archfit cannot show this file as text, so it cannot be approved here. Use the GitHub issue.';
  await open(page, root + 'revert', { [`GET ${api}owner/revert/view`]: () => [409, { error: sentence }] });
  await expect(page.getByRole('alert')).toHaveText(sentence);
  await expect(page.locator('main button')).toHaveCount(0);
  await expectPageText(page, 'owner-unreadable');
  await expectClean(page);
});

test('owner: the first-policy page links back to the request that sent the owner, and only to one of this repository', async ({ page }) => {
  const handlers = { [`GET ${api}owner/first/view`]: () => [200, fixture('first')] as [number, object] };
  await open(page, root + 'first?return=' + encodeURIComponent('/r/acme/app/requests/431'), handlers);
  await expect(page.getByRole('link', { name: 'Back to the request', exact: true })).toHaveAttribute('href', '/r/acme/app/requests/431');

  for (const other of ['/r/acme/other/requests/431', 'https://evil.example/r/acme/app/requests/431', '/r/acme/app/policy?edit=1']) {
    await page.unrouteAll();
    await open(page, root + 'first?return=' + encodeURIComponent(other), handlers);
    await expect(page.getByRole('button', { name: 'Approve with passkey' })).toBeVisible();
    await expect(page.getByRole('link', { name: 'Back to the request', exact: true })).toHaveCount(0);
  }
});

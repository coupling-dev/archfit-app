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
import { readOwnerView, type OwnerKind } from '../src/lib/owner';

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
    await expect(page.locator('.lede')).toHaveText(body.status);
    await expect(page.locator('main button')).toHaveCount(1);
    expect(writes).toEqual([]);
    await expectPageText(page, 'owner-' + name);
    await expectClean(page);
    await page.getByText('Show the files and their SHA-256s', { exact: true }).click();
    await expect(page.getByText(body.files[0].digest, { exact: true })).toBeVisible();
    await expectNoNotUsedWord(page);
    await page.getByRole('button', { name: body.main_action, exact: true }).focus();
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

for (const [name, status, sentence] of [
  ['passkey-refused', 403, 'The passkey step-up could not be started. Try again.'],
  ['moved', 409, 'The change moved since you looked. Reload it and confirm again with your passkey.'],
] as const) {
  test(`owner: ${name} is explicit, with no unseen approval`, async ({ page }) => {
    const body = fixture('workflow');
    const writes = await open(page, root + 'workflow?pull=7', {
      [`GET ${api}owner/workflow/view`]: () => [200, body],
      [`POST ${api}passkeys/step-up`]: () => [status, { error: sentence }],
    });
    await page.getByRole('button', { name: body.main_action, exact: true }).click();
    await expect(page.getByRole('alert')).toHaveText(sentence);
    await expectPageText(page, 'owner-' + name);
    expect(writes).toHaveLength(1);
    if (status === 409) {
      await expect(page.getByRole('button', { name: body.main_action, exact: true })).toBeDisabled();
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
  await expect(page.getByRole('region', { name: 'Diff: The first policy (.archfit.yaml)' })).toContainText('<img src=x onerror=alert(1)>');
  await expect(page.locator('main img')).toHaveCount(0);
  await expectClean(page);
});

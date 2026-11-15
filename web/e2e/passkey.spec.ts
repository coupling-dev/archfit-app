import { expect, test } from '@playwright/test';
import { assertion, authenticator, ceremony, creation, expectClean, expectNoNotUsedWord, open, origin, type Handlers } from './support';

const page = '/r/acme/app/passkeys';
const api = '/api/repos/acme/app/passkeys';
const created = '2026-11-10T09:00:00Z';

interface Listed {
  id: string;
  created: string;
  last_used?: string;
}

// registry is the server side of the passkey routes for one user: it hands out the options and checks that each
// ceremony the page sent back answers them, as go-webauthn would before it verifies the signature.
function registry(list: Listed[]) {
  let challenge = '';
  let tokens = 0;
  const issued = new Map<string, object>();
  const handlers: Handlers = {
    [`GET ${api}`]: () => [200, { passkeys: list }],
    [`POST ${api}/register`]: (body) => {
      if (list.length && !issued.has(String(body.token)))
        return [
          409,
          { error: 'You already have a passkey. Confirm with it first: a new passkey needs an existing one.' },
        ];
      const o = creation(list.map((p) => p.id));
      challenge = o.publicKey.challenge;
      return [200, { options: o }];
    },
    [`POST ${api}/register/finish`]: (body) => {
      const c = ceremony(body.credential);
      expect(c.type).toBe('public-key');
      expect(c.id).toBe(c.rawId);
      expect(c.client).toMatchObject({ type: 'webauthn.create', challenge, origin });
      const p = { id: c.rawId, created };
      list.push(p);
      return [200, { passkey: p, notice: 'Passkey registered. An issue tells you and the owners.' }];
    },
    [`POST ${api}/step-up`]: (body) => {
      const o = assertion(list.map((p) => p.id));
      challenge = o.publicKey.challenge;
      issued.set('pending', body);
      return [200, { options: o }];
    },
    [`POST ${api}/step-up/finish`]: (body) => {
      const c = ceremony(body.credential);
      expect(c.client).toMatchObject({ type: 'webauthn.get', challenge, origin });
      expect(c.verified).toBe(true);
      expect(list.map((p) => p.id)).toContain(c.rawId);
      const token = `token-${++tokens}`;
      issued.set(token, issued.get('pending')!);
      return [200, { token }];
    },
  };
  return { handlers, issued };
}

test('passkeys: the first passkey registers with no step-up, and the list shows it', async ({ page: p }) => {
  await authenticator(p);
  const list: Listed[] = [];
  const writes = await open(p, page, registry(list).handlers);
  await expect(p.getByRole('heading', { name: 'Passkeys', level: 1 })).toBeVisible();
  await expect(p.locator('.lede')).toHaveText('You have no passkey in this App installation yet.');
  await expectClean(p);

  await p.getByRole('button', { name: 'Add a passkey' }).click();
  await expect(p.getByRole('status')).toHaveText('Passkey registered. An issue tells you and the owners.');
  await expect(p.locator('.lede')).toHaveText('You have 1 passkey in this App installation.');
  await expect(p.getByRole('listitem')).toContainText([/^Passkey 1 \S+ · added 2026-11-10 ·\s+not used yet/]);
  expect(writes.map((w) => new URL(w.url()).pathname)).toEqual([`${api}/register`, `${api}/register/finish`]);
  expect(writes[0].postDataJSON()).toEqual({});
  await expectClean(p);
});

test('passkeys: a second passkey and a removal are each confirmed with a passkey the user has', async ({ page: p }) => {
  const device = await authenticator(p, true);
  const first = device.id;
  const list: Listed[] = [{ id: first, created, last_used: '2026-11-11T08:00:00Z' }];
  const { handlers, issued } = registry(list);
  // Once the first passkey confirmed the new one, the user makes the new one on another device: a device refuses a
  // second passkey of the same user (the next test). Chromium has one virtual platform device, so it forgets the first.
  const confirmed = handlers[`POST ${api}/step-up/finish`];
  let moved = false;
  handlers[`POST ${api}/step-up/finish`] = async (body, url) => {
    const reply = await confirmed(body, url);
    if (!moved) await device.forget();
    moved = true;
    return reply;
  };
  handlers[`POST ${api}/${first}/remove`] = (body) => {
    expect(issued.get(String(body.token))).toEqual({ action: 'remove', passkey: first });
    list.splice(0, 1);
    return [200, { notice: 'Passkey removed.' }];
  };
  const writes = await open(p, page, handlers);
  await expect(p.getByRole('listitem')).toContainText([/^Passkey 1 \S+ · added 2026-11-10 ·\s+last used 2026-11-11/]);

  await p.getByRole('button', { name: 'Add a passkey' }).click();
  await expect(p.getByRole('status')).toHaveText('Passkey registered. An issue tells you and the owners.');
  await expect(p.locator('.lede')).toHaveText('You have 2 passkeys in this App installation.');
  await expectNoNotUsedWord(p);
  expect(issued.get('token-1')).toEqual({ action: 'register' });
  expect(writes[2].postDataJSON()).toEqual({ token: 'token-1' });

  await p.getByRole('button', { name: 'Remove' }).first().click();
  await p.getByRole('button', { name: 'Confirm removal' }).click();
  await expect(p.getByRole('status')).toHaveText('Passkey removed.');
  await expect(p.locator('.lede')).toHaveText('You have 1 passkey in this App installation.');
  expect(writes.map((w) => new URL(w.url()).pathname)).toEqual([
    `${api}/step-up`,
    `${api}/step-up/finish`,
    `${api}/register`,
    `${api}/register/finish`,
    `${api}/step-up`,
    `${api}/step-up/finish`,
    `${api}/${first}/remove`,
  ]);
  await expectClean(p);
});

test('passkeys: a refused registration is the server sentence, and nothing is added', async ({ page: p }) => {
  await authenticator(p);
  const sentence =
    'Only an owner named in the policy, or a repository administrator before the first policy, can register a passkey here.';
  const writes = await open(p, page, {
    [`GET ${api}`]: () => [200, { passkeys: [] }],
    [`POST ${api}/register`]: () => [403, { error: sentence }],
  });
  await p.getByRole('button', { name: 'Add a passkey' }).click();
  await expect(p.getByRole('alert')).toHaveText(sentence);
  await expect(p.locator('.lede')).toHaveText('You have no passkey in this App installation yet.');
  expect(writes).toHaveLength(1);
  await expectClean(p);
});

test('passkeys: a device that already holds a passkey of the user does not make a second one', async ({ page: p }) => {
  const { id: first } = await authenticator(p, true);
  const list: Listed[] = [{ id: first, created }];
  const writes = await open(p, page, registry(list).handlers);
  await p.getByRole('button', { name: 'Add a passkey' }).click();
  await expect(p.getByRole('alert')).toHaveText(
    'This device already holds one of your passkeys. Use another device or another passkey.',
  );
  expect(writes.map((w) => new URL(w.url()).pathname)).toEqual([`${api}/step-up`, `${api}/step-up/finish`, `${api}/register`]);
  await expect(p.getByRole('listitem')).toHaveCount(1);
  await expectNoNotUsedWord(p);
});

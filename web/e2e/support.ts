// What the request and passkey specs share: the App's API mocked in the browser, the page opened at localhost under the
// production CSP, a Chromium virtual authenticator, the "Not used" words check (design 5), and axe.
import { generateKeyPairSync, randomBytes } from 'node:crypto';
import { readFileSync } from 'node:fs';
import AxeBuilder from '@axe-core/playwright';
import { expect, type Page, type Request } from '@playwright/test';

const csp = readFileSync(new URL('../csp.txt', import.meta.url), 'utf8').trim();

export const session = { login: 'alice', csrf: 'csrf-token-1' };

// WebAuthn takes a domain as relying party id, never an IP address, so these pages open at localhost, not 127.0.0.1.
export const origin = `http://localhost:${process.env.E2E_PORT}`;
export const rpId = 'localhost';

export type Reply = [status: number, body: object];
// Handlers answer "METHOD /api/path" (no query). The body is the JSON the page sent, the URL the whole request URL.
export type Handlers = Record<string, (body: Record<string, unknown>, url: URL) => Reply | Promise<Reply>>;

// open answers /api from handlers (a session by default, "Not found." for anything else), opens path, and returns the
// writes the page sent, in order.
export async function open(page: Page, path: string, handlers: Handlers): Promise<Request[]> {
  const writes: Request[] = [];
  await page.route('**/api/**', async (route) => {
    const req = route.request();
    const url = new URL(req.url());
    const post = req.method() === 'POST';
    if (post) writes.push(req);
    if (post) expect(req.headers()['x-csrf-token']).toBe(session.csrf);
    const key = `${req.method()} ${url.pathname}`;
    const handle = handlers[key] ?? (key === 'GET /api/session' ? () => [200, session] as Reply : undefined);
    const [status, json] = handle ? await handle(post ? req.postDataJSON() : {}, url) : [404, { error: 'Not found.' }];
    await route.fulfill({ status, json });
  });
  await page.addInitScript(() => {
    const seen: string[] = [];
    (window as unknown as { cspViolations: string[] }).cspViolations = seen;
    document.addEventListener('securitypolicyviolation', (e) => seen.push(`${e.violatedDirective} ${e.blockedURI}`));
  });
  const res = await page.goto(origin + path);
  expect(res?.headers()['content-security-policy']).toBe(csp);
  return writes;
}

// expectClean fails on a CSP violation, an axe violation in either scheme, a word of the "Not used" column, or a page
// wider than a phone at phone width.
export async function expectClean(page: Page) {
  const violations = await page.evaluate(() => (window as unknown as { cspViolations: string[] }).cspViolations);
  expect(violations).toEqual([]);
  expect(notUsedIn(await page.locator('body').innerText())).toEqual([]);
  for (const scheme of ['light', 'dark'] as const) {
    await page.emulateMedia({ colorScheme: scheme });
    const axe = await new AxeBuilder({ page }).analyze();
    expect(axe.violations.map((v) => `${scheme}: ${v.id} ${v.nodes.map((n) => n.target).join(', ')}`)).toEqual([]);
  }
  const size = page.viewportSize();
  await page.setViewportSize({ width: 390, height: 844 });
  expect(await page.evaluate(() => document.documentElement.scrollWidth)).toBeLessThanOrEqual(390);
  if (size) await page.setViewportSize(size);
}

// The "Not used" words of design 5, matched as the engine matches them (internal/wire/wording): whole words in any case,
// a phrase across any white space, after the two gate names are removed.
const notUsed = JSON.parse(readFileSync(new URL('./not-used.json', import.meta.url), 'utf8')) as {
  words: string[];
  gateNames: string;
};
const gateNames = new RegExp(notUsed.gateNames.replace(/^\(\?i\)/, ''), 'gi');
const patterns = notUsed.words.map((w) => {
  const phrase = w
    .split(/\s+/)
    .map((part) => part.replace(/[.*+?^${}()|[\]\\]/g, '\\$&'))
    .join('\\s+');
  return { w, re: new RegExp(`(^|[^\\p{L}\\p{N}_])${phrase}($|[^\\p{L}\\p{N}_])`, 'iu') };
});

export function notUsedIn(text: string): string[] {
  const t = text.replace(gateNames, ' ');
  return patterns.filter((p) => p.re.test(t)).map((p) => p.w);
}

// Device is a Chromium virtual authenticator with user verification, like a platform passkey.
export interface Device {
  id: string; // the passkey it holds, base64url; '' for none
  forget: () => Promise<void>; // takes that passkey off the device, as when the user moves to another device
}

// authenticator adds a Device. With a passkey, it already holds one for rpId.
export async function authenticator(page: Page, withPasskey = false): Promise<Device> {
  const cdp = await page.context().newCDPSession(page);
  await cdp.send('WebAuthn.enable');
  const { authenticatorId } = await cdp.send('WebAuthn.addVirtualAuthenticator', {
    options: {
      protocol: 'ctap2',
      transport: 'internal',
      hasResidentKey: true,
      hasUserVerification: true,
      isUserVerified: true,
      automaticPresenceSimulation: true,
    },
  });
  if (!withPasskey) return { id: '', forget: async () => {} };
  const id = randomBytes(16);
  const { privateKey } = generateKeyPairSync('ec', { namedCurve: 'prime256v1' });
  await cdp.send('WebAuthn.addCredential', {
    authenticatorId,
    credential: {
      credentialId: id.toString('base64'),
      isResidentCredential: false,
      rpId,
      privateKey: privateKey.export({ type: 'pkcs8', format: 'der' }).toString('base64'),
      signCount: 0,
    },
  });
  return {
    id: id.toString('base64url'),
    forget: async () => {
      await cdp.send('WebAuthn.removeCredential', { authenticatorId, credentialId: id.toString('base64') });
    },
  };
}

export const challenge = () => randomBytes(32).toString('base64url');

// creation is go-webauthn's registration options as the App answers them.
export const creation = (exclude: string[] = []) => ({
  publicKey: {
    challenge: challenge(),
    rp: { id: rpId, name: 'Archfit' },
    user: { id: randomBytes(32).toString('base64url'), name: session.login, displayName: session.login },
    pubKeyCredParams: [
      { type: 'public-key', alg: -7 },
      { type: 'public-key', alg: -257 },
    ],
    excludeCredentials: exclude.map((id) => ({ type: 'public-key', id })),
    authenticatorSelection: { residentKey: 'preferred', userVerification: 'required' },
    attestation: 'none',
    timeout: 60000,
  },
});

// assertion is go-webauthn's step-up options as the App answers them.
export const assertion = (allow: string[]) => ({
  publicKey: {
    challenge: challenge(),
    rpId,
    allowCredentials: allow.map((id) => ({ type: 'public-key', id })),
    userVerification: 'required',
    timeout: 60000,
  },
});

interface Sent {
  id: string;
  rawId: string;
  type: string;
  response: Record<string, string>;
}

// ceremony reads what a credential the page sent proves: the client data, and whether the user was verified.
export function ceremony(credential: unknown) {
  const c = credential as Sent;
  const client = JSON.parse(Buffer.from(c.response.clientDataJSON, 'base64url').toString()) as {
    type: string;
    challenge: string;
    origin: string;
  };
  // The flags byte follows the 32-byte relying party id hash; bit 2 is user verification. A new passkey carries its
  // authenticator data inside the CBOR attestation object, so only an assertion's flags are read here.
  const data = c.response.authenticatorData ? Buffer.from(c.response.authenticatorData, 'base64url') : undefined;
  return { id: c.id, rawId: c.rawId, type: c.type, client, verified: data ? (data[32] & 0x04) !== 0 : undefined };
}

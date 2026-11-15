// The passkey ceremonies of the request and passkey pages (internal/control/ui/passkey.go). A step-up turns one
// assertion into a one-use token for one purpose; the server binds and checks it.

import { postJSON, type Notice, type Registered } from './api';
import { creationOptions, credentialJSON, requestOptions } from './webauthn';

// api is the API path of the page's repository: repoAPI(owner, name, suffix).
export type RepoAPI = (suffix: string) => string;

// Purpose is what a step-up allows: an answer to a request with its change digest, a second passkey, or a removal.
export interface Purpose {
  action: string;
  request?: number;
  digest?: string;
  passkey?: string;
  target?: string;
  base_digest?: string;
  yaml?: string; // edit: revalidated by the App before the passkey prompt
}

async function credential(p: Promise<Credential | null>): Promise<object> {
  const c = await p;
  // A browser that resolves with nothing has made no passkey; treat it as a cancelled prompt.
  if (!c) throw new DOMException('no credential', 'NotAllowedError');
  return credentialJSON(c as PublicKeyCredential);
}

// confirm runs the step-up: the user's passkey with user verification, for purpose only. It returns the token.
export async function confirm(api: RepoAPI, csrf: string, purpose: Purpose): Promise<string> {
  const { options } = await postJSON<{ options: unknown }>(api('passkeys/step-up'), csrf, purpose);
  const cred = await credential(navigator.credentials.get(requestOptions(options)));
  const { token } = await postJSON<{ token: string }>(api('passkeys/step-up/finish'), csrf, { credential: cred });
  return token;
}

// addPasskey registers a passkey. A user who has one confirms with it first (hasOne), so a second needs the first.
export async function addPasskey(api: RepoAPI, csrf: string, hasOne: boolean): Promise<Registered> {
  const token = hasOne ? await confirm(api, csrf, { action: 'register' }) : '';
  const { options } = await postJSON<{ options: unknown }>(api('passkeys/register'), csrf, token ? { token } : {});
  const cred = await credential(navigator.credentials.create(creationOptions(options)));
  return postJSON<Registered>(api('passkeys/register/finish'), csrf, { credential: cred });
}

// removePasskey removes one passkey, confirmed with a passkey of the user.
export async function removePasskey(api: RepoAPI, csrf: string, id: string): Promise<Notice> {
  const token = await confirm(api, csrf, { action: 'remove', passkey: id });
  return postJSON<Notice>(api(`passkeys/${encodeURIComponent(id)}/remove`), csrf, { token });
}

// The browser side of a passkey ceremony. The App's options are go-webauthn's JSON, with bytes as base64url text; the
// browser wants bytes. The credential goes back as the W3C JSON form, which go-webauthn parses. Nothing here verifies:
// the server checks the challenge, the origin, user verification and the signature.

const b64url = /^[A-Za-z0-9_-]*$/;

export function fromBase64url(s: string): ArrayBuffer {
  if (!b64url.test(s)) throw new Error('not base64url');
  const b64 = s.replaceAll('-', '+').replaceAll('_', '/') + '='.repeat((4 - (s.length % 4)) % 4);
  const bin = atob(b64);
  const out = new Uint8Array(bin.length);
  for (let i = 0; i < bin.length; i++) out[i] = bin.charCodeAt(i);
  return out.buffer;
}

export function toBase64url(b: ArrayBuffer | Uint8Array): string {
  const bytes = b instanceof Uint8Array ? b : new Uint8Array(b);
  let bin = '';
  for (const x of bytes) bin += String.fromCharCode(x);
  return btoa(bin).replaceAll('+', '-').replaceAll('/', '_').replace(/=+$/, '');
}

interface Descriptor {
  id: string;
}

type Encoded<T> = { publicKey?: T };

function publicKeyOf<T>(options: unknown): T {
  const pk = (options as Encoded<T> | null)?.publicKey;
  if (!pk) throw new Error('passkey options without publicKey');
  return pk;
}

function descriptors(list: Descriptor[] | undefined) {
  return list?.map((d) => ({ ...d, id: fromBase64url(d.id) })) as PublicKeyCredentialDescriptor[] | undefined;
}

// creationOptions are the App's registration options for navigator.credentials.create.
export function creationOptions(options: unknown): CredentialCreationOptions {
  const pk = publicKeyOf<{ challenge: string; user: { id: string }; excludeCredentials?: Descriptor[] }>(options);
  return {
    publicKey: {
      ...pk,
      challenge: fromBase64url(pk.challenge),
      user: { ...pk.user, id: fromBase64url(pk.user.id) },
      excludeCredentials: descriptors(pk.excludeCredentials),
    } as PublicKeyCredentialCreationOptions,
  };
}

// requestOptions are the App's step-up options for navigator.credentials.get.
export function requestOptions(options: unknown): CredentialRequestOptions {
  const pk = publicKeyOf<{ challenge: string; allowCredentials?: Descriptor[] }>(options);
  return {
    publicKey: {
      ...pk,
      challenge: fromBase64url(pk.challenge),
      allowCredentials: descriptors(pk.allowCredentials),
    } as PublicKeyCredentialRequestOptions,
  };
}

// credentialJSON is the browser's credential as JSON: a new passkey (attestation) or a step-up (assertion).
export function credentialJSON(c: PublicKeyCredential): object {
  const r = c.response as Partial<AuthenticatorAttestationResponse & AuthenticatorAssertionResponse>;
  const response: Record<string, unknown> = { clientDataJSON: toBase64url(r.clientDataJSON!) };
  if (r.attestationObject) {
    response.attestationObject = toBase64url(r.attestationObject);
    const transports = r.getTransports?.();
    if (transports?.length) response.transports = transports;
  } else {
    response.authenticatorData = toBase64url(r.authenticatorData!);
    response.signature = toBase64url(r.signature!);
    if (r.userHandle) response.userHandle = toBase64url(r.userHandle);
  }
  return {
    id: c.id,
    rawId: toBase64url(c.rawId),
    type: c.type,
    ...(c.authenticatorAttachment ? { authenticatorAttachment: c.authenticatorAttachment } : {}),
    clientExtensionResults: c.getClientExtensionResults(),
    response,
  };
}

// passkeyFailure is the sentence for an error the browser's passkey prompt threw, null for any other error.
export function passkeyFailure(e: unknown): string | null {
  if (!(e instanceof DOMException)) return null;
  switch (e.name) {
    case 'InvalidStateError':
      return 'This device already holds one of your passkeys. Use another device or another passkey.';
    case 'SecurityError':
      return 'This browser cannot use a passkey for this address. Nothing was changed.';
    case 'NotSupportedError':
      return 'This browser has no passkey that Archfit can use. Nothing was changed.';
  }
  return 'The passkey step was cancelled or took too long. Nothing was changed.';
}

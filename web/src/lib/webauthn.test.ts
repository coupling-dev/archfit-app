import { describe, expect, it } from 'vitest';
import {
  creationOptions,
  credentialJSON,
  fromBase64url,
  passkeyFailure,
  requestOptions,
  toBase64url,
} from './webauthn';

const bytes = (...b: number[]) => new Uint8Array(b).buffer;

describe('base64url', () => {
  it.each([
    [[], ''],
    [[0xfb], '-w'],
    [[0xff, 0xfe], '__4'],
    [[0x00, 0x01, 0x02, 0x03], 'AAECAw'],
  ])('%o is %s, both ways', (b, s) => {
    expect(toBase64url(new Uint8Array(b))).toBe(s);
    expect(new Uint8Array(fromBase64url(s))).toEqual(new Uint8Array(b));
  });

  it('refuses text that is not base64url', () => {
    expect(() => fromBase64url('a+b/')).toThrow();
  });
});

describe('creationOptions', () => {
  it('turns the ids and the challenge of go-webauthn options into bytes and keeps the rest', () => {
    const o = creationOptions({
      publicKey: {
        challenge: 'AAECAw',
        rp: { id: 'app.coupling.dev', name: 'Archfit' },
        user: { id: '-w', name: 'alice', displayName: 'alice' },
        pubKeyCredParams: [{ type: 'public-key', alg: -7 }],
        excludeCredentials: [{ type: 'public-key', id: '__4' }],
        authenticatorSelection: { userVerification: 'required' },
      },
    });
    const pk = o.publicKey!;
    expect(new Uint8Array(pk.challenge as ArrayBuffer)).toEqual(new Uint8Array([0, 1, 2, 3]));
    expect(new Uint8Array(pk.user.id as ArrayBuffer)).toEqual(new Uint8Array([0xfb]));
    expect(new Uint8Array(pk.excludeCredentials![0].id as ArrayBuffer)).toEqual(new Uint8Array([0xff, 0xfe]));
    expect(pk.rp).toEqual({ id: 'app.coupling.dev', name: 'Archfit' });
    expect(pk.authenticatorSelection).toEqual({ userVerification: 'required' });
  });

  it('refuses options without publicKey', () => {
    expect(() => creationOptions({})).toThrow();
  });
});

describe('requestOptions', () => {
  it('turns the challenge and the allowed ids into bytes', () => {
    const o = requestOptions({
      publicKey: {
        challenge: 'AAECAw',
        rpId: 'app.coupling.dev',
        allowCredentials: [{ type: 'public-key', id: '-w' }],
        userVerification: 'required',
      },
    });
    const pk = o.publicKey!;
    expect(new Uint8Array(pk.challenge as ArrayBuffer)).toEqual(new Uint8Array([0, 1, 2, 3]));
    expect(new Uint8Array(pk.allowCredentials![0].id as ArrayBuffer)).toEqual(new Uint8Array([0xfb]));
    expect(pk.userVerification).toBe('required');
  });

  it('allows any passkey of the user when the list is absent', () => {
    expect(requestOptions({ publicKey: { challenge: 'AAECAw' } }).publicKey!.allowCredentials).toBeUndefined();
  });
});

describe('credentialJSON', () => {
  it('encodes a new passkey as the JSON go-webauthn parses', () => {
    const c = {
      id: '-w',
      rawId: bytes(0xfb),
      type: 'public-key',
      authenticatorAttachment: 'platform',
      getClientExtensionResults: () => ({}),
      response: { clientDataJSON: bytes(1), attestationObject: bytes(2), getTransports: () => ['internal'] },
    } as unknown as PublicKeyCredential;
    expect(credentialJSON(c)).toEqual({
      id: '-w',
      rawId: '-w',
      type: 'public-key',
      authenticatorAttachment: 'platform',
      clientExtensionResults: {},
      response: { clientDataJSON: 'AQ', attestationObject: 'Ag', transports: ['internal'] },
    });
  });

  it('encodes an assertion, with or without a user handle', () => {
    const c = (userHandle: ArrayBuffer | null) =>
      ({
        id: '-w',
        rawId: bytes(0xfb),
        type: 'public-key',
        authenticatorAttachment: null,
        getClientExtensionResults: () => ({}),
        response: { clientDataJSON: bytes(1), authenticatorData: bytes(3), signature: bytes(4), userHandle },
      }) as unknown as PublicKeyCredential;
    expect(credentialJSON(c(bytes(5)))).toEqual({
      id: '-w',
      rawId: '-w',
      type: 'public-key',
      clientExtensionResults: {},
      response: { clientDataJSON: 'AQ', authenticatorData: 'Aw', signature: 'BA', userHandle: 'BQ' },
    });
    expect((credentialJSON(c(null)) as { response: object }).response).not.toHaveProperty('userHandle');
  });
});

describe('passkeyFailure', () => {
  it.each([
    ['NotAllowedError', 'The passkey step was cancelled or took too long. Nothing was changed.'],
    ['InvalidStateError', 'This device already holds one of your passkeys. Use another device or another passkey.'],
    ['SecurityError', 'This browser cannot use a passkey for this address. Nothing was changed.'],
    ['NotSupportedError', 'This browser has no passkey that Archfit can use. Nothing was changed.'],
  ])('%s', (name, want) => {
    expect(passkeyFailure(new DOMException('x', name))).toBe(want);
  });

  it('leaves other errors to the caller', () => {
    expect(passkeyFailure(new Error('x'))).toBeNull();
  });
});

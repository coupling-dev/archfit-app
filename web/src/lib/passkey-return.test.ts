import { expect, it } from 'vitest';
import { passkeyReturn } from './passkey-return';

it.each([
  ['/r/acme/app/requests/415', '/r/acme/app/requests/415'],
  ['/r/acme/app/owner/workflow?pull=203', '/r/acme/app/owner/workflow?pull=203'],
  ['/r/acme/app/owner/first?pull=8', '/r/acme/app/owner/first?pull=8'],
  ['/r/acme/app/owner/confirm', '/r/acme/app/owner/confirm'],
  ['/r/acme/app/owner/revert', '/r/acme/app/owner/revert'],
  ['/r/acme/app/policy?edit=1', '/r/acme/app/policy?edit=1'],
  ['/r/acme/app/requests/4?return=https://evil.test', '/r/acme/app/requests/4'],
])('returns only the canonical initiating route %s', (input, expected) => {
  expect(passkeyReturn('acme/app', input)).toBe(expected);
});

it.each([
  null,
  '',
  'https://evil.test',
  '//evil.test/r/acme/app/requests/1',
  '/\\\\evil.test',
  '/r/acme/other/requests/1',
  '/r/other/app/requests/1',
  '/r/acme/app/../other/requests/1',
  '/r/acme/app/%2e%2e/requests/1',
  '/r/acme/app/passkeys',
  '/r/acme/app/policy',
  '/admin',
  '/auth/logout',
  '/r/acme/app/requests/0',
  '/r/acme/app/owner/workflow',
  '/r/acme/app/owner/first?pull=1&pull=2',
  '/r/acme/app/requests/1#fragment',
  '/r/acme/app/requests/1\n',
])('rejects unsafe or unrelated destination %s', (input) => {
  expect(passkeyReturn('acme/app', input)).toBeUndefined();
});

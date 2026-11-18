import { describe, expect, it } from 'vitest';
import { inAppURL } from './nav';

const current = new URL('https://app.example/r/acme/app');

describe('inAppURL', () => {
  it.each([
    ['/r/acme/app/policy', 'https://app.example/r/acme/app/policy'],
    ['/r/acme/app/pull/4', 'https://app.example/r/acme/app/pull/4'],
    ['/r/acme/app/owner/workflow?pull=7', 'https://app.example/r/acme/app/owner/workflow?pull=7'],
    ['/', 'https://app.example/'],
    ['/admin', 'https://app.example/admin'],
    ['https://app.example/r/acme/app/passkeys', 'https://app.example/r/acme/app/passkeys'],
  ])('moves inside the App for %s', (href, want) => {
    expect(inAppURL(href, current)?.href).toBe(want);
  });

  it.each([
    ['another origin', 'https://github.com/coupling-dev/archfit-app'],
    ['a path the App has no page for', '/repos'],
    ['the sign-in route', '/auth/login?return=%2F'],
    ['a server asset', '/licenses.txt'],
    ['the skip link on the page shown', '#main'],
    ['an unparseable address', 'http://[::1'],
  ])('leaves %s to the browser', (_, href) => {
    expect(inAppURL(href, current)).toBeNull();
  });

  it('moves to a fragment on another page', () => {
    expect(inAppURL('/r/acme/app/policy#rules', current)?.href).toBe('https://app.example/r/acme/app/policy#rules');
  });
});

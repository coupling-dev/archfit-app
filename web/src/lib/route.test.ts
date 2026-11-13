import { describe, expect, it } from 'vitest';
import { parseRoute, repoPage, safeHref, serverError, signInHref } from './route';

describe('parseRoute', () => {
  it.each([
    ['/', { kind: 'home' }],
    ['/admin', { kind: 'admin' }],
    ['/admin/x', { kind: 'unknown' }],
    ['/r/acme/app', { kind: 'repo', owner: 'acme', name: 'app', fullName: 'acme/app' }],
    ['/r/acme/app/', { kind: 'repo', owner: 'acme', name: 'app', fullName: 'acme/app' }],
    ['/r/acme/my.app', { kind: 'repo', owner: 'acme', name: 'my.app', fullName: 'acme/my.app' }],
    ['/r/acme', { kind: 'unknown' }],
    ['/r/acme/app/data-sharing', { kind: 'data-sharing', owner: 'acme', name: 'app', fullName: 'acme/app' }],
    ['/r/acme/app/data-sharing/x', { kind: 'unknown' }],
    ['/r/acme/app/pull/4/data-sharing', { kind: 'unknown' }],
    ['/r/acme/app/graph/data-sharing', { kind: 'unknown' }],
    ['/r/acme/app/policy', { kind: 'policy', owner: 'acme', name: 'app', fullName: 'acme/app' }],
    ['/r/acme/app/decisions', { kind: 'unknown' }],
    ['/r/acme/app/passkeys', { kind: 'passkeys', owner: 'acme', name: 'app', fullName: 'acme/app' }],
    ['/r/acme/app/passkeys/', { kind: 'passkeys', owner: 'acme', name: 'app', fullName: 'acme/app' }],
    ['/r/acme/app/passkeys/x', { kind: 'unknown' }],
    ['/r/acme/app/pull/4/passkeys', { kind: 'unknown' }],
    ['/r/acme/app/requests/431', { kind: 'request', number: 431, owner: 'acme', name: 'app', fullName: 'acme/app' }],
    ['/r/acme/app/requests/431/', { kind: 'request', number: 431, owner: 'acme', name: 'app', fullName: 'acme/app' }],
    ['/r/acme/app/requests/0', { kind: 'unknown' }],
    ['/r/acme/app/requests/4x', { kind: 'unknown' }],
    ['/r/acme/app/requests', { kind: 'unknown' }],
    ['/r/acme/app/requests/431/graph', { kind: 'unknown' }],
    ['/r/acme/app/pull/4/requests/431', { kind: 'unknown' }],
    ['/r/acme/app/consent', { kind: 'unknown' }],
    ['/r/acme/app/graph', { kind: 'graph', owner: 'acme', name: 'app', fullName: 'acme/app' }],
    ['/r/acme/app/graph/x', { kind: 'unknown' }],
    ['/r/acme/%E0%A4%A', { kind: 'unknown' }],
    ['/r/acme/%2E%2E', { kind: 'unknown' }],
    ['/r/./app', { kind: 'unknown' }],
    ['/r/acme/app/pull/4', { kind: 'pull', number: 4, owner: 'acme', name: 'app', fullName: 'acme/app' }],
    ['/r/acme/app/pull/0', { kind: 'unknown' }],
    ['/r/acme/app/pull/4x', { kind: 'unknown' }],
    ['/r/acme/app/pull/4/graph', { kind: 'pull-graph', number: 4, owner: 'acme', name: 'app', fullName: 'acme/app' }],
    ['/r/acme/app/pull/4/graph/', { kind: 'pull-graph', number: 4, owner: 'acme', name: 'app', fullName: 'acme/app' }],
    ['/elsewhere', { kind: 'unknown' }],
  ])('%s', (path, want) => {
    expect(parseRoute(path)).toEqual(want);
  });
});

describe('links', () => {
  it('escapes each part of a repository path', () => {
    expect(repoPage('acme/app', '/passkeys')).toBe('/r/acme/app/passkeys');
    expect(repoPage('ac me/a?p')).toBe('/r/ac%20me/a%3Fp');
  });

  it('returns to the current page after sign-in', () => {
    expect(signInHref('/r/acme/app?x=1')).toBe('/auth/login?return=%2Fr%2Facme%2Fapp%3Fx%3D1');
  });
});

describe('safeHref', () => {
  it.each([
    ['/r/acme/app/pull/4', '/r/acme/app/pull/4'],
    ['https://github.com/acme/app/pull/4', 'https://github.com/acme/app/pull/4'],
    ['javascript:alert(1)', undefined],
    ['JavaScript:alert(1)', undefined],
    ['data:text/html,x', undefined],
    ['//evil.example/x', undefined],
    ['/\\evil.example/x', undefined],
    ['https://github.com.evil.example/x', undefined],
    ['http://github.com/acme', undefined],
    ['https://evil.example/https://github.com/', undefined],
    ['', undefined],
    [undefined, undefined],
  ])('%s', (url, want) => {
    expect(safeHref(url)).toBe(want);
  });
});

describe('serverError', () => {
  it.each([
    ['Not found.', 'Not found.'],
    ['', ''],
    [null, ''],
    [undefined, ''],
    ['__APP_ERROR__', ''],
  ])('%s', (content, want) => {
    expect(serverError(content)).toBe(want);
  });
});

describe('owner action routes', () => {
  it.each([
    ['first', '', 'first', 0], ['first', '?pull=7', 'first', 7], ['workflow', '?pull=7', 'workflow', 7],
    ['confirm', '', 'confirm', 0], ['revert', '', 'revert', 0],
  ])('%s%s', (path, search, action, pull) => {
    expect(parseRoute('/r/acme/app/owner/' + path, search)).toEqual({ kind: 'owner', action, pull, owner: 'acme', name: 'app', fullName: 'acme/app' });
  });
  it.each([
    ['workflow', ''], ['first', '?pull=0'], ['first', '?pull=01'], ['first', '?pull=-1'], ['first', '?pull='],
    ['first', '?pull=1&pull=2'], ['first', '?pull=x'], ['confirm', '?pull=7'], ['revert', '?pull=7'], ['nope', ''],
  ])('refuses %s%s', (path, search) => {
    expect(parseRoute('/r/acme/app/owner/' + path, search)).toEqual({ kind: 'unknown' });
  });
});

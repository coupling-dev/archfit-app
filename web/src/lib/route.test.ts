import { describe, expect, it } from 'vitest';
import { parseRoute, repoPage, signInHref } from './route';

describe('parseRoute', () => {
  it.each([
    ['/', { kind: 'home' }],
    ['/r/acme/app', { kind: 'repo', owner: 'acme', name: 'app', fullName: 'acme/app' }],
    ['/r/acme/app/', { kind: 'repo', owner: 'acme', name: 'app', fullName: 'acme/app' }],
    ['/r/acme/my.app', { kind: 'repo', owner: 'acme', name: 'my.app', fullName: 'acme/my.app' }],
    ['/r/acme', { kind: 'unknown' }],
    ['/r/acme/app/data-sharing', { kind: 'data-sharing', owner: 'acme', name: 'app', fullName: 'acme/app' }],
    ['/r/acme/app/data-sharing/x', { kind: 'unknown' }],
    ['/r/acme/app/consent', { kind: 'unknown' }],
    ['/r/acme/app/graph', { kind: 'unknown' }],
    ['/r/acme/%E0%A4%A', { kind: 'unknown' }],
    ['/r/acme/%2E%2E', { kind: 'unknown' }],
    ['/r/./app', { kind: 'unknown' }],
    ['/elsewhere', { kind: 'unknown' }],
  ])('%s', (path, want) => {
    expect(parseRoute(path)).toEqual(want);
  });
});

describe('links', () => {
  it('escapes each part of a repository path', () => {
    expect(repoPage('acme/app', '/policy')).toBe('/r/acme/app/policy');
    expect(repoPage('ac me/a?p')).toBe('/r/ac%20me/a%3Fp');
  });

  it('returns to the current page after sign-in', () => {
    expect(signInHref('/r/acme/app?x=1')).toBe('/auth/login?return=%2Fr%2Facme%2Fapp%3Fx%3D1');
  });
});

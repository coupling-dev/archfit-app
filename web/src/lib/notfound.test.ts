import { describe, expect, it } from 'vitest';
import { missingSentence } from './notfound';
import type { Route } from './route';

const repo = { owner: 'alexei-led', name: 'pumba', fullName: 'alexei-led/pumba' };

describe('missing page sentences', () => {
  it.each([
    [{ kind: 'pull', number: 999999, ...repo }, 'Pull request #999999 was not found in alexei-led/pumba.'],
    [{ kind: 'request', number: 999999, ...repo }, 'Request #999999 was not found in alexei-led/pumba.'],
    [{ kind: 'policy', ...repo }, 'This page was not found in alexei-led/pumba.'],
    [{ kind: 'owner', action: 'first', pull: 0, ...repo }, 'This page was not found in alexei-led/pumba.'],
  ] as [Route, string][])('names the repository the user can see: %j', (route, text) => {
    expect(missingSentence(route)).toBe(text);
  });

  it.each([{ kind: 'unknown' }, { kind: 'home' }, { kind: 'admin' }] as Route[])('names nothing for %j', (route) => {
    expect(missingSentence(route)).toBe('');
  });
});

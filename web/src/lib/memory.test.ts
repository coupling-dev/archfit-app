import { beforeEach, describe, expect, it } from 'vitest';
import { currentEpoch, drop, forget, recall, remember, signedIn } from './memory';

beforeEach(() => {
  signedIn(null);
  signedIn('alice');
});

describe('memory of the last answer', () => {
  it.each([
    '/api/repos',
    '/api/repos/acme/app/home',
    '/api/repos/acme/app/policy',
    '/api/repos/acme/app/pulls/4',
  ])('keeps the answer to %s', (path) => {
    remember(path, { a: 1 });
    expect(JSON.parse(recall(path) ?? 'null')).toEqual({ a: 1 });
  });

  it.each([
    ['the session', '/api/session'],
    ['an owner action', '/api/repos/acme/app/owner/workflow/view?pull=4'],
    ['a request', '/api/repos/acme/app/requests/431'],
    ['a passkey list', '/api/repos/acme/app/passkeys'],
    ['the policy being edited', '/api/repos/acme/app/policy?edit=1'],
    ['the installations', '/api/admin/installations'],
  ])('never keeps %s', (_, path) => {
    remember(path, { a: 1 });
    expect(recall(path)).toBeUndefined();
  });

  it('keeps nothing while nobody is signed in', () => {
    signedIn(null);
    remember('/api/repos', { a: 1 });
    signedIn('alice');
    expect(recall('/api/repos')).toBeUndefined();
  });

  it('shows an answer only to the user who read it', () => {
    remember('/api/repos', { who: 'alice' });
    signedIn('bob');
    expect(recall('/api/repos')).toBeUndefined();
    signedIn('alice');
    expect(recall('/api/repos')).toBeUndefined();
  });

  it('forgets every answer, and one on its own', () => {
    remember('/api/repos', 1);
    remember('/api/repos/acme/app/home', 2);
    drop('/api/repos');
    expect(recall('/api/repos')).toBeUndefined();
    expect(recall('/api/repos/acme/app/home')).toBeDefined();
    forget();
    expect(recall('/api/repos/acme/app/home')).toBeUndefined();
  });

  it('hands out a copy of what it was given', () => {
    const body = { rows: [1] };
    remember('/api/repos', body);
    body.rows.push(2);
    expect(JSON.parse(recall('/api/repos') ?? '')).toEqual({ rows: [1] });
  });

  it('keeps the most recent answers when it is full', () => {
    for (let i = 1; i <= 45; i++) remember(`/api/repos/acme/app/pulls/${i}`, i);
    expect(recall('/api/repos/acme/app/pulls/1')).toBeUndefined();
    expect(recall('/api/repos/acme/app/pulls/45')).toBeDefined();
  });

  it('does not remember an answer to a read that started before everything was forgotten', () => {
    const since = currentEpoch();
    forget();
    remember('/api/repos', { late: true }, since);
    expect(recall('/api/repos')).toBeUndefined();
    remember('/api/repos', { late: false }, currentEpoch());
    expect(recall('/api/repos')).toBeDefined();
  });
});

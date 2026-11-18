import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest';
import { ApiError } from './api';
import { firstJSON, firstPaths, hint, prefetch } from './early';
import { visit } from './gate';
import { forget, recall, remember, signedIn } from './memory';
import { parseRoute } from './route';

afterEach(() => {
  vi.unstubAllGlobals();
  vi.useRealTimers();
  signedIn(null);
});

describe('early reads', () => {
  it.each([
    ['/', ['/api/repos']],
    ['/r/acme/app', ['/api/repos/acme/app/home']],
    ['/r/acme/app/policy', ['/api/repos/acme/app/policy']],
    ['/r/acme/app/pull/4', ['/api/repos/acme/app/pulls/4']],
    ['/r/acme/app/passkeys', ['/api/repos/acme/app/passkeys']],
    ['/r/acme/app/requests/431', ['/api/repos/acme/app/requests/431']],
    ['/admin', ['/api/repos', '/api/admin/installations']],
    ['/nowhere', []],
  ])('starts the first reads of %s', (path, want) => {
    expect(firstPaths(parseRoute(path))).toEqual(want);
  });

  it('starts an owner action view with its pull request number', () => {
    expect(firstPaths(parseRoute('/r/acme/app/owner/first', '?pull=4'))).toEqual([
      '/api/repos/acme/app/owner/first/view?pull=4',
    ]);
  });

  it('hands the early read to the page once, then reads again', async () => {
    const early = vi.fn(async () => ({ from: 'early' }));
    prefetch(parseRoute('/r/acme/app/pull/4'), early);
    vi.stubGlobal(
      'fetch',
      vi.fn(
        async () => new Response('{"from":"server"}', { status: 200, headers: { 'Content-Type': 'application/json' } }),
      ),
    );
    await expect(firstJSON('/api/repos/acme/app/pulls/4')).resolves.toEqual({ from: 'early' });
    await expect(firstJSON('/api/repos/acme/app/pulls/4')).resolves.toEqual({ from: 'server' });
    expect(early).toHaveBeenCalledTimes(1);
  });

  it('hands a failed early read to the page that takes it', async () => {
    prefetch(parseRoute('/r/acme/app/policy'), () => Promise.reject(new Error('refused')));
    await expect(firstJSON('/api/repos/acme/app/policy')).rejects.toThrow('refused');
  });
});

describe('the last answer, then the current one', () => {
  const home = '/api/repos/acme/app/home';
  const route = parseRoute('/r/acme/app');
  beforeEach(() => {
    signedIn(null);
    signedIn('alice');
    remember(home, { state: 'old' });
  });

  it('gives the page the last answer at once and says the current one is the same', async () => {
    const current = prefetch(route, async () => ({ state: 'old' }), true);
    await expect(firstJSON(home)).resolves.toEqual({ state: 'old' });
    await expect(current).resolves.toBe(false);
  });

  it('says the page must be shown again when the current answer differs, and then gives it', async () => {
    const current = prefetch(route, async () => ({ state: 'new' }), true);
    await expect(firstJSON(home)).resolves.toEqual({ state: 'old' });
    await expect(current).resolves.toBe(true);
    await expect(firstJSON(home)).resolves.toEqual({ state: 'new' });
    expect(JSON.parse(recall(home) ?? '')).toEqual({ state: 'new' });
  });

  it('gives the refusal, not the last answer, when the current answer cannot be read', async () => {
    visit.set(3);
    const current = prefetch(route, () => Promise.reject(new ApiError(404, 'Not found.', '', 2)), true);
    await expect(firstJSON(home)).resolves.toEqual({ state: 'old' });
    await expect(current).resolves.toBe(true);
    await expect(firstJSON(home)).rejects.toMatchObject({ status: 404, message: 'Not found.', from: 3 });
    expect(recall(home)).toBeUndefined();
  });

  it('does not remember a changed answer to a read that started before access was refused', async () => {
    let answer!: (body: object) => void;
    const current = prefetch(route, () => new Promise((resolve) => (answer = resolve)), true);
    await firstJSON(home);
    forget();
    answer({ state: 'new' });
    await expect(current).resolves.toBe(true);
    await expect(firstJSON(home)).resolves.toEqual({ state: 'new' });
    expect(recall(home)).toBeUndefined();
  });

  it.each([
    ['a first visit', route, false],
    ['an owner action', parseRoute('/r/acme/app/owner/first'), true],
    ['a passkey list', parseRoute('/r/acme/app/passkeys'), true],
  ])('shows no last answer for %s', async (_, r, again) => {
    remember('/api/repos/acme/app/owner/first/view', { x: 1 });
    remember('/api/repos/acme/app/passkeys', { x: 1 });
    const get = vi.fn(async () => ({ fresh: true }));
    expect(prefetch(r, get, again)).toBeNull();
    await expect(firstJSON(firstPaths(r)[0])).resolves.toEqual({ fresh: true });
  });

  it('shows no last answer to a different user', async () => {
    signedIn('bob');
    expect(prefetch(route, async () => ({ state: 'new' }), true)).toBeNull();
    await expect(firstJSON(home)).resolves.toEqual({ state: 'new' });
  });
});

describe('a read started by a pointer or focus on a link', () => {
  const policy = parseRoute('/r/acme/app/policy');

  it('is taken by the click, which starts no second read', async () => {
    const get = vi.fn(async () => ({ from: 'hint' }));
    hint(policy, get);
    hint(policy, get);
    prefetch(policy, get);
    expect(get).toHaveBeenCalledTimes(1);
    await expect(firstJSON('/api/repos/acme/app/policy')).resolves.toEqual({ from: 'hint' });
  });

  it('is read again after access was refused, whatever it holds', async () => {
    signedIn('alice');
    const get = vi.fn(async () => ({}));
    hint(policy, get);
    forget();
    prefetch(policy, get);
    expect(get).toHaveBeenCalledTimes(2);
    await firstJSON('/api/repos/acme/app/policy');
  });

  it('is read again when it is old', async () => {
    vi.useFakeTimers();
    const get = vi.fn(async () => ({}));
    hint(policy, get);
    vi.advanceTimersByTime(11_000);
    prefetch(policy, get);
    expect(get).toHaveBeenCalledTimes(2);
    await firstJSON('/api/repos/acme/app/policy');
  });

  it.each(['/r/acme/app/owner/first', '/r/acme/app/passkeys', '/r/acme/app/requests/4', '/admin'])(
    'starts nothing for %s',
    (path) => {
      const get = vi.fn(async () => ({}));
      hint(parseRoute(path), get);
      expect(get).not.toHaveBeenCalled();
    },
  );

  it('starts nothing where the browser asks for less data', () => {
    vi.stubGlobal('navigator', { connection: { saveData: true } });
    const get = vi.fn(async () => ({}));
    hint(parseRoute('/r/acme/app/pull/4'), get);
    expect(get).not.toHaveBeenCalled();
  });

  it('leaves a failed read to the page that takes it', async () => {
    hint(parseRoute('/r/acme/app/pull/9'), () => Promise.reject(new Error('refused')));
    prefetch(parseRoute('/r/acme/app/pull/9'), async () => ({}));
    await expect(firstJSON('/api/repos/acme/app/pulls/9')).rejects.toThrow('refused');
  });
});

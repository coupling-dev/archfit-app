import { afterEach, describe, expect, it, vi } from 'vitest';
import { firstJSON, firstPaths, prefetch } from './early';
import { parseRoute } from './route';

afterEach(() => {
  vi.unstubAllGlobals();
});

describe('early reads', () => {
  it.each([
    ['/', ['/api/repos']],
    ['/r/acme/app', ['/api/repos/acme/app/home']],
    ['/r/acme/app/policy', ['/api/repos/acme/app/policy']],
    ['/r/acme/app/pull/4', ['/api/repos/acme/app/pulls/4']],
    ['/r/acme/app/passkeys', ['/api/repos/acme/app/passkeys']],
    ['/r/acme/app/requests/431', ['/api/repos/acme/app/requests/431']],
    ['/nowhere', []],
  ])('starts the first reads of %s', (path, want) => {
    expect(firstPaths(parseRoute(path))).toEqual(want);
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

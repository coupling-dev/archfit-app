import { afterEach, describe, expect, it, vi } from 'vitest';
import { ApiError, getJSON, postJSON, repoAPI } from './api';

function reply(status: number, body: string) {
  const fetch = vi.fn(async () => new Response(body, { status, headers: { 'Content-Type': 'application/json' } }));
  vi.stubGlobal('fetch', fetch);
  return fetch;
}

afterEach(() => {
  vi.unstubAllGlobals();
});

describe('api', () => {
  it('posts JSON with the CSRF header', async () => {
    const fetch = reply(200, '{"notice":"Done."}');
    await expect(postJSON('/api/x', 'tok', { a: 1 })).resolves.toEqual({ notice: 'Done.' });
    const [path, init] = fetch.mock.calls[0] as unknown as [string, RequestInit];
    expect(path).toBe('/api/x');
    expect(init.method).toBe('POST');
    expect(init.body).toBe('{"a":1}');
    expect(init.headers).toMatchObject({ 'Content-Type': 'application/json', 'X-CSRF-Token': 'tok' });
    expect(init.credentials).toBe('same-origin');
  });

  it.each([
    [401, '{"error":"Sign in to continue."}', 'Sign in to continue.'],
    [403, '{"error":""}', 'The App did not answer. Try again in a minute.'],
    [502, '<html>bad gateway</html>', 'The App did not answer. Try again in a minute.'],
  ])('answers %i with the server sentence or a fallback', async (status, body, message) => {
    reply(status, body);
    const err = await getJSON('/api/x').catch((e: unknown) => e);
    expect(err).toBeInstanceOf(ApiError);
    expect(err).toMatchObject({ status, message });
  });

  it('reports a network failure as status 0', async () => {
    vi.stubGlobal(
      'fetch',
      vi.fn(async () => {
        throw new TypeError('offline');
      }),
    );
    await expect(getJSON('/api/x')).rejects.toMatchObject({ status: 0 });
  });

  it('escapes the repository in API paths', () => {
    expect(repoAPI('acme', 'app', 'overview')).toBe('/api/repos/acme/app/overview');
    expect(repoAPI('a/b', 'c?d', 'overview')).toBe('/api/repos/a%2Fb/c%3Fd/overview');
  });
});

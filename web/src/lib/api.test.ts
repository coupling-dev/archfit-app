import { afterEach, describe, expect, it, vi } from 'vitest';
import { get } from 'svelte/store';
import { ApiError, explain, explainLoad, getJSON, postJSON, repoAPI } from './api';
import { refused } from './gate';

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

  it('carries the problems of a refused policy text', async () => {
    reply(422, '{"error":"The policy has problems.","diagnostics":[{"line":2,"column":1,"message":"Unknown key."}]}');
    const err = await postJSON('/api/x', 'tok', {}).catch((e: unknown) => e);
    expect(err).toMatchObject({ status: 422, diagnostics: [{ line: 2, column: 1, message: 'Unknown key.' }] });
    reply(422, '{"error":"No.","diagnostics":"not a list"}');
    await expect(postJSON('/api/x', 'tok', {})).rejects.toMatchObject({ diagnostics: [] });
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

  it('sends a 401 to sign-in and shows every other failure', () => {
    const go = vi.fn();
    expect(explain(new ApiError(401, 'Sign in to continue.'), '/r/acme/app', go)).toBe('');
    expect(go).toHaveBeenCalledWith('/auth/login?return=%2Fr%2Facme%2Fapp');
    go.mockClear();
    expect(explain(new ApiError(503, 'GitHub is rate limiting.'), '/', go)).toBe('GitHub is rate limiting.');
    expect(go).not.toHaveBeenCalled();
  });

  it("replaces the page with its own read's 404, never with a write's", () => {
    const go = vi.fn();
    expect(explain(new ApiError(404, 'Not found.'), '/r/acme/app/policy', go)).toBe('Not found.');
    expect(get(refused)).toBe('');
    expect(explainLoad(new ApiError(404, 'Not found.'), '/r/acme/secret', go)).toBe('');
    expect(get(refused)).toBe('Not found.');
    expect(go).not.toHaveBeenCalled();
    refused.set('');
    expect(explainLoad(new ApiError(503, 'GitHub is rate limiting.'), '/', go)).toBe('GitHub is rate limiting.');
    expect(explainLoad(new ApiError(401, 'Sign in to continue.'), '/r/acme/app', go)).toBe('');
    expect(go).toHaveBeenCalledWith('/auth/login?return=%2Fr%2Facme%2Fapp');
    expect(get(refused)).toBe('');
  });

  it('escapes the repository in API paths', () => {
    expect(repoAPI('acme', 'app', 'overview')).toBe('/api/repos/acme/app/overview');
    expect(repoAPI('a/b', 'c?d', 'overview')).toBe('/api/repos/a%2Fb/c%3Fd/overview');
  });
});

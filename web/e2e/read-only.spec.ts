import { expect, test } from '@playwright/test';
import { open } from './support';

test('read-only fixture guard aborts API writes and auth forms before dispatch', async ({ page }) => {
  let dispatched = false;
  const writes = await open(
    page,
    '/',
    {
      'GET /api/repos': () => [200, { repos: [], truncated: false }],
      'POST /api/repos': () => {
        dispatched = true;
        return [200, {}];
      },
    },
    true,
  );
  await expect(page.getByRole('heading', { name: 'No repository yet' })).toBeVisible();
  const attempts = [
    ['POST', '/api/repos'],
    ['PUT', '/api/repos'],
    ['DELETE', '/api/repos'],
    ['POST', '/auth/logout'],
  ];
  const refused = await page.evaluate(async (attempts) => {
    return Promise.all(
      attempts.map(async ([method, path]) => {
        try {
          await fetch(path, { method });
          return false;
        } catch {
          return true;
        }
      }),
    );
  }, attempts);
  expect(refused).toEqual([true, true, true, true]);
  expect(dispatched).toBe(false);
  expect(writes.map((request) => [request.method(), new URL(request.url()).pathname])).toEqual(attempts);
});

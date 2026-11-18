import { readFileSync } from 'node:fs';
import { expect, test, type Page } from '@playwright/test';
import type { Policy } from '../src/lib/api';
import { expectClean, open } from './support';

const fixture = (path: string) =>
  JSON.parse(readFileSync(new URL('../../internal/control/ui/testdata/' + path + '.json', import.meta.url), 'utf8'));
const pumba: Policy = fixture('policy/pumba');
const home = fixture('home/not-set-up');

// marker lives on the window: a document load clears it, so it survives only when the App moved without one.
async function markDocument(page: Page) {
  await page.evaluate(() => {
    (window as unknown as Record<string, unknown>).marker = 1;
  });
}
const pathOf = (page: Page) => new URL(page.url()).pathname;
const documentMarker = (page: Page) =>
  page.evaluate(() => (window as unknown as Record<string, unknown>).marker);

test('a link inside the App moves to its page without a document load', async ({ page }) => {
  await open(page, '/r/acme/app', {
    'GET /api/repos/acme/app/home': () => [200, home],
    'GET /api/repos/acme/app/policy': () => [200, pumba],
  });
  await expect(page.getByRole('link', { name: 'Policy', exact: true })).toBeVisible();
  await markDocument(page);

  await page.getByRole('link', { name: 'Policy', exact: true }).click();

  await expect.poll(() => pathOf(page)).toBe('/r/acme/app/policy');
  await expect(page.getByRole('heading', { name: 'Policy', exact: true })).toBeVisible();
  expect(await documentMarker(page)).toBe(1);
});

test('back and forward walk the App history without a document load', async ({ page }) => {
  await open(page, '/r/acme/app', {
    'GET /api/repos/acme/app/home': () => [200, home],
    'GET /api/repos/acme/app/policy': () => [200, pumba],
  });
  await page.getByRole('link', { name: 'Policy', exact: true }).click();
  await expect.poll(() => pathOf(page)).toBe('/r/acme/app/policy');
  await markDocument(page);

  await page.goBack();
  await expect.poll(() => pathOf(page)).toBe('/r/acme/app');
  await expect(page.getByRole('link', { name: 'Policy', exact: true })).toBeVisible();

  await page.goForward();
  await expect.poll(() => pathOf(page)).toBe('/r/acme/app/policy');
  await expect(page.getByRole('heading', { name: 'Policy', exact: true })).toBeVisible();
  expect(await documentMarker(page)).toBe(1);
});

test('a read still in flight from a page the user left does not replace the page shown', async ({ page }) => {
  let release!: () => void;
  const held = new Promise<void>((resolve) => (release = resolve));
  await open(page, '/r/acme/gone/pull/9', {
    'GET /api/repos/acme/gone/pulls/9': async () => {
      await held;
      return [404, { error: 'Repository not found.' }];
    },
    'GET /api/repos': () => [200, { repos: [], truncated: false }],
  });

  await page.getByRole('banner').getByRole('link', { name: 'Repositories', exact: true }).click();
  await expect.poll(() => pathOf(page)).toBe('/');
  await expect(page.getByRole('heading', { name: 'Repositories', exact: true })).toBeVisible();

  const answered = page.waitForResponse((response) => response.url().includes('/pulls/9'));
  release();
  await answered;
  await expect(page.getByRole('heading', { name: 'Repositories', exact: true })).toBeVisible();
  await expect(page.getByRole('heading', { name: 'Page not available', exact: true })).toHaveCount(0);
});

// Back to a page the user was on shows its last answer at once and says so; the current answer then replaces it.
const lastAnswer = 'Showing your last answer. Reading the current one.';

const homeStatus = (page: Page, body: { status: string }) => page.getByText(body.status, { exact: true });

type Reply = [number, object];
// homeReads answers the first read of Home at once and each later one when it is released, with the next of replies.
function homeReads(replies: Reply[]) {
  const held: (() => void)[] = [];
  let reads = 0;
  return {
    get reads() {
      return reads;
    },
    release: () => held.shift()?.(),
    handler: async (): Promise<Reply> => {
      const n = reads++;
      if (n === 0) return [200, home];
      await new Promise<void>((resolve) => held.push(resolve));
      return replies[n - 1];
    },
  };
}

async function backToHome(page: Page, replies: Reply[]) {
  const reads = homeReads(replies);
  await open(page, '/r/acme/app', {
    'GET /api/repos/acme/app/home': reads.handler,
    'GET /api/repos/acme/app/policy': () => [200, pumba],
  });
  await expect(homeStatus(page, home)).toBeVisible();
  await page.getByRole('link', { name: 'Policy', exact: true }).click();
  await expect(page.getByRole('heading', { name: 'Policy', exact: true })).toBeVisible();
  await page.goBack();
  return reads;
}

test('back to Home shows the last answer at once, then the current one', async ({ page }) => {
  const changed = { ...home, status: 'The setup steps are finished.' };
  const reads = await backToHome(page, [[200, changed]]);

  await expect(homeStatus(page, home)).toBeVisible();
  await expect(page.getByRole('status').filter({ hasText: lastAnswer })).toBeVisible();
  expect(reads.reads).toBe(2);
  await expectClean(page);
  await page.setViewportSize({ width: 390, height: 844 });
  await expect(page.getByRole('status').filter({ hasText: lastAnswer })).toBeInViewport();
  await expectClean(page);

  reads.release();
  await expect(homeStatus(page, changed)).toBeVisible();
  await expect(homeStatus(page, home)).toHaveCount(0);
  await expect(page.getByText(lastAnswer)).toHaveCount(0);
});

test('back to Home keeps its page when the current answer is the same', async ({ page }) => {
  const reads = await backToHome(page, [[200, home]]);
  await expect(page.getByText(lastAnswer)).toBeVisible();
  reads.release();
  await expect(page.getByText(lastAnswer)).toHaveCount(0);
  await expect(homeStatus(page, home)).toBeVisible();
});

test('a refresh that fails shows the failure, not the last answer, and the next visit starts empty', async ({ page }) => {
  const reads = await backToHome(page, [
    [500, { error: 'The App did not answer. Try again in a minute.' }],
    [200, home],
  ]);
  await expect(homeStatus(page, home)).toBeVisible();

  reads.release();
  await expect(page.getByRole('alert')).toHaveText('The App did not answer. Try again in a minute.');
  await expect(homeStatus(page, home)).toHaveCount(0);
  await expect(page.getByText(lastAnswer)).toHaveCount(0);

  await page.getByRole('link', { name: 'Policy', exact: true }).click();
  await expect(page.getByRole('heading', { name: 'Policy', exact: true })).toBeVisible();
  await page.goBack();
  await expect(page.getByRole('status', { name: 'Loading' })).toBeVisible();
  await expect(homeStatus(page, home)).toHaveCount(0);
});

test('a refusal on refresh replaces the last answer with the refusal page', async ({ page }) => {
  const reads = await backToHome(page, [[404, { error: 'Repository not found.' }]]);
  await expect(homeStatus(page, home)).toBeVisible();
  reads.release();
  await expect(page.getByRole('heading', { name: 'Page not available', exact: true })).toBeVisible();
  await expect(homeStatus(page, home)).toHaveCount(0);
});

test('signing out forgets the last answers', async ({ page }) => {
  const reads = homeReads([]);
  await open(page, '/r/acme/app', {
    'GET /api/repos/acme/app/home': reads.handler,
    'GET /api/repos/acme/app/policy': () => [200, pumba],
  });
  // The sign-out request is answered without leaving the page, so what the App still holds can be looked at.
  await page.route('**/auth/logout', (route) => route.fulfill({ status: 204 }));
  await expect(homeStatus(page, home)).toBeVisible();
  await page.getByRole('link', { name: 'Policy', exact: true }).click();
  await expect(page.getByRole('heading', { name: 'Policy', exact: true })).toBeVisible();

  await page.getByRole('button', { name: 'Sign out' }).click();
  await page.goBack();
  await expect(page.getByRole('status', { name: 'Loading' })).toBeVisible();
  await expect(homeStatus(page, home)).toHaveCount(0);
  await expect(page.getByText(lastAnswer)).toHaveCount(0);
});

test('a pointer or the focus on a link starts its read, and the click starts no second one', async ({ page }) => {
  let reads = 0;
  await open(page, '/r/acme/app', {
    'GET /api/repos/acme/app/home': () => [200, home],
    'GET /api/repos/acme/app/policy': () => {
      reads++;
      return [200, pumba];
    },
  });
  const link = page.getByRole('link', { name: 'Policy', exact: true });
  await link.hover();
  await expect.poll(() => reads).toBe(1);
  await link.focus();
  await link.click();
  await expect(page.getByRole('heading', { name: 'Policy', exact: true })).toBeVisible();
  expect(reads).toBe(1);
});

test('a browser asking for less data starts no read for a link', async ({ page }) => {
  const reads: string[] = [];
  await page.addInitScript(() => Object.defineProperty(navigator, 'connection', { value: { saveData: true } }));
  await open(page, '/r/acme/app', {
    'GET /api/repos/acme/app/home': () => [200, home],
    'GET /api/repos/acme/app/policy': () => {
      reads.push('policy');
      return [200, pumba];
    },
  });
  await expect(homeStatus(page, home)).toBeVisible();
  await page.getByRole('link', { name: 'Policy', exact: true }).hover();
  await page.waitForTimeout(400);
  expect(reads).toEqual([]);
  await page.getByRole('link', { name: 'Policy', exact: true }).click();
  await expect(page.getByRole('heading', { name: 'Policy', exact: true })).toBeVisible();
  expect(reads).toEqual(['policy']);
});

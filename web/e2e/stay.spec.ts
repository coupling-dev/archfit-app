import { readFileSync } from 'node:fs';
import { expect, test } from '@playwright/test';
import { open, type Mock } from './fixtures';
import { expectClean, expectNoNotUsedWord, docsClock, docsShot } from './support';

const reposGolden = (name: string) =>
  JSON.parse(
    readFileSync(new URL(`../../internal/control/ui/testdata/repos/${name}.json`, import.meta.url), 'utf8'),
  ) as object;

const cases: { name: string; path: string; ready: string; mock?: Mock; keyboard: string }[] = [
  { name: 'repositories', path: '/', ready: 'Repositories', keyboard: 'acme/clean' },
  {
    name: 'repositories-empty',
    path: '/',
    ready: 'Repositories',
    mock: { repos: reposGolden('no-repositories') },
    keyboard: 'Documentation',
  },
  {
    name: 'repositories-truncated',
    path: '/',
    ready: 'Repositories',
    mock: { repos: { ...reposGolden('all-states'), truncated: true } },
    keyboard: 'acme/clean',
  },
  {
    name: 'sign-in',
    path: '/',
    ready: 'Architecture checks on every pull request',
    mock: { signedIn: false },
    keyboard: 'Sign in with GitHub',
  },
  {
    name: 'waiting',
    path: '/r/octo/tool',
    ready: 'Architecture checks on every pull request',
    keyboard: 'How approval works',
  },
  { name: 'admin', path: '/admin', ready: 'Installations', mock: { operator: true }, keyboard: 'Approve octo' },
  {
    name: 'passkeys-empty',
    path: '/r/acme/app/passkeys',
    ready: 'Passkeys',
    mock: { passkeys: { passkeys: [] } },
    keyboard: 'Add a passkey',
  },
  {
    name: 'passkeys',
    path: '/r/acme/app/passkeys',
    ready: 'Passkeys',
    mock: {
      passkeys: {
        passkeys: [{ id: 'passkey-one-12345', created: '2026-11-10T09:00:00Z', last_used: '2026-11-11T08:00:00Z' }],
      },
    },
    keyboard: 'Add a passkey',
  },
  { name: 'error', path: '/r/acme/hidden', ready: 'This page cannot be shown', keyboard: 'See your repositories.' },
  {
    name: 'server-error',
    path: '/r/acme/hidden',
    ready: 'This page cannot be shown',
    mock: { serverError: { status: 404, sentence: 'Not found.' } },
    keyboard: 'See your repositories.',
  },
];

for (const width of [1280, 390]) {
  for (const c of cases) {
    test(`pages that stay: ${c.name} whole-page text at ${width}px${c.name === 'passkeys' && width === 1280 ? ' @docs' : ''}`, async ({ page }) => {
      await docsClock(page);
      await page.setViewportSize({ width, height: 900 });
      await open(page, c.path, c.mock);
      await expect(page.getByRole('heading', { name: c.ready, exact: true })).toBeVisible();
      await expect(page.getByRole('status', { name: 'Loading' })).toHaveCount(0);
      await expect(page.locator('body')).toContainText('Documentation');
      expect(await page.locator('body').innerText()).toMatchSnapshot(`stay-${c.name}-${width}.txt`);
      if (c.name === 'passkeys' && width === 1280) await docsShot(page, 'passkeys');
      await expectClean(page);

      const target = page.getByRole(
        c.keyboard.startsWith('Approve') || c.keyboard === 'Add a passkey' ? 'button' : 'link',
        { name: c.keyboard, exact: true },
      );
      for (let i = 0; i < 30; i++) {
        await page.keyboard.press('Tab');
        if (await target.evaluate((el) => el === document.activeElement)) break;
      }
      await expect(target).toBeFocused();
      expect(await target.evaluate((el) => el.matches(':focus-visible'))).toBe(true);

      for (const detail of await page.locator('details').all()) {
        await detail.locator('summary').click();
      }
      await expectNoNotUsedWord(page);
    });
  }
}

test('passkeys: removal confirmation works with the keyboard', async ({ page }) => {
  await open(page, '/r/acme/app/passkeys', {
    passkeys: { passkeys: [{ id: 'passkey-one', created: '2026-11-10T09:00:00Z' }] },
  });
  const remove = page.getByRole('button', { name: 'Remove', exact: true });
  await expect(remove).toBeVisible();
  for (let i = 0; i < 20; i++) {
    await page.keyboard.press('Tab');
    if (await remove.evaluate((el) => el === document.activeElement)) break;
  }
  await expect(remove).toBeFocused();
  await page.keyboard.press('Enter');
  await expect(page.getByRole('button', { name: 'Confirm removal' })).toBeVisible();
  await page.getByRole('button', { name: 'Keep it' }).click();
  await expect(remove).toBeVisible();
  await expectNoNotUsedWord(page);
});

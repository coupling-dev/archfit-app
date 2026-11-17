import { readFileSync, readdirSync } from 'node:fs';
import { expect, test } from '@playwright/test';
import { docsClock, docsShot, expectClean, open } from './support';
import { homeAction, readHome } from '../src/lib/home';

const fixture = (path: string) =>
  JSON.parse(readFileSync(new URL('../../internal/control/ui/testdata/' + path + '.json', import.meta.url), 'utf8'));
const approvedCopy = [
  'Policy and review status',
  'Review workflow change',
  'Review first policy',
  'Rules reach the default branch through review, so the approval binds to exact file contents.',
  'Ready for review',
  'Review needed',
  'A password or a GitHub review can be replayed by a compromised account',
  'A passkey is needed to approve, but not to read the review.',
  "Next: review owner approval",
  "Review and approve",
  "Review proposed files",
  "Review the proposed files before approving.",
  "PR #203 changed after approval. The approval covers the old head; a new approval is needed.",
  "Could not read your passkeys. Review is still available; try again before approving.",
];

for (const width of [390, 1280]) {
  for (const [name, path, api, title] of [
    ['home', '', 'home', 'acme/app'],
    ['pull', '/pull/4', 'pulls/4', 'Pull request #4'],
    ['owner', '/owner/first?pull=12', 'owner/first/view', 'Owner action'],
  ]) {
    test(`page identity: ${name} loading and read failure at ${width}`, async ({ page }) => {
      await page.setViewportSize({ width, height: 900 });
      let release = () => {};
      const held = new Promise<void>((resolve) => {
        release = resolve;
      });
      await open(page, '/r/acme/app' + path, {
        ['GET /api/repos/acme/app/' + api]: async () => {
          await held;
          return [502, { error: 'The page could not be read.' }];
        },
      });
      const head = page.locator('.page-head');
      await expect(page.locator('main h1')).toHaveText(title);
      if (name !== 'home') await expect(head.locator('.lede')).toContainText('acme/app');
      await expect(head.locator('.status, .status-label')).toHaveCount(0);
      release();
      await expect(head.getByRole('alert')).toHaveText('The page could not be read.');
      await expect(page.locator('main h1')).toHaveText(title);
      await expect(head.locator('.status, .status-label')).toHaveCount(0);
      if (name === 'owner') await expect(head.locator('.lede')).toContainText('PR #12');
      await expectClean(page);
    });
  }

  for (const href of [undefined, 'javascript:alert(1)', '/r/other/app/owner/first?pull=12']) {
    test(`page identity: setup without a safe owner link (${href}) at ${width}`, async ({ page }) => {
      await page.setViewportSize({ width, height: 900 });
      const body = fixture('home/not-set-up');
      body.setup.owner_action = { label: 'An owner must act.', ...(href ? { href } : {}) };
      await open(page, '/r/acme/app', { 'GET /api/repos/acme/app/home': () => [200, body] });
      await expect(page.locator('main .primary')).toHaveCount(0);
      await expect(page.getByText('An owner must act.', { exact: true })).toBeVisible();
      await expect(page.getByRole('link', { name: 'An owner must act.' })).toHaveCount(0);
      await expectClean(page, approvedCopy);
    });
  }

  for (const [name, path, api, data, title] of [
    ['home', '', 'home', 'home/one-request', 'acme/app'],
    ['pull', '/pull/4', 'pulls/4', 'pull/waiting', 'Pull request #4'],
    ['owner', '/owner/first', 'owner/first/view', 'owner/first', 'Approve the first policy'],
  ]) {
    test(`page identity: ${name} at ${width} @docs`, async ({ page }) => {
      await page.setViewportSize({ width, height: 900 });
      await docsClock(page);
      const body = fixture(data);
      const writes = await open(
        page,
        '/r/acme/app' + path,
        { ['GET /api/repos/acme/app/' + api]: () => [200, body] },
        true,
      );
      const head = page.locator('.page-head');
      await expect(page.locator('main h1')).toHaveText(title);
      if (name !== 'home') await expect(head.locator('.lede')).toContainText('acme/app');
      const status = head.locator(name === 'home' ? '.status-line' : name === 'pull' ? '.page-status' : '.status');
      await expect(status).toContainText(body.view?.status_line ?? body.status);
      const actions = head.locator(name === 'home' ? '.actions a' : '.row a');
      if (name === 'pull') {
        expect(await page.locator('main .primary').count()).toBeLessThanOrEqual(1);
        await expect(actions.first()).not.toHaveClass(/primary/);
      } else if (name === 'owner') {
        await expect(page.locator('main .primary')).toHaveCount(0);
        await expect(page.locator('main button.approval')).toHaveCount(1);
        await expect(actions.first()).not.toHaveClass(/btn/);
      } else if (name === 'home') {
        await expect(page.locator('main .primary')).toHaveCount(1);
        await expect(actions).toHaveText(['Open the request', 'View policy']);
        await expect(actions.first()).toHaveClass(/primary/);
      } else {
        await expect(page.locator('main .primary')).toHaveCount(1);
        await expect(actions.first()).toHaveClass(/primary/);
      }
      const headingBox = await head.locator('h1').boundingBox();
      const statusBox = await status.boundingBox();
      const actionBox = await actions.first().boundingBox();
      expect(statusBox!.y).toBeGreaterThan(headingBox!.y);
      // From 768px the page head puts its actions beside the title instead of under the status.
      if ((name !== 'home' && name !== 'pull') || width < 768) expect(actionBox!.y).toBeGreaterThan(statusBox!.y);
      await actions.first().focus();
      for (let i = 1; i < (await actions.count()); i++) {
        await page.keyboard.press('Tab');
        await expect(actions.nth(i)).toBeFocused();
      }
      if (name === 'owner') {
        await expect(actions.first()).toHaveAttribute('href', '/r/acme/app/policy');
        await expect(page.getByRole('link', { name: 'Read the change' })).toHaveCount(0);
        const approval = page.getByRole('button', { name: body.main_action, exact: true });
        await expect(approval).toBeEnabled();
        const approvalBox = await approval.boundingBox();
        const diffBox = await page
          .getByRole('region', { name: 'Diff: The first policy (.archfit.yaml)' })
          .boundingBox();
        expect(approvalBox!.y).toBeGreaterThan(diffBox!.y + diffBox!.height);
      }
      await expectClean(page, approvedCopy);
      await docsShot(page, `page-identity/${name}-${width}`, approvedCopy);
      expect(writes).toEqual([]);
    });
  }

  for (const file of readdirSync(new URL('../../internal/control/ui/testdata/home/', import.meta.url)).filter((name) =>
    name.endsWith('.json'),
  )) {
    const body = fixture('home/' + file.replace('.json', ''));
    if (!body.setup) continue;
    test(`page identity: setup ${file} at ${width}`, async ({ page }) => {
      await page.setViewportSize({ width, height: 900 });
      await open(page, '/r/acme/app', { 'GET /api/repos/acme/app/home': () => [200, body] });
      const actions = page.locator('.page-head .actions > a');
      const action = homeAction(readHome(body), { owner: 'acme', name: 'app', fullName: 'acme/app' });
      await expect(actions).toHaveCount((action ? 1 : 0) + (body.setup.policy ? 1 : 0));
      await expect(page.locator('.page-head .actions > :not(a)')).toHaveCount(0);
      await expect(page.locator('main .primary')).toHaveCount(action ? 1 : 0);
      if (action) {
        await expect(actions.first()).toHaveAttribute('href', action.href);
        await expect(actions.first()).toHaveClass(/primary/);
      }
      if (action?.needsPasskey) {
        await expect(actions.first()).toHaveAccessibleDescription('Approval needs your passkey.');
        await expect(page.getByText('Approval needs your passkey.', { exact: true })).toBeVisible();
      }
      if ((await actions.count()) > 1) {
        await actions.first().focus();
        await page.keyboard.press('Tab');
        await expect(actions.nth(1)).toBeFocused();
      }
      await expect(page.getByRole('heading', { name: 'Set up Archfit' })).toBeVisible();
      await expectClean(page, approvedCopy);
    });
  }
}

import { readFileSync } from 'node:fs';
import { expect, test, type Locator, type Page } from '@playwright/test';
import { docsClock, docsShot, expectClean, expectPageText, open } from './support';
import { installations } from './fixtures';

const root = '/r/acme/app';
const api = '/api/repos/acme/app';
const policy = JSON.parse(
  readFileSync(new URL('../../internal/control/ui/testdata/policy/typical.json', import.meta.url), 'utf8'),
);
const owner = JSON.parse(
  readFileSync(new URL('../../internal/control/ui/testdata/owner/workflow.json', import.meta.url), 'utf8'),
);

async function keyboard(page: Page, target: Locator) {
  for (let i = 0; i < 40; i++) {
    await page.keyboard.press('Tab');
    if (await target.evaluate((el) => el === document.activeElement)) break;
  }
  await expect(target).toBeFocused();
  expect(await target.evaluate((el) => el.matches(':focus-visible'))).toBe(true);
}

for (const width of [390, 1280]) {
  const states = [
    {
      name: 'policy-unreadable',
      path: '/policy',
      endpoint: '/policy',
      status: 409,
      error: 'The policy on the default branch could not be read. Fix .archfit.yaml first.',
      text: 'The policy on the default branch could not be read.',
      recovery: true,
    },
    {
      name: 'owner-not-needed',
      path: '/owner/first',
      endpoint: '/owner/first/view',
      status: 409,
      error: 'The repository no longer needs this action. Reload the page.',
      text: 'The first policy is already approved.',
      recovery: true,
      link: 'Open the repository',
    },
    {
      name: 'owner-confirm-not-needed',
      path: '/owner/confirm',
      endpoint: '/owner/confirm/view',
      status: 409,
      error: 'The repository no longer needs this action. Reload the page.',
      text: 'Nothing to confirm: the policy has not changed outside the flow.',
      recovery: true,
      link: 'Open the repository',
    },
    {
      name: 'owner-nothing-to-revert',
      path: '/owner/revert',
      endpoint: '/owner/revert/view',
      status: 409,
      error: 'The policy has no change outside the flow to revert. Reload the page.',
      text: 'Nothing to revert: the policy has not changed outside the flow.',
      recovery: true,
      link: 'Open the repository',
    },
    {
      name: 'owner-missing-pull',
      path: '/owner/first',
      endpoint: '/owner/first/view',
      status: 409,
      error:
        'This pull request cannot be approved. It must be open, from this repository, into the default branch, with a policy and a workflow file.',
      text: 'Nothing to approve yet: the default branch has no policy of version 3.',
      recovery: true,
      link: 'Open the repository',
    },
    {
      name: 'owner-merged-pull',
      path: '/owner/workflow?pull=213',
      endpoint: '/owner/workflow/view',
      status: 409,
      error: 'This pull request is merged. There is nothing to approve.',
      text: 'Nothing to approve: pull request #213 is merged.',
      recovery: true,
      link: 'Open pull request #213',
      href: root + '/pull/213',
    },
    {
      name: 'policy-transient',
      path: '/policy',
      endpoint: '/policy',
      status: 503,
      error: 'The App did not answer. Try again in a minute.',
      text: 'The App did not answer.',
      recovery: false,
    },
    {
      name: 'owner-transient',
      path: '/owner/workflow?pull=7',
      endpoint: '/owner/workflow/view',
      status: 503,
      error: 'The App did not answer. Try again in a minute.',
      text: 'The App did not answer.',
      recovery: false,
    },
  ];
  for (const state of states) {
    test(`recovery: ${state.name} at ${width}px @docs`, async ({ page }) => {
      await docsClock(page);
      await page.setViewportSize({ width, height: 900 });
      let reads = 0;
      const writes = await open(page, root + state.path, {
        ['GET ' + api + state.endpoint]: () => {
          reads++;
          return reads > 1 && !state.recovery
            ? [200, state.name.startsWith('policy') ? policy : owner]
            : [state.status, { error: state.error }];
        },
      });
      await expect(page.locator('main')).toContainText(state.text);
      await expect(page.locator('main')).not.toContainText('Fix .archfit.yaml first.');
      await expect(page.getByRole('link', { name: 'Try again' })).toHaveCount(state.recovery ? 0 : 1);
      const target = page.getByRole('link', { name: state.link ?? (state.recovery ? 'View current setup' : 'Try again') });
      await expect(target).toHaveAttribute('href', state.href ?? (state.recovery ? root : root + state.path));
      await keyboard(page, target);
      await expectPageText(page, `recovery-${state.name}-${width}`);
      await docsShot(page, `recovery/${state.name}-${width}`);
      await expectClean(page);
      expect(writes).toEqual([]);
      if (!state.recovery) {
        await target.press('Enter');
        await expect(page.getByRole('alert')).toHaveCount(0);
        await expect(
          page.getByRole('heading', {
            name: state.name.startsWith('policy') ? 'Rules' : owner.main_action,
            exact: true,
          }),
        ).toBeVisible();
        expect(reads).toBe(2);
        expect(writes).toEqual([]);
      } else if (state.name.startsWith('owner')) {
        await expect(page.getByRole('alert')).toHaveCount(0);
        await expect(page.getByRole('heading', { level: 1 })).toHaveText(
          state.path.includes('confirm') ? 'Confirm the policy' : state.path.includes('revert') ? 'Revert the policy' : state.path.includes('workflow') ? 'Approve the workflow change' : 'Approve the first policy',
        );
        await expect(page.locator('main .primary')).toHaveCount(1);
        await expect(page.getByRole('main').getByRole('button')).toHaveCount(0);
      }
    });
  }

  test(`recovery: editor unavailable at ${width}px @docs`, async ({ page }) => {
    await docsClock(page);
    await page.setViewportSize({ width, height: 900 });
    const writes = await open(page, root + '/policy?edit=1', {
      ['GET ' + api + '/policy']: () => [
        200,
        { ...policy, edit: { available: false, reason: 'Change control has not started.' } },
      ],
    });
    await expect(page.getByRole('heading', { name: 'Policy editor unavailable' })).toBeVisible();
    await expect(page.getByRole('heading', { name: 'Rules', exact: true })).toBeVisible();
    await expect(page.getByLabel('Policy YAML')).toHaveCount(0);
    await expect(page.getByRole('link', { name: 'Try again' })).toHaveCount(0);
    await keyboard(page, page.getByRole('link', { name: 'View current setup' }));
    await expectPageText(page, `recovery-editor-unavailable-${width}`);
    await docsShot(page, `recovery/editor-unavailable-${width}`);
    await expectClean(page);
    expect(writes).toEqual([]);
  });

  test(`recovery: missing, private and unknown pages are identical at ${width}px @docs`, async ({ page }) => {
    await docsClock(page);
    await page.setViewportSize({ width, height: 900 });
    const shown: string[] = [];
    for (const path of ['/r/acme/missing/policy', '/r/acme/private/policy', '/unknown']) {
      await page.unrouteAll();
      await open(page, path, {});
      await expect(page.getByRole('heading', { name: 'Page not available' })).toBeVisible();
      await expect(page.getByRole('link', { name: 'Try again' })).toHaveCount(0);
      await expect(page.getByRole('navigation', { name: 'Repository' })).toHaveCount(0);
      shown.push(await page.locator('main').innerHTML());
    }
    expect(new Set(shown).size).toBe(1);
    await keyboard(page, page.getByRole('main').getByRole('link', { name: 'Repositories' }));
    await expectPageText(page, `recovery-not-found-${width}`);
    await docsShot(page, `recovery/not-found-${width}`);
    await expectClean(page);
  });

  test(`admin: managed rows and authorized links at ${width}px @docs`, async ({ page }) => {
    await docsClock(page);
    await page.setViewportSize({ width, height: 900 });
    const managed = installations.approved.find((row) => row.auto)!;
    await open(page, '/admin', {
      'GET /api/admin/installations': () => [200, { waiting: [], approved: [managed], unreadable: 0 }],
      'GET /api/repos': () => [200, { repos: [{ full_name: 'alexei-led/ccgram' }], truncated: false }],
    });
    await expect(page.getByText('Managed by configuration')).toBeVisible();
    await expect(page.getByText('No installation is waiting.')).toBeVisible();
    await expect(page.getByRole('main').getByRole('button')).toHaveCount(0);
    await expect(page.getByRole('main')).not.toContainText('Revoke');
    const link = page.getByRole('link', { name: 'ccgram', exact: true });
    await expect(link).toHaveAttribute('href', '/r/alexei-led/ccgram');
    await keyboard(page, link);
    await expectPageText(page, `recovery-admin-managed-${width}`);
    await docsShot(page, `recovery/admin-managed-${width}`);
    await expectClean(page);
  });
}

for (const list of [
  { repos: [], truncated: false },
  { repos: [], truncated: true },
  { repos: [{ full_name: 'alexei-led/ccgram', waiting: true }], truncated: false },
  null,
]) {
  test(`admin: membership is not access (${JSON.stringify(list)})`, async ({ page }) => {
    await open(page, '/admin', {
      'GET /api/admin/installations': () => [200, installations],
      'GET /api/repos': () => (list ? [200, list] : [503, { error: 'Unavailable' }]),
    });
    await expect(page.getByText('Managed by configuration')).toBeVisible();
    await expect(page.getByRole('link', { name: 'ccgram', exact: true })).toHaveCount(0);
    await expect(page.getByRole('button', { name: 'Revoke acme' })).toBeVisible();
    await expect(page.getByRole('button', { name: 'Revoke alexei-led' })).toHaveCount(0);
  });
}

for (const width of [390, 1280]) {
  test(`recovery: a moved approval cannot submit again at ${width}px @docs`, async ({ page }) => {
    await docsClock(page);
    await page.setViewportSize({ width, height: 900 });
    const writes = await open(page, root + '/owner/workflow?pull=7', {
      ['GET ' + api + '/owner/workflow/view']: () => [200, owner],
      ['POST ' + api + '/passkeys/step-up']: () => [409, {
        error: 'The change moved since you looked. Reload it and confirm again with your passkey.',
      }],
    });
    await page.getByRole('button', { name: 'Approve with passkey', exact: true }).click();
    await expect(page.getByRole('button', { name: 'Approve with passkey', exact: true })).toBeDisabled();
    await expect(page.getByRole('link', { name: 'Try again' })).toHaveCount(0);
    const setup = page.getByRole('link', { name: 'View current setup' });
    await expect(setup).toHaveAttribute('href', root);
    await keyboard(page, setup);
    await expectPageText(page, `recovery-owner-moved-${width}`);
    await docsShot(page, `recovery/owner-moved-${width}`);
    await expectClean(page);
    expect(writes.map((r) => new URL(r.url()).pathname)).toEqual([api + '/passkeys/step-up']);
  });
}

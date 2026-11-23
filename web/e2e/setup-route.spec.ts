import { readFileSync } from 'node:fs';
import { expect, test } from '@playwright/test';
import { docsClock, expectClean, expectPageText, open } from './support';

const fixture = JSON.parse(readFileSync(new URL('../../internal/control/ui/testdata/home/not-set-up.json', import.meta.url), 'utf8'));

// anApprovedAdvisoryRepository is the state after the first approval: the policy and workflow are
// active, the check is advisory, and the ruleset is still to do.
function setupBody(mode: 'advisory' | 'required') {
  const body = structuredClone(fixture);
  body.state = 'clean';
  body.status = 'The default branch follows the policy.';
  body.setup = {
    weekly: { known: false },
    mode,
    approval: { status: 'active', kind: 'first' },
    passkey_known: true,
    admitted: true,
    policy: true,
    workflow: true,
    passkey: true,
    open_pull: 0,
    state: 'approved',
    missing: [],
    rules: [{ code: 'gate_check', state: 'warning', text: 'Require archfit/gate.' }],
  };
  return body;
}

test('setup without a policy directs the user to the setup command', async ({ page }) => {
  const body = structuredClone(fixture);
  delete body.setup.first_pull;
  delete body.setup.owner_action;
  body.setup.approval = { status: 'needed', kind: 'first' };
  body.setup.state = 'not_admitted';
  await open(page, '/r/acme/app', { 'GET /api/repos/acme/app/home': () => [200, body] });
  await expect(page.getByText('archfit setup', { exact: true })).toBeVisible();
  await expect(page.getByText(/copy the workflow/i)).toHaveCount(0);
});

test('setup advisory: the check is made required before the ruleset @docs', async ({ page }) => {
  await page.setViewportSize({ width: 1280, height: 900 });
  await docsClock(page);
  const writes = await open(page, '/r/acme/app', {
    'GET /api/repos/acme/app/home': () => [200, setupBody('advisory')],
    'POST /api/repos/acme/app/setup/required/preview': () => [
      200,
      {
        digest: 'sha256:' + 'a'.repeat(64),
        target: 'b'.repeat(40),
        against: 'the default branch',
        diff: '--- a/.archfit.yaml\n+++ b/.archfit.yaml\n@@ -1,2 +1,3 @@\n version: 1\n owners: [alice]\n+gate: required\n',
        errors: [],
      },
    ],
  });
  const step = page.locator('.setup .steps > li').nth(3);
  await expect(step.getByRole('button', { name: 'Make the check required', exact: true })).toBeVisible();
  // The step owns the action: the page head does not offer the ruleset first.
  await expect(page.locator('.page-head .btn.primary')).toHaveCount(0);
  await expectPageText(page, 'home-setup-advisory');
  await expectClean(page);
  if (process.env.ARCHFIT_DOCS_SHOTS)
    await page.screenshot({ path: new URL('../../docs/images/home/setup-advisory-1280-light.png', import.meta.url).pathname, animations: 'disabled' });

  await step.getByRole('button', { name: 'Make the check required', exact: true }).click();
  await expect(step.getByRole('region', { name: /The policy change in \.archfit\.yaml/ })).toContainText('+gate: required');
  await expect(step.getByRole('button', { name: 'Approve with your passkey', exact: true })).toBeVisible();
  // The preview sends no policy bytes: the App computes the change.
  expect(writes.map((w) => new URL(w.url()).pathname)).toEqual(['/api/repos/acme/app/setup/required/preview']);
  expect(writes[0].postDataJSON()).toEqual({});
  await expectClean(page);
});

test('setup required: the ruleset instructions stay and the action is gone', async ({ page }) => {
  await page.setViewportSize({ width: 1280, height: 900 });
  await docsClock(page);
  await open(page, '/r/acme/app', { 'GET /api/repos/acme/app/home': () => [200, setupBody('required')] });
  const step = page.locator('.setup .steps > li').nth(3);
  await expect(step.getByRole('button', { name: 'Make the check required' })).toHaveCount(0);
  await expect(page.getByRole('link', { name: /Open ruleset settings/ })).toHaveAttribute(
    'href',
    'https://github.com/acme/app/settings/rules',
  );
  await expect(step.getByRole('heading', { name: 'Require archfit/gate', exact: true })).toBeVisible();
  await expectPageText(page, 'home-setup-required');
  await expectClean(page);
});

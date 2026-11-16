import { readFileSync } from 'node:fs';
import { expect, test } from '@playwright/test';
import { readHome } from '../src/lib/home';
import { expectClean, open } from './support';

const fixture = () =>
  readHome(
    JSON.parse(
      readFileSync(new URL('../../internal/control/ui/testdata/home/not-set-up.json', import.meta.url), 'utf8'),
    ),
  );
const approvedCopy = [
  'Require the gate',
  'Rules reach the default branch through review, so the approval binds to exact file contents.',
  'Next: review owner approval',
  'Ready for review',
  'Review needed',
  'Review and approve',
  'Review the proposed files before approving.',
  'A password or a GitHub review can be replayed by a compromised account',
];

for (const width of [390, 1280]) {
  test(`setup rule guidance: enforcement and manual advice at ${width}`, async ({ page }, testInfo) => {
    const body = fixture();
    if (!body.setup) throw new Error('Expected setup fixture');
    body.setup.rules = [
      { code: 'pull_requests', state: 'ok', text: '' },
      { code: 'gate_check', state: 'ok', text: '' },
      { code: 'administrator_bypass', state: 'unknown', text: '' },
      { code: 'app_bypass', state: 'warning', text: 'The App can bypass branch ruleset protect-main.' },
      { code: 'policy_push', state: 'not_applicable', text: '' },
      {
        code: 'policy_push_bypass',
        state: 'unknown',
        text: 'The App bypass for policy push ruleset policy-only cannot be read.',
      },
    ];
    await page.setViewportSize({ width, height: 900 });
    const writes = await open(page, '/r/acme/app', { 'GET /api/repos/acme/app/home': () => [200, body] });
    const step = page.getByRole('listitem').nth(3);
    await expect(step).toHaveClass(/done/);
    await expect(step.locator(':scope > .chip')).toHaveText('On');
    const row = (title: string) =>
      step.locator('.rule').filter({ has: page.getByRole('heading', { name: title, exact: true }) });
    const check = row('Require archfit/gate');
    await expect(check.getByRole('link', { name: 'Repository rulesets', exact: true })).toHaveAttribute(
      'href',
      'https://github.com/acme/app/settings/rules',
    );
    await expect(check.getByRole('link', { name: 'Classic branch protection', exact: true })).toHaveAttribute(
      'href',
      'https://github.com/acme/app/settings/branches',
    );
    await expect(check).toContainText('Archfit app as its source');
    const admin = row('Administrator bypass');
    await expect(admin.getByText('Verify yourself', { exact: true })).toBeVisible();
    await expect(admin).toContainText('does not hold Administration permission');
    await expect(admin).toContainText('no bypass actors');
    await expect(admin).toContainText('administrators only for emergencies');
    await expect(admin.getByRole('link', { name: 'Personal-account App installations' })).toHaveAttribute(
      'href',
      'https://github.com/settings/installations',
    );
    await expect(admin.getByRole('link', { name: 'Organization App installations (acme)' })).toHaveAttribute(
      'href',
      'https://github.com/organizations/acme/settings/installations',
    );
    await expect(row('Keep the App subject to branch rules')).toContainText('Remove the Archfit app');
    await expect(row('Keep the App subject to branch rules').getByText('Advisory', { exact: true })).toBeVisible();
    await expect(row('Let the App write policy proposals')).toContainText('bypass mode Always');
    const policy = row('Policy-push restriction');
    await expect(policy.getByText('Does not apply', { exact: true })).toBeVisible();
    await expect(policy).toContainText('nothing to do');
    await expect(step.getByText('Not available', { exact: true })).toHaveCount(0);
    await expectClean(page, approvedCopy);
    await step.screenshot({ path: testInfo.outputPath(`setup-step4-${width}.png`) });
    expect(writes).toEqual([]);
  });
}

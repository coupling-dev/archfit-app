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
    const step = page.locator('.setup .steps > li').nth(3);
    await expect(step).toHaveClass(/done/);
    await expect(step.locator('.step-head > .status-label')).toHaveText('done');
    await step.getByText('Details', { exact: true }).click();
    const row = (title: string) =>
      step.locator('.rule').filter({ has: page.getByRole('heading', { name: title, exact: true }) });
    const check = row('Require archfit/gate');
    await expect(check.getByRole('status')).toHaveCount(0);
    await expect(check).toContainText('Archfit app as its source');
    await expect(check.getByText('on', { exact: true })).toBeVisible();
    const admin = row('Administrator bypass');
    await expect(admin.getByText('verify in GitHub', { exact: true })).toBeVisible();
    await expect(admin).toContainText('Optional. Keep an emergency bypass for administrators only');
    await expect(admin.getByRole('link', { name: 'Repository rulesets', exact: true })).toHaveAttribute(
      'href',
      'https://github.com/acme/app/settings/rules',
    );
    await expect(row('Keep the App subject to branch rules')).toContainText('Remove the Archfit app');
    await expect(row('Keep the App subject to branch rules').getByText('optional', { exact: true })).toBeVisible();
    await expect(row('Let the App write policy pull requests')).toContainText('with mode Always');
    await expect(step.getByRole('heading', { name: 'Restrict .archfit.yaml', exact: true })).toHaveCount(0);
    await expect(step.getByText('Not available', { exact: true })).toHaveCount(0);
    await expectClean(page);
    await step.screenshot({ path: testInfo.outputPath(`setup-step4-${width}.png`) });
    expect(writes).toEqual([]);
  });
}

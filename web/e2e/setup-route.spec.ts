import { readFileSync } from 'node:fs';
import { expect, test } from '@playwright/test';
import { open } from './support';

const fixture = JSON.parse(readFileSync(new URL('../../internal/control/ui/testdata/home/not-set-up.json', import.meta.url), 'utf8'));

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

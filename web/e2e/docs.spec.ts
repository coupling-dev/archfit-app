import { expect, test } from '@playwright/test';
import { open, shotsDir, callouts } from './fixtures';


test('docs: the screenshots the user guide shows', async ({ page }) => {
  test.skip(!process.env.ARCHFIT_DOCS_SHOTS, 'set ARCHFIT_DOCS_SHOTS=1 to regenerate the guide screenshots');
  await page.setViewportSize({ width: 1800, height: 900 });

  await open(page, '/r/octo/tool');
  await expect(page.getByRole('status').getByText('waiting for approval')).toBeVisible();
  await callouts(page, [
    { at: page.locator('.pill'), text: 'Shown at once: this account waits for an operator.' },
    { at: page.getByRole('status'), text: 'Nothing runs and nothing is posted until it is approved.' },
    { at: page.getByRole('link', { name: 'How approval works' }), text: 'The approval rules, in this guide.' },
  ]);
  await page.screenshot({ path: new URL('waiting-page.png', shotsDir).pathname, fullPage: true });

});

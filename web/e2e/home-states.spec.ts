import { readFileSync } from 'node:fs';
import { expect, test } from '@playwright/test';
import { docsClock, open } from './support';

// The five states an owner meets on Home, from the contract fixtures. home.spec.ts runs the axe, CSP and words checks
// on every fixture; this file only captures them. Run with ARCHFIT_DOCS_SHOTS=1 to write
// docs/images/home/<state>-<width>-<scheme>.png (npm run docs:shots).
const states = [
  ['clean', 'clean'],
  ['drift', 'drift'],
  ['workflow-change', 'workflow-change'],
  ['unknown', 'partial-evidence'],
  ['not-set-up', 'not-set-up'],
] as const;
const fixture = (name: string) =>
  JSON.parse(readFileSync(new URL(`../../internal/control/ui/testdata/home/${name}.json`, import.meta.url), 'utf8'));

for (const [label, file] of states) {
  for (const width of [1280, 390]) {
    test(`home states: ${label} at ${width} @docs`, async ({ page }) => {
      await page.setViewportSize({ width, height: 900 });
      await docsClock(page);
      const body = fixture(file);
      const writes = await open(page, '/r/acme/app', { 'GET /api/repos/acme/app/home': () => [200, body] });
      await expect(page.getByRole('heading', { level: 1, name: 'acme/app', exact: true })).toBeVisible();
      await expect(page.locator('.page-head .status-line p')).toHaveText(body.status);
      await expect(page.getByRole('heading', { name: 'Needs you' })).toHaveCount(body.needs_you?.length ? 1 : 0);
      if (process.env.ARCHFIT_DOCS_SHOTS)
        for (const scheme of ['light', 'dark'] as const) {
          await page.emulateMedia({ colorScheme: scheme });
          await page.screenshot({
            path: new URL(`../../docs/images/home/${label}-${width}-${scheme}.png`, import.meta.url).pathname,
            animations: 'disabled',
          });
        }
      expect(writes).toEqual([]);
    });
  }
}

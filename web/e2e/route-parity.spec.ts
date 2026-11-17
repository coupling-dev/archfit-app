import { expect, test } from '@playwright/test';
import { open } from './fixtures';
import { pageRoutes } from './page-routes';
import { expectNoNotUsedWord } from './support';

for (const route of pageRoutes) {
  test(`route words: ${route.kind} ${route.path}`, async ({ page }) => {
    await open(page, route.path, {
      operator: route.kind === 'admin',
      ...(route.kind === 'unknown' ? { serverError: { status: 404, sentence: 'Not found.' } } : {}),
    });
    await expect(page.getByRole('heading', { name: route.heading, level: 1 })).toBeVisible();
    await expect(page.getByRole('status', { name: 'Loading' })).toHaveCount(0);
    await expect(page.getByRole('alert')).toHaveCount(0);
    await expectNoNotUsedWord(page);
  });
}

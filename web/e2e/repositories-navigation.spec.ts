import { expect, test } from '@playwright/test';
import { open } from './fixtures';
import { docsShot } from './support';

for (const width of [390, 1280]) {
  test(`Repositories navigation uses the canonical root at ${width}`, async ({ page }) => {
    await page.setViewportSize({ width, height: 900 });
    await open(page, '/r/acme/app/pull/4');
    const repositories = page.getByRole('banner').getByRole('link', { name: 'Repositories', exact: true });
    await expect(repositories).toHaveAttribute('href', '/');
    await repositories.click();
    await expect(page).toHaveURL('/');
    await expect(page.getByRole('heading', { name: 'Repositories', exact: true })).toBeVisible();
  });

  test(`landing and footer at ${width} @docs`, async ({ page }) => {
    await page.setViewportSize({ width, height: width === 390 ? 844 : 900 });
    await open(page, '/', { signedIn: false });
    await expect(page.getByRole('heading', { level: 1 })).toHaveText(
      'Check pull requests against your architecture policy',
    );
    await expect(page.locator('footer .footer-separator')).toHaveText('·');
    await expect(page.locator('footer .footer-separator')).toHaveAttribute('aria-hidden', 'true');
    await page.getByText('How the policy stays protected', { exact: true }).click();
    await docsShot(page, `states/landing-${width}`);
  });
}

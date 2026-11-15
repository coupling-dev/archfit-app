import { expect, test } from '@playwright/test';
import { open } from './fixtures';

for (const theme of ['light', 'dark'] as const) {
  test(`token cleanup preserves ${theme} computed colours`, async ({ page }) => {
    await page.emulateMedia({ colorScheme: theme });
    await open(page, '/', { signedIn: false });
    await expect(page.locator('.pitch')).toBeVisible();
    const selectors = [
      'body',
      '.app-header',
      '.brand',
      '.btn.primary',
      '.pitch-flow li',
      '.pitch-flow .ic',
      '.status.success',
      '.status.danger',
      '.status.attention',
      '.status.neutral',
      '.app-footer',
    ];
    const colours: Record<string, object | string> = {};
    for (const selector of selectors) {
      colours[selector] = await page
        .locator(selector)
        .first()
        .evaluate((el) => {
          const css = getComputedStyle(el);
          return { color: css.color, background: css.backgroundColor, border: css.borderColor, shadow: css.boxShadow };
        });
    }
    await page.locator('.btn.primary').hover();
    colours.hover = await page.locator('.btn.primary').evaluate((el) => getComputedStyle(el).backgroundColor);
    expect(JSON.stringify(colours, null, 2)).toMatchSnapshot(`token-colours-${theme}.json`);
  });
}

import { expect, test } from '@playwright/test';
import { open } from './fixtures';

test('segmented control: one tab stop, arrow keys choose and move focus, the selection wraps', async ({ page }) => {
  await open(page, '/');
  const theme = page.getByRole('radiogroup', { name: 'Theme' });
  const radios = theme.getByRole('radio');
  await expect(radios).toHaveText(['Auto', 'Light', 'Dark']);
  await expect(radios.first()).toHaveAttribute('aria-checked', 'true');

  await expect(radios.first()).toHaveAttribute('tabindex', '0');
  await expect(radios.nth(1)).toHaveAttribute('tabindex', '-1');
  await expect(radios.nth(2)).toHaveAttribute('tabindex', '-1');
  await radios.first().focus();
  await expect(page.locator('html')).not.toHaveAttribute('data-theme', /./);

  await page.keyboard.press('ArrowRight');
  await expect(radios.nth(1)).toBeFocused();
  await expect(radios.nth(1)).toHaveAttribute('aria-checked', 'true');
  await expect(page.locator('html')).toHaveAttribute('data-theme', 'light');

  await page.keyboard.press('ArrowLeft');
  await page.keyboard.press('ArrowLeft');
  await expect(radios.nth(2)).toBeFocused();
  await expect(radios.nth(2)).toHaveAttribute('aria-checked', 'true');
  await expect(page.locator('html')).toHaveAttribute('data-theme', 'dark');
  await page.keyboard.press('ArrowDown');
  await expect(radios.first()).toBeFocused();
  await expect(radios.first()).toHaveAttribute('aria-checked', 'true');
});

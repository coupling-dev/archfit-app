import { readFileSync } from 'node:fs';
import { expect, test } from '@playwright/test';
import { expectClean, expectPageText, open } from './support';
const request = {
  number: 431,
  state: 'open',
  reason: 'Use the helper.',
  rule: 'graph',
  from: 'app',
  to: 'util',
  record: 'app -> util',
  verified: false,
  policy: true,
  dependencies: [],
  layers: [],
  modules: [],
  allowed: false,
  earlier: [],
};

const home = JSON.parse(
  readFileSync(new URL('../../internal/control/ui/testdata/home/one-request.json', import.meta.url), 'utf8').replaceAll(
    '431',
    '415',
  ),
);
const graphName = 'Modules by layer and their dependencies';

for (const width of [390, 1280]) {
  test('map: list, complete diagram and real request at ' + width, async ({ page }) => {
    await page.setViewportSize({ width, height: 844 });
    await open(page, '/r/acme/app', { 'GET /api/repos/acme/app/home': () => [200, home] });
    const diagram = page.getByRole('img', { name: graphName });
    const listToggle = page.getByText('Show the map as a list', { exact: true });
    const diagramToggle = page.getByText('Show the map as a diagram', { exact: true });
    await expect(page.locator('.map-node')).toHaveCount(3);
    const legend = page.locator('.map-diagram').getByRole('list', { name: 'Map legend', includeHidden: true });
    await expect(legend).toHaveCount(1);
    await expect(page.locator('main')).not.toContainText('#431');
    if (width < 720) {
      await expect(diagram).toBeHidden();
      await expect(legend).toBeHidden();
      expect(await page.locator('main').innerText()).not.toContain('A requested dependency');
      await expect(page.getByText('app → other: Requested dependency · #415', { exact: true })).toBeVisible();
    } else {
      await expect(diagram).toBeVisible();
      await listToggle.click();
    }
    await expect(page.getByRole('link', { name: '#415', exact: true })).toHaveAttribute(
      'href',
      '/r/acme/app/requests/415',
    );
    await expectPageText(page, 'map-list-' + width);
    await expect(page.locator('.map-list')).toHaveAttribute('open', '');
    await page.screenshot({ path: '../tmp/map-touch/after-' + width + '.png', fullPage: true });
    await expect(page.locator('.map-list')).toHaveAttribute('open', '');
    if (width < 720) {
      await diagramToggle.focus();
      await page.keyboard.press('Enter');
    }
    await expect(diagram).toBeVisible();
    await expect(legend).toBeVisible();
    await expect(legend).not.toContainText('#');
    await expect(legend).toContainText('A requested dependency');
    expect(await diagram.evaluate((el) => el.getBoundingClientRect().width)).toBeLessThanOrEqual(width - 32);
    expect(
      await diagram.evaluate((el: SVGSVGElement) => {
        const content = el.getBBox();
        const box = el.viewBox.baseVal;
        return (
          content.x >= 0 &&
          content.y >= 0 &&
          content.x + content.width <= box.width &&
          content.y + content.height <= box.height
        );
      }),
    ).toBe(true);
    expect(await page.evaluate(() => document.documentElement.scrollWidth)).toBeLessThanOrEqual(width);
    await page.getByRole('button', { name: 'Dark', exact: true }).click();
    await expect(page.locator('html')).toHaveAttribute('data-theme', 'dark');
    await expect(diagram).toBeVisible();
    await page.screenshot({ path: '../tmp/map-touch/after-dark-' + width + '.png', fullPage: true });
    await page.getByRole('button', { name: 'Auto', exact: true }).click();
    await expectClean(page);
  });

  test('shell: skip link and touch controls at ' + width, async ({ page }) => {
    await page.setViewportSize({ width, height: 844 });
    await open(page, '/r/acme/app', { 'GET /api/repos/acme/app/home': () => [200, home] });
    await expect(page.getByRole('heading', { name: 'Home', exact: true })).toBeVisible();
    await page.keyboard.press('Tab');
    const skip = page.getByRole('link', { name: 'Skip to main content' });
    await expect(skip).toBeFocused();
    await expect(skip).toBeInViewport();
    await page.keyboard.press('Enter');
    await expect(page.locator('main')).toBeFocused();
    const dark = page.getByRole('button', { name: 'Dark', exact: true });
    await dark.focus();
    await expect(dark).toHaveCSS('outline-style', 'solid');
    if (width < 720) {
      for (const control of [
        dark,
        page.getByRole('button', { name: 'Sign out' }),
        page.getByRole('link', { name: 'Policy', exact: true }),
        page.getByText('Show the map as a diagram', { exact: true }),
      ]) {
        const box = await control.boundingBox();
        expect(box?.height).toBeGreaterThanOrEqual(44);
        expect(box?.width).toBeGreaterThanOrEqual(44);
      }
    }
  });

  test('request: shared fields in dark mode at ' + width, async ({ page }) => {
    await page.setViewportSize({ width, height: 844 });
    await open(page, '/r/acme/app/requests/431', {
      'GET /api/repos/acme/app/requests/431': () => [200, request],
      'GET /api/repos/acme/app/requests/431/preview': () => [422, { error: 'Choose an answer.' }],
    });
    const date = page.getByLabel('The exception ends on');
    const text = page.getByLabel('What to do instead');
    await page.getByRole('radio', { name: 'Approve until a date', exact: true }).check();
    await expect(date).toHaveClass(/form-control/);
    await page.getByRole('button', { name: 'Dark', exact: true }).click();
    await expect(date).toHaveCSS('background-color', 'rgb(13, 17, 23)');
    await date.fill('2027-01-15');
    await expect(date).toHaveValue('2027-01-15');
    if (width < 720) expect((await date.boundingBox())?.height).toBeGreaterThanOrEqual(44);
    await page.getByRole('radio', { name: 'Reject', exact: true }).check();
    await expect(text).toHaveClass(/form-control/);
    await text.fill('Use the existing port.');
    await expect(text).toHaveValue('Use the existing port.');
    await expect(text).toHaveCSS('background-color', 'rgb(13, 17, 23)');
    await expect(text).toHaveCSS('outline-style', 'solid');
    expect(await page.evaluate(() => document.documentElement.scrollWidth)).toBeLessThanOrEqual(width);
    await page.screenshot({ path: '../tmp/map-touch/request-dark-' + width + '.png', fullPage: true });
  });
}

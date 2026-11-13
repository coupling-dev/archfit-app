import { expect, test } from '@playwright/test';
import { session, watching, installed, workflowMerged, workflowYAML, workflowNeeded, open, expectClean } from './fixtures';


test('overview: state, pull requests, setup and history', async ({ page }) => {
  const writes = await open(page, '/r/acme/app');
  await expect(page.getByRole('heading', { name: 'acme/app', level: 1 })).toBeVisible();
  await expect(page.locator('.hero .state.attention')).toHaveText('attention');
  await expect(page.locator('#attention-reason')).toHaveText(
    'In the latest report: 1 advisory; testability and operations are partially measured.',
  );
  await expect(page.getByRole('link', { name: 'Overview' })).toHaveAttribute('aria-current', 'page');
  await expect(page.getByRole('navigation', { name: 'Repository' }).getByRole('link')).toHaveText(['Overview', 'Architecture']);
  await expect(page.getByRole('link', { name: '#4 feat: read options' })).toHaveAttribute('href', '/r/acme/app/pull/4');
  await expect(page.getByText('2 more open pull requests')).toBeVisible();
  await expect(page.getByText('The trial ends')).toBeVisible();
  await expect(page.getByText('Advisory (current step)')).toBeAttached();
  const needsYou = page.getByRole('region', { name: 'Needs you' });
  await expect(needsYou).toContainText('Make the gate required.');
  await expect(needsYou.getByText('Then merge the settings pull request.')).toBeHidden();
  await needsYou.getByText('How', { exact: true }).click();
  await expect(needsYou.getByText('Then merge the settings pull request.')).toBeVisible();
  const counts = page.getByRole('group', { name: 'Counts on main' });
  await expect(counts).toContainText('3 Rule violations');
  await expect(counts).toContainText('1 Advisory never block');
  // a pull request with no report yet is waiting, not unhealthy
  await expect(counts).toContainText('0 Open pull requests not healthy among the 2 listed; 1 waiting for a report');
  await expect(page.getByRole('heading', { name: 'Findings on main' })).toBeVisible();
  await expect(page.getByText('Go through the repository.')).toBeVisible();
  await expect(page.getByRole('link', { name: 'Open the graph' })).toHaveAttribute('href', '/r/acme/app/graph');
  await expect(page.getByRole('link', { name: 'Data sharing' })).toHaveAttribute('href', '/r/acme/app/data-sharing');
  await expect(page.getByRole('link', { name: 'Consent' })).toHaveCount(0);
  // the next step is the gate, so no button for another step: one sentence, one action
  const needs = page.getByRole('region', { name: 'Needs you' });
  await expect(needs.getByRole('button')).toHaveCount(0);
  await expectClean(page);
  expect(writes).toEqual([]);
});


test('overview: counters and the main-branch watch line', async ({ page }) => {
  const writes = await open(page, '/r/acme/app', { overview: watching });
  const counters = page.getByRole('group', { name: 'Pull requests' });
  await expect(counters).toContainText('1 Pull request to update');
  await expect(counters.getByRole('link')).toHaveCount(0);
  await expect(page.locator('#main-watch')).toHaveText('Last checked commit on main: 9f3a2c1, 2 hours ago.');

  // the pull request that waits for an update names the modules, in the words of the comment
  const pulls = page.getByRole('region', { name: 'Open pull requests' });
  await expect(pulls.getByRole('link', { name: '#6 refactor: split orders' })).toHaveAttribute(
    'href',
    '/r/acme/app/pull/6',
  );
  await expect(pulls).toContainText('To update. Pull request author: The target branch moved');
  await expect(pulls).toContainText('closes a module cycle: orders, billing.');
  await expect(pulls.getByText('To update.')).toHaveCount(1);
  await expectClean(page);
  expect(writes).toEqual([]);
});


test('overview: counters say when they could not be read, and a quiet repository shows zeros', async ({ page }) => {
  await open(page, '/r/acme/app', {
    overview: { ...watching, pulls_error: true },
  });
  const counters = page.getByRole('group', { name: 'Pull requests' });
  await expect(counters).toContainText('– Pull requests to update the open pull requests could not be read');

  // One pull request's check could not be read: the count is a floor and says so, never the whole.
  await page.unrouteAll();
  await open(page, '/r/acme/app', { overview: { ...watching, pulls_unread: 2 } });
  await expect(counters).toContainText('1+ Pull requests to update 2 could not be read, so there may be more');

  await page.unrouteAll();
  await open(page, '/r/acme/app');
  await expect(counters).toContainText('0 Pull requests to update');
  await expect(page.locator('#main-watch')).toHaveCount(0);
});


test('overview: discovery is offered at its own step and runs in the workflow', async ({ page }) => {
  const writes = await open(page, '/r/acme/app', { overview: workflowMerged });
  const needs = page.getByRole('region', { name: 'Needs you' });
  await expect(needs).toContainText('Run discovery: it proposes a first policy');
  await expect(needs).not.toContainText('workflow_dispatch with'); // the diagnostic yields to the one-sentence step
  await expectClean(page);
  await needs.getByRole('button', { name: 'Run discovery in your workflow' }).click();
  await expect(page.getByRole('status')).toBeVisible();
  expect(writes.map((w) => w.url())).toEqual([expect.stringMatching(/\/api\/repos\/acme\/app\/discover$/)]);
});


test('overview: an admin opens the onboarding pull request', async ({ page }) => {
  const writes = await open(page, '/r/acme/app', { overview: installed });
  await page.getByLabel('Policy owners').fill('alice\nacme/platform');
  await expectClean(page);
  await page.getByRole('button', { name: 'Open the onboarding pull request' }).click();
  await expect(page.getByRole('status')).toHaveText(
    'The onboarding pull request is open. Next: add the workflow file to it.',
  );

  expect(writes).toHaveLength(1);
  const [w] = writes;
  expect(new URL(w.url()).pathname).toBe('/api/repos/acme/app/onboarding');
  expect(w.headers()['x-csrf-token']).toBe(session.csrf);
  expect(w.headers()['content-type']).toBe('application/json');
  expect(w.postDataJSON()).toEqual({ mode: 'customer', owners: 'alice\nacme/platform' });
});


test('overview: the owner adds the workflow file the App does not write', async ({ page, context }) => {
  await context.grantPermissions(['clipboard-read', 'clipboard-write']);
  const writes = await open(page, '/r/acme/app', { overview: workflowNeeded });
  const needs = page.getByRole('region', { name: 'Needs you' });
  await expect(needs).toContainText('Add the Archfit workflow.');
  await expect(needs).not.toContainText('to PR #1'); // the card says the step; the raw diagnostic stays out
  await expect(needs.getByRole('link', { name: 'Add the file on GitHub' })).toHaveAttribute(
    'href',
    workflowNeeded.workflow.add_url,
  );
  await expect(needs).toContainText('Commit directly to the archfit/onboarding branch');
  await expect(needs.getByLabel('Policy owners')).toHaveCount(0); // onboarding is started: no second form
  await expectClean(page);

  await needs.getByRole('button', { name: 'Copy the file' }).click();
  await expect(needs.getByRole('status')).toContainText('Copied.');
  expect(await page.evaluate(() => navigator.clipboard.readText())).toBe(workflowYAML);

  const reread = page.waitForRequest('**/api/repos/acme/app/overview');
  await needs.getByRole('button', { name: 'Check again' }).click();
  await reread;
  expect(writes).toEqual([]);
});

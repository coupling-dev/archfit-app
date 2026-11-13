import { expect, test } from '@playwright/test';
import { analysisCheck, steps, reporting, watching, installed, pull, open, shotsDir, callouts } from './fixtures';


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

  await page.unrouteAll();
  await open(page, '/r/acme/app', { overview: installed });
  await expect(page.getByRole('heading', { name: 'Needs you' })).toBeVisible();
  await callouts(page, [
    { at: page.getByLabel(/Policy owners/), text: 'One GitHub login per line. Owners approve changes to the policy.' },
    {
      at: page.getByRole('button', { name: 'Open the onboarding pull request' }),
      text: 'Opens a pull request with the settings.',
    },
    { at: page.locator('ol.track'), text: 'Setup: each step is marked done or current.' },
  ]);
  await page.screenshot({
    path: new URL('onboarding-page.png', shotsDir).pathname,
    fullPage: true,
    clip: { x: 0, y: 0, width: 1800, height: 820 },
  });
});


// The screenshots of the pages that changed for the agent-first gate: the Pull page under an action-required result
// whose reasons all come from the report, and the Overview of a gate that the settings call required while GitHub does
// not require it. Light and dark, with sample data. Regenerate with `npm run docs:shots`.
test('docs: the screenshots of the report-derived action-required page and the unenforced gate', async ({ page }) => {
  test.skip(!process.env.ARCHFIT_DOCS_SHOTS, 'set ARCHFIT_DOCS_SHOTS=1 to regenerate the guide screenshots');
  await page.setViewportSize({ width: 1280, height: 900 });

  const coverage = {
    ...pull,
    result: 'action required',
    tone: 'attention',
    headline: 'Action required',
    required: true,
    check: {
      ...analysisCheck,
      reason: 'missing_required_coverage (evidence)',
      next: 'Install the required tool in the workflow, then re-run it.',
      detail: 'layers',
    },
    decision: {
      verdict: 'needs attention',
      detail: 'hard gates unmeasured, 1 required rule not evaluated',
      hard_gates: 'unmeasured',
      blockers: 0,
      unevaluated: [{ rule_id: 'layers', reason: 'required tool go/packages was absent' }],
      origin_reasons: [],
    },
  };
  const unenforced = {
    ...reporting,
    steps: steps(4),
    stage: 'Gate is required in settings, not enforced by GitHub',
    next: 'Make archfit/gate a required status check in a GitHub ruleset on main.',
    next_how:
      "The settings say gate: required, but GitHub does not require the check on main, so a failing pull request can still be merged. In the repository's Settings, Rules, add a branch ruleset for main with the required status check archfit/gate.",
  };

  for (const scheme of ['light', 'dark'] as const) {
    await page.unrouteAll();
    await page.emulateMedia({ colorScheme: scheme });
    await open(page, '/r/acme/app/pull/4', { pull: coverage });
    await expect(page.getByText('Action required').first()).toBeVisible();
    await expect(page.getByRole('heading', { name: /^Rule violations/ })).toBeVisible();
    await page.screenshot({
      path: new URL(`pr-report-action-required-${scheme}.png`, shotsDir).pathname,
      fullPage: true,
    });

    await page.unrouteAll();
    await open(page, '/r/acme/app', { overview: unenforced });
    await expect(
      page.getByText('Make archfit/gate a required status check in a GitHub ruleset on main.'),
    ).toBeVisible();
    await page.screenshot({
      path: new URL(`overview-gate-not-enforced-${scheme}.png`, shotsDir).pathname,
      fullPage: true,
    });
  }
});


// The pages of the agent-first gate that watch many agents: the repository overview with its counters, the watch line on
// the main branch and the decision requests under Needs you. Light and dark, sample data.
// Regenerate with ARCHFIT_DOCS_SHOTS=1.
test('docs: the screenshot of the pull requests to update', async ({ page }) => {
  test.skip(!process.env.ARCHFIT_DOCS_SHOTS, 'set ARCHFIT_DOCS_SHOTS=1 to regenerate the guide screenshots');
  await page.setViewportSize({ width: 1280, height: 900 });
  for (const scheme of ['light', 'dark'] as const) {
    await page.unrouteAll();
    await page.emulateMedia({ colorScheme: scheme });
    await open(page, '/r/acme/app', { overview: watching });
    await expect(page.getByRole('heading', { name: 'Findings on main' })).toBeVisible();
    await page.getByRole('region', { name: 'Open pull requests' }).screenshot({
      path: new URL(`overview-pulls-to-update-${scheme}.png`, shotsDir).pathname,
    });
  }
});

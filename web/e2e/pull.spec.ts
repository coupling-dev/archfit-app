import { expect, test } from '@playwright/test';
import { appBuild, analysisCheck, pull, pullGraph, firstScreen, open, expectClean } from './fixtures';


test('pull request: the first screen is the comment, as text', async ({ page }) => {
  await open(page, '/r/acme/app/pull/4', {
    pull: {
      ...pull,
      first_screen: [...firstScreen, { kind: 'text', text: '<img src=x onerror=alert(1)> **not bold**' }],
    },
  });
  const screen = page.getByRole('region', { name: 'Architecture gate failing' });
  await expect(screen).toBeVisible();
  await expect(screen).toContainText('Checked with engine v3.4.0 and policy digest 1d44a1486570.');
  await expect(screen.getByRole('listitem').first()).toContainText(
    'active finding pkg/a/a.go:3 no_direct_b_dependency: a → b. Why:',
  );
  await expect(screen.getByRole('heading', { name: 'Policy change', level: 3 })).toBeVisible();
  await expect(screen.getByRole('listitem')).toHaveCount(2);
  await expect(screen).toContainText('<img src=x onerror=alert(1)> **not bold**');
  await expect(screen.locator('img, strong, b')).toHaveCount(0);
  // it sits between the result and the analysis
  const order = await page.locator('#result, #first-screen, #analysis').evaluateAll((els) => els.map((e) => e.id));
  expect(order).toEqual(['result', 'first-screen', 'analysis']);
  await expectClean(page);
});


test('pull request: the first screen carries the sentence the green flash said, once', async ({ page }) => {
  const clean = {
    ...pull,
    required: true,
    result: 'healthy',
    tone: 'success',
    headline: 'Architecture gate passed',
    decision: { verdict: 'healthy', detail: '', hard_gates: 'pass', blockers: 0, unevaluated: [], origin_reasons: [] },
    violation_count: 0,
    cycles: null,
    other: null,
  };
  const sentence = 'No required architecture change requested in the evaluated scope.';
  await open(page, '/r/acme/app/pull/4', { pull: clean });
  await expect(page.locator('.flash.success')).toContainText(sentence);

  await page.unrouteAll();
  await open(page, '/r/acme/app/pull/4', {
    pull: {
      ...clean,
      first_screen: [
        { kind: 'headline', text: 'Architecture gate passed' },
        { kind: 'text', text: sentence + ' Checked with engine v3.4.0 and policy digest 1d44a1486570.' },
      ],
    },
  });
  await expect(page.getByText(sentence)).toHaveCount(1);
  await expect(page.locator('.flash.success')).toHaveCount(0);
});


test('pull request: without a first screen the page is as it was', async ({ page }) => {
  await open(page, '/r/acme/app/pull/4');
  await expect(page.getByRole('heading', { name: 'PR analysis' })).toBeVisible();
  await expect(page.locator('#first-screen')).toHaveCount(0);
});


test('pull request: the cycle drawn as on the comment, then the other findings', async ({ page, context }) => {
  await context.grantPermissions(['clipboard-read', 'clipboard-write']);
  await open(page, '/r/acme/app/pull/4');
  await expect(page.getByRole('heading', { name: 'feat: read options #4', level: 1 })).toBeVisible();
  await expect(page.getByRole('link', { name: '#4' })).toHaveAttribute('aria-current', 'page');
  await expect(page.locator('#result')).toHaveText('violations found');
  await expect(page.getByText('Advisory: does not block merging')).toBeVisible();
  await expect(page.locator('.result .box-foot')).toContainText('customer-attested');
  const cycle = page.getByRole('article').filter({ hasText: 'Dependency cycle between pkg_chaos and pkg_container' });
  await expect(cycle.locator('svg path.arrow')).toHaveCount(2);
  await expect(cycle.locator('svg path.arrow.added')).toHaveCount(1);
  await expect(cycle.getByText('37 locations', { exact: true })).toBeAttached();
  await expect(cycle.getByText('All 37 locations')).toBeVisible();
  await expect(cycle.getByText('Fix:')).toBeVisible();
  // an advisory on a violation's dependency joins its row, as on the comment
  await expect(cycle.getByRole('columnheader', { name: 'Also reported as' })).toBeVisible();
  await expect(cycle.getByRole('row', { name: /pkg_chaos → pkg_container/ })).toContainText(
    'Imbalanced coupling · critical',
  );
  await expect(page.getByText('Go through the repository.')).toBeVisible();
  await expect(page.getByRole('link', { name: 'Graph', exact: true })).toHaveAttribute(
    'href',
    '/r/acme/app/pull/4/graph',
  );
  await expect(page.locator('#decision')).toContainText(
    'Engine verdict blocked: hard gates fail, 3 active blockers, 1 required rule not evaluated',
  );
  await expect(cycle.getByText('Allowed alternatives:')).toBeVisible();
  await expect(cycle.locator('.cycle-legend')).toHaveText(
    /introduced finding; source-site origin unknown\s*pre-existing finding; source-site origin unknown/,
  );
  await expect(page.locator('#decision')).toContainText('layers: selector matches no module');
  await page.getByText('Advisories on other dependencies · never block').click();
  await expect(page.getByText('UI skips a layer.')).toBeVisible();
  const advisories = page.locator('#advisories');
  await expect(advisories).toContainText(
    "layering · Unselected hypothesis: Move the functionality: lower the distance, so the coupled code lives in one module. This is not a selected repair; review the finding's evidence and constraints first.",
  );
  await expect(advisories.getByText('owner decision')).toBeVisible();
  await expect(advisories).toContainText('metric fan_out: 3 → 5 (at most 4)');
  await expect(advisories).not.toContainText('(no module)');
  // known debt: its coupling, and no move to make
  await page.getByText('Known issues accepted earlier').click();
  const accepted = page.locator('#accepted');
  await expect(accepted.getByRole('columnheader', { name: 'Volatility' })).toBeVisible();
  await expect(accepted).toContainText('model');
  await expect(accepted).not.toContainText('Suggested move');
  await expect(page.locator('#fixed')).toContainText(
    'Previously accepted finding no longer observed; resolution not established.',
  );
  await expect(page.locator('#fixed')).not.toContainText('old → gone');
  await page.getByText('To measure more').click();
  const evidence = page.getByRole('table');
  await expect(evidence.getByRole('columnheader', { name: 'Result' })).toBeVisible();
  await expect(evidence.getByRole('columnheader', { name: 'Gate' })).toHaveCount(0);
  await expect(evidence.getByRole('row', { name: /Testability/ })).toContainText('3 of 3 ("classified source files")');
  await expect(evidence.getByRole('row', { name: /Drift/ })).toContainText('nothing to measure');
  await expect(evidence.getByRole('row', { name: /Drift/ })).toContainText('not applicable');
  await expectClean(page);

  // a fold first in its box draws no rule above it; a dimension that warns stands out
  await expect(page.locator('#tasks').locator('xpath=ancestor::section').locator('details.disclosure')).toHaveCSS(
    'border-top-width',
    '0px',
  );
  await expect(
    evidence
      .getByRole('row', { name: /Change locality/ })
      .getByRole('cell')
      .last(),
  ).toHaveCSS('font-weight', '600');
  await page.getByRole('button', { name: 'Copy 1 task' }).click();
  await expect(page.getByRole('status')).toHaveText('Copied 1 task.');
  expect(await page.evaluate(() => navigator.clipboard.readText())).toBe(pull.tasks);
});


test('feedback: positive accepted debt and no-task lifecycle are honest', async ({ page }, info) => {
  await open(page, '/r/acme/app/pull/4', {
    pull: {
      ...pull,
      required: true,
      result: 'healthy',
      tone: 'success',
      no_baseline: false,
      check: { ...analysisCheck, reason: 'healthy (architecture)', next: '', detail: '' },
      headline: 'Architecture gate passed',
      compared: false,
      violation_count: 0,
      cycles: [],
      other: [],
      advisories: [],
      advisory_count: 0,
      tasks: '',
      task_count: 0,
      decision: {
        ...pull.decision,
        verdict: 'healthy',
        hard_gates: 'pass',
        blockers: 0,
        unevaluated: [],
        detail:
          'hard gates pass. PR impact: unknown — no comparable Git-base finding-origin evidence. Declared rule checks: 41/41 evaluated. Implicit accepted-reference metric checks: inventory not reported. Accepted reference: comparable.',
      },
    },
  });
  await expect(
    page.getByText('No required architecture change requested in the evaluated scope. Accepted debt remains.', {
      exact: true,
    }),
  ).toBeVisible();
  await expect(page.getByText('No automated source-repair task supplied.', { exact: false })).toBeVisible();
  await expect(page.locator('#result')).toHaveClass(/success/);
  await expect(page.locator('#result')).not.toHaveClass(/danger/);
  const analysis = page.getByRole('region', { name: 'PR analysis', exact: true });
  await expect(analysis).toContainText('Reason: healthy (architecture)');
  await expect(analysis).not.toContainText('blocking_findings');
  await expect(analysis).not.toContainText('Fix the rule violations');
  await expect(page.locator('.result .box-foot')).not.toContainText('No baseline');
  await expect(page.getByRole('button', { name: /Copy.*task/ })).toHaveCount(0);
  await expect(page.locator('#fixed')).toContainText('resolution not established');
  await expect(page.locator('#fixed')).not.toContainText('old → gone');
  await expect(page.locator('#decision')).toContainText(
    'Declared rule checks: 41/41 evaluated. Implicit accepted-reference metric checks: inventory not reported.',
  );
  await expectClean(page);
  await page.screenshot({ path: info.outputPath('positive-debt-desktop.png'), fullPage: true });
  await page.setViewportSize({ width: 390, height: 844 });
  await expectClean(page);
  await page.screenshot({ path: info.outputPath('positive-debt-mobile.png'), fullPage: true });
});


test('feedback: a finding opens only its admitted pair in the existing graph', async ({ page }, info) => {
  await open(page, '/r/acme/app/pull/4');
  const link = page.getByRole('link', { name: 'Inspect api → db in the authenticated graph', exact: true });
  await link.focus();
  await expect(link).toBeFocused();
  await page.keyboard.press('Enter');
  await expect(page).toHaveURL(/\/graph\?from=api&to=db$/);
  const inspector = page.getByRole('complementary');
  await expect(inspector.getByRole('heading', { name: 'Dependency', exact: true })).toBeVisible();
  await expect(inspector).toContainText('api → db');
  await expect(page.getByText('Finding origin is not source-site origin.', { exact: false })).toBeVisible();
  await expectClean(page);
  await page.screenshot({ path: info.outputPath('focused-pair-desktop.png'), fullPage: true });
  await page.setViewportSize({ width: 390, height: 844 });
  await page.reload();
  await expect(inspector.getByRole('heading', { name: 'Dependency', exact: true })).toBeVisible();
  await expect(inspector).toContainText('api → db');
  await expect(page.getByRole('region', { name: /^Problems/ })).toContainText('api → db');
  const canvas = page.locator('.g-canvas');
  const apiNode = page.getByRole('button', { name: /^Module api(?:,|$)/ });
  const dbNode = page.getByRole('button', { name: /^Module db(?:,|$)/ });
  const nodes = [apiNode, dbNode];
  const visibleFraction = 0.8;
  for (const node of nodes) await expect(node).toBeVisible();
  await canvas.scrollIntoViewIfNeeded();
  const nodesInsideCanvas = async () => {
    const frame = await canvas.boundingBox();
    if (!frame) return false;
    for (const node of nodes) {
      const box = await node.boundingBox();
      if (!box || !box.width || !box.height) return false;
      const width = Math.max(0, Math.min(box.x + box.width, frame.x + frame.width) - Math.max(box.x, frame.x));
      const height = Math.max(0, Math.min(box.y + box.height, frame.y + frame.height) - Math.max(box.y, frame.y));
      if (width * height < visibleFraction * box.width * box.height) return false;
    }
    return true;
  };
  const initiallyInsideCanvas = await nodesInsideCanvas();
  if (!initiallyInsideCanvas) {
    await page.getByRole('button', { name: /fit view/i }).click();
    info.annotations.push({
      type: 'phone-fit-view',
      description:
        'Used the existing Fit View control after initial phone navigation; no desktop-resize auto-fit claim.',
    });
  }
  await expect.poll(nodesInsideCanvas).toBe(true);
  for (const node of nodes) await expect(node).toBeInViewport({ ratio: visibleFraction });
  await expectClean(page);
  await info.attach('initial-phone-canvas.json', {
    body: JSON.stringify(
      {
        viewport: page.viewportSize(),
        phoneBeforeReload: true,
        initiallyInsideCanvas,
        usedFitView: !initiallyInsideCanvas,
        canvas: await canvas.boundingBox(),
        api: await apiNode.boundingBox(),
        db: await dbNode.boundingBox(),
      },
      null,
      2,
    ),
    contentType: 'application/json',
  });
  await page.screenshot({ path: info.outputPath('focused-pair-initial-phone.png'), fullPage: true });
});


test('pull request: a link that is neither the App nor github.com is not rendered', async ({ page }) => {
  await open(page, '/r/acme/app/pull/4', {
    pull: { ...pull, url: 'javascript:alert(1)', checks_url: '//evil.example/c' },
  });
  await expect(page.getByRole('heading', { level: 1 })).toBeVisible();
  await expect(page.getByRole('link', { name: 'View on GitHub' })).toHaveCount(0);
  await expect(page.getByRole('link', { name: 'Graph', exact: true })).toHaveAttribute(
    'href',
    '/r/acme/app/pull/4/graph',
  );
  await expect(page.locator('a[href^="javascript:"], a[href^="//"]')).toHaveCount(0);
});


test('pull request: no report says why and links the checks', async ({ page }) => {
  await open(page, '/r/acme/app/pull/4', {
    pull: {
      ...pull,
      unavailable: 'GitHub no longer keeps the report of this run.',
      cycles: null,
      other: null,
      violation_count: 0,
    },
  });
  await expect(page.getByText('GitHub no longer keeps the report')).toBeVisible();
  await expect(page.getByText('Follow the next step in PR analysis above.')).toBeVisible();
  await expect(page.getByText('re-run the workflow')).toHaveCount(0);
  await expect(page.getByRole('link', { name: 'Checks on GitHub' })).toHaveAttribute('href', pull.checks_url);
  await expectClean(page);
});


test('PR diagnostics: historical producer stays fixed across App upgrades; partial evidence remains qualified', async ({
  page,
}, info) => {
  for (const version of ['v0.12.0', 'v0.13.0']) {
    for (const graph of [false, true]) {
      await page.unrouteAll();
      const partial = {
        ...pull,
        check: { ...analysisCheck, also: ['missing_required_coverage: layers. Next: Supply the missing evidence.'] },
        decision: {
          ...pull.decision,
          hard_gates: 'unmeasured',
          detail: 'hard gates unmeasured, 3 active blockers, 1 required rule not evaluated',
        },
      };
      await open(page, '/r/acme/app/pull/4' + (graph ? '/graph' : ''), {
        appBuild: { ...appBuild, version },
        pull: partial,
        pullGraph: { ...pullGraph, pull: partial, view: { ...pullGraph.view, incomplete: true } },
      });
      await expect(page.getByText(analysisCheck.producer, { exact: false })).not.toBeVisible();
      await page.getByText('Analysis details', { exact: true }).click();
      await expect(page.getByText(analysisCheck.producer, { exact: false })).toBeVisible();
      await expect(page.getByText('Action version / SHA: Not recorded', { exact: true })).toBeVisible();
      await expect(page.getByText('Analysis time (UTC): Not recorded', { exact: true })).toBeVisible();
      await page.getByText('About this App · ' + version, { exact: true }).click();
      await expect(page.locator('.app-about').getByText(appBuild.source_revision, { exact: true })).toBeVisible();
      await expect(page.locator('.app-about').getByText(appBuild.built_at, { exact: true })).toBeVisible();
      await expect(
        page
          .locator('.app-about')
          .getByText('Serving App build, not the Engine or Action that produced a PR analysis.'),
      ).toBeVisible();
      if (graph) {
        await expect(page.locator('#incomplete')).toBeVisible();
        await expect(page.locator('.svelte-flow__node').first()).toBeVisible();
      } else {
        await expect(
          page.getByText('Partial evidence: findings cover only what was measured.', { exact: false }),
        ).toBeVisible();
        await expect(page.getByRole('heading', { name: 'Evidence', exact: true })).toBeVisible();
      }
      await expectClean(page);
      if (version === 'v0.13.0')
        await page.screenshot({
          path: info.outputPath(graph ? 'partial-graph-about.png' : 'partial-findings-about.png'),
          fullPage: true,
        });
    }
  }
});


test('PR diagnostics: missing historical metadata and unsafe links stay explicit and escaped', async ({ page }) => {
  await open(page, '/r/acme/app/pull/4', {
    pull: {
      ...pull,
      check: { ...analysisCheck, producer: '', head_sha: '', run_id: 0, next: '[unsafe](javascript:alert(1))' },
      url: 'https://github.com.evil.example/acme/app/pull/4',
      checks_url: 'javascript:alert(1)',
    },
    appBuild: {},
  });
  await page.getByText('Analysis details', { exact: true }).click();
  await expect(page.getByText('Analysis producer (claimed): Not recorded', { exact: true })).toBeVisible();
  await expect(page.getByText('[unsafe](javascript:alert(1))', { exact: false })).toBeVisible();
  await expect(page.getByText('About this App · build not recorded', { exact: true })).toBeVisible();
  await expect(page.locator('a[href^="javascript:"], a[href*="evil.example"]')).toHaveCount(0);
  await expectClean(page);
});


test('PR diagnostics: run links preserve owners and repositories named pull', async ({ page }) => {
  for (const repository of ['acme/app', 'acme/pull', 'pull/app', 'pull/pull']) {
    await page.unrouteAll();
    await open(page, '/r/acme/app/pull/4', {
      pull: { ...pull, url: 'https://github.com/' + repository + '/pull/4' },
    });
    await expect(page.getByRole('link', { name: 'Run 88 · attempt 1' })).toHaveAttribute(
      'href',
      'https://github.com/' + repository + '/actions/runs/88/attempts/1',
    );
  }
});


test('PR diagnostics: an explicit no-upload producer is not missing history', async ({ page }) => {
  await open(page, '/r/acme/app/pull/4', {
    pull: {
      ...pull,
      check: { ...analysisCheck, producer: 'no upload', run_id: 0, run_attempt: 0 },
      unavailable: 'This result came without a report, so there is nothing to show here.',
      run_id: 0,
    },
  });
  await page.getByText('Analysis details', { exact: true }).click();
  await expect(page.getByText('Analysis producer (claimed): no upload', { exact: true })).toBeVisible();
  await expect(page.getByRole('heading', { name: 'Findings unavailable', exact: true })).toBeVisible();
  await expectClean(page);
});


test('PR diagnostics: keyboard disclosures and long metadata fit a phone viewport', async ({ page }) => {
  await page.setViewportSize({ width: 390, height: 844 });
  await open(page, '/r/acme/app/pull/4');
  const details = page.getByText('Analysis details', { exact: true });
  await details.press('Enter');
  await expect(details).toBeFocused();
  await expect(page.getByText(analysisCheck.report_digest, { exact: true })).toBeVisible();
  const about = page.getByText('About this App · ' + appBuild.version, { exact: true });
  await about.press('Enter');
  await expect(about).toBeFocused();
  await expect(page.locator('.app-about').getByText(appBuild.source_revision, { exact: true })).toBeVisible();
  expect(await page.evaluate(() => document.documentElement.scrollWidth)).toBeLessThanOrEqual(390);
  await expectClean(page);
});


test('pull request: on a phone the cycle stacks, and its labels stay readable', async ({ page }) => {
  await page.setViewportSize({ width: 390, height: 844 });
  await open(page, '/r/acme/app/pull/4');
  const label = page.locator('.cycle-drawing .arrow-label').first();
  await expect(label).toBeVisible();
  // the rendered height of 12px text: at least 11px, so the drawing is not scaled down
  expect((await label.boundingBox())!.height).toBeGreaterThanOrEqual(11);
  const boxes = await page.locator('.cycle-drawing .module rect').evaluateAll((rs) =>
    rs.map((r) => {
      const b = r.getBoundingClientRect();
      return Math.round(b.x + b.width / 2);
    }),
  );
  expect(new Set(boxes).size).toBe(1);

  // the findings tables read as one block per dependency: nothing off-screen, no row taller than its cells
  await page.getByText('Known issues accepted earlier').click();
  for (const wrap of await page.locator('.table-wrap:visible').all()) {
    const fit = await wrap.evaluate((w) => w.scrollWidth <= w.clientWidth);
    expect(fit).toBe(true);
  }
  // a cell as tall as its row but not its content is the empty block: each cell's box against what it renders, with
  // room for its padding and its label's line
  const slack = await page.locator('table.cards tbody td:visible').evaluateAll((cells) =>
    cells.map((td) => {
      const content = document.createRange();
      content.selectNodeContents(td);
      const rects = [...content.getClientRects()].filter((r) => r.width > 0 && r.height > 0);
      const used = Math.max(...rects.map((r) => r.bottom)) - Math.min(...rects.map((r) => r.top));
      return Math.round(td.getBoundingClientRect().height - used);
    }),
  );
  expect(slack.length).toBeGreaterThan(0);
  for (const s of slack) expect(s).toBeLessThanOrEqual(32);
  await expect(page.locator('table.cards td.sdv').first()).toBeVisible();
});


for (const graph of [false, true]) {
  for (const reason of ['unknown_engine_identity', 'missing_required_coverage', 'stale_inputs']) {
    test(`PR diagnostics: ${reason} stays actionable on ${graph ? 'Graph' : 'Findings'} without admitted data`, async ({
      page,
    }, info) => {
      const next =
        reason === 'unknown_engine_identity'
          ? 'Update the workflow producer and re-run.'
          : reason === 'stale_inputs'
            ? 'Restore authorization and re-run.'
            : 'Install the missing tool and re-run.';
      const check = {
        ...analysisCheck,
        reason: reason + ' (evidence)',
        next,
        detail: '<img src=x onerror=alert(1)> & missing evidence',
        // The API has projected historical prelude authority; the raw-summary cases run through both Go routes.
        also:
          reason === 'missing_required_coverage'
            ? ['missing_required_coverage (evidence): six rules not measured. Next: Supply the evidence.']
            : [
                'owner_approval_missing (governance): protected input. Next: Ask an architecture owner to authorize this commit.',
                'Historical report-derived claims and repair actions are withheld because source authority was not established.',
              ],
        profile: reason === 'missing_required_coverage' ? analysisCheck.profile : 'not available for display',
        approved:
          reason === 'unknown_engine_identity'
            ? {
                engine_version: 'v3.2.0',
                image_digest: 'sha256:' + '9'.repeat(64),
                platform: 'linux/amd64',
                action_ref: 'cadf35c0d4885a49a3a1482b876f2a456d20de06',
              }
            : undefined,
      };
      const blocked = {
        ...pull,
        check,
        run_id: 0,
        result: 'action required',
        tone: 'attention',
        headline: 'Action required',
        unavailable: 'This result asks for action before its findings mean anything.',
        decision: undefined,
        cycles: null,
        other: null,
        tasks: '',
        violation_count: 0,
      };
      await open(page, '/r/acme/app/pull/4' + (graph ? '/graph' : ''), {
        pull: blocked,
        pullGraph: { ...pullGraph, pull: blocked, view: null, unavailable: blocked.unavailable },
      });
      const analysis = page.getByRole('region', { name: 'PR analysis', exact: true });
      await expect(analysis.getByText('Reason: ' + reason + ' (evidence)', { exact: true })).toBeVisible();
      await expect(analysis.getByText(next, { exact: false })).toBeVisible();
      await expect(analysis.getByText(check.detail, { exact: true })).toBeVisible();
      if (reason === 'missing_required_coverage') {
        await expect(analysis).toContainText('Supply the evidence.');
      } else {
        await expect(analysis).not.toContainText('Supply the evidence.');
        await expect(analysis).toContainText('Ask an architecture owner to authorize this commit.');
        await expect(analysis).toContainText('Historical report-derived claims and repair actions are withheld');
      }
      await expect(analysis.getByText('Analysis time (UTC): Not recorded', { exact: true })).not.toBeVisible();
      await analysis.getByText('Analysis details', { exact: true }).click();
      await expect(analysis.getByText('Analysis time (UTC): Not recorded', { exact: true })).toBeVisible();
      await expect(page.getByRole('link', { name: 'Run 88 · attempt 1' })).toHaveAttribute(
        'href',
        'https://github.com/acme/app/actions/runs/88/attempts/1',
      );
      await expect(
        page.getByRole('heading', { name: graph ? 'Graph unavailable' : 'Findings unavailable', exact: true }),
      ).toBeVisible();
      await expect(
        page.getByRole('heading', { name: graph ? 'Findings unavailable' : 'Graph unavailable', exact: true }),
      ).toHaveCount(0);
      await expect(page.locator('.svelte-flow__node')).toHaveCount(0);
      await expect(page.getByRole('heading', { name: /^Rule violations/ })).toHaveCount(0);
      await expect(page.getByRole('heading', { name: 'For coding agents' })).toHaveCount(0);
      await expect(analysis.locator('img, script')).toHaveCount(0);
      if (check.approved) {
        await expect(
          analysis.getByText('Current approved workflow pins — not the producer of this analysis'),
        ).toBeVisible();
        await expect(analysis.getByText('Engine v3.2.0 · linux/amd64', { exact: true })).toBeVisible();
      }
      await expectClean(page);
      await analysis.getByText('Analysis details', { exact: true }).click();
      if (reason === 'unknown_engine_identity') {
        await page.screenshot({
          path: info.outputPath(graph ? 'blocked-graph.png' : 'blocked-findings.png'),
          fullPage: true,
        });
      }
    });
  }
}

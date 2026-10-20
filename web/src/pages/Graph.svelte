<script lang="ts">
  import FlowCanvas from '../components/graph/FlowCanvas.svelte';
  import GraphTables from '../components/graph/GraphTables.svelte';
  import Inspector from '../components/graph/Inspector.svelte';
  import Legend from '../components/graph/Legend.svelte';
  import Matrix from '../components/graph/Matrix.svelte';
  import ProblemList from '../components/graph/ProblemList.svelte';
  import DecisionLine from '../components/DecisionLine.svelte';
  import PullHead from '../components/PullHead.svelte';
  import SeamSummary from '../components/graph/SeamSummary.svelte';
  import Skeleton from '../components/Skeleton.svelte';
  import { explain, repoAPI, type ApiError } from '../lib/api';
  import { firstJSON } from '../lib/early';
  import {
    acceptedRows,
    dependencyWord,
    focus,
    focusLimit,
    isAdded,
    isProblem,
    maxDrawn,
    meaning,
    problemRows,
    statusCount,
    statuses,
    type Graph,
    type Status,
  } from '../lib/graph';
  import { safeHref, type Repo } from '../lib/route';
  import { ui } from '../lib/selection.svelte';

  // The repository graph (the latest default-branch report) or a pull request's graph (the report its check names).
  // This page and the drawing libraries are one lazy chunk; the rest of the App never loads them.
  let { route, here }: { route: Repo & { number?: number }; here: string } = $props();

  let body = $state<Graph | null>(null);
  let failure = $state('');
  let mode = $state<'graph' | 'matrix'>('graph');
  let scope = $state<'auto' | 'all' | 'problems'>('auto');
  let show = $state<Record<Status, boolean>>({
    violation: true,
    advisory: true,
    accepted: true,
    allowed: true,
    observed: true,
  });

  function load() {
    const path = route.number ? `pulls/${route.number}/graph` : 'graph';
    firstJSON<Graph>(repoAPI(route.owner, route.name, path)).then(
      (b) => (body = b),
      (e: ApiError) => (failure = explain(e, here)),
    );
  }
  load();

  const view = $derived(body?.view ?? null);
  const pull = $derived(!!body?.pull);
  const hasProblem = $derived(!!view && view.links.some((l) => isProblem(l, pull)));
  const big = $derived(!!view && view.nodes.length > maxDrawn);
  const focused = $derived(
    !!view && hasProblem && (scope === 'problems' || (scope === 'auto' && view.nodes.length > focusLimit)),
  );
  const part = $derived(view ? (focused ? focus(view, pull) : { nodes: view.nodes, links: view.links }) : null);
  const drawnLinks = $derived(part ? part.links.filter((l) => show[l.status]) : []);
  const rows = $derived(view ? problemRows(view.links, pull, ui.emphasis, show) : []);
  const accepted = $derived(view ? acceptedRows(view.links, pull, show) : []);
  const counts = $derived(
    statuses.map((s) => [s, view ? view.links.filter((l) => l.status === s).length : 0] as const).filter(([, n]) => n),
  );
  // kept: whether focus mode keeps any dependency of a status (problems, and in a pull request what it added); drawn:
  // whether the status is on the canvas or in the matrix.
  const kept = (s: Status) => !focused || mode === 'matrix' || !!part?.links.some((l) => l.status === s);
  const drawn = (s: Status) => show[s] && kept(s);
  const added = $derived(view ? view.links.filter((l) => isAdded(l, pull)).length : 0);
</script>

{#if failure}
  <p class="flash error" role="alert">{failure}</p>
{:else if !body}
  <Skeleton rows={6} />
{:else}
  {#if body.pull}
    <PullHead head={body.pull} fullName={route.fullName} current="graph" />
    <DecisionLine decision={body.pull.decision} />
  {:else}
    <div class="page-head">
      <div class="stack-sm">
        <h1>Architecture</h1>
        <p class="lede">
          The modules of the policy and the dependencies between them, in the latest report on <code>{body.branch}</code>.
          Read-only.
        </p>
      </div>
    </div>
  {/if}

  {#if body.unavailable || !view}
    <p class="flash warn">
      {body.unavailable}
      {#if body.pull && safeHref(body.pull.checks_url)}
        <a href={safeHref(body.pull.checks_url)} rel="noreferrer">Checks on GitHub</a>
      {/if}
    </p>
  {:else}
    <div class="stack-sm">
      {#if view.stale}
        <div class="flash warn" id="stale">
          <details>
            <summary>The policy text differs from the policy of this report: module names and lines may not match.</summary>
            <p class="small">{body.stale_reason || 'The graph shows the current policy text with the report it was not computed from.'}</p>
          </details>
        </div>
      {/if}
      {#if view.incomplete}
        <div class="flash warn" id="incomplete">
          <details>
            <summary>The report is incomplete: an observed dependency can still break a rule.</summary>
            <p class="small">
              A required rule was not evaluated, or a dimension the gate applies to measured nothing. Read the
              archfit/state check for the evidence gaps.
            </p>
          </details>
        </div>
      {/if}
      {#if body.pull && !body.pull.compared}
        <div class="flash warn" id="not-compared">
          <details>
            <summary>This run did not compare with the base branch, so the graph cannot show what this PR added.</summary>
            <p class="small">Every dependency is drawn without an "added" claim. The PR comment says the same.</p>
          </details>
        </div>
      {/if}
    </div>
    <SeamSummary links={view.links} />

    <div class="g-bar">
      <div class="row" role="group" aria-label="Show dependencies by status">
        <span class="muted small" id="g-unit">{dependencyWord(view.links.length)}:</span>
        {#if pull && body.pull?.compared}
          <button
            type="button"
            class="g-chip added"
            aria-pressed={ui.emphasis}
            title="Dependencies this pull request added, as the PR comment says. The halo marks them."
            onclick={() => (ui.emphasis = !ui.emphasis)}><i aria-hidden="true"></i><b>{added}</b> added in this PR</button
          >
        {/if}
        {#each counts as [s, n] (s)}
          <button
            type="button"
            class="g-chip {s}"
            aria-pressed={drawn(s)}
            title={meaning[s]}
            onclick={() => {
              // asking for the other dependencies while focused means the full picture
              if (!kept(s)) {
                show[s] = true;
                scope = 'all';
              } else show[s] = !show[s];
            }}><i aria-hidden="true"></i><b>{n}</b> {statusCount(s, n)}</button
          >
        {/each}
      </div>
      {#if !big}
        <div class="seg" role="group" aria-label="View">
          <button type="button" aria-pressed={mode === 'graph'} onclick={() => (mode = 'graph')}>Graph</button>
          <button type="button" aria-pressed={mode === 'matrix'} onclick={() => (mode = 'matrix')}>Matrix</button>
        </div>
      {/if}
    </div>

    <Legend {pull} />

    {#if big}
      <p class="flash warn">
        {view.nodes.length} modules are too many to draw. The tables below are the view.
      </p>
    {:else}
      <ProblemList {rows} {accepted} />
      <div class="g-grid">
        <div class="stack-sm">
          {#if mode === 'graph' && part}
            <FlowCanvas nodes={part.nodes} links={drawnLinks} all={view.links} {pull} />
            <p class="muted small" id="g-note">
              {#if focused}
                Showing the {part.nodes.length} of {view.nodes.length} modules with a problem.
                <button type="button" class="btn invisible sm" onclick={() => (scope = 'all')}
                  >Show all {view.nodes.length}</button
                >
              {:else if view.nodes.length > focusLimit && hasProblem}
                {view.nodes.length} modules.
                <button type="button" class="btn invisible sm" onclick={() => (scope = 'problems')}>Problems only</button>
              {/if}
            </p>
          {:else}
            <Matrix nodes={view.nodes} links={view.links} {pull} {show} />
          {/if}
        </div>
        <Inspector {view} {pull} policyHref={body.policy_href} />
      </div>
    {/if}

    <GraphTables {view} {pull} policyHref={body.policy_href} open={big} />
  {/if}
{/if}

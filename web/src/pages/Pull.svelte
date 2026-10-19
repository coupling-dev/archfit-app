<script lang="ts">
  import CycleCard from '../components/CycleCard.svelte';
  import FindingCard from '../components/FindingCard.svelte';
  import FindingTable from '../components/FindingTable.svelte';
  import { explain, getJSON, repoAPI, type ApiError, type Pull } from '../lib/api';
  import { repoPage, safeHref, type Repo } from '../lib/route';

  let { route, here }: { route: Repo & { number: number }; here: string } = $props();

  let body = $state<Pull | null>(null);
  let failure = $state('');
  let copied = $state('');

  function load() {
    getJSON<Pull>(repoAPI(route.owner, route.name, `pulls/${route.number}`)).then(
      (b) => (body = b),
      (e: ApiError) => (failure = explain(e, here)),
    );
  }
  load();

  async function copyTasks() {
    if (!body) return;
    try {
      await navigator.clipboard.writeText(body.tasks);
      copied = `Copied ${body.task_count} ${body.task_count === 1 ? 'task' : 'tasks'}.`;
    } catch {
      copied = 'The browser did not allow copying. Select the text below instead.';
    }
  }

  const stateTone: Record<string, string> = { open: 'success', draft: 'neutral', merged: 'done', closed: 'danger' };
  const findings = $derived(
    body ? (body.cycles?.length ?? 0) + (body.other?.length ?? 0) + (body.advisories?.length ?? 0) + (body.accepted?.length ?? 0) : 0,
  );
</script>

{#if failure}
  <p class="flash error" role="alert">{failure}</p>
{:else if !body}
  <p class="muted" aria-live="polite">Loading…</p>
{:else}
  <div class="page-head">
    <div class="stack-sm">
      <h1>{body.title || 'Pull request'} <span class="muted">#{body.number}</span></h1>
      <div class="row">
        <span class="state {stateTone[body.state] ?? 'neutral'}">{body.state}</span>
        {#if body.author}<span class="muted">{body.author} wants to merge</span>{/if}
        <code>{body.head_ref}</code><span class="muted">into</span><code>{body.base_ref}</code>
        <span class="muted small mono" title="Head commit">{body.head_sha}</span>
        {#if safeHref(body.url)}<a href={safeHref(body.url)} rel="noreferrer">View on GitHub</a>{/if}
      </div>
    </div>
  </div>

  <nav class="unav sub" aria-label="Pull request">
    <a href={repoPage(route.fullName, `/pull/${route.number}`)} aria-current="page">Findings</a>
    {#if safeHref(body.graph_href)}<a href={safeHref(body.graph_href)}>Graph</a>{/if}
  </nav>

  {#if body.result}
    <section class="box result" aria-labelledby="result">
      <div class="box-body row">
        <span class="state {body.tone}" id="result">{body.result}</span>
        <strong>{body.headline}</strong>
      </div>
      <div class="box-foot row">
        {#if body.required}
          <span class="label danger">gate: required</span>
        {:else}
          <span class="label">gate: advisory · does not block merging</span>
        {/if}
        {#if !body.unavailable}
          {#if body.no_baseline}<span class="label attention">no baseline</span>{/if}
          {#if body.compared}<span class="label accent">compared with base</span>{:else}<span class="label"
              >PR attribution unavailable</span
            >{/if}
          <span>customer-attested · run {body.run_id}</span>
        {/if}
      </div>
    </section>
  {/if}

  {#if body.unavailable}
    <p class="flash warn">
      {body.unavailable}
      {#if safeHref(body.checks_url)}<a href={safeHref(body.checks_url)} rel="noreferrer">Checks on GitHub</a>{/if}
    </p>
  {:else}
    {#if body.no_baseline || !body.compared}
      <p class="muted small">
        {#if body.no_baseline}Without a baseline every finding counts as new, including older debt.{/if}
        {#if body.compared}"Added by this PR" means the engine did not find it on the base branch.{:else}"New" means not
          accepted in a baseline, not "added by this pull request": this run did not compare against the base branch.{/if}
        The producer is authenticated; the report content is not independently checked.
      </p>
    {/if}

    <section class="stack" aria-labelledby="violations">
      <h2 id="violations">Rule violations <span class="counter">{body.violation_count}</span></h2>
      {#if body.violation_count === 0}
        <div class="box blankslate"><p>No rule violations in this run.</p></div>
      {/if}
      {#each body.cycles ?? [] as c, i (i)}
        <CycleCard {c} compared={body.compared} />
      {/each}
      {#each body.other ?? [] as f, i (i)}
        <FindingCard {f} />
      {/each}
    </section>

    {#if body.advisories?.length}
      <FindingTable
        id="advisories"
        title="Advisories · never block"
        count={body.advisory_count}
        rows={body.advisories}
        compared={body.compared}
      />
    {/if}
    {#if body.accepted?.length}
      <FindingTable
        id="accepted"
        title="Accepted · baseline or waiver"
        count={body.accepted_count}
        rows={body.accepted}
        compared={body.compared}
      />
    {/if}
    {#if body.omitted}
      <p class="muted small">{body.omitted} more findings are not listed: each group shows at most 50.</p>
    {/if}
    {#if findings === 0 && body.violation_count === 0}
      <p class="muted small">The report lists no finding to show.</p>
    {/if}

    {#if body.tasks}
      <section class="box" aria-labelledby="tasks">
        <div class="box-head">
          <h2 id="tasks">For coding agents <span class="counter">{body.task_count}</span></h2>
          <button class="btn sm" type="button" onclick={copyTasks}>
            Copy {body.task_count}
            {body.task_count === 1 ? 'task' : 'tasks'}
          </button>
        </div>
        <div class="box-body stack-sm">
          {#if copied}<p class="small" role="status">{copied}</p>{/if}
          <details class="disclosure">
            <summary>The exact <code>agent_tasks</code> of this run</summary>
            <pre>{body.tasks}</pre>
          </details>
        </div>
      </section>
    {/if}

    {#if body.dimensions?.length}
      <section class="box" aria-labelledby="evidence">
        <div class="box-head"><h2 id="evidence">Evidence</h2></div>
        <div class="table-wrap">
          <table class="data">
            <thead>
              <tr>
                <th scope="col">Dimension</th>
                <th scope="col">Status</th>
                <th scope="col">Coverage</th>
                <th scope="col">Confidence</th>
                <th scope="col">Gate</th>
              </tr>
            </thead>
            <tbody>
              {#each body.dimensions as d (d.key)}
                <tr>
                  <td><span title={d.key}>{d.name}</span></td>
                  <td>{d.status}</td>
                  <td>{d.coverage}</td>
                  <td>{d.confidence}</td>
                  <td>{d.gate}</td>
                </tr>
              {/each}
            </tbody>
          </table>
        </div>
        {#if body.unknowns?.length}
          <details class="disclosure box-body" id="measure-more">
            <summary>To measure more <span class="counter">{body.unknowns.length}</span></summary>
            <ul class="details-body">
              {#each body.unknowns as u, i (i)}
                <li><strong>{u.dimension}:</strong> {u.fact} <span class="muted">— {u.reason}</span></li>
              {/each}
            </ul>
          </details>
        {/if}
      </section>
    {/if}
  {/if}
{/if}

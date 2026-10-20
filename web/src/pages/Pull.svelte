<script lang="ts">
  import FindingGroups from '../components/FindingGroups.svelte';
  import DecisionLine from '../components/DecisionLine.svelte';
  import PullHead from '../components/PullHead.svelte';
  import { basisText, resultWord } from '../lib/evidence';
  import Skeleton from '../components/Skeleton.svelte';
  import { explainLoad, repoAPI, type ApiError, type Pull } from '../lib/api';
  import { firstJSON } from '../lib/early';
  import { safeHref, type Repo } from '../lib/route';

  let { route, here }: { route: Repo & { number: number }; here: string } = $props();

  let body = $state<Pull | null>(null);
  let failure = $state('');
  let copied = $state('');

  function load() {
    firstJSON<Pull>(repoAPI(route.owner, route.name, `pulls/${route.number}`)).then(
      (b) => (body = b),
      (e: ApiError) => (failure = explainLoad(e, here)),
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

</script>

{#if failure}
  <p class="flash error" role="alert">{failure}</p>
{:else if !body}
  <Skeleton rows={2} box={false} />
  <Skeleton rows={5} />
{:else}
  <PullHead head={body} fullName={route.fullName} current="findings" />
  <DecisionLine decision={body.decision} />

  {#if body.unavailable}
    <p class="flash warn">
      {body.unavailable}
      {#if safeHref(body.checks_url)}<a href={safeHref(body.checks_url)} rel="noreferrer">Checks on GitHub</a>{/if}
    </p>
  {:else}
    {#if body.no_baseline || !body.compared}
      <p class="muted small">
        {#if body.no_baseline}Without a baseline every finding counts as new, including older debt.{/if}
        {#if body.compared}"added by this PR" means the engine did not find it on the base branch.{:else}This run did
          not compare against the base branch, so the page does not say what this pull request added.{/if}
        The producer is authenticated; the report content is not independently checked.
      </p>
    {/if}

    <FindingGroups f={body} compared={body.compared} gatesFail={body.decision?.hard_gates === 'fail'} />

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
          <table class="data cards">
            <thead>
              <tr>
                <th scope="col">Dimension</th>
                <th scope="col">Status</th>
                <th scope="col">Measured</th>
                <th scope="col">Confidence</th>
                <th scope="col">Result</th>
              </tr>
            </thead>
            <tbody>
              {#each body.dimensions as d (d.key)}
                <tr>
                  <td><strong title={d.key}>{d.name}</strong></td>
                  <td data-label="Status">{d.status}</td>
                  <td data-label="Measured">{basisText(d)}</td>
                  <td data-label="Confidence">{d.confidence}</td>
                  <td data-label="Result">{resultWord(d.gate)}</td>
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

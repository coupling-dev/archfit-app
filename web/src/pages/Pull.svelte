<script lang="ts">
  import FindingGroups from '../components/FindingGroups.svelte';
  import FirstScreen from '../components/FirstScreen.svelte';
  import DecisionLine from '../components/DecisionLine.svelte';
  import DecisionRequests from '../components/DecisionRequests.svelte';
  import PullHead from '../components/PullHead.svelte';
  import { basisText, resultWord } from '../lib/evidence';
  import Skeleton from '../components/Skeleton.svelte';
  import { explain, explainLoad, postJSON, repoAPI, type ApiError, type Approved, type Pull, type Session } from '../lib/api';
  import { digestWord, offersApprove, stateLine } from '../lib/approval';
  import { firstJSON } from '../lib/early';
  import { repoPage, type Repo } from '../lib/route';
  import { trustSentence } from '../lib/feedback';

  let { route, session, here }: { route: Repo & { number: number }; session: Session; here: string } = $props();

  let body = $state<Pull | null>(null);
  let failure = $state('');
  let copied = $state('');
  let approving = $state(false);
  let approvalNotice = $state('');
  let approvalError = $state('');

  function load() {
    firstJSON<Pull>(repoAPI(route.owner, route.name, `pulls/${route.number}`)).then(
      (b) => (body = b),
      (e: ApiError) => (failure = explainLoad(e, here)),
    );
  }
  load();

  // approve sends back the exact head and the digests the page showed: the server refuses any other.
  async function approve() {
    const a = body?.approval;
    if (!body || !a) return;
    approving = true;
    approvalNotice = approvalError = '';
    try {
      const path = repoAPI(route.owner, route.name, `pulls/${route.number}/approve`);
      const res = await postJSON<Approved>(path, session.csrf, { head_sha: a.head_sha, digests: a.digests });
      body.approval = res.approval ?? undefined;
      approvalNotice = res.notice;
    } catch (e) {
      // A refusal or a failure after the record was written: show the sentence over the state read again.
      approvalError = explain(e as ApiError, here);
      load();
    } finally {
      approving = false;
    }
  }

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
  {@const first = body.first_screen ?? []}
  <PullHead head={body} fullName={route.fullName} current="findings">
    {#snippet screen()}{#if first.length}<FirstScreen lines={first} />{/if}{/snippet}
  </PullHead>
  <DecisionLine decision={body.decision} />
  {#if body.approval}
    {@const a = body.approval}
    <section class="box" aria-labelledby="owner-approval">
      <div class="box-head">
        <h2 id="owner-approval">Owner approval</h2>
        <span class="status {a.state === 'granted' ? 'success' : 'attention'}">
          {a.state === 'granted' ? 'approved' : 'needed'}
        </span>
      </div>
      <div class="box-body stack-sm">
        {#if a.author_is_agent}
          <p class="flash warn">Opened by an agent identity. Review its changes to the protected files before you approve.</p>
        {/if}
        <p>{stateLine(a, body.head_sha)}</p>
        <ul class="plain" aria-label="Protected files this pull request changes">
          {#each a.paths as path (path)}
            <li><code>{path}</code> <span class="muted small">{digestWord(a, path)}</span></li>
          {/each}
        </ul>
        {#if offersApprove(a)}
          <div>
            <button class="btn sm primary" type="button" disabled={approving} onclick={approve}>
              Approve this exact commit
            </button>
          </div>
        {:else if a.state === 'missing' && a.reason}
          <p class="muted">{a.reason}</p>
        {/if}
        <p class="muted small">
          An approval holds for this commit only: a new commit needs a new approval. Approve on GitHub, or here when
          GitHub does not let you approve your own pull request.
        </p>
        {#if approvalNotice}<p class="flash success" role="status">{approvalNotice}</p>{/if}
        {#if approvalError}<p class="flash error" role="alert">{approvalError}</p>{/if}
      </div>
    </section>
  {/if}
  {#if body.decisions?.length}
    <DecisionRequests
      requests={body.decisions}
      number={route.number}
      fullName={route.fullName}
      {session}
      {here}
      onchange={(list) => (body!.decisions = list)}
      onfailed={load}
    />
  {/if}
  {#if !first.length && !body.unavailable && body.required && body.result !== 'action required' && body.decision?.hard_gates === 'pass' && body.decision.blockers === 0 && !body.decision.unevaluated.length && body.violation_count === 0}
    <p class="flash success">No required architecture change requested in the evaluated scope.{body.accepted_count ? ' Accepted debt remains.' : ''}</p>
  {/if}

  {#if body.unavailable}
    <section class="box" aria-labelledby="findings-unavailable">
      <div class="box-head"><h2 id="findings-unavailable">Findings unavailable</h2></div>
      <div class="box-body stack-sm">
        <p>{body.unavailable}</p>
        <p>No findings, counts or agent tasks from this report are admitted for display. This is not a clean result.</p>
        <p>Follow the next step in PR analysis above. A usable result follows when the workflow reports on the commit.</p>
      </div>
    </section>
  {:else}
    {#if body.decision?.hard_gates === 'unmeasured' || body.decision?.unevaluated.length || body.dimensions?.some((d) => d.status !== 'measured')}
      <p class="flash warn">Partial evidence: findings cover only what was measured. Missing evidence is not a pass; read the required-rule gaps and Evidence below.</p>
    {/if}
    {#if body.no_baseline || !body.compared}
      <p class="muted small">
        {#if body.no_baseline}Without a baseline every finding counts as new, including older debt.{/if}
        {#if body.compared}An introduced finding is a new finding identity against the Git base; it does not establish a new module pair or that each listed source site was added.{:else}This run did
          not compare against the base branch, so the page does not say what this pull request added.{/if}
        {trustSentence}
      </p>
    {/if}

    <FindingGroups f={body} compared={body.compared} gatesFail={body.decision?.hard_gates === 'fail'} graphPage={repoPage(route.fullName, `/pull/${route.number}/graph`)} />

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
    {:else}
      <p class="muted small">No automated source-repair task supplied. Review any recovery or owner action above; no task is invented from an empty array.</p>
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
                  <td data-label="Result" class="result-{d.gate}">{resultWord(d.gate)}</td>
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

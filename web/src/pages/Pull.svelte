<script lang="ts">
  import FindingGroups from '../components/FindingGroups.svelte';
  import FirstScreen from '../components/FirstScreen.svelte';
  import DecisionLine from '../components/DecisionLine.svelte';
  import DecisionRequests from '../components/DecisionRequests.svelte';
  import PullHead from '../components/PullHead.svelte';
  import { basisText, resultWord } from '../lib/evidence';
  import Skeleton from '../components/Skeleton.svelte';
  import { explain, explainLoad, postJSON, repoAPI, type ApiError, type Approved, type Pull, type Session } from '../lib/api';
  import { digestWord, offersApprove, offersOverride } from '../lib/approval';
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
  let refreshNeeded = $state(false);
  let changesReason = $state('');
  let withdrawReason = $state('');
  let overrideReason = $state('');

  function load() {
    firstJSON<Pull>(repoAPI(route.owner, route.name, `pulls/${route.number}`)).then(
      (b) => (body = b),
      (e: ApiError) => (failure = explainLoad(e, here)),
    );
  }
  load();

  async function decide(action: 'approve' | 'request-changes' | 'withdraw' | 'override', reason = '') {
    const a = body?.approval;
    if (!body || !a) return;
    approving = true;
    approvalNotice = approvalError = '';
    refreshNeeded = false;
    try {
      const path = repoAPI(route.owner, route.name, `pulls/${route.number}/${action}`);
      const res = await postJSON<Approved>(path, session.csrf, {
        head_sha: a.head_sha,
        base_sha: a.base_sha,
        digests: a.digests,
        ...(reason ? { reason } : {}),
        ...(action === 'override'
          ? { objection_ids: a.objections.map((objection) => objection.id) }
          : {}),
      });
      body.approval = res.approval ?? undefined;
      approvalNotice = res.notice;
      if (action === 'request-changes') changesReason = '';
      if (action === 'withdraw') withdrawReason = '';
      if (action === 'override') overrideReason = '';
    } catch (e) {
      const failure = e as ApiError;
      approvalError = explain(failure, here);
      refreshNeeded = failure.status === 502;
      load();
    } finally {
      approving = false;
    }
  }

  async function retryDecisionRefresh() {
    if (!body?.approval) return;
    approving = true;
    approvalNotice = approvalError = '';
    try {
      const path = repoAPI(route.owner, route.name, `pulls/${route.number}/refresh-decision`);
      const res = await postJSON<Approved>(path, session.csrf, {});
      body.approval = res.approval ?? undefined;
      approvalNotice = res.notice;
      refreshNeeded = false;
    } catch (e) {
      approvalError = explain(e as ApiError, here);
      refreshNeeded = true;
      load();
    } finally {
      approving = false;
    }
  }

  function requestChanges(event: SubmitEvent) {
    event.preventDefault();
    void decide('request-changes', changesReason);
  }

  function withdraw(event: SubmitEvent) {
    event.preventDefault();
    void decide('withdraw', withdrawReason);
  }

  function override(event: SubmitEvent) {
    event.preventDefault();
    void decide('override', overrideReason);
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
    {@const githubPR = `https://github.com/${encodeURIComponent(route.owner)}/${encodeURIComponent(route.name)}/pull/${route.number}`}
    {@const activeObjections = a.objections.filter((objection) => !objection.overridden)}
    {@const nativeObjections = activeObjections.filter((objection) => objection.source === 'github')}
    {@const overriddenObjections = a.objections.filter((objection) => objection.overridden)}
    {@const overriddenNative = overriddenObjections.filter((objection) => objection.source === 'github')}
    {@const canApproveNormally = offersApprove(a, session.login)}
    {@const canOverride = offersOverride(a)}
    {@const openForDecision = body.state === 'open' || body.state === 'draft'}
    <section class="box" aria-labelledby="architecture-decision">
      <div class="box-head">
        <h2 id="architecture-decision">Architecture decision</h2>
        <span class="status {activeObjections.length ? 'attention' : a.state === 'granted' ? 'success' : 'attention'}">
          {activeObjections.length ? 'changes requested' : a.state === 'granted' ? 'approval saved' : 'decision needed'}
        </span>
      </div>
      <div class="box-body stack-sm">
        <p>This PR changes protected architecture files:</p>
        {#if a.author_is_agent}
          <p class="flash warn">The pull request author is a configured agent identity. Review the changes to the protected files before you decide.</p>
        {/if}
        <ul class="plain" aria-label="Protected files this pull request changes">
          {#each a.paths as path (path)}
            <li><code>{path}</code></li>
          {/each}
        </ul>

        <h3>Who can decide</h3>
        <p>Any one eligible owner may decide; not everyone listed must approve. Agents never decide.</p>
        <ul class="plain" aria-label="Configured architecture owners">
          {#each a.owners as owner (owner)}<li><code>{owner}</code></li>{/each}
        </ul>
        <p class="muted small">Owners come from <a href={a.settings_url} target="_blank" rel="noreferrer">protected .archfit-app.yaml settings</a>.</p>
        {#if a.required}
          <p class="muted small">Required mode: Archfit's gate requires a current owner approval. GitHub's branch rules decide whether this blocks merging.</p>
        {:else}
          <p class="flash">Advisory mode: this architecture decision does not prevent merging.</p>
        {/if}
        <p><a href={`${githubPR}/files`} target="_blank" rel="noreferrer">Review proposed changes on GitHub</a></p>
        {#if a.notification_problem}
          <p class="flash warn">Reviewer requests are not confirmed. Ask the configured owners on GitHub. This does not change who may decide.</p>
        {/if}
        <p class="muted small">A new push invalidates approval. Approval is not a successful check or merge.</p>
        {#if body.author.toLowerCase() === session.login.toLowerCase() && a.can_decide}
          <p class="muted small">You opened this PR. You can decide here as an architecture owner; GitHub cannot accept your own review.</p>
        {:else if !a.can_decide && a.reason}
          <p class="muted">{a.reason}</p>
        {/if}

        {#if activeObjections.length}
          <section aria-labelledby="active-objections">
            <h3 id="active-objections">Active owner objections</h3>
            <ul class="plain">
              {#each activeObjections as objection (objection.id)}
                <li>
                  <strong>@{objection.login}</strong> · {objection.source === 'github' ? 'GitHub review' : 'Archfit App decision'}
                  {#if objection.head_sha}<span class="muted small"> on <code>{objection.head_sha.slice(0, 7)}</code></span>{/if}
                  <p>{objection.reason || 'No reason was supplied.'}</p>
                </li>
              {/each}
            </ul>
            {#if nativeObjections.length}
              <p class="flash warn">This GitHub review may also block merging. An Archfit override leaves it unchanged; resolve or dismiss it on GitHub.</p>
            {/if}
          </section>
        {/if}

        {#if a.state === 'granted'}
          <p class="flash success">An eligible owner approved this exact candidate{a.by ? ` (@${a.by})` : ''}. The check result refreshes separately.</p>
        {:else if activeObjections.length}
          <p class="flash warn">Changes are requested. The pull request remains open while its contributor or an owner responds.</p>
        {:else}
          <p class="flash warn">Decision needed. No current eligible owner approval exists for this candidate.</p>
        {/if}

        {#if !openForDecision}
          <p class="flash">This pull request is {body.state}. No new architecture decision is available.</p>
        {/if}
        {#if openForDecision && canApproveNormally}
          <button class="btn sm primary" type="button" disabled={approving} onclick={() => decide('approve')}>
            Approve architecture change
          </button>
        {/if}

        {#if overriddenObjections.length}
          <section aria-labelledby="earlier-overrides">
            <h3 id="earlier-overrides">Earlier overridden objections</h3>
            <p class="muted small">A new override must also cover these objections.</p>
            <ul class="plain">
              {#each overriddenObjections as objection (objection.id)}
                <li><strong>@{objection.login}</strong> · {objection.source === 'github' ? 'GitHub review' : 'Archfit App decision'}<p>{objection.reason}</p></li>
              {/each}
            </ul>
            {#if overriddenNative.length}
              <h3>GitHub review remains independent</h3>
              <p class="flash warn">The Archfit override did not dismiss the native review. GitHub may still require it to be resolved before merge.</p>
            {/if}
          </section>
        {/if}

        {#if openForDecision && canOverride}
          <form class="stack-sm" onsubmit={override}>
            <label for="override-reason"><strong>Override and approve</strong> — give a reason covering all objections above, including earlier overrides.</label>
            <textarea id="override-reason" bind:value={overrideReason} required maxlength="1000" rows="3" disabled={approving}></textarea>
            <p class="muted small">Your reason will be posted to the PR (public for public repositories). GitHub reviews and branch protection remain independent.</p>
            <button class="btn sm" type="submit" disabled={approving}>Override and approve</button>
          </form>
        {/if}

        {#if openForDecision && a.can_decide}
          <form class="stack-sm" onsubmit={requestChanges}>
            <label for="changes-reason"><strong>Request changes</strong> with a reason. This does not close the pull request.</label>
            <textarea id="changes-reason" bind:value={changesReason} required maxlength="1000" rows="3" disabled={approving}></textarea>
            <p class="muted small">Your reason will be posted to the PR (public for public repositories).</p>
            <button class="btn sm" type="submit" disabled={approving}>Request changes</button>
          </form>
        {/if}

        {#if openForDecision && a.can_withdraw}
          <form class="stack-sm" onsubmit={withdraw}>
            <label for="withdraw-reason"><strong>Withdraw my approval</strong> with a reason.</label>
            <textarea id="withdraw-reason" bind:value={withdrawReason} required maxlength="1000" rows="3" disabled={approving}></textarea>
            <p class="muted small">Your reason will be posted to the PR (public for public repositories). Archfit will stop counting your approval. Your GitHub review is unchanged; other owners' approvals remain in force.</p>
            <button class="btn sm" type="submit" disabled={approving}>Withdraw my approval</button>
          </form>
        {/if}

        <p class="muted small">Latest Archfit state: {body.result || body.unavailable || 'no report received for this commit yet'}. A saved decision can still be waiting for evidence or other checks.</p>
        <p><a href={githubPR} target="_blank" rel="noreferrer">Open GitHub PR to merge or close it</a></p>
        <details class="disclosure">
          <summary>Decision details</summary>
          <div class="details-body stack-sm">
            <p class="muted small">Candidate <code>{a.head_sha}</code>. Protected settings commit <code>{a.settings_ref}</code>.</p>
            <ul class="plain">
              {#each a.paths as path (path)}<li><code>{path}</code> · {digestWord(a, path)}</li>{/each}
            </ul>
          </div>
        </details>
        {#if a.history.length}
          <details class="disclosure">
            <summary>Decision history <span class="counter">{a.history.length}</span></summary>
            <ol class="details-body">
              {#each a.history as decision (decision.id)}
                <li>
                  <strong>@{decision.login}</strong> · {decision.action.replaceAll('_', ' ')} · {decision.at} · candidate <code>{decision.head_sha.slice(0, 7)}</code> · settings <code>{decision.base_sha.slice(0, 7)}</code>
                  {#if decision.reason}<p>{decision.reason}</p>{/if}
                  {#if decision.overrode_objections?.length}
                    <ul>
                      {#each decision.overrode_objections as objection (objection.id)}
                        <li>Overrode <code>{objection.id}</code> · @{objection.login} · {objection.source}{#if objection.reason}<p>{objection.reason}</p>{/if}</li>
                      {/each}
                    </ul>
                  {/if}
                </li>
              {/each}
            </ol>
          </details>
        {/if}
        {#if approvalNotice}<p class="flash success" role="status">{approvalNotice}</p>{/if}
        {#if approvalError}<p class="flash error" role="alert">{approvalError}</p>{/if}
        {#if refreshNeeded || (openForDecision && a.can_decide && a.history.length > 0)}<button class="btn sm" type="button" disabled={approving} onclick={retryDecisionRefresh}>Retry check refresh</button>{/if}
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

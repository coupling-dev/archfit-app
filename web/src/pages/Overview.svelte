<script lang="ts">
  import FindingGroups from '../components/FindingGroups.svelte';
  import {
    ApiError,
    explain,
    explainLoad,
    postJSON,
    repoAPI,
    type Notice,
    type Overview,
    type RepoFindings,
    type Session,
    type ShareState,
  } from '../lib/api';
  import Skeleton from '../components/Skeleton.svelte';
  import { firstJSON } from '../lib/early';
  import { repoPage, safeHref } from '../lib/route';

  let {
    route,
    session,
    here,
  }: { route: { owner: string; name: string; fullName: string }; session: Session; here: string } = $props();

  let body = $state<Overview | null>(null);
  let failure = $state('');
  let notice = $state('');
  let busy = $state(false);
  let owners = $state('');

  function failed(e: ApiError) {
    failure = explain(e, here);
  }

  // The findings come from the latest report artifact, a slower read: the rest of the page never waits for it.
  let findings = $state<RepoFindings | null>(null);
  let findingsFailed = $state(false);

  function load() {
    firstJSON<Overview>(repoAPI(route.owner, route.name, 'overview')).then(
      (b) => (body = b),
      (e: ApiError) => (failure = explainLoad(e, here)),
    );
    firstJSON<RepoFindings>(repoAPI(route.owner, route.name, 'findings')).then(
      (f) => (findings = f),
      () => (findingsFailed = true),
    );
  }
  load();

  // write posts one action; a next step on another page (consent) is a navigation, anything else reloads the page.
  async function write(action: 'onboarding' | 'discover', payload: object) {
    busy = true;
    failure = notice = '';
    try {
      const res = await postJSON<Notice>(repoAPI(route.owner, route.name, action), session.csrf, payload);
      const next = safeHref(res.location);
      if (next) {
        location.assign(next);
        return;
      }
      notice = res.notice ?? '';
      load();
    } catch (e) {
      failed(e as ApiError);
    } finally {
      busy = false;
    }
  }

  function startOnboarding(ev: SubmitEvent) {
    ev.preventDefault();
    write('onboarding', { mode: 'customer', owners });
  }

  let copied = $state('');

  async function copyWorkflow() {
    if (!body?.workflow) return;
    try {
      await navigator.clipboard.writeText(body.workflow.content);
      copied = 'Copied. Paste it as a new file on GitHub.';
    } catch {
      copied = 'The browser did not allow copying. Open "Show the file" and select the text instead.';
    }
  }

  function checkAgain() {
    copied = notice = '';
    load();
  }

  const shareWords: Record<ShareState, string> = { allowed: 'allowed', not_allowed: 'not allowed', unknown: 'cannot tell' };
  // A pull request whose archfit/state has no report yet is waiting, not unhealthy: the server's words for "no check
  // yet" and "running". Every other state but healthy needs a look.
  const waitingStates = ['no state yet', 'in progress'];
  const pullsWaiting = $derived(body?.pulls?.filter((p) => waitingStates.includes(p.state)).length ?? 0);
  const pullsToSee = $derived(
    body?.pulls?.filter((p) => p.tone !== 'success' && !waitingStates.includes(p.state)).length ?? 0,
  );
  const actions = $derived(body !== null && body.admin && (body.can_start || body.can_discover));
  const needsYou = $derived(body !== null && (body.next !== '' || body.diagnostic !== '' || actions));
</script>

<div class="page-head">
  <div class="stack-sm">
    <h1>{route.fullName}</h1>
    {#if body?.main_state}
      <div class="hero">
        <span class="state {body.main_tone}">{body.main_state}</span>
        <span class="muted">on <code>{body.default_branch}</code></span>
        {#if body.main_reason}<span class="muted">{body.main_reason}</span>{/if}
      </div>
      <!-- Why "attention", in the engine's terms, when the latest report says so: never over another word. -->
      {#if body.main_tone === 'attention' && findings?.attention_reason}
        <p class="muted small" id="attention-reason">In the latest report: {findings.attention_reason}.</p>
      {/if}
    {/if}
  </div>
</div>

{#if notice}<p class="flash success" role="status">{notice}</p>{/if}
{#if failure}<p class="flash error" role="alert">{failure}</p>{/if}

{#if !body}
  {#if !failure}<Skeleton rows={4} />{/if}
{:else}
  {#if body.banner}<p class="flash warn">{body.banner}</p>{/if}

  {#if findings && !findings.unavailable}
    <div class="stats" role="group" aria-label="Counts on {findings.branch}">
      <div>
        <span class="n">{findings.violation_count}</span>
        <span class="t">Rule {findings.violation_count === 1 ? 'violation' : 'violations'}</span>
        <span class="s">on <code>{findings.branch}</code></span>
      </div>
      <div>
        <span class="n">{findings.advisory_count}</span>
        <span class="t">{findings.advisory_count === 1 ? 'Advisory' : 'Advisories'}</span>
        <span class="s">never block</span>
      </div>
      {#if !body.pulls_error && body.pulls}
        <div>
          <span class="n">{pullsToSee}</span>
          <span class="t">Open pull {pullsToSee === 1 ? 'request' : 'requests'} not healthy</span>
          <span class="s"
            >{body.pulls_more ? `among the ${body.pulls.length} listed` : `of ${body.pulls.length} open`}{pullsWaiting
              ? `; ${pullsWaiting} waiting for a report`
              : ''}</span
          >
        </div>
      {/if}
    </div>
  {/if}

  {#if needsYou}
    <section class="box" aria-labelledby="needs-you">
      <div class="box-head"><h2 id="needs-you">Needs you</h2></div>
      <div class="box-body stack">
        {#if body.next}
          <p><strong>Next:</strong> {body.next}</p>
          {#if body.next_how}
            <details class="disclosure">
              <summary>How</summary>
              <p class="details-body">{body.next_how}</p>
            </details>
          {/if}
        {/if}
        <!-- One sentence: the next step when there is one, else onboarding's own word for the stage. The workflow card
             says its step itself. -->
        {#if body.diagnostic && !body.next && !body.workflow}<p>{body.diagnostic}</p>{/if}
        {#if body.workflow}
          {@const wf = body.workflow}
          <div class="stack-sm" id="add-workflow">
            <p><strong>Add the Archfit workflow.</strong> One file, added once, by you.</p>
            <ol class="howto">
              <li>
                <a class="btn primary" href={safeHref(wf.add_url)} target="_blank" rel="noreferrer">
                  Add the file on GitHub
                </a>
                <span class="muted small">GitHub opens with <code>{wf.path}</code> filled in.</span>
              </li>
              {#if wf.branch !== body.default_branch}
                <li>
                  Press <em>Commit changes</em> and keep <em>Commit directly to the <code>{wf.branch}</code> branch</em>.
                  The file joins the onboarding pull request.
                </li>
              {:else}
                <li>
                  Press <em>Commit changes</em>. If <code>{wf.branch}</code> is protected, choose <em>Create a new branch</em>
                  and merge that pull request.
                </li>
              {/if}
              <li>Come back here and check again.</li>
            </ol>
            <div class="row">
              <button class="btn" type="button" onclick={checkAgain}>Check again</button>
              <button class="btn" type="button" onclick={copyWorkflow}>Copy the file</button>
            </div>
            {#if copied}<p class="small" role="status">{copied}</p>{/if}
            <details class="disclosure">
              <summary>Show the file</summary>
              <div class="details-body"><pre>{wf.content}</pre></div>
            </details>
            <details class="disclosure">
              <summary>Why Archfit does not add it itself</summary>
              <p class="details-body">
                Writing to <code>.github/workflows/</code> needs a GitHub permission that would let Archfit change every
                workflow in your repository, including the ones that hold your secrets. Archfit does not ask for it. The
                file runs the Archfit Action with <code>contents: read</code> and <code>id-token: write</code> only: no
                secrets, no repository scripts.
              </p>
            </details>
          </div>
        {/if}
        {#if body.draft_url}
          <p>
            Open pull request ({body.draft_state}):
            <a href={safeHref(body.draft_url)} rel="noreferrer">{body.draft_url}</a>
          </p>
        {/if}
        {#if body.admin && body.can_start}
          <form class="form" onsubmit={startOnboarding}>
            <label>
              Policy owners, one GitHub login or org/team-slug per line
              <textarea name="owners" rows="4" required bind:value={owners}></textarea>
            </label>
            <p class="muted small">
              Archfit opens a pull request with its settings. You then add one workflow file to it: this page shows how.
              Customer mode: source code never leaves your CI.
            </p>
            <div><button class="btn primary" type="submit" disabled={busy}>Open the onboarding pull request</button></div>
          </form>
        {/if}
        {#if body.admin && body.can_discover}
          <div>
            <button class="btn" type="button" disabled={busy} onclick={() => write('discover', {})}>
              Run discovery in your workflow
            </button>
          </div>
        {/if}
      </div>
    </section>
  {/if}

  <section class="box" aria-labelledby="setup">
    <div class="box-head">
      <h2 id="setup">Setup</h2>
      <span class="muted small">{body.stage}</span>
    </div>
    <div class="box-body">
      <ol class="track">
        {#each body.steps as s, i (s.label)}
          <li class={s.done ? 'done' : s.current ? 'current' : ''}>
            <span class="mark" aria-hidden="true">{s.done ? '✓' : i + 1}</span>
            {s.label}
            <span class="sr-only">{s.done ? '(done)' : s.current ? '(current step)' : '(to do)'}</span>
          </li>
        {/each}
      </ol>
    </div>
  </section>

  {#if body.main_state}
    <section class="box" aria-labelledby="pulls">
      <div class="box-head"><h2 id="pulls">Open pull requests</h2></div>
      {#if body.pulls_error}
        <p class="box-row muted">The open pull requests could not be read. Try again in a minute.</p>
      {:else if !body.pulls?.length}
        <p class="box-row muted">None.</p>
      {:else}
        {#each body.pulls as p (p.number)}
          <div class="box-row row-between">
            <a href={safeHref(p.href)}>#{p.number} {p.title}</a>
            <span class="row">
              <span class="status {p.tone}">{p.state}</span>
              {#if p.reason}<span class="muted small">{p.reason}</span>{/if}
            </span>
          </div>
        {/each}
      {/if}
      {#if body.pulls_more}
        <p class="box-foot">{body.pulls_more} more open pull requests are not listed here.</p>
      {/if}
    </section>
  {/if}

  {#if body.main_state}
    <section class="stack" aria-labelledby="branch-findings">
      <div class="row-between">
        <h2 id="branch-findings">Findings on <code>{body.default_branch}</code></h2>
        <a href={repoPage(route.fullName, '/graph')}>Open the graph</a>
      </div>
      {#if findingsFailed}
        <p class="muted">The latest report could not be read. Try again in a minute.</p>
      {:else if !findings}
        <Skeleton rows={3} />
      {:else if findings.unavailable}
        <p class="muted">{findings.unavailable}</p>
      {:else}
        {#if findings.no_baseline}
          <p class="muted small">Without a baseline every finding counts as new, including older debt.</p>
        {/if}
        <FindingGroups f={findings} compared={false} />
      {/if}
    </section>
  {/if}

  {#if body.admin && body.managed_ai}
    <section class="box" aria-labelledby="admin">
      <div class="box-head"><h2 id="admin">Administration</h2></div>
      <div class="box-row row">
        <a href={repoPage(route.fullName, '/data-sharing')}>Data sharing</a>
        <span class="muted small">Managed AI: {shareWords[body.managed_ai]}</span>
      </div>
    </section>
  {/if}

  <p class="muted">
    GitHub keeps the history:
    <a href={safeHref(body.pulls_url)} rel="noreferrer">Archfit pull requests</a> and
    <a href={safeHref(body.checks_url)} rel="noreferrer">check runs on the default branch</a>.
  </p>
{/if}

<script lang="ts">
  import FindingGroups from '../components/FindingGroups.svelte';
  import {
    ApiError,
    explain,
    getJSON,
    postJSON,
    repoAPI,
    type Notice,
    type Overview,
    type RepoFindings,
    type Session,
    type ShareState,
  } from '../lib/api';
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
    getJSON<Overview>(repoAPI(route.owner, route.name, 'overview')).then((b) => (body = b), failed);
    getJSON<RepoFindings>(repoAPI(route.owner, route.name, 'findings')).then(
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

  const shareWords: Record<ShareState, string> = { allowed: 'allowed', not_allowed: 'not allowed', unknown: 'cannot tell' };
  // An open pull request needs attention when its archfit/state is anything but healthy.
  const pullsToSee = $derived(body?.pulls?.filter((p) => p.tone !== 'success').length ?? 0);
  const actions = $derived(body !== null && body.admin && (body.can_start || body.can_discover));
  // The check of an advisory repository is neutral ("attention") whatever the engine found. The engine's blocked verdict
  // is never hidden behind it.
  const hero = $derived.by(() => {
    if (!body) return { state: '', tone: '' };
    if (body.main_tone === 'attention' && findings?.blocked) return { state: 'violations found', tone: 'danger' };
    return { state: body.main_state, tone: body.main_tone };
  });
  const needsYou = $derived(body !== null && (body.next !== '' || body.diagnostic !== '' || actions));
</script>

<div class="page-head">
  <div class="stack-sm">
    <h1>{route.fullName}</h1>
    {#if body?.main_state}
      <div class="hero">
        <span class="state {hero.tone}">{hero.state}</span>
        <span class="muted">on <code>{body.default_branch}</code></span>
        {#if body.main_reason}<span class="muted">{body.main_reason}</span>{/if}
      </div>
    {/if}
  </div>
</div>

{#if notice}<p class="flash success" role="status">{notice}</p>{/if}
{#if failure}<p class="flash error" role="alert">{failure}</p>{/if}

{#if !body}
  {#if !failure}<p class="muted" aria-live="polite">Loading…</p>{/if}
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
          <span class="t">Open pull {pullsToSee === 1 ? 'request needs' : 'requests need'} attention</span>
          <span class="s">{body.pulls_more ? `among the ${body.pulls.length} listed` : 'archfit/state not healthy'}</span>
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
        {#if body.diagnostic}<p>{body.diagnostic}</p>{/if}
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
            <p class="muted small">Customer mode: source code never leaves your CI.</p>
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
        <p class="muted" aria-live="polite">Loading the latest report…</p>
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

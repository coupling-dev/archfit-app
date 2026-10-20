<script lang="ts">
  import { ApiError, explain, getJSON, postJSON, repoAPI, type Notice, type Overview, type Session } from '../lib/api';

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

  function load() {
    getJSON<Overview>(repoAPI(route.owner, route.name, 'overview')).then((b) => (body = b), failed);
  }
  load();

  // write posts one action; a next step on another page (consent) is a navigation, anything else reloads the page.
  async function write(action: 'onboarding' | 'discover', payload: object) {
    busy = true;
    failure = notice = '';
    try {
      const res = await postJSON<Notice>(repoAPI(route.owner, route.name, action), session.csrf, payload);
      if (res.location) {
        location.assign(res.location);
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
    {/if}
  </div>
</div>

{#if notice}<p class="flash success" role="status">{notice}</p>{/if}
{#if failure}<p class="flash error" role="alert">{failure}</p>{/if}

{#if !body}
  {#if !failure}<p class="muted" aria-live="polite">Loading…</p>{/if}
{:else}
  {#if body.banner}<p class="flash warn">{body.banner}</p>{/if}

  {#if needsYou}
    <section class="box" aria-labelledby="needs-you">
      <div class="box-head"><h2 id="needs-you">Needs you</h2></div>
      <div class="box-body stack">
        {#if body.next}<p><strong>Next:</strong> {body.next}</p>{/if}
        {#if body.diagnostic}<p>{body.diagnostic}</p>{/if}
        {#if body.draft_url}
          <p>
            Open pull request ({body.draft_state}):
            <a href={body.draft_url} rel="noreferrer">{body.draft_url}</a>
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
            <a href={p.href}>#{p.number} {p.title}</a>
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

  <p class="muted">
    GitHub keeps the history:
    <a href={body.pulls_url} rel="noreferrer">Archfit pull requests</a> and
    <a href={body.checks_url} rel="noreferrer">check runs on the default branch</a>.
  </p>
{/if}

<script lang="ts">
  import Status from '../components/Status.svelte';
  import Skeleton from '../components/Skeleton.svelte';
  import { ApiError, explain, explainLoad, postJSON, repoAPI, type Session } from '../lib/api';
  import { firstJSON } from '../lib/early';
  import { ownerViewPath, outcomeText, readOwnerView, ownerFileVersion, ownerDiffLines, type OwnerOutcome, type OwnerRoute, type OwnerView } from '../lib/owner';
  import { canRetryRead, ownerUnavailable } from '../lib/recovery';
  import { confirm } from '../lib/passkey';
  import { repoPage } from '../lib/route';
  import { passkeyFailure } from '../lib/webauthn';

  let { route, session, here }: { route: OwnerRoute; session: Session; here: string } = $props();
  const api = (suffix: string) => repoAPI(route.owner, route.name, suffix);
  const github = $derived(`https://github.com/${encodeURIComponent(route.owner)}/${encodeURIComponent(route.name)}`);
  let view = $state<OwnerView | null>(null);
  let failure = $state('');
  let unavailable = $state('');
  let retry = $state(false);
  let recovery = $state(false);
  let actionError = $state('');
  let busy = $state(false);
  let stale = $state(false);
  let outcome = $state<OwnerOutcome | null>(null);

  function load() {
    firstJSON<unknown>(api(ownerViewPath(route))).then(
    (raw) => { view = readOwnerView(raw, route); },
    ).catch((e: unknown) => {
      retry = canRetryRead(e);
      recovery = !(e instanceof ApiError) || e.status === 409;
      unavailable = ownerUnavailable(e, route.action, route.pull);
      if (!unavailable) failure = e instanceof ApiError ? explainLoad(e, here) : e instanceof Error ? e.message : 'The owner action could not be read. Reload the page.';
    });
  }
  load();

  async function act() {
    if (!view || busy || stale || outcome) return;
    // Keep this displayed artifact through the whole ceremony; never fetch a fresh action digest here.
    const seen = view;
    busy = true;
    actionError = '';
    try {
      const token = await confirm(api, session.csrf, { action: route.action, digest: seen.digest });
      outcome = await postJSON<OwnerOutcome>(api(`owner/${route.action}`), session.csrf, {
        pull_request: seen.pull_request, digest: seen.action_digest, view_digest: seen.digest, token,
      });
    } catch (e) {
      stale = e instanceof ApiError && e.status === 409;
      actionError = passkeyFailure(e) ?? explain(e as ApiError, here);
    } finally {
      busy = false;
    }
  }
</script>

<div class="page-head">
  <div class="stack-sm page-identity">
    <h1>{view?.main_action ?? 'Owner action'}</h1>
    <p class="lede">{route.fullName}{route.pull ? ` · PR #${route.pull}` : ''}</p>
    {#if unavailable}<h2><Status>{unavailable}</Status></h2>
    {:else if failure}<p class="flash error" role="alert">{failure}</p>
    {:else if outcome}<p role="status"><Status>{outcomeText(route.action, outcome)}</Status></p>
    {:else if view}<Status>{view.kind === 'workflow' ? 'unknown — no verdict until this workflow change is approved.' : view.status}</Status>{/if}
    {#if view?.kind === 'workflow'}
      {#if !outcome}<p>This pull request changes the workflow that runs Archfit. The App cannot use its result without your approval.</p>{/if}
      <p>Approval does not mean the check passed.</p>
    {:else if view}<p>{view.note}</p>{/if}
    <div class="row">
      {#if failure || unavailable}
        {#if retry}<a href={here}>Try again</a>{/if}
        {#if recovery}
          <a href={repoPage(route.fullName)}>View current setup</a>
          <a href={repoPage(route.fullName, '/policy')}>View policy</a>
        {/if}
      {:else if view}
        {#if outcome}<a href={repoPage(route.fullName)}>View current setup</a>
        {:else if stale}<a href={here}>Reload the change</a>{/if}
        {#if view.pull_request}<a href={`${github}/pull/${view.pull_request}`}>Pull request #{view.pull_request} on GitHub</a>{/if}
        <a href={repoPage(route.fullName, '/policy')}>View policy</a>
      {/if}
    </div>
  </div>
</div>

{#if unavailable}
  <p>Open current setup or policy to see what to do next.</p>
{:else if !view && !failure}
  <Skeleton rows={5} />
{:else if view}

  {#each view.diffs as diff, i (diff.title)}
    <section class="box" aria-labelledby={`diff-${i}`}>
      <div class="stack-sm" class:box-head={!!diff.text} class:box-body={!diff.text}>
        <h2 id={`diff-${i}`}>{#if view.kind === 'workflow'}{diff.path === '.archfit.yaml' ? 'Policy' : 'Workflow'} — <code>{diff.path}</code>{:else}{diff.title}{/if}</h2>
        {#if view.kind === 'workflow'}
          <p class="small">
            {#if diff.path === '.archfit.yaml'}
              {diff.text ? 'Compared with' : 'Unchanged from'} this pull request's base at <code>{view.base.slice(0, 7)}</code>.
              These policy bytes are included in this approval.
            {:else}
              Compared with the default branch at <code>{view.tip.slice(0, 7)}</code>.
            {/if}
          </p>
        {/if}
        {#if !diff.text && (view.kind !== 'workflow' || diff.path !== '.archfit.yaml')}
          <p>No change to <code>{diff.path}</code>.</p>
        {/if}
      </div>
      {#if diff.text}
        <div class="box-body stack-sm">
          <p class="small diff-legend"><span class="addition">+ Added</span> <span class="deletion">- Removed</span></p>
          <!-- svelte-ignore a11y_no_noninteractive_tabindex (Keyboard users must be able to scroll the unwrapped diff.) -->
          <pre class="diff" role="region" tabindex="0" aria-label={`Diff: ${diff.title} (${diff.path})`}><code>{#each ownerDiffLines(diff.text) as line, lineIndex (lineIndex)}<span class={`diff-line ${line.kind}`}>{line.text}</span>{/each}</code></pre>
        </div>
      {/if}
    </section>
  {/each}

  <details class="box">
    <summary>Full files and approval details</summary>
    <div class="box-body stack-sm">
      <p>These are the file versions included in this approval. Use them to inspect unchanged lines or compare another copy. SHA-256 fingerprints identify the file bytes. You do not need to compare them to approve.</p>
      {#each view.proposals as proposal (proposal.id)}
        <section class="stack-sm">
          <h2>Approved answers in #{proposal.pull_request}</h2>
          <p class="small">Pull request at commit <code>{proposal.head}</code></p>
          <details>
            <summary>Technical details</summary>
            <p class="small">Proposal <code>{proposal.id}</code> · SHA-256 <code>{proposal.digest}</code></p>
            <ol>{#each proposal.approvals as approval (approval.id)}<li><code>{approval.id}</code>{approval.request ? ` · request #${approval.request}` : ''} · <code>{approval.digest}</code></li>{/each}</ol>
          </details>
        </section>
      {/each}
      {#each [...new Set(view.files.map((file) => file.path))] as path (path)}
        <section class="stack-sm">
          <h2>{path}</h2>
          {#each view.files.filter((file) => file.path === path) as file (file.role)}
            <section class="stack-sm">
              <h3>{ownerFileVersion(file.role)} {#if file.ref}— commit <code>{file.ref}</code>{/if}</h3>
              {#if file.missing}<p>This file is not present at this commit.</p>
              {:else}
                <!-- svelte-ignore a11y_no_noninteractive_tabindex (Keyboard users must be able to scroll the full file.) -->
                <pre role="region" tabindex="0" aria-label={`File: ${ownerFileVersion(file.role)} ${file.path}`}>{file.text}</pre>
              {/if}
              <details>
                <summary>Technical details</summary>
                <p class="small">SHA-256: <code>{file.digest}</code></p>
              </details>
            </section>
          {/each}
        </section>
      {/each}
    </div>
  </details>

  {#if outcome}
    {#if outcome.pull_request}
      <p><a href={`${github}/pull/${outcome.pull_request}`}>Open pull request #{outcome.pull_request}</a></p>
    {/if}
  {:else}
    <section class="box" aria-labelledby="passkey">
      <div class="box-head"><h2 id="passkey">Confirm with your passkey</h2></div>
      <form class="box-body stack-sm" onsubmit={(event) => { event.preventDefault(); void act(); }}>
        <input type="hidden" name="view_digest" value={view.digest} />
        <input type="hidden" name="digest" value={view.action_digest} />
        {#if view.kind === 'workflow'}
          <p>Approve the workflow and policy shown for PR #{view.pull_request} at commit <code>{view.head.slice(0, 7)}</code>.</p>
        {/if}
        <p>Your passkey confirms exactly the change shown above. Nothing is approved until this step finishes.</p>
        <details>
          <summary>Why a passkey?</summary>
          <div class="stack-sm">
            <p>GitHub sign-in identifies your account. An agent can also use that account. Your passkey adds a separate confirmation for these file versions. {view.kind === 'workflow' || view.pull_request ? 'Read both files before you confirm.' : 'Read the policy before you confirm.'} Approval does not mean the check passed.</p>
            <p>If your passkey is unavailable, use a second registered passkey.</p>
          </div>
        </details>
        {#if view.passkey}
          <p class="small"><a href={repoPage(route.fullName, '/passkeys')}>Manage your passkeys</a></p>
        {:else}
          <p class="small">No passkey yet? <a href={repoPage(route.fullName, '/passkeys?return=' + encodeURIComponent(here))}>Register a passkey</a> to return here after registration.</p>
        {/if}
        {#if actionError}<p class="flash error" role="alert">{actionError}</p>{/if}
        {#if stale}<p><a href={repoPage(route.fullName)}>View current setup</a></p>{/if}
        <div><button class="btn approval" type="submit" disabled={busy || stale}>{busy ? 'Waiting for your passkey and the App…' : view.main_action}</button></div>
      </form>
    </section>
  {/if}
{/if}

<style>
  pre { max-width: 100%; overflow: auto; white-space: pre; }
  code { overflow-wrap: anywhere; }
  a { text-decoration: underline; }
  summary { padding: 1rem; cursor: pointer; }
  section { min-width: 0; }
  h2, h3 { overflow-wrap: anywhere; }
  .box-head.stack-sm { align-items: stretch; }
  .box-body > h2 { font-size: 14px; }
  .diff code { display: block; width: max-content; min-width: 100%; }
  .diff-line { display: block; }
  .addition { background: var(--success-subtle); }
  .deletion { background: var(--danger-subtle); }
  .hunk, .header { color: var(--fg-muted); }
  .diff-legend { display: flex; gap: 1rem; }
  .diff-legend span { padding: 0 0.25rem; }
  .btn.approval { background: var(--fg); color: var(--bg); border-color: var(--fg); }
</style>

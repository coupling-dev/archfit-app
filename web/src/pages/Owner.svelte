<script lang="ts">
  import Skeleton from '../components/Skeleton.svelte';
  import { ApiError, explain, explainLoad, getJSON, postJSON, repoAPI, type Session } from '../lib/api';
  import { ownerViewPath, outcomeText, readOwnerView, type OwnerOutcome, type OwnerRoute, type OwnerView } from '../lib/owner';
  import { confirm } from '../lib/passkey';
  import { repoPage } from '../lib/route';
  import { passkeyFailure } from '../lib/webauthn';

  let { route, session, here }: { route: OwnerRoute; session: Session; here: string } = $props();
  const api = (suffix: string) => repoAPI(route.owner, route.name, suffix);
  const github = $derived(`https://github.com/${encodeURIComponent(route.owner)}/${encodeURIComponent(route.name)}`);
  let view = $state<OwnerView | null>(null);
  let failure = $state('');
  let actionError = $state('');
  let busy = $state(false);
  let stale = $state(false);
  let outcome = $state<OwnerOutcome | null>(null);

  function load() {
    getJSON<unknown>(api(ownerViewPath(route))).then(
    (raw) => { view = readOwnerView(raw, route); },
  ).catch((e: unknown) => { failure = e instanceof ApiError ? explainLoad(e, here) : e instanceof Error ? e.message : 'The owner action could not be read. Reload the page.'; });
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

{#if failure}
  <h1>Owner action</h1>
  <p class="flash error" role="alert">{failure}</p>
{:else if !view}
  <Skeleton rows={5} />
{:else}
  <div class="page-head">
    <div class="stack-sm">
      <p class="lede">{view.status}</p>
      <h1>{view.main_action}</h1>
      <p>{view.note}</p>
      {#if view.pull_request}<p><a href={`${github}/pull/${view.pull_request}`}>Pull request #{view.pull_request} on GitHub</a></p>{/if}
    </div>
  </div>

  {#each view.diffs as diff, i (diff.title)}
    <section class="box" aria-labelledby={`diff-${i}`}>
      <div class="box-head"><h2 id={`diff-${i}`}>{diff.title}</h2></div>
      <div class="box-body">
        {#if diff.text}<pre role="region" aria-label={`Diff: ${diff.title} (${diff.path})`}>{diff.text}</pre>
        {:else}<p>No change to <code>{diff.path}</code>.</p>{/if}
      </div>
    </section>
  {/each}

  <details class="box">
    <summary>Show the files and their SHA-256s</summary>
    <div class="box-body stack-sm">
      <p class="small">Head: <code>{view.head || 'none'}</code> · Base: <code>{view.base || 'none'}</code></p>
      <p class="small">Default branch: <code>{view.tip || 'none'}</code> · Approved: <code>{view.approved || 'none'}</code></p>
      {#each view.proposals as proposal (proposal.id)}
        <section class="stack-sm">
          <h2>Approved answers in #{proposal.pull_request}</h2>
          <p class="small">Proposal <code>{proposal.id}</code> · Head <code>{proposal.head}</code> · SHA-256 <code>{proposal.digest}</code></p>
          <ol>{#each proposal.approvals as approval (approval.id)}<li><code>{approval.id}</code>{approval.request ? ` · request #${approval.request}` : ''} · <code>{approval.digest}</code></li>{/each}</ol>
        </section>
      {/each}
      {#each view.files as file (file.role)}
        <section class="stack-sm">
          <h2>{file.role}: {file.path}</h2>
          <p class="small">Commit: <code>{file.ref || 'the proposed result'}</code></p>
          <p class="small">SHA-256: <code>{file.digest}</code></p>
          {#if file.missing}<p>This file is not present at this commit.</p>
          {:else}<pre role="region" aria-label={`File: ${file.role} ${file.path}`}>{file.text}</pre>{/if}
        </section>
      {/each}
    </div>
  </details>

  {#if outcome}
    <p class="flash" role="status">{outcomeText(route.action, outcome)}</p>
    {#if outcome.pull_request}
      <p><a href={`${github}/pull/${outcome.pull_request}`}>Open pull request #{outcome.pull_request}</a></p>
    {/if}
  {:else}
    <section class="box" aria-labelledby="passkey">
      <div class="box-head"><h2 id="passkey">Confirm with your passkey</h2></div>
      <form class="box-body stack-sm" onsubmit={(event) => { event.preventDefault(); void act(); }}>
        <input type="hidden" name="view_digest" value={view.digest} />
        <input type="hidden" name="digest" value={view.action_digest} />
        <p>Your passkey confirms exactly the change shown above. Nothing is approved until this step finishes.</p>
        <p class="small">No passkey yet? <a href={repoPage(route.fullName, '/passkeys')}>Register a passkey</a>, then reload this page.</p>
        {#if actionError}<p class="flash error" role="alert">{actionError}</p>{/if}
        {#if stale}<p><a href={here}>Reload the change</a></p>{/if}
        <div><button class="btn primary" type="submit" disabled={busy || stale}>{busy ? 'Waiting for your passkey and the App…' : view.main_action}</button></div>
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
</style>

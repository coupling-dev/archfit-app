<script lang="ts">
  import PasskeyToken from '../components/PasskeyToken.svelte';
  import Skeleton from '../components/Skeleton.svelte';
  import Banner from '../components/ui/Banner.svelte';
  import Diff from '../components/ui/Diff.svelte';
  import PageHead from '../components/ui/PageHead.svelte';
  import StatusLabel from '../components/ui/StatusLabel.svelte';
  import { ApiError, explain, explainLoad, postJSON, repoAPI, type Session } from '../lib/api';
  import { firstJSON } from '../lib/early';
  import { ownerViewPath, outcomeText, outcomeTone, ownerButton, ownerTitle, readOwnerView, ownerFileVersion, type OwnerOutcome, type OwnerRoute, type OwnerView } from '../lib/owner';
  import { canRetryRead, ownerStale, type OwnerStale } from '../lib/recovery';
  import { confirm } from '../lib/passkey';
  import { passkeyReturn } from '../lib/passkey-return';
  import { repoPage } from '../lib/route';
  import { passkeyFailure } from '../lib/webauthn';

  let { route, session, here }: { route: OwnerRoute; session: Session; here: string } = $props();
  const api = (suffix: string) => repoAPI(route.owner, route.name, suffix);
  const github = $derived(`https://github.com/${encodeURIComponent(route.owner)}/${encodeURIComponent(route.name)}`);
  let view = $state<OwnerView | null>(null);
  let failure = $state('');
  // The action is already done, or its pull request is merged: a normal state, so it reads as a notice, not an error.
  let nothingToDo = $state<OwnerStale | null>(null);
  let retry = $state(false);
  let recovery = $state(false);
  let actionError = $state('');
  let busy = $state(false);
  let moved = $state(false);
  let outcome = $state<OwnerOutcome | null>(null);
  // The request page that sent the owner here, when one did (a request waits on the first approval): linked back to.
  const back = $derived.by(() => {
    const to = passkeyReturn(route.fullName, new URLSearchParams(window.location.search).get('return'));
    return to && /\/requests\/\d+$/.test(to) ? to : undefined;
  });

  // "unknown — why": the status word gets the one status look, and the reason follows it.
  const status = $derived.by(() => {
    const text = view?.status ?? '';
    const at = text.indexOf(' — ');
    return at < 0 ? { word: text, why: '' } : { word: text.slice(0, at), why: text.slice(at + 3) };
  });
  const showStatus = $derived(!!view && !failure && !nothingToDo && !outcome);
  const sibling = $derived(
    route.action === 'confirm'
      ? { to: 'revert', text: 'Revert instead' }
      : route.action === 'revert'
        ? { to: 'confirm', text: 'Confirm instead' }
        : null,
  );

  function load() {
    firstJSON<unknown>(api(ownerViewPath(route))).then(
    (raw) => { view = readOwnerView(raw, route); },
    ).catch((e: unknown) => {
      retry = canRetryRead(e);
      recovery = !(e instanceof ApiError) || e.status === 409;
      nothingToDo = ownerStale(e, route.action, route.pull);
      if (!nothingToDo) failure = e instanceof ApiError ? explainLoad(e, here) : e instanceof Error ? e.message : 'The owner action could not be read. Reload the page.';
    });
  }
  load();

  async function act() {
    if (!view || busy || moved || outcome) return;
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
      moved = e instanceof ApiError && e.status === 409;
      actionError = passkeyFailure(e) ?? explain(e as ApiError, here);
    } finally {
      busy = false;
    }
  }
</script>

{#snippet statusLine()}
  <span class="status-line"><StatusLabel label={status.word} /> {#if status.why}<span>— {status.why}</span>{/if}</span>
{/snippet}

{#snippet next()}
  {#if nothingToDo}
    {#if nothingToDo.next === 'pull'}
      <a class="btn primary" href={repoPage(route.fullName, `/pull/${route.pull}`)}>Open pull request #{route.pull}</a>
    {:else}
      <a class="btn primary" href={repoPage(route.fullName)}>Open the repository</a>
    {/if}
  {/if}
{/snippet}

<PageHead title={ownerTitle(route.action)} repo={route.fullName} status={showStatus ? statusLine : undefined} actions={nothingToDo ? next : undefined}>
  {#if route.pull && !nothingToDo}<p class="pull-line">Pull request #{route.pull}</p>{/if}
  {#if back}<p class="small"><a class="inline" href={back}>Back to the request</a></p>{/if}
  {#if nothingToDo}
    <Banner tone="info" role="status">{nothingToDo.reason}</Banner>
  {:else if failure}<Banner tone="danger" role="alert">{failure}</Banner>
  {:else if view}
    {#if outcome}<Banner tone={outcomeTone(route.action, outcome)} role="status">{outcomeText(route.action, outcome)}</Banner>{/if}
    {#if view.kind === 'workflow'}
      {#if !outcome}<p>This pull request changes the workflow that runs Archfit. The App cannot use its result without your approval.</p>{/if}
      <p>Approval does not mean the check passed.</p>
    {:else}
      <p>{view.note} {#if sibling}<a href={repoPage(route.fullName, `/owner/${sibling.to}`)}>{sibling.text}</a>{/if}</p>
    {/if}
  {/if}
  {#if !nothingToDo}
    <div class="row">
      {#if failure}
        {#if retry}<a href={here}>Try again</a>{/if}
        {#if recovery}
          <a href={repoPage(route.fullName)}>View current setup</a>
          <a href={repoPage(route.fullName, '/policy')}>View policy</a>
        {/if}
      {:else if view}
        {#if outcome}<a href={repoPage(route.fullName)}>View current setup</a>
        {:else if moved}<a href={here}>Reload the change</a>{/if}
        {#if view.pull_request}<a href={`${github}/pull/${view.pull_request}`}>Pull request #{view.pull_request} on GitHub</a>{/if}
        <a href={repoPage(route.fullName, '/policy')}>View policy</a>
      {/if}
    </div>
  {/if}
</PageHead>

{#if nothingToDo || failure}
  <!-- the head already says everything -->
{:else if !view}
  <Skeleton rows={5} />
{:else}

  {#each view.diffs as diff, i (diff.title)}
    <section class="box" aria-labelledby={`diff-${i}`}>
      <div class="stack-sm" class:box-head={!!diff.text} class:box-body={!diff.text}>
        <h2 class="title-box" id={`diff-${i}`}>{#if view.kind === 'workflow'}{diff.path === '.archfit.yaml' ? 'Policy' : 'Workflow'} — <code>{diff.path}</code>{:else}{diff.title}{/if}</h2>
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
          <Diff text={diff.text} label={`Diff: ${diff.title} (${diff.path})`} />
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
          <h2 class="title-box">Approved answers in pull request #{proposal.pull_request}</h2>
          <p class="small">Pull request at commit <code>{proposal.head}</code></p>
          <details>
            <summary>Technical details</summary>
            <p class="small">Policy pull request #{proposal.pull_request} · <code>{proposal.id}</code> · SHA-256 <code>{proposal.digest}</code></p>
            <ol>{#each proposal.approvals as approval (approval.id)}<li><code>{approval.id}</code>{approval.request ? ` · request #${approval.request}` : ''} · <code>{approval.digest}</code></li>{/each}</ol>
          </details>
        </section>
      {/each}
      {#each [...new Set(view.files.map((file) => file.path))] as path (path)}
        <section class="stack-sm">
          <h2 class="title-box">{path}</h2>
          {#each view.files.filter((file) => file.path === path) as file (file.role)}
            <section class="stack-sm">
              <h3 class="title-box">{ownerFileVersion(file.role)} {#if file.ref}— commit <code>{file.ref}</code>{/if}</h3>
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
      <div class="box-head"><h2 class="title-box" id="passkey">Confirm with your passkey</h2></div>
      <form class="box-body stack-sm" onsubmit={(event) => { event.preventDefault(); void act(); }}>
        <input type="hidden" name="view_digest" value={view.digest} />
        <input type="hidden" name="digest" value={view.action_digest} />
        {#if view.kind === 'workflow'}
          <p>Approve the workflow and policy shown for pull request #{view.pull_request} at commit <code>{view.head.slice(0, 7)}</code>.</p>
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
          <PasskeyToken {api} {session} repo={route.fullName} returnTo={here} {here} />
        {/if}
        {#if actionError}<Banner tone="danger" role="alert">{actionError}</Banner>{/if}
        {#if moved}<p><a href={repoPage(route.fullName)}>View current setup</a></p>{/if}
        <div><button class="btn primary approval" type="submit" disabled={busy || moved}>{busy ? 'Waiting for your passkey and the App…' : ownerButton(route.action)}</button></div>
      </form>
    </section>
  {/if}
{/if}

<style>
  pre { max-width: 100%; overflow: auto; white-space: pre; }
  code { overflow-wrap: anywhere; }
  summary { padding: 16px; cursor: pointer; }
  section { min-width: 0; }
  h2, h3 { overflow-wrap: anywhere; }
  .box-head.stack-sm { align-items: stretch; }
  .status-line { display: block; }
  .pull-line { margin: 0; color: var(--fg-muted); font-size: 14px; }
</style>

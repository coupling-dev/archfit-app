<script lang="ts">
  import Skeleton from '../components/Skeleton.svelte';
  import PolicyRead from '../components/PolicyRead.svelte';
  import PageHead from '../components/ui/PageHead.svelte';
  import Banner from '../components/ui/Banner.svelte';
  import Diff from '../components/ui/Diff.svelte';
  import { explain, explainLoad, postJSON, repoAPI, type ApiError, type Policy, type Session, type Answered } from '../lib/api';
  import { canRetryRead, policyReadFailure } from '../lib/recovery';
  import { confirm } from '../lib/passkey';
  import { passkeyFailure } from '../lib/webauthn';
  import { firstJSON } from '../lib/early';
  import { isPolicyMissing, policyMissing } from '../lib/policy';
  import { repoPage, type Repo } from '../lib/route';

  let { route, here, session }: { route: Repo; here: string; session: Session } = $props();
  const editing = new URLSearchParams(window.location.search).get('edit') === '1';
  const api = (suffix: string) => repoAPI(route.owner, route.name, suffix);
  interface EditPreview { digest?: string; target?: string; diff: string; errors: string[] }
  let yaml = $state('');
  let preview = $state<EditPreview | null>(null);
  let editError = $state('');
  let busy = $state(false);
  let answered = $state<Answered | null>(null);
  let body = $state<Policy | null>(null);
  let failure = $state('');
  let retry = $state(false);
  let recovery = $state(false);
  let missing = $state(false);
  let showSource = $state(false);

  function load() {
    failure = ''; retry = recovery = missing = false;
    firstJSON<Policy>(api(editing ? 'policy?edit=1' : 'policy')).then(
      (b) => { body = b; yaml = b.edit.file ?? ''; },
      (e: ApiError) => {
        if (isPolicyMissing(e)) { missing = true; return; }
        retry = canRetryRead(e);
        recovery = e.status === 409;
        failure = explainLoad(e, here) ? policyReadFailure(e) : '';
      },
    );
  }
  load();

  async function reloadEditor() {
    busy = true; editError = '';
    try {
      const edit = await postJSON<Policy['edit']>(api('policy/edit/reload'), session.csrf, {});
      if (!body) return;
      body.edit = edit; yaml = edit.file ?? ''; preview = null; answered = null;
    } catch (e) { editError = explain(e as ApiError, here); }
    finally { busy = false; }
  }

  async function check() {
    if (!body?.edit.digest) { editError = 'the policy changed; reload'; return; }
    busy = true; editError = ''; preview = null;
    try {
      const p = await postJSON<EditPreview>(api('policy/edit/preview'), session.csrf,
        { answer: 'edit', yaml, base_digest: body.edit.digest });
      if (!Array.isArray(p.errors) || p.errors.some((e) => typeof e !== 'string') || typeof p.diff !== 'string' ||
          (p.errors.length === 0 && (!/^sha256:[0-9a-f]{64}$/.test(p.digest ?? '') || !/^[0-9a-f]{40}$/.test(p.target ?? '')))) {
        throw new Error('The edit preview could not be read.');
      }
      preview = p;
    } catch (e) { editError = explain(e as ApiError, here); }
    finally { busy = false; }
  }

  async function approve() {
    if (!body?.edit.digest || !preview?.digest || !preview.target || preview.errors.length) return;
    busy = true; editError = '';
    const sent = { answer: 'edit', yaml, base_digest: body.edit.digest, target: preview.target };
    try {
      const token = await confirm(api, session.csrf, { action: 'edit', digest: preview.digest,
        target: preview.target, base_digest: body.edit.digest, yaml: sent.yaml });
      answered = await postJSON<Answered>(api('policy/edit'), session.csrf, { ...sent, token });
      preview = null;
    } catch (e) { editError = passkeyFailure(e) ?? explain(e as ApiError, here); }
    finally { busy = false; }
  }
</script>

<PageHead title="Policy" repo={route.fullName}>
  {#snippet actions()}
    {#if body && !(editing && body.edit.available)}<a class="btn" href="#raw" onclick={() => showSource = true}>View source</a>{/if}
    {#if body?.edit.available && !editing}
      <a class="btn primary" href={repoPage(route.fullName, '/policy?edit=1')}>Edit</a>
    {/if}
  {/snippet}
</PageHead>

{#if missing}
  <section class="box" aria-labelledby="no-policy"><div class="blankslate">
    <h2 class="title-box" id="no-policy">{policyMissing}</h2>
    <a class="btn primary" href={repoPage(route.fullName)}>Continue setup</a>
  </div></section>
{:else if failure}
  <section class="box" aria-label="Policy unavailable"><div class="box-body stack-sm">
    <p class="flash error" role="alert">{failure}</p>
    {#if retry}<a class="btn" href={here}>Try again</a>{/if}
    {#if recovery}<a class="btn" href={repoPage(route.fullName)}>View current setup</a>{/if}
  </div></section>
{:else if !body}
  <Skeleton rows={5} />
{:else if editing && body.edit.available}
  <section class="box" aria-labelledby="edit-policy">
    <div class="box-head"><h2 class="title-box" id="edit-policy">Edit the policy</h2></div>
    <div class="box-body stack-sm">
      <p>Owners can edit here. Each edit needs your passkey and goes through the policy pull request.</p>
      <label for="policy-yaml">Policy YAML</label>
      <textarea class="form-control code" id="policy-yaml" rows="20" bind:value={yaml} disabled={busy || answered !== null || body.edit.recovery === true}
        aria-invalid={preview?.errors.length ? 'true' : undefined}
        oninput={() => { preview = null; editError = ''; }} spellcheck="false"></textarea>
      <p>preview it: <code>archfit check --policy &lt;file&gt;</code></p>
      {#if editError}<p class="flash error" role="alert">{editError}</p>{/if}
      {#if body.edit.recovery}
        <p>the policy changed; reload. Reload restores the recorded policy proposal before you edit again.</p>
        <button class="btn primary" disabled={busy} onclick={reloadEditor}>Reload the policy</button>
      {/if}
      {#if preview?.errors.length}
        <Banner tone="danger" role="alert" title="Fix the policy">
          <ul>{#each preview.errors as error (error)}<li>{error}</li>{/each}</ul>
        </Banner>
      {/if}
      {#if preview && preview.errors.length === 0 && !preview.diff}
        <p>Nothing to approve. Change the YAML first.</p>
      {:else if preview && preview.errors.length === 0}
        <h3>Diff against the current policy</h3>
        <Diff text={preview.diff} label="The policy change" />
        <p>Confirm this edit with your passkey.</p>
        <button class="btn primary" disabled={busy} onclick={approve}>{busy ? 'Confirming…' : 'Approve and merge with passkey'}</button>
      {:else if !answered && !body.edit.recovery}
        <button class="btn primary" disabled={busy} onclick={check}>{busy ? 'Checking…' : 'Preview the edit'}</button>
      {/if}
      {#if answered?.state === 'void'}<p class="flash error" role="alert">{answered.notice}</p>
      {:else if answered}<p class="flash success" role="status">{answered.notice}</p>{/if}
      <p><a href={repoPage(route.fullName, '/passkeys?return=' + encodeURIComponent(here))}>Your passkeys</a> · <a href={repoPage(route.fullName, '/policy')}>Back to policy</a></p>
    </div>
  </section>
{:else}
  {#if !body.edit.available}
    <section class="box" aria-labelledby="editor-unavailable"><div class="box-body stack-sm">
      <h2 class="title-box" id="editor-unavailable">Policy editor unavailable</h2>
      <p>{body.edit.reason}</p>
      <p>The policy below is read-only.</p>
      <a class="btn" href={repoPage(route.fullName)}>View current setup</a>
    </div></section>
  {/if}
  <PolicyRead policy={body} home={repoPage(route.fullName)} bind:showSource />
{/if}

<style>
  li { overflow-wrap: anywhere; }
  textarea[aria-invalid='true'] { border-color: var(--danger); }
</style>

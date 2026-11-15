<script lang="ts">
  import Skeleton from '../components/Skeleton.svelte';
  import { explain, explainLoad, postJSON, repoAPI, type ApiError, type Policy, type Session, type Answered } from '../lib/api';
  import { confirm } from '../lib/passkey';
  import { passkeyFailure } from '../lib/webauthn';
  import { firstJSON } from '../lib/early';
  import { repoPage, safeHref, type Repo } from '../lib/route';

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

  function load() {
    firstJSON<Policy>(api(editing ? 'policy?edit=1' : 'policy')).then(
      (b) => { body = b; yaml = b.edit.file ?? ''; },
      (e: ApiError) => (failure = explainLoad(e, here)),
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

<div class="page-head">
  <div><h1>Policy</h1><p class="muted">{route.fullName} · default branch</p></div>
  {#if body?.edit.available && !editing}
    <a class="btn primary" href={repoPage(route.fullName, '/policy?edit=1')}>Edit</a>
  {:else if body && !body.edit.available}
    <p class="muted">{body.edit.reason}</p>
  {/if}
</div>

{#if failure}
  <p class="flash error" role="alert">{failure}</p>
{:else if !body}
  <Skeleton rows={5} />
{:else if editing && body.edit.available}
  <section class="box" aria-labelledby="edit-policy">
    <div class="box-head"><h2 id="edit-policy">Edit the policy</h2></div>
    <div class="box-body stack-sm">
      <p>Every YAML edit needs your passkey and goes through the policy pull request.</p>
      <label for="policy-yaml">Policy YAML</label>
      <textarea id="policy-yaml" rows="20" bind:value={yaml} disabled={busy || answered !== null || body.edit.recovery === true}
        oninput={() => { preview = null; editError = ''; }} spellcheck="false"></textarea>
      <p>preview it: <code>archfit check --policy &lt;file&gt;</code></p>
      {#if editError}<p class="flash error" role="alert">{editError}</p>{/if}
      {#if body.edit.recovery}
        <p>the policy changed; reload. Reload restores the recorded policy proposal before you edit again.</p>
        <button class="btn primary" disabled={busy} onclick={reloadEditor}>Reload the policy</button>
      {/if}
      {#if preview?.errors.length}
        <div role="alert"><h3>Fix the policy</h3><ul>{#each preview.errors as error (error)}<li>{error}</li>{/each}</ul></div>
      {/if}
      {#if preview && preview.errors.length === 0}
        <h3>Diff against the current policy</h3>
        <pre aria-label="The policy change">{preview.diff || 'No text changed.'}</pre>
        <p>Confirm this edit with your passkey.</p>
        <button class="btn primary" disabled={busy} onclick={approve}>{busy ? 'Confirming…' : 'Approve and merge with passkey'}</button>
      {:else if !answered && !body.edit.recovery}
        <button class="btn primary" disabled={busy} onclick={check}>{busy ? 'Checking…' : 'Preview the edit'}</button>
      {/if}
      {#if answered?.state === 'void'}<p class="flash error" role="alert">{answered.notice}</p>
      {:else if answered}<p class="flash success" role="status">{answered.notice}</p>{/if}
      <p><a href={repoPage(route.fullName, '/passkeys')}>Your passkeys</a> · <a href={repoPage(route.fullName, '/policy')}>Back to policy</a></p>
    </div>
  </section>
{:else}
  <section class="box" aria-labelledby="policy-summary">
    <div class="box-head"><h2 id="policy-summary">Policy settings</h2></div>
    <div class="box-body">
      <dl class="kv">
        <dt>Check mode</dt><dd>{body.gate}</dd>
        <dt>Owners</dt><dd>{body.owners?.join(', ') || 'None'}</dd>
        <dt>Layers, inner first</dt><dd>{body.layers?.join(', ') || 'None'}</dd>
      </dl>
    </div>
  </section>

  <section class="box" aria-labelledby="modules">
    <div class="box-head"><h2 id="modules">Modules</h2></div>
    {#each body.modules as m (m.name)}
      <div class="box-row stack-sm">
        <h3>{m.name}</h3>
        <dl class="kv">
          <dt>Paths</dt><dd>{m.paths?.join(', ') || 'None'}</dd>
          <dt>Public</dt><dd>{m.public === null ? 'Whole module' : m.public.join(', ')}</dd>
          <dt>Volatility</dt><dd>{m.volatility}</dd>
          {#if m.subdomain}<dt>Subdomain</dt><dd>{m.subdomain}</dd>{/if}
          <dt>Layer</dt><dd>{m.layer || 'None'}</dd>
          <dt>Allowed dependencies</dt><dd>{m.depends_on.join(', ') || 'None'}</dd>
        </dl>
      </div>
    {:else}<p class="box-row muted">None.</p>{/each}
  </section>

  <section class="box" aria-labelledby="builtin">
    <div class="box-head"><h2 id="builtin">Built-in rules</h2></div>
    <div class="box-body">
      <dl class="kv">
        <dt>Graph</dt><dd>{body.builtin.graph}</dd>
        <dt>Coupling</dt><dd>{body.builtin.coupling}</dd>
        <dt>Duplication</dt><dd>{body.builtin.duplication}</dd>
        <dt>Cycle</dt><dd>{body.builtin.cycle}</dd>
        <dt>Layers</dt><dd>{body.builtin.layers}</dd>
        <dt>Unowned</dt><dd>{body.builtin.unowned}</dd>
        <dt>Minimum balance</dt><dd>{body.builtin.min_balance ?? 'None'}</dd>
      </dl>
    </div>
  </section>

  <section class="box" aria-labelledby="rules">
    <div class="box-head"><h2 id="rules">Custom rules and notes</h2></div>
    {#each body.rules as rule, i (i)}
      <div class="box-row stack-sm">
        <h3>{rule.id}{rule.not_checked ? ' (not checked)' : ''}</h3>
        {#if rule.deny}<p>Deny: {rule.deny.from.join(', ')} → {rule.deny.to.join(', ')}</p>{/if}
        {#if rule.only}<p>Only use {rule.only.use} in {rule.only.in.join(', ')}</p>{/if}
        {#if rule.note}<p>{rule.note}</p>{/if}
        {#if rule.why}<p><strong>Why:</strong> {rule.why}</p>{/if}
        {#if rule.instead}<p><strong>Instead:</strong> {rule.instead}</p>{/if}
      </div>
    {:else}<p class="box-row muted">None.</p>{/each}
  </section>

  <section class="box" aria-labelledby="exceptions">
    <div class="box-head"><h2 id="exceptions">Exceptions</h2></div>
    {#each body.exceptions as exception, i (i)}
      <div class="box-row stack-sm">
        <h3>{exception.rule}: {exception.from}{exception.to ? ` → ${exception.to}` : ''}</h3>
        {#if exception.expires}<p>Expires: <time datetime={exception.expires}>{exception.expires}</time>{#if exception.days_left != null}<span>{exception.days_left < 0 ? ` (expired ${-exception.days_left} ${exception.days_left === -1 ? 'day' : 'days'} ago)` : ` (${exception.days_left} ${exception.days_left === 1 ? 'day' : 'days'} left)`}</span>{/if}</p>{/if}
        {#if exception.why}<p>{exception.why}</p>{/if}
        {#if exception.request && safeHref(exception.request_href)}<p><a href={safeHref(exception.request_href)}>Request #{exception.request}</a></p>{/if}
      </div>
    {:else}<p class="box-row muted">None.</p>{/each}
  </section>

  <section class="box" aria-labelledby="raw">
    <div class="box-head"><h2 id="raw">Policy file</h2></div>
    <div class="box-body stack-sm">
      <p class="small analysis">Digest: <code>{body.digest}</code></p>
      <details class="disclosure"><summary>Show raw YAML</summary><pre>{body.yaml}</pre></details>
    </div>
  </section>
{/if}

<style>
  .box-row { overflow-wrap: anywhere; }
  textarea { width: 100%; max-width: 100%; font-family: var(--mono, monospace); }
  pre, li { white-space: pre-wrap; overflow-wrap: anywhere; }
</style>

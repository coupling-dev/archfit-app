<script lang="ts">
  import Skeleton from '../components/Skeleton.svelte';
  import {
    explain,
    explainLoad,
    getJSON,
    postJSON,
    repoAPI,
    type AnswerKind,
    type Answered,
    type ApiError,
    type Preview,
    type RequestView,
    type Session,
  } from '../lib/api';
  import { firstJSON } from '../lib/early';
  import { confirm } from '../lib/passkey';
  import {
    answerProblem,
    balanceText,
    dependency,
    earlierLine,
    loosens,
    moduleLine,
    previewPath,
    pullLine,
    statusOf,
  } from '../lib/request';
  import { repoPage, type Repo } from '../lib/route';
  import { passkeyFailure } from '../lib/webauthn';

  // One request, one decision (design 12.3): the reason, the record with its balance, the two modules, the preview of
  // each answer, earlier answers, then the answers. An answer that loosens the policy is confirmed with a passkey.
  let { route, session, here }: { route: Repo & { number: number }; session: Session; here: string } = $props();

  const api = (suffix: string) => repoAPI(route.owner, route.name, suffix);
  const github = $derived(`https://github.com/${encodeURIComponent(route.owner)}/${encodeURIComponent(route.name)}`);
  const kinds: { kind: AnswerKind; title: string }[] = [
    { kind: 'approve', title: 'Approve' },
    { kind: 'approve-until', title: 'Approve until a date' },
    { kind: 'reject', title: 'Reject' },
  ];

  let body = $state<RequestView | null>(null);
  let failure = $state('');
  let until = $state('');
  let instead = $state('');
  let mergeLater = $state(false);
  let busy = $state(false);
  let answered = $state<Answered | null>(null);
  let answerError = $state('');

  interface Shown {
    preview?: Preview;
    note?: string;
  }
  let previews = $state<Record<AnswerKind, Shown>>({ approve: {}, 'approve-until': {}, reject: {} });
  // The newest preview read of each answer wins: an older answer that arrives late is dropped.
  const reads: Record<AnswerKind, number> = { approve: 0, 'approve-until': 0, reject: 0 };

  function load(first = false) {
    const path = api(`requests/${route.number}`);
    (first ? firstJSON<RequestView>(path) : getJSON<RequestView>(path)).then(
      (b) => {
        body = b;
        if (b.state === 'open' && b.policy) for (const { kind } of kinds) void preview(kind);
      },
      (e: ApiError) => (failure = explainLoad(e, here)),
    );
  }
  load(true);

  async function preview(kind: AnswerKind) {
    const n = ++reads[kind];
    if (kind === 'reject' && !instead.trim()) {
      previews[kind] = { note: 'Write what to do instead to see what this answer writes.' };
      return;
    }
    // Without a date of the owner's, the preview takes the date the request asked for.
    const date = answerProblem('approve-until', until, '') ? '' : until;
    previews[kind] = {};
    try {
      const p = await getJSON<Preview>(api(previewPath(route.number, kind, date, instead.trim())));
      if (n === reads[kind]) previews[kind] = { preview: p };
    } catch (e) {
      if (n === reads[kind]) previews[kind] = { note: explain(e as ApiError, here) };
    }
  }

  async function answer(kind: AnswerKind) {
    if (!body || answerProblem(kind, until, instead)) return;
    busy = true;
    answered = null;
    answerError = '';
    const sent = {
      answer: kind,
      ...(kind === 'approve-until' ? { until } : {}),
      ...(kind === 'reject' ? { instead: instead.trim() } : {}),
      ...(mergeLater ? { merge_later: true } : {}),
    };
    try {
      let token = '';
      if (loosens(kind)) {
        const { digest } = await postJSON<{ digest: string }>(api(`requests/${route.number}/answer/digest`), session.csrf, sent);
        token = await confirm(api, session.csrf, { action: kind, request: route.number, digest });
      }
      answered = await postJSON<Answered>(api(`requests/${route.number}/answer`), session.csrf, token ? { ...sent, token } : sent);
      load();
    } catch (e) {
      answerError = passkeyFailure(e) ?? explain(e as ApiError, here);
    } finally {
      busy = false;
    }
  }
</script>

{#if failure}
  <p class="flash error" role="alert">{failure}</p>
{:else if !body}
  <Skeleton rows={2} box={false} />
  <Skeleton rows={5} />
{:else}
  {@const status = statusOf(body)}
  {@const balance = balanceText(body)}
  <div class="page-head">
    <div class="stack-sm">
      <h1>Request #{body.number}</h1>
      <p class="lede"><span class="status {status.tone}">{status.word}</span> {status.line}</p>
    </div>
  </div>

  <section class="box" aria-labelledby="reason">
    <div class="box-head"><h2 id="reason">Reason</h2></div>
    <div class="box-body stack-sm">
      <p>{body.reason}</p>
      <p class="small">
        Rule <code>{body.rule}</code> · <code>{dependency(body)}</code>{body.expires ? ` · asked until ${body.expires}` : ''}
      </p>
      <p class="small"><a href={`${github}/issues/${body.number}`}>Request #{body.number} on GitHub</a></p>
    </div>
  </section>

  <section class="box" aria-labelledby="record">
    <div class="box-head"><h2 id="record">Record</h2></div>
    <div class="box-body stack-sm">
      {#if balance}<p>Balance: <strong>{balance}</strong></p>{/if}
      <pre>{body.record}</pre>
    </div>
  </section>

  {#if body.modules.length}
    <section class="box" aria-labelledby="modules">
      <div class="box-head"><h2 id="modules">Modules</h2></div>
      <div class="box-body grid-2">
        {#each body.modules as m (m.name)}
          <div class="stack-sm">
            <h3><code>{m.name}</code></h3>
            <p class="small">{moduleLine(m)}</p>
            <p class="small">
              Allowed dependencies: {#if m.depends_on.length}{#each m.depends_on as d, i (d)}{i ? ', ' : ''}<code>{d}</code
                  >{/each}{:else}none{/if}
            </p>
          </div>
        {/each}
      </div>
    </section>
  {/if}

  {#if body.state === 'open' && body.policy}
    <section class="box" aria-labelledby="preview">
      <div class="box-head"><h2 id="preview">Preview</h2></div>
      {#each kinds as { kind, title } (kind)}
        {@const shown = previews[kind]}
        <div class="box-row stack-sm">
          <h3>{title}</h3>
          {#if shown.note}
            <p class="muted small">{shown.note}</p>
          {:else if !shown.preview}
            <Skeleton rows={2} box={false} />
          {:else}
            {@const p = shown.preview}
            <p class="small">
              {p.loosens ? 'Loosens the policy: it needs your passkey.' : 'Tightens the policy: no passkey needed.'}
              {#if p.until}Exception until {p.until}.{/if}
            </p>
            {#if p.diff}<pre aria-label={`${title}: the change to .archfit.yaml`}>{p.diff}</pre>
            {:else}<p class="small">It writes no line in <code>.archfit.yaml</code>.</p>{/if}
            {#if p.pulls.length}
              <ul class="plain small" aria-label={`${title}: open pull requests`}>
                {#each p.pulls as e (e.number)}<li>{pullLine(e)}</li>{/each}
              </ul>
            {:else}
              <p class="muted small">No open pull request changes with this answer.</p>
            {/if}
          {/if}
        </div>
      {/each}
    </section>
  {/if}

  <section class="box" aria-labelledby="earlier">
    <div class="box-head"><h2 id="earlier">Earlier answers <span class="counter">{body.earlier.length}</span></h2></div>
    {#if body.earlier.length}
      <ul class="box-body plain stack-sm">
        {#each body.earlier as e, i (i)}
          <li>{earlierLine(e)}{#if e.why}<span class="muted">{` Why: ${e.why}`}</span>{/if}</li>
        {/each}
      </ul>
    {:else}
      <p class="box-row muted">No earlier answer about these modules.</p>
    {/if}
  </section>

  <section class="box" aria-labelledby="answer">
    <div class="box-head"><h2 id="answer">Answer</h2></div>
    <div class="box-body stack">
      {#if body.state === 'open' && body.policy}
        <p class="small">
          Approve and approve until a date loosen the policy: your passkey confirms them.
          <a class="inline" href={repoPage(route.fullName, '/passkeys')}>Your passkeys</a>
        </p>
        <label class="small"><input type="checkbox" bind:checked={mergeLater} disabled={busy} /> Merge later: keep the policy
          pull request open, so the next answers join it</label>
        <div class="stack-sm">
          <button class="btn primary" type="button" disabled={busy} onclick={() => answer('approve')}>Approve and merge</button>
        </div>
        <div class="stack-sm">
          <label for="until">The exception ends on</label>
          <input id="until" type="date" bind:value={until} disabled={busy} onchange={() => preview('approve-until')} />
          <div>
            <button
              class="btn"
              type="button"
              disabled={busy || !!answerProblem('approve-until', until, '')}
              onclick={() => answer('approve-until')}>Approve until a date</button
            >
          </div>
        </div>
        <div class="stack-sm">
          <label for="instead">What to do instead</label>
          <textarea id="instead" rows="3" maxlength="500" bind:value={instead} disabled={busy} onchange={() => preview('reject')}
          ></textarea>
          <div>
            <button class="btn" type="button" disabled={busy || !instead.trim()} onclick={() => answer('reject')}>Reject</button>
          </div>
        </div>
      {:else if body.state === 'open'}
        <p>Add <code>.archfit.yaml</code> of version 3 to the default branch, then answer.</p>
      {:else}
        <p>No answer is open. The request on GitHub names the owner who answered and the passkey used.</p>
      {/if}
      {#if busy}<p class="small" role="status">Answering…</p>{/if}
      {#if answered}
        <p class="flash success" role="status">
          {answered.notice}
          {#if answered.pull_request}<a class="inline" href={`${github}/pull/${answered.pull_request}`}>Policy pull request #{answered.pull_request}</a
            >{/if}
        </p>
      {/if}
      {#if answerError}<p class="flash error" role="alert">{answerError}</p>{/if}
    </div>
  </section>
{/if}

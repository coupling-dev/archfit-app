<script lang="ts">
  import Skeleton from '../components/Skeleton.svelte';
  import Map from '../components/map/Map.svelte';
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
    decisionState,
    earlierLine,
    loosens,
    moduleLine,
    requestMap,
    previewPath,
    pullLine,
    statusOf,
  } from '../lib/request';
  import { repoPage, type Repo } from '../lib/route';
  import { passkeyFailure } from '../lib/webauthn';

  // Preview reads never answer a request. Only the adjacent confirmation starts the existing answer ceremony.
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
  let selected = $state<AnswerKind>('approve');
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
        if (b.state === 'open' && b.policy) void preview(selected);
      },
      (e: ApiError) => (failure = explainLoad(e, here)),
    );
  }
  load(true);

  function invalidate(kind: AnswerKind) {
    ++reads[kind];
    previews[kind] = {};
  }

  async function preview(kind: AnswerKind) {
    const n = ++reads[kind];
    const problem = answerProblem(kind, until, instead);
    if (problem) {
      previews[kind] = { note: problem };
      return;
    }
    previews[kind] = {};
    try {
      const p = await getJSON<Preview>(api(previewPath(route.number, kind, until, instead.trim())));
      if (n === reads[kind]) previews[kind] = { preview: p };
    } catch (e) {
      if (n === reads[kind]) previews[kind] = { note: explain(e as ApiError, here) };
    }
  }

  async function answer(kind: AnswerKind) {
    const decision = decisionState(kind, previews[kind].preview, mergeLater);
    if (busy || !body || body.state !== 'open' || !decision.ready || answerProblem(kind, until, instead)) return;
    busy = true;
    answered = null;
    answerError = '';
    const sent = {
      answer: kind,
      ...(kind === 'approve-until' ? { until } : {}),
      ...(kind === 'reject' ? { instead: instead.trim() } : {}),
      ...(decision.mergeLater ? { merge_later: true } : {}),
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
    <div class="box-head"><h2 class="title-box" id="reason">Reason</h2></div>
    <div class="box-body stack-sm">
      <p>{body.reason}</p>
      <p class="small">
        Rule <code>{body.rule}</code> · <code>{dependency(body)}</code>{body.expires ? ` · asked until ${body.expires}` : ''}
      </p>
      {#if body.issue_href === `${github}/issues/${body.number}`}<p class="small"><a href={body.issue_href}>Request #{body.number} on GitHub</a></p>{/if}
    </div>
  </section>

  <section class="box" aria-labelledby="answer">
    <div class="box-head"><h2 class="title-box" id="answer">Answer</h2></div>
    <div class="box-body stack">
      {#if body.state === 'open' && body.policy}
        <p class="small">Choose an answer and read its policy change before confirming. Previewing or leaving this page changes nothing.</p>
        <fieldset class="stack-sm">
          <legend>Choose an answer</legend>
          {#each kinds as { kind, title } (kind)}
            <label class="form-choice"><input type="radio" name="answer-kind" value={kind} bind:group={selected} disabled={busy} /> {title}</label>
          {/each}
        </fieldset>
        {@const title = kinds.find((item) => item.kind === selected)?.title ?? ''}
        {@const shown = previews[selected]}
        {@const decision = decisionState(selected, shown.preview, mergeLater)}
        <section class="stack-sm" aria-label={title}>
          <h3>{title}</h3>
          {#if selected === 'approve-until'}
            <label for="until">The exception ends on</label>
            <input class="form-control" id="until" type="date" bind:value={until} disabled={busy} oninput={() => invalidate('approve-until')} />
            <p class="small">Choose an expiry date to preview. The exception is valid through that date, in UTC.</p>
          {:else if selected === 'reject'}
            <label for="instead">What to do instead</label>
            <textarea class="form-control" id="instead" rows="3" maxlength="500" bind:value={instead} disabled={busy} oninput={() => invalidate('reject')}></textarea>
          {/if}
          {#if !decision.ready}
            <p class="small">{answerProblem(selected, until, instead) || 'Preview this answer to see its policy change and affected pull requests.'}</p>
            <div><button class="btn" type="button" disabled={busy || !!answerProblem(selected, until, instead)} onclick={() => preview(selected)}>Preview {title.toLowerCase()}</button></div>
          {/if}
          {#if shown.note}<p class="flash error" role="alert">{shown.note}</p>{/if}
          {#if shown.preview && decision.ready}
            {@const p = shown.preview}
            <div class="stack-sm" aria-label={`${title}: preview`}>
              <h4 class="title-box">Policy change</h4>
              <p class="small">
                {p.loosens ? 'Loosens the policy: it needs your passkey.' : 'Tightens the policy: no passkey needed.'}
                {#if p.until}Exception until {p.until}, inclusive (UTC).{/if}
              </p>
              {#if p.diff && selected === 'reject'}<p class="small">Adds the deny rule shown below, with what to do instead.</p>{/if}
              {#if p.diff}
                <!-- svelte-ignore a11y_no_noninteractive_tabindex (Keyboard users must be able to scroll the unwrapped diff.) -->
                <pre tabindex="0" role="region" aria-label={`${title}: the change to .archfit.yaml`}>{p.diff}</pre>
              {:else}<p class="small">It writes no line in <code>.archfit.yaml</code>.</p>{/if}
              <h4 class="title-box">Affected pull requests</h4>
              {#if p.pulls.length}
                <ul class="plain small stack-sm" aria-label={`${title}: open pull requests`}>
                  {#each p.pulls as e (e.number)}
                    <li>
                      <p>{pullLine(e)}</p>
                      <a href={repoPage(route.fullName, `/pull/${e.number}`)}>PR #{e.number} in Archfit</a>
                      · <a href={`${github}/pull/${e.number}`}>PR #{e.number} on GitHub</a>
                    </li>
                  {/each}
                </ul>
                <p class="small">These code pull requests are not merged by this answer. They still need a fresh check result.</p>
              {:else}<p class="muted small">No open pull request changes with this answer.</p>{/if}
            </div>
            {#if decision.changesPolicy}
              <label class="small form-choice"><input type="checkbox" bind:checked={mergeLater} disabled={busy} /> Keep the policy pull request open</label>
            {/if}
            <p class="small">{decision.effect}</p>
            {#if loosens(selected)}
              <p class="small">Confirm with your passkey. <a class="inline" href={repoPage(route.fullName, '/passkeys?return=' + encodeURIComponent(here))}>Your passkeys</a></p>
            {/if}
            <div><button class="btn primary" type="button" disabled={busy} onclick={() => answer(selected)}>{decision.confirm}</button></div>
          {/if}
        </section>
      {:else if body.state === 'open'}
        <p>Add <code>.archfit.yaml</code> of version 3 to the default branch, then answer.</p>
      {:else}
        {#if body.answer}
          {#if body.answer.kind === 'approve'}<p>Approved.</p>
          {:else if body.answer.kind === 'approve-until'}<p>Approved until {body.answer.until}.</p>
          {:else}<p>Rejected. What to do instead: {body.answer.instead}</p>{/if}
        {/if}
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

  <section class="box" aria-labelledby="record">
    <div class="box-head"><h2 class="title-box" id="record">Record</h2></div>
    <div class="box-body stack-sm">
      {#if balance}<p>Balance: <strong>{balance}</strong></p>{/if}
      <!-- svelte-ignore a11y_no_noninteractive_tabindex (Keyboard users must be able to scroll the original record.) -->
      <pre tabindex="0" role="region" aria-label="Original request record">{body.record}</pre>
    </div>
  </section>

  {#if body.modules.length}
    <section class="box" aria-labelledby="modules">
      <div class="box-head"><h2 class="title-box" id="modules">The map</h2></div>
      <div class="box-body"><Map model={requestMap(body)} repo={route.fullName} /></div>
    </section>
    <section class="box" aria-labelledby="balance-why">
      <div class="box-head"><h2 class="title-box" id="balance-why">Why the balance</h2></div>
      <div class="box-body stack-sm">
        {#if balance}<p>Balance: <strong>{balance}</strong></p>{:else}<p>The record has no balance.</p>{/if}
        <p>The policy declares the volatility of each module:</p>
        {#each body.modules as m (m.name)}<p><code>{m.name}</code>: {moduleLine(m)}</p>{/each}
      </div>
    </section>
  {/if}

  <section class="box" aria-labelledby="earlier">
    <div class="box-head"><h2 class="title-box" id="earlier">Earlier answers <span class="counter">{body.earlier.length}</span></h2></div>
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

{/if}

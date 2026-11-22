<script lang="ts">
  import Skeleton from '../components/Skeleton.svelte';
  import Map from '../components/map/Map.svelte';
  import Banner from '../components/ui/Banner.svelte';
  import Diff from '../components/ui/Diff.svelte';
  import Disclosure from '../components/ui/Disclosure.svelte';
  import StoryRow from '../components/ui/StoryRow.svelte';
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
    answerKinds,
    answerProblem,
    balanceText,
    changedWhileOpen,
    decisionState,
    defaultUntil,
    dependency,
    earlierLine,
    effectLead,
    ledeLine,
    effectLines,
    loosens,
    moduleLine,
    previewPath,
    pullLine,
    question,
    requestMap,
    statusOf,
    storySteps,
  } from '../lib/request';
  import { repoPage, type Repo } from '../lib/route';
  import { passkeyFailure } from '../lib/webauthn';

  // The answer page (task 8.19, story spec 8.18): the question, the evidence, and each answer with its effect before
  // the click. Preview reads never answer a request; only the confirm button starts the answer ceremony.
  let { route, session, here }: { route: Repo & { number: number }; session: Session; here: string } = $props();

  // The page reads the request again on this interval while it is open, so it never asks the owner to reload.
  const refreshMs = 20_000;

  const api = (suffix: string) => repoAPI(route.owner, route.name, suffix);
  const github = $derived(`https://github.com/${encodeURIComponent(route.owner)}/${encodeURIComponent(route.name)}`);
  let body = $state<RequestView | null>(null);
  const kinds = $derived(answerKinds(body));
  let failure = $state('');
  let until = $state(defaultUntil(new Date()));
  let instead = $state('');
  let selected = $state<AnswerKind | null>(null);
  let busy = $state(false);
  let answered = $state<Answered | null>(null);
  let answerError = $state('');
  let changed = $state('');

  interface Shown {
    preview?: Preview;
    note?: string;
  }
  let previews = $state<Record<AnswerKind, Shown>>({ approve: {}, 'approve-until': {}, reject: {}, 'declare-public': {}, 'declare-model': {} });
  // The newest preview read of each answer wins: an older answer that arrives late is dropped.
  const reads: Record<AnswerKind, number> = { approve: 0, 'approve-until': 0, reject: 0, 'declare-public': 0, 'declare-model': 0 };
  let decision = $derived(selected ? decisionState(selected, previews[selected].preview) : null);

  // own is true when the owner's answer caused the read: the page then shows no "answered while you looked" for it.
  // loads numbers the request reads: only the newest one may change the page, so a refresh that started before an
  // answer cannot bring back the open state or report the owner's own answer as a change.
  let loads = 0;

  function load(first = false, own = false) {
    const n = ++loads;
    const path = api(`requests/${route.number}`);
    (first ? firstJSON<RequestView>(path) : getJSON<RequestView>(path)).then(
      (b) => {
        if (n !== loads) return;
        const before = body;
        body = b;
        if (b.state === 'open' && b.policy) void previewAll(before, n);
        else if (before && !own) changed = changedWhileOpen(before, b, false);
      },
      (e: ApiError) => {
        if (!body) failure = explainLoad(e, here);
      },
    );
  }
  load(true);

  $effect(() => {
    const timer = setInterval(() => {
      if (!busy && body?.state === 'open' && document.visibilityState === 'visible') load();
    }, refreshMs);
    return () => clearInterval(timer);
  });

  // previewAll reads the effect of every answer that can be previewed now. When the policy moved since the last read,
  // the page says so above the answers.
  async function previewAll(before: RequestView | null, n: number) {
    const old = Object.values(previews).find((s) => s.preview)?.preview?.policy_digest;
    await Promise.all(kinds.map(({ kind }) => preview(kind, true)));
    const now = Object.values(previews).find((s) => s.preview)?.preview?.policy_digest;
    if (n === loads && !busy && before && body) changed = changedWhileOpen(before, body, !!old && !!now && old !== now);
  }

  // preview reads the effect of one answer. An edit of its date or text drops the old effect at once, so it cannot be
  // confirmed while the new one is read; a refresh with the same inputs keeps it on the page until the new one lands.
  async function preview(kind: AnswerKind, refresh = false) {
    const n = ++reads[kind];
    if (!refresh || answerProblem(kind, until, instead)) previews[kind] = {};
    if (answerProblem(kind, until, instead)) return;
    try {
      const p = await getJSON<Preview>(api(previewPath(route.number, kind, until, instead.trim())));
      if (n === reads[kind]) previews[kind] = { preview: p };
    } catch (e) {
      if (n === reads[kind]) previews[kind] = { note: explain(e as ApiError, here) };
    }
  }

  async function answer(kind: AnswerKind) {
    const d = decisionState(kind, previews[kind].preview);
    if (busy || !body || body.state !== 'open' || !d.ready || answerProblem(kind, until, instead)) return;
    busy = true;
    ++loads;
    answered = null;
    answerError = '';
    const sent = {
      answer: kind,
      ...(kind === 'approve-until' ? { until } : {}),
      ...(kind === 'reject' ? { instead: instead.trim() } : {}),
    };
    try {
      let token = '';
      if (loosens(kind)) {
        const { digest } = await postJSON<{ digest: string }>(api(`requests/${route.number}/answer/digest`), session.csrf, sent);
        token = await confirm(api, session.csrf, { action: kind, request: route.number, digest });
      }
      answered = await postJSON<Answered>(api(`requests/${route.number}/answer`), session.csrf, token ? { ...sent, token } : sent);
      changed = '';
      load(false, true);
    } catch (e) {
      answerError = passkeyFailure(e) ?? explain(e as ApiError, here);
      // An answer refused because the request or the policy moved is shown with the new state, not as a dead end.
      load();
    } finally {
      busy = false;
    }
  }

  function answeredLine(r: RequestView): string {
    const a = r.answer;
    if (!a)
      return r.state === 'closed'
        ? 'No answer is open. The request on GitHub names the owner who answered.'
        : 'Answered. The request on GitHub names the owner who answered and the passkey.';
    const owner = ' The request on GitHub names the owner and the passkey.';
    if (a.kind === 'approve') return 'Allowed.' + owner;
    if (a.kind === 'approve-until') return `Allowed until ${a.until}.` + owner;
    if (a.kind === 'declare-public') return 'Made public.' + owner;
    if (a.kind === 'declare-model') return 'Declared as shared model.' + owner;
    return `Rejected. What to do instead: ${(a.instead ?? '').replace(/\.$/, '')}.` + owner;
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
  {@const where = body.record.split('\n')[0]}
  <div class="page-head">
    <div class="stack-sm">
      <h1>{question(body)}</h1>
      <p class="lede">
        {#if body.state !== 'open'}<span class="status {status.tone}">{status.word}</span>{/if}
        {ledeLine(body, new Date())}
      </p>
      {#if body.state !== 'open' || status.line !== 'Waiting for an owner to answer.'}<p class="small">{status.line}</p>{/if}
    </div>
  </div>

  <StoryRow steps={storySteps(body, answered?.state === 'merged')} ask={`#${body.number}`} />

  {#if changed}<Banner role="status">{changed}</Banner>{/if}

  <section class="box" aria-label="Evidence">
    <dl class="evidence">
      <dt>Where</dt>
      <dd><code class="codeline">{where}</code></dd>
      <dt>The rule</dt>
      <dd>
        {#if body.surface?.package}<code>{body.surface.package}</code> is past the public part of <code>{body.to}</code>.
        {:else}The policy does not allow <code>{dependency(body).replace(' -> ', ' → ')}</code> (rule <code>{body.rule}</code>).{/if}
        {#if body.allowed}The policy already allows this dependency.{/if}
      </dd>
      {#if balance}<dt>Balance</dt><dd>{balance}</dd>{/if}
      {#if body.expires}<dt>Asked until</dt><dd>{body.expires}</dd>{/if}
      {#if body.issue_href === `${github}/issues/${body.number}`}
        <dt>On GitHub</dt>
        <dd><a href={body.issue_href}>Request #{body.number}</a></dd>
      {/if}
    </dl>
  </section>

  <section class="box" aria-labelledby="answer">
    <div class="box-head"><h2 class="title-box" id="answer">{body.state === 'open' ? 'Your answer' : 'Answer'}</h2></div>
    <div class="box-body stack">
      {#if body.state === 'open' && body.policy}
        <div class="answers" role="radiogroup" aria-label="Choose an answer">
          {#each kinds.filter((k) => !k.minor) as { kind, title } (kind)}
            {@const shown = previews[kind]}
            <label class="answer" class:selected={selected === kind}>
              <input type="radio" name="answer" value={kind} bind:group={selected} disabled={busy} />
              <span class="stack-sm">
                {#if kind === 'approve-until'}
                  <span class="name field">
                    Allow until
                    <input class="form-control date" type="text" aria-label="The exception ends on (yyyy-mm-dd)" placeholder="yyyy-mm-dd"
                      bind:value={until} disabled={busy} oninput={() => preview('approve-until')} />
                  </span>
                {:else}<span class="name">{title}</span>{/if}
                <span class="effects">
                  {#each effectLines(kind, body, shown.preview, until) as line (line)}
                    {@const [lead, rest] = effectLead(line)}
                    <span>{#if lead}<strong>{lead}</strong>{/if}{rest}</span>
                  {/each}
                </span>
                {#if kind === 'reject'}
                  <textarea class="form-control" rows="2" maxlength="500" aria-label="What to do instead" placeholder="Instead: …"
                    bind:value={instead} disabled={busy} oninput={() => preview('reject')}></textarea>
                {/if}
                {#if shown.note && selected === kind}<span class="flash error" role="alert">{shown.note}</span>{/if}
              </span>
            </label>
          {/each}
        </div>
        {#each kinds.filter((k) => k.minor) as { kind, title } (kind)}
          <label class="minor form-choice small">
            <input type="radio" name="answer" value={kind} bind:group={selected} disabled={busy} />
            <span><strong>{title}</strong>: {effectLines(kind, body, previews[kind].preview, until).join(' ')}</span>
          </label>
        {/each}

        {@const p = selected && decision?.ready ? previews[selected].preview : undefined}
        {#if p}
          <Disclosure summary="Show the change to the policy and the pull requests">
            <div class="stack-sm small">
              {#if p.diff}<Diff text={p.diff} label="The change to .archfit.yaml" />{:else}<p>It writes no line in <code>.archfit.yaml</code>.</p>{/if}
              {#if p.pulls.length}
                <ul class="plain stack-sm" aria-label="Open pull requests">
                  {#each p.pulls as e (e.number)}
                    <li>
                      {pullLine(e)} · <a class="inline" href={repoPage(route.fullName, `/pull/${e.number}`)}>in Archfit</a> · <a class="inline"
                        href={`${github}/pull/${e.number}`}>on GitHub</a
                      >
                    </li>
                  {/each}
                </ul>
              {/if}
            </div>
          </Disclosure>
        {/if}
        {@const problem = selected ? answerProblem(selected, until, instead) : ''}
        <div class="confirm">
          <button class="btn primary" type="button" disabled={busy || !selected || !decision?.ready || !!problem}
            onclick={() => selected && answer(selected)}>{decision?.confirm ?? 'Answer with passkey'}</button>
          <span class="small muted">
            {#if !selected}Choose an answer. Nothing changes until you confirm it.
            {:else if problem}{problem}
            {:else if loosens(selected)}Your passkey confirms this one answer. <a class="inline" href={repoPage(route.fullName, '/passkeys?return=' + encodeURIComponent(here))}>Your passkeys</a>
            {:else}No passkey: a reject does not loosen the policy.{/if}
          </span>
        </div>
        <p class="small muted">
          Need to change something that is not a request? <a class="inline" href={repoPage(route.fullName, '/policy')}>Advanced: edit the policy file</a>
        </p>
      {:else if body.state === 'open'}
        <p>Add a valid <code>.archfit.yaml</code> (version 1) to the default branch, then answer.</p>
      {:else}
        <p>{answeredLine(body)}</p>
      {/if}
      {#if busy}<p class="small" role="status">Answering…</p>{/if}
      {#if answered}
        {@const pr = answered.pull_request}
        {#if answered.state === 'void'}
          <Banner tone="attention" role="status" title="Answered, but the policy pull request cannot merge.">
            <p>{answered.notice}{#if pr} <a class="inline" href={`${github}/pull/${pr}`}>Policy pull request #{pr}</a>{/if}</p>
          </Banner>
        {:else if !pr}
          <Banner tone="success" role="status" title="Answered.">
            <p>The policy does not change. The author sees your answer on the request and in the check.</p>
          </Banner>
        {:else if answered.state === 'merged'}
          <Banner tone="success" role="status" title="Answered. The policy is updated.">
            <p>Merged in <a class="inline" href={`${github}/pull/${pr}`}>policy pull request #{pr}</a>.</p>
          </Banner>
        {:else}
          <Banner role="status" title="Answered. Nothing else for you to do.">
            <p>The App merges <a class="inline" href={`${github}/pull/${pr}`}>policy pull request #{pr}</a> when its checks pass, and the policy updates by itself. You can close this page.</p>
          </Banner>
        {/if}
      {/if}
      {#if answerError}<p class="flash error" role="alert">{answerError}</p>{/if}
    </div>
  </section>

  <section class="box" aria-label="More about this request">
    <div class="box-body">
      {#if body.modules.length}
        <Disclosure summary="The map">
          <Map model={requestMap(body)} repo={route.fullName} showNeed={body.state === 'open'} />
        </Disclosure>
        <Disclosure summary="Why the balance">
          <div class="stack-sm">
            {#if balance}<p>Balance: <strong>{balance}</strong>. The balance is the value the policy checks against its minimum.</p>{/if}
            <p>The policy declares the volatility of each module:</p>
            {#each body.modules as m (m.name)}<p><code>{m.name}</code>: {moduleLine(m)}</p>{/each}
          </div>
        </Disclosure>
      {/if}
      <Disclosure summary={`Earlier answers about these modules (${body.earlier.length})`}>
        {#if body.earlier.length}
          <ul class="plain stack-sm">
            {#each body.earlier as e, i (i)}
              <li>{earlierLine(e)}{#if e.why}<span class="muted">{` Why: ${e.why}`}</span>{/if}</li>
            {/each}
          </ul>
        {:else}<p class="muted">No earlier answer about these modules.</p>{/if}
      </Disclosure>
      <Disclosure summary={body.state === 'answered' ? 'The record as checked when the request was made' : 'The record'}>
        <!-- svelte-ignore a11y_no_noninteractive_tabindex (Keyboard users must be able to scroll the original record.) -->
        <pre tabindex="0" role="region" aria-label="Original request record">{body.record}</pre>
      </Disclosure>
    </div>
  </section>
{/if}

<style>
  .evidence { display: grid; grid-template-columns: 140px minmax(0, 1fr); margin: 0; }
  .evidence dt, .evidence dd { margin: 0; padding: 8px 16px; border-top: 1px solid var(--border-muted); }
  .evidence dt:first-of-type, .evidence dt:first-of-type + dd { border-top: 0; }
  .evidence dt { color: var(--fg-muted); font-size: 12px; }
  .codeline { display: block; overflow-x: auto; white-space: pre; }
  .answers { display: grid; gap: 10px; }
  .answer {
    display: grid; grid-template-columns: 20px minmax(0, 1fr); gap: 10px; padding: 12px 14px; cursor: pointer;
    border: 1px solid var(--border); border-radius: var(--radius); background: var(--bg);
  }
  .answer:hover { border-color: var(--fg-muted); }
  .answer.selected { border-color: var(--accent); box-shadow: 0 0 0 2px var(--accent-border); background: var(--accent-subtle); }
  .answer input[type='radio'] { margin-top: 3px; align-self: start; accent-color: var(--accent); }
  .effects :global(strong) { color: var(--fg); font-weight: 600; }
  .name { font-weight: 600; font-size: 15px; }
  .effects { display: flex; flex-direction: column; gap: 2px; color: var(--fg-muted); font-size: 13px; }
  .field { display: inline-flex; align-items: center; gap: 8px; }
  .date { width: 9em; }
  .minor { color: var(--fg-muted); align-items: flex-start; }
  .minor strong { color: var(--fg); }
  .confirm { display: flex; gap: 12px; align-items: center; flex-wrap: wrap; }
  @media (max-width: 640px) {
    .evidence { grid-template-columns: 1fr; }
    .evidence dd { border-top: 0; padding-top: 0; }
  }
</style>

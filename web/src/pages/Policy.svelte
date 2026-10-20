<script lang="ts">
  import Skeleton from '../components/Skeleton.svelte';
  import type { EditorView } from '@codemirror/view';
  import { onDestroy } from 'svelte';
  import {
    ApiError,
    explain,
    getJSON,
    postJSON,
    repoAPI,
    type Checked,
    type Diagnostic,
    type PolicyPage,
    type Session,
  } from '../lib/api';
  import { createEditor, goToLine, setText, showProblems, textOf, type EditorOptions } from '../lib/editor';
  import { lineOfHash, placeOf, runWords, timeline } from '../lib/policy';
  import { repoPage, safeHref, type Repo } from '../lib/route';

  let { route, session, here }: { route: Repo; session: Session; here: string } = $props();

  const query = new URLSearchParams(location.search);
  const branchQuery = query.get('branch') ?? '';
  let since = $state(query.get('since') ?? '');

  // page is the latest answer. base is the version the editor's text started from: every write sends its head, so a
  // draft that moved on GitHub since then refuses the write (409) instead of losing the move.
  let page = $state<PolicyPage | null>(null);
  let base = $state({ branch: '', head_sha: '', content: '' });
  let text = $state('');
  let problems = $state<Diagnostic[]>([]);
  let failure = $state('');
  let notice = $state('');
  let stale = $state(false);
  let busy = $state('');
  let instruction = $state('');
  let mode = $state<'customer' | 'managed'>('customer');

  let host = $state<HTMLDivElement>();
  let view: EditorView | undefined;

  const dirty = $derived(page !== null && text !== base.content);
  // behind: a reload found a newer draft head than the one the kept edits started from.
  const behind = $derived(page !== null && page.head_sha !== base.head_sha);
  const steps = $derived(page ? timeline(page) : []);
  const api = (suffix: string) => repoAPI(route.owner, route.name, suffix);
  const nonce = document.querySelector('meta[name="csp-nonce"]')?.getAttribute('content') ?? '';
  const options: EditorOptions = {
    text: '',
    label: `Policy text, .archfit.yaml`,
    describedBy: 'policy-hint',
    nonce,
    onChange: (t) => (text = t),
  };

  function pageQuery(): string {
    const pairs: [string, string][] = [];
    if (branchQuery) pairs.push(['branch', branchQuery]);
    if (since) pairs.push(['since', since]);
    return pairs.length ? `?${new URLSearchParams(pairs)}` : '';
  }

  // accept takes a version of the page as the editor's base; replace also puts its text into the editor.
  function accept(p: PolicyPage, replace: boolean) {
    page = p;
    base = { branch: p.branch, head_sha: p.head_sha, content: p.content };
    problems = p.diagnostics;
    stale = false;
    if (replace) {
      text = p.content;
      if (view) setText(view, p.content, options);
    }
    if (view) showProblems(view, problems);
  }

  // load reads the page again. Edits in the editor are kept unless discard: the answer then only updates the state
  // around the text, and the next save is refused if the draft moved meanwhile.
  async function load(discard = false) {
    try {
      const p = await getJSON<PolicyPage>(api('policy') + pageQuery());
      if (!page || discard || !dirty) accept(p, true);
      else page = { ...p, diagnostics: problems };
    } catch (e) {
      failure = explain(e as ApiError, here);
    }
  }
  load();

  $effect(() => {
    if (host && page && !view) {
      view = createEditor(host, { ...options, text: base.content });
      text = base.content;
      showProblems(view, problems);
      const line = lineOfHash(location.hash);
      if (line) goToLine(view, line);
    }
  });
  onDestroy(() => view?.destroy());

  // Leaving with unsaved edits asks first: the browser's own question, no dialog of ours.
  $effect(() => {
    if (!dirty) return;
    const stay = (e: BeforeUnloadEvent) => e.preventDefault();
    addEventListener('beforeunload', stay);
    return () => removeEventListener('beforeunload', stay);
  });

  async function act(name: string, run: () => Promise<void>) {
    busy = name;
    failure = notice = '';
    try {
      await run();
    } catch (e) {
      const err = e as ApiError;
      failure = explain(err, here);
      stale = err.status === 409;
      if (err.diagnostics.length) {
        problems = err.diagnostics;
        if (view) showProblems(view, problems);
      }
    } finally {
      busy = '';
    }
  }

  const current = () => (view ? textOf(view) : text);

  const check = () =>
    act('check', async () => {
      const res = await postJSON<Checked>(api('policy/check'), session.csrf, { content: current() });
      problems = res.diagnostics;
      notice = res.notice;
      if (view) showProblems(view, problems);
    });

  // write saves the text the editor holds; the answer becomes the base, and edits typed meanwhile stay unsaved.
  async function write(content: string) {
    const res = await postJSON<PolicyPage>(api('policy'), session.csrf, {
      branch: base.branch,
      head_sha: base.head_sha,
      content,
    });
    accept(res, false);
    notice = res.notice ?? '';
  }

  const save = () => act('save', () => write(current()));

  const ready = () =>
    act('ready', async () => {
      const res = await postJSON<PolicyPage>(api('policy/ready'), session.csrf, {
        branch: base.branch,
        head_sha: base.head_sha,
      });
      accept(res, false);
      notice = res.notice ?? '';
    });

  const propose = (e: SubmitEvent) => {
    e.preventDefault();
    return act('propose', async () => {
      // Save first, as its own request: when the AI request then fails, the page already has the head the save left.
      const sent = current();
      if (sent !== base.content) await write(sent);
      const res = await postJSON<PolicyPage>(api('assist'), session.csrf, {
        branch: base.branch,
        head_sha: base.head_sha,
        content: sent,
        instruction,
        mode,
      });
      // Managed mode answers with the proposal on the draft; customer mode with the saved text and the run. Text typed
      // while the request ran is kept: the page then is behind the draft and offers to discard the edits.
      if (current() === sent) accept(res, true);
      else page = { ...res, diagnostics: problems };
      notice = res.notice ?? '';
      if (res.since) {
        since = res.since;
        history.replaceState(null, '', location.pathname + pageQuery());
      }
    });
  };

  // allowManagedAI is the administrator's inline Allow, the same write as on Data sharing.
  const allowManagedAI = () =>
    act('allow', async () => {
      await postJSON(api('data-sharing'), session.csrf, { kind: 'managed_llm', action: 'allow' });
      await load();
      notice = 'Managed AI is allowed.';
    });

  // The AI box sits under the editor, out of sight on a long policy: the toolbar takes the user to it.
  function askAI() {
    const box = document.getElementById('ai-title')?.closest('section');
    box?.scrollIntoView({ block: 'center' });
    box?.querySelector('textarea')?.focus({ preventScroll: true });
  }

  const discard = () => act('reload', () => load(true));

  function show(d: Diagnostic) {
    if (view && d.line) goToLine(view, d.line);
  }
</script>

<div class="page-head">
  <div class="stack-sm">
    <h1>Policy</h1>
    <p class="muted">
      Edit the architecture policy and save it to a draft pull request. Merging that pull request on GitHub publishes
      it.
    </p>
  </div>
</div>

{#if notice}<p class="flash success" role="status">{notice}</p>{/if}
{#if failure}
  <div class="flash error" role="alert">
    <span>{failure}</span>
    {#if stale && !behind}
      <button class="btn sm" type="button" disabled={busy !== ''} onclick={discard}>
        Discard my edits and load the draft
      </button>
    {/if}
  </div>
{/if}

{#if page}
  {#if behind}
    <div class="flash warn" role="status">
      <span>The draft changed on GitHub after you started editing. Your edits are kept, and saving them is refused.</span>
      <button class="btn sm" type="button" disabled={busy !== ''} onclick={discard}>
        Discard my edits and load the draft
      </button>
    </div>
  {/if}
  {#if page.run}
    <p class="flash" role="status">
      <span>
        AI proposal in your CI: <strong>{runWords[page.run.state]}</strong> ·
        <a href={safeHref(page.run.url)} rel="noreferrer">workflow runs</a>
      </span>
      <button class="btn sm" type="button" disabled={busy !== ''} onclick={() => act('run', () => load())}>
        Check again
      </button>
    </p>
  {/if}

  <section class="box" aria-labelledby="policy-file">
    <div class="box-head row-between">
      <div class="row">
        <h2 id="policy-file"><code>.archfit.yaml</code></h2>
        <span class="muted small">on <code>{page.branch}</code></span>
        {#if page.draft_state}<span class="label accent">{page.draft_state}</span>{/if}
        {#if dirty}<span class="label attention">unsaved edits</span>{/if}
      </div>
      {#if safeHref(page.draft_url)}
        <a href={safeHref(page.draft_url)} rel="noreferrer">Draft pull request</a>
      {/if}
    </div>
    <div class="box-row toolbar">
      <button class="btn" type="button" disabled={busy !== ''} onclick={check}>Check</button>
      {#if page.can_write}
        <button class="btn primary" type="button" disabled={busy !== ''} onclick={save}>Save to draft PR</button>
        {#if page.can_ready}
          <button class="btn" type="button" disabled={busy !== '' || dirty} onclick={ready}>Ready for review</button>
          {#if dirty}<span class="disabled-reason">Save your edits first.</span>{/if}
        {/if}
      {:else}
        <span class="disabled-reason">Saving needs write access to {route.fullName}.</span>
      {/if}
      {#if page.can_write}
        <button class="btn push-right" type="button" onclick={askAI}>Ask AI for a proposal</button>
      {/if}
    </div>
    <p class="box-row small muted" id="policy-hint">
      Check validates the schema. The Archfit check on the draft pull request validates the rest.
    </p>
    {#if problems.length}
      <div class="box-row problems" role="region" aria-labelledby="problems-title">
        <h3 id="problems-title" class="small">
          {problems.length === 1 ? '1 problem' : `${problems.length} problems`}
        </h3>
        <ol>
          {#each problems as d, i (i)}
            <li>
              {#if d.line}
                <button class="link-button" type="button" onclick={() => show(d)}>{placeOf(d)}</button>:
              {/if}
              {d.message}
            </li>
          {/each}
        </ol>
      </div>
    {/if}
    <div class="editor" bind:this={host}></div>
  </section>

  {#if page.draft !== 'none'}
    <section class="box" aria-labelledby="draft-title">
      <div class="box-head"><h2 id="draft-title">Draft</h2></div>
      <div class="box-body stack">
        <ol class="track">
          {#each steps as s, i (s.label)}
            <li class={s.done ? 'done' : s.current ? 'current' : ''}>
              <span class="mark" aria-hidden="true">{s.done ? '✓' : i + 1}</span>
              {s.label}
              <span class="sr-only">{s.done ? '(done)' : s.current ? '(current step)' : '(to do)'}</span>
            </li>
          {/each}
        </ol>
        {#if page.checks.length}
          <ul class="plain">
            {#each page.checks as c (c.name)}
              <li>
                <code>{c.name}</code>: <span class="status {c.tone}">{c.state}</span>
                {#if c.reason}<span class="muted"> · {c.reason}</span>{/if}
              </li>
            {/each}
          </ul>
        {/if}
        <p class="small muted">Policy owners approve and merge the pull request on GitHub. The App never merges.</p>
      </div>
    </section>
  {/if}

  {#if page.can_write}
    <section class="box" aria-labelledby="ai-title">
      <div class="box-head"><h2 id="ai-title">Ask AI for a proposal</h2></div>
      <form class="box-body form" onsubmit={propose}>
        <p class="small muted">
          Your text is saved first. The proposal arrives as a commit on the draft pull request; nothing is published.
        </p>
        <label>
          Instruction
          <textarea name="instruction" rows="3" maxlength="2000" required bind:value={instruction}></textarea>
        </label>
        <fieldset class="stack-sm">
          <legend>Mode</legend>
          <label class="choice">
            <input type="radio" name="mode" value="customer" bind:group={mode} />
            Customer: the assist workflow runs in your CI
          </label>
          <label class="choice">
            <input
              type="radio"
              name="mode"
              value="managed"
              bind:group={mode}
              disabled={!page.managed.offer}
              aria-describedby={page.managed.off ? 'managed-off' : undefined}
            />
            Managed: Archfit sends the text and the instruction to the AI provider
          </label>
          {#if page.managed.off}
            <p class="disabled-reason row" id="managed-off">
              {page.managed.off}
              {#if safeHref(page.managed.data_sharing)}
                <button class="btn sm" type="button" disabled={busy !== ''} onclick={allowManagedAI}>
                  Allow managed AI
                </button>
                <a href={safeHref(page.managed.data_sharing)}>Data sharing</a>
              {/if}
            </p>
          {/if}
        </fieldset>
        <div><button class="btn" type="submit" disabled={busy !== ''}>Propose</button></div>
      </form>
    </section>
  {/if}
{:else if !failure}
  <Skeleton rows={6} />
{/if}

<p><a href={repoPage(route.fullName)}>Back to {route.fullName}</a></p>

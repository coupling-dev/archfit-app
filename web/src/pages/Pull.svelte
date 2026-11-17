<script lang="ts">
  import Status from '../components/Status.svelte';
  import { homeHref, type OwnerReview } from '../lib/home';
  import Skeleton from '../components/Skeleton.svelte';
  import { explainLoad, repoAPI, type ApiError } from '../lib/api';
  import { firstJSON } from '../lib/early';
  import { repoPage, type Repo } from '../lib/route';

  interface RecordLine {
    path?: string; line?: number; rule: string; what: string; why?: string; instead?: string;
    balance?: { value: number; min: number }; next: { kind: string; text: string; note?: string }; show_next: boolean; request?: number;
  }
  interface PullPage {
    message?: string;
    owner_action?: OwnerReview;
    view?: { status: string; status_line: string; records: RecordLine[]; author?: string; owner?: string };
  }

  let { route, here }: { route: Repo & { number: number }; here: string } = $props();
  let body = $state<PullPage | null>(null);
  let failure = $state('');
  function load() {
    firstJSON<PullPage>(repoAPI(route.owner, route.name, `pulls/${route.number}`)).then(
      (b) => (body = b), (e: ApiError) => (failure = explainLoad(e, here)),
    );
  }
  load();
  const reviewHref = $derived(body?.owner_action?.href ? homeHref(route, body.owner_action.href) : undefined);
  const github = $derived(`https://github.com/${encodeURIComponent(route.owner)}/${encodeURIComponent(route.name)}/pull/${route.number}`);
  const requestNumbers = $derived(new Set(body?.view?.records.map((record) => record.request).filter((n) => n !== undefined)));
  const pieces = (s: string, authored: boolean) => (authored ? s.replaceAll('`', '') : s).split(/(#[1-9][0-9]*)/g);
</script>

{#snippet linked(s: string, authored = false)}
  {#each pieces(s, authored) as part, i (i)}
    {#if /^#[1-9][0-9]*$/.test(part) && requestNumbers.has(Number(part.slice(1)))}<a class="inline" href={repoPage(route.fullName, `/requests/${part.slice(1)}`)}>{part}</a>{:else}{part}{/if}
  {/each}
{/snippet}

<div class="page-head">
  <div class="stack-sm page-identity">
    <h1>Pull request #{route.number}</h1>
    <p class="lede">{route.fullName}</p>
    {#if failure}<p class="flash error" role="alert">{failure}</p>
    {:else if body?.view}
      <Status state={body.view.status} tone={body.view.status === 'pass' ? 'success' : body.view.status === 'fail' ? 'danger' : body.view.status === 'waiting' ? 'attention' : 'neutral'}>{@render linked(body.view.status_line)}</Status>
    {:else if body}<Status>{body.message}</Status>{/if}
    {#if body}
      {#if body.owner_action && !reviewHref}<p>{body.owner_action.label}</p>{/if}
      <div class="row">
        {#if reviewHref && body.owner_action}<a class="btn primary" href={reviewHref}>{body.owner_action.label}</a>{/if}
        <a class="btn" class:primary={!reviewHref} href={github}>Pull request #{route.number} on GitHub</a>
        <a class="btn" href={repoPage(route.fullName, '/policy')}>View policy</a>
      </div>
    {/if}
  </div>
</div>

{#if !body && !failure}
  <Skeleton rows={2} box={false} />
{:else if body?.view}
  <section aria-label="Pull request">
    {#each body.view.records as record, i (i)}
      <div class="pull-record">
        <div>{record.path ? record.path + (record.line ? `:${record.line}` : '') + ' ' : ''}{record.rule}{record.what ? ` ${record.what}` : ''}</div>
        {#if record.why}<div>why: {record.why}</div>{/if}
        {#if record.instead}<div>instead: {record.instead}</div>{/if}
        {#if record.balance}<div>balance: {record.balance.value}/10 (min {record.balance.min})</div>{/if}
        {#if record.show_next}<div>next: {@render linked(record.next.text)}{#if record.next.note} ({record.next.note}){/if}</div>{/if}
      </div>
    {/each}
    {#if body.view.author}<p>Author: {@render linked(body.view.author, true)}</p>{/if}
    {#if body.view.owner}<p>Owner: {@render linked(body.view.owner, true)}</p>{/if}
  </section>
{/if}

<style>
  .pull-record { margin-block: 16px; overflow-wrap: anywhere; }
</style>

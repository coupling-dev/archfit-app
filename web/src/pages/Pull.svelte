<script lang="ts">
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
  const requestNumbers = $derived(new Set(body?.view?.records.map((record) => record.request).filter((n) => n !== undefined)));
  const pieces = (s: string) => s.replaceAll('`', '').split(/(#[1-9][0-9]*)/g);
</script>

{#snippet linked(s: string)}
  {#each pieces(s) as part, i (i)}
    {#if /^#[1-9][0-9]*$/.test(part) && requestNumbers.has(Number(part.slice(1)))}<a href={repoPage(route.fullName, `/requests/${part.slice(1)}`)}>{part}</a>{:else}{part}{/if}
  {/each}
{/snippet}

{#if failure}
  <div class="flash error" role="alert"><h1>{failure}</h1></div>
{:else if !body}
  <Skeleton rows={2} box={false} />
{:else if body.view}
  <section aria-label="Pull request">
    <h1 data-status={body.view.status}>{@render linked(body.view.status_line)}</h1>
    {#each body.view.records as record, i (i)}
      <div class="pull-record">
        <div>{record.path ? record.path + (record.line ? `:${record.line}` : '') + ' ' : ''}{record.rule}{record.what ? ` ${record.what}` : ''}</div>
        {#if record.why}<div>why: {record.why}</div>{/if}
        {#if record.instead}<div>instead: {record.instead}</div>{/if}
        {#if record.balance}<div>balance: {record.balance.value}/10 (min {record.balance.min})</div>{/if}
        {#if record.show_next}<div>next: {@render linked(record.next.text)}{#if record.next.note} ({record.next.note}){/if}</div>{/if}
      </div>
    {/each}
    {#if body.view.author}<p>Author: {@render linked(body.view.author)}</p>{/if}
    {#if body.view.owner}<p>Owner: {@render linked(body.view.owner)}</p>{/if}
  </section>
{:else}
  <h1>{body.message}</h1>
{/if}

<style>
a { text-decoration: underline; }
  h1[data-status]::before { content: ''; display: inline-block; inline-size: .5em; block-size: .5em; border-radius: 50%; background: currentColor; margin-inline-end: .5em; }
  h1[data-status='pass'] { color: var(--success); }
  h1[data-status='fail'] { color: var(--danger); }
  h1[data-status='waiting'] { color: var(--attention); }
  .pull-record { margin-block: 1rem; overflow-wrap: anywhere; }
</style>

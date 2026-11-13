<script lang="ts">
  import Skeleton from '../components/Skeleton.svelte';
  import { explainLoad, repoAPI, type ApiError } from '../lib/api';
  import { firstJSON } from '../lib/early';
  import { repoPage, type Repo } from '../lib/route';

  interface RecordLine {
    path?: string; line?: number; rule: string; what: string; why?: string; instead?: string;
    balance?: { value: number; min: number }; next: { kind: string; text: string; note?: string }; show_next: boolean;
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
  const pieces = (s: string) => s.replaceAll('`', '').split(/(#[1-9][0-9]*)/g);
</script>

{#snippet linked(s: string)}
  {#each pieces(s) as part, i (i)}
    {#if /^#[1-9][0-9]*$/.test(part)}<a href={repoPage(route.fullName, `/requests/${part.slice(1)}`)}>{part}</a>{:else}{part}{/if}
  {/each}
{/snippet}

{#if failure}
  <h1 class="flash error" role="alert">{failure}</h1>
{:else if !body}
  <Skeleton rows={2} box={false} />
{:else if body.view}
  <section aria-label="Pull request">
    <h1>{@render linked(body.view.status_line)}</h1>
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
  .pull-record { margin-block: 1rem; overflow-wrap: anywhere; }
</style>

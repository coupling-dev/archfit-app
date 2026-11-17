<script lang="ts">
  import PageHead from '../components/ui/PageHead.svelte';
  import StatusLabel from '../components/ui/StatusLabel.svelte';
  import Icon from '../components/ui/Icon.svelte';
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

<PageHead title={`Pull request #${route.number}`} repo={route.fullName}>
  {#snippet status()}
    {#if failure}<p class="flash error" role="alert">{failure}</p>
    {:else if body?.view}<div class="stack-sm"><StatusLabel label={body.view.status} /><p>{@render linked(body.view.status_line)}</p>{#if body.view.note}<p>{body.view.note}</p>{/if}</div>
    {:else if body}<div class="stack-sm"><StatusLabel label="no result" /><p>{body.message}</p></div>{/if}
  {/snippet}
  {#snippet actions()}
    {#if body}
      {#if reviewHref && body.owner_action}<a class="btn primary" href={reviewHref}>{body.owner_action.label}</a>{/if}
      <a class="btn" href={github}>Pull request #{route.number} on GitHub <Icon name="link-external" size={12} /></a>
      <a class="btn" href={repoPage(route.fullName, '/policy')}>View policy</a>
    {/if}
  {/snippet}
  {#if body?.owner_action && !reviewHref}<p>{body.owner_action.label}</p>{/if}
</PageHead>

{#if !body && !failure}
  <Skeleton rows={2} box={false} />
{:else if body?.view}
  <section aria-label="Pull request">
    {#each body.view.records as record, i (i)}
      <article class="pull-record">
        <p>{record.path ? `${record.path}${record.line ? `:${record.line}` : ''} ` : ''}<strong>{record.rule}</strong>{record.what ? ` ${record.what}` : ''}</p>
        {#if record.why}<p class="record-field">why: {record.why}</p>{/if}
        {#if record.instead}<p class="record-field">instead: {record.instead}</p>{/if}
        {#if record.balance}<p class="record-field">balance: {record.balance.value}/10 (min {record.balance.min})</p>{/if}
        {#if record.show_next}<p class="record-field">next: {@render linked(record.next.text)}{#if record.next.note} ({record.next.note}){/if}</p>{/if}
      </article>
    {/each}
    {#if body.view.author}<p class="small">Author: {@render linked(body.view.author, true)}</p>{/if}
    {#if body.view.owner}<p class="small">Owner: {@render linked(body.view.owner, true)}</p>{/if}
  </section>
{/if}

<style>
  .pull-record { padding-block: 12px; border-bottom: 1px solid var(--border-muted); overflow-wrap: anywhere; }
  .record-field { margin: 4px 0 0; font-size: 14px; color: var(--fg-muted); }
</style>

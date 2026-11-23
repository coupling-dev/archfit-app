<script lang="ts">
  import Skeleton from '../components/Skeleton.svelte';
  import StatusLabel from '../components/ui/StatusLabel.svelte';
  import PageHead from '../components/ui/PageHead.svelte';
  import { explain, type ApiError, type Repos } from '../lib/api';
  import { firstJSON } from '../lib/early';
  import { enforcementLabel, nextStep, repoStateLabel } from '../lib/repos';

  let { here }: { here: string } = $props();

  const installURL = 'https://github.com/apps/archfit-review/installations/new';
  const list = '/api/repos';
  const probed = '/api/repos?probe=1';

  let body = $state<Repos | null>(null);
  const filters = ['All', 'Not set up', 'Unknown', 'archfit/gate not required'] as const;
  let filter = $state<(typeof filters)[number]>('All');
  let search = $state('');
  let active = $derived(filter !== 'All' || search.trim() !== '');
  let visible = $derived((body?.repos ?? []).filter((row) =>
    row.full_name.toLowerCase().includes(search.trim().toLowerCase()) &&
    (filter === 'All' || (filter === 'Not set up' && row.state === 'not set up') ||
      (filter === 'Unknown' && row.state === 'unknown') ||
      (filter === 'archfit/gate not required' && row.enforcement === 'not_required')),
  ));
  let grouped = $derived(new Set((body?.repos ?? []).map((row) => row.full_name.split('/')[0])).size > 1);
  let groups = $derived([...new Set(visible.map((row) => row.full_name.split('/')[0]))].sort().map((owner) => ({
    owner, rows: visible.filter((row) => row.full_name.split('/')[0] === owner),
  })));
  function clearFilters() { search = ''; filter = 'All'; }
  let failure = $state('');
  // The list answers from stored state. The probe reads each repository's required-check settings and answers the same
  // rows later, so the list shows at once and its rows fill in when the probe returns.
  let probing = $state(true);
  let probeFailure = $state('');
  let probeRows = false; // the probe's rows are in `body`; the list's rows never replace them, whichever answers first

  firstJSON<Repos>(list).then(
    (b) => {
      if (!probeRows) body = b;
    },
    (e: ApiError) => (failure = explain(e, here)),
  );
  firstJSON<Repos>(probed).then(
    (b) => {
      body = b;
      probeRows = true;
      probing = false;
    },
    (e: ApiError) => {
      probing = false;
      probeFailure = explain(e, here);
    },
  );
</script>

<PageHead title="Repositories">
  <p class="lede">Choose a repository to open its Home.</p>
</PageHead>

{#if failure && !body}
  <p class="flash error" role="alert">{failure}</p>
{:else if !body}
  <Skeleton rows={4} />
{:else if body.repos.length === 0}
  <div class="box blankslate">
    <h2>No repository yet</h2>
    <p>No repository is available to you through an Archfit installation yet.</p>
    <p>If you asked to install Archfit on an organization and you are not its administrator, GitHub sent your request to the organization's administrators. Send one of them this link and one sentence:</p>
    <p><a href={installURL}>{installURL}</a></p>
  </div>
{:else}
  {#if probeFailure}
    <p class="flash error" role="alert">{probeFailure}</p>
  {/if}
  <div class="filters">
    <label for="repo-search">Search repositories</label>
    <input id="repo-search" type="search" bind:value={search} />
    <div class="filter-buttons" role="group" aria-label="Repository filters">
      {#each filters as option (option)}
        <button class="btn" type="button" aria-pressed={filter === option} onclick={() => (filter = option)}>{option}</button>
      {/each}
    </div>
  </div>
  {#if active}<p role="status">Showing {visible.length} of {body.repos.length} repositories.</p>{/if}
  {#if visible.length === 0}
    <p>No repository matches these filters. <button class="btn" type="button" onclick={clearFilters}>Clear filters</button></p>
  {:else}
  <div class="box repo-list" aria-busy={probing}>
    <table class="data">
      <thead>
        <tr><th scope="col">Repository</th><th scope="col">State</th><th scope="col">archfit/gate</th><th scope="col">Next step</th></tr>
      </thead>
      {#each groups as group (group.owner)}
      <tbody>
        {#if grouped}<tr class="group-row"><th scope="colgroup" colspan="4">{group.owner} · {group.rows.length}</th></tr>{/if}
        {#each group.rows as row (row.full_name)}
          {@const next = nextStep(row)}
          <tr>
            <td class="repository">
              <span class="mobile-label">Repository</span>
              <a href={`/r/${row.full_name}`}>{row.full_name}</a>
            </td>
            <td>
              <span class="mobile-label">State</span>
              <StatusLabel label={repoStateLabel(row.state)} />
            </td>
            <td>
              <span class="mobile-label">archfit/gate</span>
              {#if probing && !row.waiting}
                <span class="muted">Checking…</span>
              {:else}
                <StatusLabel label={enforcementLabel(row.enforcement)} />
              {/if}
            </td>
            <td>
              {#if next}
                <span class="mobile-label">Next step</span>
                {#if next.href}<a href={next.href}>{next.text}</a>{:else}<span>{next.text}</span>{/if}
              {/if}
            </td>
          </tr>
        {/each}
      </tbody>
      {/each}
    </table>
  </div>
  {/if}
  <p class="muted small">
    archfit/gate is required when GitHub requires pull requests and this check on the default branch. Not confirmed:
    the App could not confirm this check. Ask a repository administrator.
  </p>
  {#if body.truncated}
    <p class="muted small">Only the first repositories by name are listed.</p>
  {/if}
{/if}

<style>
  .filters { display: flex; flex-wrap: wrap; align-items: center; gap: 10px; margin-bottom: 16px; }
  .filter-buttons { display: flex; flex-wrap: wrap; gap: 8px; }
  .filter-buttons button[aria-pressed="true"] { font-weight: 700; text-decoration: underline; }
  .group-row th { background: var(--bg-subtle, #f5f5f5); color: var(--fg-muted); font-size: 12px; font-weight: 600; text-align: left; padding: 10px 12px; }
  .repo-list { overflow: hidden; }
  td { vertical-align: top; overflow-wrap: anywhere; }
  .repository { width: 40%; }
  /* The name sits in a table cell next to labels; underline it so it reads as a link in both schemes. */
  td a { text-decoration: underline; text-underline-offset: 2px; }
  .mobile-label { display: none; }

  @media (max-width: 640px) {
    .repo-list { border: 0; background: transparent; }
    table, tbody, tr, td { display: block; width: 100%; }
    .repository { width: 100%; }
    thead { display: none; }
    .group-row { border: 0; margin-bottom: 8px; padding: 0; }
    .group-row th { display: block; }
    tr { border: 1px solid var(--border); border-radius: var(--radius); margin-bottom: 16px; padding: 16px; background: var(--bg); }
    table.data td { border: 0; padding: 0 0 12px; }
    table.data td:last-child { padding-bottom: 0; }
    .mobile-label { display: block; font-size: 12px; color: var(--fg-muted); margin-bottom: 4px; }
  }
</style>

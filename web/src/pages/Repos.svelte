<script lang="ts">
  import Skeleton from '../components/Skeleton.svelte';
  import StatusLabel from '../components/ui/StatusLabel.svelte';
  import PageHead from '../components/ui/PageHead.svelte';
  import Icon from '../components/ui/Icon.svelte';
  import { explain, getJSON, type ApiError, type OwnerActions, type Repos } from '../lib/api';
  import { firstJSON } from '../lib/early';
  import { enforcementLabel, nextStep, repoStateLabel } from '../lib/repos';
  import { actionTitle, waitingFor } from '../lib/owner-actions';

  let { here }: { here: string } = $props();

  const list = '/api/repos';
  const probed = '/api/repos?probe=1';

  let body = $state<Repos | null>(null);
  let failure = $state('');
  // The list answers from stored state. The probe reads each repository's required-check settings and answers the same
  // rows later, so the list shows at once and its rows fill in when the probe returns.
  let probing = $state(true);
  let probeFailure = $state('');
  let probeRows = false; // the probe's rows are in `body`; the list's rows never replace them, whichever answers first

  // The owner steps that wait are always read fresh: a remembered answer could show a step that is already done.
  // A failure is said below, not hidden: no list is not the same as no steps.
  let owner = $state<OwnerActions | null>(null);
  let ownerFailed = $state(false);
  getJSON<OwnerActions>('/api/owner-actions').then(
    (b) => (owner = b),
    () => (ownerFailed = true),
  );

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
  </div>
{:else}
  {#if probeFailure}
    <p class="flash error" role="alert">{probeFailure}</p>
  {/if}
  {#if owner?.actions.length}
    <section class="box needs" aria-labelledby="repos-needs">
      <div class="box-head"><h2 class="title-box" id="repos-needs">Needs you</h2></div>
      {#each owner.actions as row (row.href)}
        {@const age = waitingFor(row.since)}
        <div class="box-row need">
          <span class="need-icon"><Icon name="clock" /></span>
          <div class="need-body">
            <a href={row.href}>{actionTitle(row)}</a>
            <a class="muted need-repo" href={`/r/${row.repo}`}>{row.repo}</a>
          </div>
          {#if age}<span class="muted need-age">{age}</span>{/if}
        </div>
      {/each}
    </section>
  {/if}
  {#if ownerFailed || owner?.unread || owner?.truncated}
    <p class="muted small" role="status">
      {#if ownerFailed}The App could not read the steps that wait for an owner.
      {:else if owner?.unread}The App could not read the owner steps of {owner.unread} {owner.unread === 1 ? 'repository' : 'repositories'}.
      {:else}Owner steps are shown for the first repositories by name only.{/if}
    </p>
  {/if}
  <div class="box repo-list" aria-busy={probing}>
    <table class="data">
      <thead>
        <tr><th scope="col">Repository</th><th scope="col">State</th><th scope="col">archfit/gate</th><th scope="col">Next step</th></tr>
      </thead>
      <tbody>
        {#each body.repos as row (row.full_name)}
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
    </table>
  </div>
  <p class="muted small">
    archfit/gate is required when GitHub requires pull requests and this check on the default branch. Not confirmed:
    the App could not confirm this check. Ask a repository administrator.
  </p>
  {#if body.truncated}
    <p class="muted small">Only the first repositories by name are listed.</p>
  {/if}
{/if}

<style>
  .repo-list { overflow: hidden; }
  td { vertical-align: top; overflow-wrap: anywhere; }
  .repository { width: 40%; }
  /* The name sits in a table cell next to labels; underline it so it reads as a link in both schemes. */
  td a { text-decoration: underline; text-underline-offset: 2px; }
  .mobile-label { display: none; }
  .needs { margin-bottom: 16px; }
  .need { display: flex; align-items: flex-start; gap: 10px; overflow-wrap: anywhere; }
  .need-icon { display: inline-flex; padding-top: 2px; color: var(--fg-muted); }
  .need-body { flex: 1; min-width: 0; display: flex; flex-wrap: wrap; gap: 2px 12px; }
  .need-repo { text-decoration: none; }
  .need-age { white-space: nowrap; }

  @media (max-width: 640px) {
    .repo-list { border: 0; background: transparent; }
    table, tbody, tr, td { display: block; width: 100%; }
    .repository { width: 100%; }
    thead { display: none; }
    tr { border: 1px solid var(--border); border-radius: var(--radius); margin-bottom: 16px; padding: 16px; background: var(--bg); }
    table.data td { border: 0; padding: 0 0 12px; }
    table.data td:last-child { padding-bottom: 0; }
    .mobile-label { display: block; font-size: 12px; color: var(--fg-muted); margin-bottom: 4px; }
  }
</style>

<script lang="ts">
  import Skeleton from '../components/Skeleton.svelte';
  import { explain, type ApiError, type Repos } from '../lib/api';
  import { firstJSON } from '../lib/early';
  import { repoPage } from '../lib/route';
  import { setupLabel, enforcementLabel, nextStep } from '../lib/repos';

  let { here }: { here: string } = $props();

  let body = $state<Repos | null>(null);
  let failure = $state('');

  firstJSON<Repos>('/api/repos').then(
    (b) => (body = b),
    (e: ApiError) => (failure = explain(e, here)),
  );
</script>

<div class="page-head">
  <div>
    <h1>Repositories</h1>
    <p class="lede">Choose a repository or continue setup.</p>
  </div>
</div>

{#if failure}
  <p class="flash error" role="alert">{failure}</p>
{:else if !body}
  <Skeleton rows={4} />
{:else if body.repos.length === 0}
  <div class="box blankslate">
    <h2 class="title-box">No repository yet</h2>
    <p>No repository is available to you through an Archfit installation yet.</p>
  </div>
{:else}
  <p class="muted small">Required check is separate from policy approval. Open Home for requests and other owner decisions; those counts are not checked here.</p>
  <div class="box repo-list">
    <table class="data">
      <thead>
        <tr><th scope="col">Repository</th><th scope="col">Setup</th><th scope="col">Needs attention</th><th scope="col">Next step</th></tr>
      </thead>
      <tbody>
        {#each body.repos as row (row.full_name)}
          {@const next = nextStep(row)}
          <tr>
            <td class="repository">
              <span class="mobile-label">Repository</span>
              <a class="desktop-name" href={repoPage(row.full_name)}>{row.full_name}</a>
              <strong class="mobile-name">{row.full_name}</strong>
            </td>
            <td>
              <span class="mobile-label">Setup</span>
              {#if row.waiting}
                <span class="status attention">Waiting for installation approval</span>
              {:else}
                <span class="status"
                  class:success={row.setup_stage === 'policy_approved'}
                  class:accent={row.setup_stage === 'first_approval' || row.setup_stage === 'workflow_review'}
                  class:attention={row.setup_stage === 'not_started'}
                  class:neutral={!['not_started', 'first_approval', 'workflow_review', 'policy_approved'].includes(row.setup_stage ?? '')}
                >{setupLabel(row.setup_stage)}</span>
                {#if row.setup_reason}<p class="muted small reason">{row.setup_reason}</p>{/if}
                <div class="enforcement"><span class="muted small">Required check:</span>
                  <span class="status" class:success={row.enforcement === 'required'}
                    class:attention={row.enforcement === 'not_required'}
                    class:neutral={!['required', 'not_required'].includes(row.enforcement)}
                  >{enforcementLabel(row.enforcement)}</span>
                </div>
              {/if}
            </td>
            <td>
              <span class="mobile-label">Needs attention</span>
              {#if row.attention}<span class="status accent">{row.attention}</span>
              {:else}<span class="muted small">{row.waiting ? 'Installation approval' : 'Not checked here'}</span>{/if}
            </td>
            <td>
              <span class="mobile-label">Next step</span>
              <a href={next.href}>{next.text}</a>
            </td>
          </tr>
        {/each}
      </tbody>
    </table>
  </div>
  <p class="muted small">Check means the App could not confirm that archfit/gate is required; ask an admin.</p>
  {#if body.truncated}
    <p class="muted small">Only the first repositories by name are listed.</p>
  {/if}
{/if}

<style>
  .repo-list { overflow: hidden; }
  td { vertical-align: top; overflow-wrap: anywhere; }
  .repository { width: 25%; }
  /* Each cell also holds a hidden label for the phone layout, so axe reads the link as running text: underline it. */
  td a { text-decoration: underline; text-underline-offset: 2px; }
  .reason { margin: 6px 0 0; max-width: 36ch; }
  .enforcement { display: flex; flex-wrap: wrap; align-items: center; gap: 6px; margin-top: 10px; }
  .mobile-label, .mobile-name { display: none; }

  @media (max-width: 640px) {
    .repo-list { border: 0; background: transparent; }
    table, tbody, tr, td { display: block; width: 100%; }
    .repository { width: 100%; }
    thead, .desktop-name { display: none; }
    tr { border: 1px solid var(--border); border-radius: var(--radius); margin-bottom: 16px; padding: 16px; background: var(--bg); }
    table.data td { border: 0; padding: 0 0 12px; }
    table.data td:last-child { padding-bottom: 0; }
    .mobile-label { display: block; font-size: 12px; color: var(--fg-muted); margin-bottom: 4px; }
    .mobile-name { display: block; }
  }
</style>

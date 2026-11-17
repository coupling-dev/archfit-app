<script lang="ts">
  import Skeleton from '../components/Skeleton.svelte';
  import StatusLabel from '../components/ui/StatusLabel.svelte';
  import PageHead from '../components/ui/PageHead.svelte';
  import { explain, type ApiError, type Repos } from '../lib/api';
  import { firstJSON } from '../lib/early';
  import { enforcementLabel, nextStep, repoStateLabel } from '../lib/repos';

  let { here }: { here: string } = $props();

  let body = $state<Repos | null>(null);
  let failure = $state('');

  firstJSON<Repos>('/api/repos').then(
    (b) => (body = b),
    (e: ApiError) => (failure = explain(e, here)),
  );
</script>

<PageHead title="Repositories">
  <p class="lede">Choose a repository to open its Home.</p>
</PageHead>

{#if failure}
  <p class="flash error" role="alert">{failure}</p>
{:else if !body}
  <Skeleton rows={4} />
{:else if body.repos.length === 0}
  <div class="box blankslate">
    <h2>No repository yet</h2>
    <p>No repository is available to you through an Archfit installation yet.</p>
  </div>
{:else}
  <div class="box repo-list">
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
              <StatusLabel label={enforcementLabel(row.enforcement)} />
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
    the App cannot read the rulesets. Ask a repository administrator.
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

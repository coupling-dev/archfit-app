<script lang="ts">
  import { getJSON, type Repos } from '../lib/api';
  import { repoPage } from '../lib/route';

  let body = $state<Repos | null>(null);
  let failure = $state('');

  getJSON<Repos>('/api/repos').then(
    (b) => (body = b),
    (e: Error) => (failure = e.message),
  );
</script>

<div class="page-head">
  <div>
    <h1>Repositories</h1>
    <p class="lede">The state of each default branch, from the last Archfit check on its head.</p>
  </div>
</div>

{#if failure}
  <p class="flash error" role="alert">{failure}</p>
{:else if !body}
  <p class="muted" aria-live="polite">Loading…</p>
{:else if body.repos.length === 0}
  <div class="box blankslate">
    <h3>No repository yet</h3>
    <p>No repository is available to you through an Archfit installation yet.</p>
  </div>
{:else}
  <div class="box table-wrap">
    <table class="data">
      <thead>
        <tr><th scope="col">Repository</th><th scope="col">Default branch</th><th scope="col">Reason</th></tr>
      </thead>
      <tbody>
        {#each body.repos as row (row.full_name)}
          <tr>
            <td><a href={repoPage(row.full_name)}>{row.full_name}</a></td>
            <td><span class="status {row.tone}">{row.state}</span></td>
            <td class="muted">{row.reason}</td>
          </tr>
        {/each}
      </tbody>
    </table>
  </div>
  {#if body.truncated}
    <p class="muted small">Only the first repositories by name are listed.</p>
  {/if}
{/if}

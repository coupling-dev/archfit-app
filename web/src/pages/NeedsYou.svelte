<script lang="ts">
  import PageHead from '../components/ui/PageHead.svelte';
  import Icon from '../components/ui/Icon.svelte';
  import { getJSON, type OwnerActions, type Repos } from '../lib/api';
  import { actionTitle, waitingFor } from '../lib/owner-actions';
  let { here }: { here: string } = $props();
  const installURL = 'https://github.com/apps/archfit-review/installations/new';
  let repos = $state<Repos | null>(null);
  getJSON<Repos>('/api/repos').then((b) => (repos = b), () => {});
  let owner = $state<OwnerActions | null>(null);
  let ownerFailed = $state(false);
  getJSON<OwnerActions>('/api/owner-actions').then((b) => (owner = b), () => (ownerFailed = true));
  let firstRun = $derived(!!repos && repos.repos.length === 0 && !!owner && !ownerFailed && owner.actions.length === 0);
  let oldest = $derived(waitingFor(owner?.actions.find((row) => row.since)?.since));
</script>

<PageHead title="Needs you">
  {#if owner?.actions.length}
    <p class="lede">{owner.actions.length} need you.{#if oldest} The oldest has waited {oldest}.{/if}</p>
  {:else if owner && !ownerFailed && !owner.unread && !owner.truncated && !firstRun}
    <p class="lede">Nothing needs you.</p>
  {/if}
</PageHead>
  {#if owner?.actions.length}
    <section class="box needs" aria-label="Needs you">
      {#each owner.actions as row, index (index)}
        {@const age = waitingFor(row.since)}
        <div class="box-row need">
          <span class="need-icon"><Icon name="clock" /></span>
          <div class="need-body">
            <span>{row.text}</span>
            <a class="muted need-repo" href={`/r/${row.repo}`}>{row.repo}</a>
          </div>
          {#if age}<span class="muted need-age">Waiting {age}</span>{/if}
          <a class:primary={row.kind !== 'exception' && row.kind !== 'drift'} class="btn need-act" href={row.href}>{actionTitle(row)}</a>
        </div>
      {/each}
    </section>
  {/if}
  {#if firstRun}
    <section class="box blankslate">
      <h2>Start checking pull requests</h2>
      <p>Install Archfit on GitHub, then follow four steps for each repository.</p>
      <ol>
        <li>Add the policy</li>
        <li>Open a pull request with both files</li>
        <li>Owner approval with a passkey</li>
        <li>Require the archfit/gate check</li>
      </ol>
      <a class="btn primary" href={installURL}>Install Archfit on GitHub</a>
    </section>
  {/if}
  {#if ownerFailed}
    <p class="muted small" role="alert">The App could not read the steps that wait for an owner.</p>
  {:else if owner?.unread}
    <p class="muted small" role="status">
      The App could not read the owner steps of {owner.unread} {owner.unread === 1 ? 'repository' : 'repositories'}.
    </p>
  {/if}
  {#if owner?.truncated}
    <p class="muted small" role="status">Owner steps are shown for the first repositories by name only.</p>
  {/if}
<p><a href="/repositories">All repositories</a></p>

<style>
  .needs { margin-bottom: 16px; }
  .need { display: flex; align-items: flex-start; gap: 10px; overflow-wrap: anywhere; }
  .need-icon { display: inline-flex; padding-top: 2px; color: var(--fg-muted); }
  .need-body { flex: 1; min-width: 0; display: flex; flex-wrap: wrap; gap: 2px 12px; }
  .need-repo { text-decoration: none; }
  .need-age { white-space: nowrap; }

  @media (max-width: 640px) {
    .need { flex-wrap: wrap; }
    .need-act { margin-left: 26px; }
    .need-age { margin-left: auto; }
    .need-act { flex-basis: calc(100% - 26px); }
    .need-body { flex-basis: calc(100% - 36px); }
  }
</style>

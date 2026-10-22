<script lang="ts">
  import Skeleton from '../components/Skeleton.svelte';
  import { explain, explainLoad, getJSON, postJSON, type AdminBody, type ApiError, type Installation, type Notice, type Session } from '../lib/api';

  // Admin is the operators' page: approve an account so Archfit runs for it, or revoke it. The server answers 404 to
  // anyone else, so a non-operator sees "Not found" here as on any unknown page.
  let { session, here }: { session: Session; here: string } = $props();

  let body = $state<AdminBody | null>(null);
  let failure = $state('');
  let notice = $state('');
  let busy = $state(0); // the installation a write is running for
  let confirming = $state(0); // the installation whose revoke asks once, inline

  function load() {
    getJSON<AdminBody>('/api/admin/installations').then(
      (b) => (body = b),
      (e: ApiError) => (failure = explainLoad(e, here)),
    );
  }
  load();

  async function act(row: Installation, action: 'approve' | 'revoke') {
    busy = row.id;
    failure = notice = '';
    try {
      const res = await postJSON<Notice>(`/api/admin/installations/${row.id}/${action}`, session.csrf, {});
      notice = res.notice ?? '';
      confirming = 0;
      load();
    } catch (e) {
      failure = explain(e as ApiError, here);
    } finally {
      busy = 0;
    }
  }

  const day = (iso: string) => new Date(iso).toLocaleDateString('en-GB', { day: 'numeric', month: 'long', year: 'numeric' });
  const repoCount = (row: Installation) => `${row.repos.length} ${row.repos.length === 1 ? 'repository' : 'repositories'}`;
</script>

<div class="page-head">
  <div>
    <h1>Installations</h1>
    <p class="lede">
      Approve an account to let Archfit run on its repositories. Until then nothing runs and nothing is posted.
    </p>
  </div>
</div>

{#if notice}<p class="flash success" role="status">{notice}</p>{/if}
{#if failure}<p class="flash error" role="alert">{failure}</p>{/if}

{#snippet who(row: Installation)}
  <span class="av" class:org={row.type === 'Organization'} aria-hidden="true">{row.account.slice(0, 1).toUpperCase()}</span>
{/snippet}

{#snippet repos(row: Installation)}
  {#if row.repos_error}
    <div class="muted small">The repositories could not be read.</div>
  {:else if row.repos.length}
    <ul class="chips" aria-label="Repositories of {row.account}">
      {#each row.repos as r (r.name)}
        <li class="chip" class:priv={r.private}>{r.name}{r.private ? ' · private' : ''}</li>
      {/each}
    </ul>
  {/if}
{/snippet}

{#if !body}
  {#if !failure}<Skeleton rows={4} />{/if}
{:else}
  {#if body.unreadable}
    <p class="flash warn">
      The approval of {body.unreadable} installation{body.unreadable === 1 ? '' : 's'} could not be read. They are listed
      as waiting, and Archfit does not run for them. Try again in a minute.
    </p>
  {/if}

  <section class="box" aria-labelledby="waiting">
    <div class="box-head"><h2 id="waiting">Waiting <span class="counter">{body.waiting.length}</span></h2></div>
    {#each body.waiting as row (row.id)}
      <div class="box-row acct">
        {@render who(row)}
        <div>
          <strong><a href="https://github.com/{row.account}" rel="noreferrer">{row.account}</a></strong>
          <span class="label">{row.type}</span>
          <span class="muted small">· installed {day(row.installed_at)}</span>
          {@render repos(row)}
        </div>
        <button class="btn sm primary" type="button" disabled={busy !== 0} onclick={() => act(row, 'approve')}>
          Approve<span class="sr-only"> {row.account}</span>
        </button>
      </div>
    {:else}
      <p class="box-row muted">No installation is waiting.</p>
    {/each}
  </section>

  <section class="box" aria-labelledby="approved">
    <div class="box-head"><h2 id="approved">Approved <span class="counter">{body.approved.length}</span></h2></div>
    {#each body.approved as row (row.id)}
      <div class="box-row acct">
        {@render who(row)}
        <div>
          <strong>{row.account}</strong>
          <span class="label">{row.type}</span>
          {#if row.auto}<span class="label success">auto</span>{/if}
          <div class="muted small">
            {#if row.auto}From the App configuration{:else}Approved by {row.approved_by} on {day(row.approved_at ?? '')}{/if}
            · {repoCount(row)}
          </div>
          {@render repos(row)}
        </div>
        {#if row.auto}
          <span></span>
        {:else if confirming === row.id}
          <div class="row">
            <span class="small">Revoke {row.account}? Checks stop on its next event.</span>
            <button class="btn sm danger" type="button" disabled={busy !== 0} onclick={() => act(row, 'revoke')}>Revoke</button>
            <button class="btn sm" type="button" onclick={() => (confirming = 0)}>Cancel</button>
          </div>
        {:else}
          <button class="btn sm danger" type="button" onclick={() => (confirming = row.id)}>
            Revoke<span class="sr-only"> {row.account}</span>
          </button>
        {/if}
      </div>
    {:else}
      <p class="box-row muted">None.</p>
    {/each}
    <div class="box-foot">
      Revoke stops Archfit from the account's next event on. Checks already on GitHub stay. Accounts marked "auto" change
      by pull request.
    </div>
  </section>
{/if}

<script lang="ts">
  import { ApiError, explain, getJSON, postJSON, repoAPI, type DataSharing, type Session } from '../lib/api';
  import { repoPage, type Repo } from '../lib/route';
  import { allowedLine, checkWords } from '../lib/sharing';

  let { route, session, here }: { route: Repo; session: Session; here: string } = $props();

  let body = $state<DataSharing | null>(null);
  let failure = $state('');
  let notice = $state('');
  let busy = $state('');

  const api = () => repoAPI(route.owner, route.name, 'data-sharing');

  // load shows the state on the server; a page that cannot read it shows no state and no button.
  function load(): Promise<void> {
    return getJSON<DataSharing>(api()).then(
      (b) => {
        body = b;
      },
      (e: ApiError) => {
        body = null;
        failure ||= explain(e, here);
      },
    );
  }
  load();

  async function change(kind: string, action: 'allow' | 'revoke') {
    busy = kind;
    failure = notice = '';
    try {
      body = await postJSON<DataSharing>(api(), session.csrf, { kind, action });
      notice = body.notice ?? '';
    } catch (e) {
      // A failed write may still have changed the record (it was saved but not read back): read it again.
      failure = explain(e as ApiError, here);
      await load();
    } finally {
      busy = '';
    }
  }

  const marks = { met: '✓', not_met: '✗', unknown: '?' } as const;
</script>

<div class="page-head">
  <div class="stack-sm">
    <h1>Data sharing</h1>
    <p class="muted">
      Archfit runs in your CI: your source code and reports stay there. Each managed feature below sends data out of
      your environment, and stays off until an administrator allows it.
    </p>
  </div>
</div>

{#if notice}<p class="flash success" role="status">{notice}</p>{/if}
{#if failure}<p class="flash error" role="alert">{failure}</p>{/if}

{#if body}
  <p>
    A choice here applies to every repository of <strong>{body.owner}</strong> that the Archfit App can access.
  </p>
  {#each body.features as f (f.kind)}
    <section class="box" aria-labelledby="feature-{f.kind}">
      <div class="box-head">
        <h2 id="feature-{f.kind}">{f.title}</h2>
        <span class="status {f.allowed ? 'success' : 'neutral'}">{f.allowed ? 'allowed' : 'not allowed'}</span>
      </div>
      <div class="box-body stack">
        <div class="stack-sm">
          <h3 class="small">What leaves your environment</h3>
          <p>{f.leaves}</p>
        </div>
        {#if f.note}<p class="muted">{f.note}</p>{/if}
        <ul class="checks" aria-label="What {f.title} needs">
          {#each f.checks as c (c.label)}
            <li class={c.state}>
              <span class="mark" aria-hidden="true">{marks[c.state]}</span>
              {c.label}
              <span class="sr-only">({checkWords[c.state]})</span>
            </li>
          {/each}
        </ul>
      </div>
      <div class="box-foot row-between">
        <span>{allowedLine(f)}</span>
        {#if f.allowed}
          <button class="btn sm" type="button" disabled={busy !== ''} onclick={() => change(f.kind, 'revoke')}>
            Revoke
          </button>
        {:else}
          <button class="btn sm primary" type="button" disabled={busy !== ''} onclick={() => change(f.kind, 'allow')}>
            Allow {f.title}
          </button>
        {/if}
      </div>
    </section>
  {/each}
{:else if !failure}
  <p class="muted" aria-live="polite">Loading…</p>
{/if}

<p><a href={repoPage(route.fullName)}>Back to {route.fullName}</a></p>

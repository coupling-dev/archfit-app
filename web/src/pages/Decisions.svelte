<script lang="ts">
  import Skeleton from '../components/Skeleton.svelte';
  import { explainLoad, repoAPI, type ApiError, type DecisionRow, type Inbox, type PolicyChangeRow, type Tone } from '../lib/api';
  import { ageWord } from '../lib/age';
  import { firstJSON } from '../lib/early';
  import { safeHref, type Repo } from '../lib/route';

  let { route, here }: { route: Repo; here: string } = $props();

  let body = $state<Inbox | null>(null);
  let failure = $state('');

  function load() {
    firstJSON<Inbox>(repoAPI(route.owner, route.name, 'decisions')).then(
      (b) => (body = b),
      (e: ApiError) => (failure = explainLoad(e, here)),
    );
  }
  load();

  const statusTone: Record<DecisionRow['status'], Tone> = {
    pending: 'attention',
    approved: 'success',
    declined: 'neutral',
    stale: 'neutral',
  };
  const approvalTone: Record<PolicyChangeRow['approval'], Tone> = {
    granted: 'success',
    missing: 'attention',
    not_needed: 'neutral',
    unknown: 'neutral',
  };
  const approvalWord = (p: PolicyChangeRow) =>
    ({ granted: p.by ? `approved by @${p.by}` : 'approved', missing: 'approval needed', not_needed: 'no approval needed', unknown: 'cannot tell' })[
      p.approval
    ];
</script>

<div class="page-head">
  <div class="stack-sm">
    <h1>Decisions</h1>
    <p class="lede">
      What waits for an architecture owner: decision requests from unattended agents, and pull requests that change the
      policy. An agent never approves its own request. Read-only: the owner decides on the pull request page.
    </p>
  </div>
</div>

{#if failure}
  <p class="flash error" role="alert">{failure}</p>
{:else if !body}
  <Skeleton rows={4} />
{:else}
  <section class="box" aria-labelledby="requests">
    <div class="box-head"><h2 id="requests">Decision requests <span class="counter">{body.requests.length}</span></h2></div>
    {#if body.requests_error}
      <p class="box-row muted">The decision requests could not be read. Try again in a minute.</p>
    {:else if !body.requests.length}
      <div class="blankslate">
        <h3>No decision request</h3>
        <p>
          When an unattended agent is blocked by a finding that code cannot fix, it asks on the pull request. The request
          appears here until an owner decides.
        </p>
      </div>
    {:else}
      <div class="table-wrap">
        <table class="data cards">
          <thead>
            <tr>
              <th scope="col">Status</th>
              <th scope="col">Pull request</th>
              <th scope="col">Rule</th>
              <th scope="col">Dependency</th>
              <th scope="col">Requested by</th>
              <th scope="col">Commit</th>
              <th scope="col">Age</th>
            </tr>
          </thead>
          <tbody>
            {#each body.requests as r (r.href + r.finding + r.created)}
              <tr>
                <td><span class="status {statusTone[r.status] ?? 'neutral'}">{r.status}</span></td>
                <td data-label="Pull request">
                  <a href={safeHref(r.href)}>#{r.pull}{r.title ? ' ' + r.title : ''}</a>
                  <span class="muted small mono" title="Finding">{r.finding}</span>
                </td>
                <td data-label="Rule"><code>{r.rule}</code></td>
                <td data-label="Dependency">{r.edge}</td>
                <td data-label="Requested by">@{r.requester}</td>
                <td data-label="Commit"><code>{r.head}</code></td>
                <td data-label="Age" title={r.created}>{ageWord(r.created)}</td>
              </tr>
            {/each}
          </tbody>
        </table>
      </div>
      {#if body.requests_more}
        <p class="box-foot">{body.requests_more} more pending requests are not listed here.</p>
      {/if}
    {/if}
  </section>

  <section class="box" aria-labelledby="policy-changes">
    <div class="box-head">
      <h2 id="policy-changes">Policy changes <span class="counter">{body.policy_changes.length}</span></h2>
    </div>
    {#if body.policy_error}
      <p class="box-row muted">The open pull requests could not be read. Try again in a minute.</p>
    {:else if !body.policy_changes.length}
      <div class="blankslate">
        <h3>No policy change</h3>
        <p>No open pull request changes the protected policy. One that does appears here, with the owner approval it needs.</p>
      </div>
    {:else}
      {#each body.policy_changes as p (p.number)}
        <div class="box-row stack-sm">
          <div class="row-between">
            <a href={safeHref(p.href)}>#{p.number} {p.title}</a>
            <span class="status {approvalTone[p.approval] ?? 'neutral'}">{approvalWord(p)}</span>
          </div>
          <p class="small">{p.headline}</p>
        </div>
      {/each}
    {/if}
    {#if body.policy_more}
      <p class="box-foot">{body.policy_more} more open pull requests are not read here.</p>
    {/if}
  </section>
{/if}

<script lang="ts">
  import { explain, postJSON, repoAPI, type ApiError, type Decided, type DecisionRequest, type Session } from '../lib/api';
  import { decisionPath, offersDecision, statusTone, statusWord } from '../lib/decisions';

  let {
    requests,
    number,
    fullName,
    session,
    here,
    onchange,
    onfailed,
  }: {
    requests: DecisionRequest[];
    number: number;
    fullName: string;
    session: Session;
    here: string;
    onchange: (list: DecisionRequest[]) => void;
    onfailed: () => void; // the write was refused or failed: read the page again
  } = $props();

  let deciding = $state(false);
  let notice = $state('');
  let error = $state('');

  // decide names the exact request the page showed; the server checks who may decide and that it still holds.
  async function decide(r: DecisionRequest, action: 'approve' | 'decline') {
    deciding = true;
    notice = error = '';
    try {
      const [owner, name] = fullName.split('/');
      const res = await postJSON<Decided>(repoAPI(owner, name, decisionPath(r, number, action)), session.csrf, {});
      onchange(res.decisions);
      notice = res.notice;
    } catch (e) {
      error = explain(e as ApiError, here);
      onfailed();
    } finally {
      deciding = false;
    }
  }
</script>

<section class="box" aria-labelledby="decision-requests">
  <div class="box-head">
    <h2 id="decision-requests">Decision requests <span class="counter">{requests.length}</span></h2>
  </div>
  <div class="box-body stack-sm">
    <p class="muted small">
      An agent that a finding blocked asked the architecture owners for a policy change. Its words are quoted below, not
      verified. Approving opens a draft pull request with the proposed policy; an architecture owner approves that like
      any policy change. The agent cannot approve its own request.
    </p>
    <ul class="plain" aria-label="Decision requests on this pull request">
      {#each requests as r (r.finding_id + r.policy_digest)}
        <li class="stack-sm">
          <div>
            <strong>{r.rule_id}</strong>: <code>{r.from}</code> to <code>{r.to}</code>
            <span class="status {statusTone(r)}">{statusWord(r)}</span>
          </div>
          {#if r.proposal_pr}
            <p>
              {#if r.proposal_url}
                <a href={r.proposal_url} rel="noreferrer">Proposal PR #{r.proposal_pr}</a>
              {:else}
                Proposal PR #{r.proposal_pr}
              {/if}
            </p>
          {/if}
          <p class="muted small">Requested by @{r.requester}. Finding <code>{r.finding_id.slice(0, 8)}</code>.</p>
          <blockquote class="quote">{r.why}</blockquote>
          {#if r.alternatives.length}
            <p class="small">Alternatives the agent says it tried:</p>
            <ul class="plain small">
              {#each r.alternatives as alt, i (i)}
                <li class="quote">{alt}</li>
              {/each}
            </ul>
          {/if}
          {#if offersDecision(r)}
            <div class="row">
              <button class="btn sm primary" type="button" disabled={deciding} onclick={() => decide(r, 'approve')}>
                Approve and open a proposal pull request
              </button>
              <button class="btn sm" type="button" disabled={deciding} onclick={() => decide(r, 'decline')}>Decline</button>
            </div>
          {/if}
        </li>
      {/each}
    </ul>
    {#if notice}<p class="flash success" role="status">{notice}</p>{/if}
    {#if error}<p class="flash error" role="alert">{error}</p>{/if}
  </div>
</section>

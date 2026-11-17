<script lang="ts">
  import { repoPage, type Repo } from '../lib/route';
  import { setupRuleHelp } from '../lib/setup-rules';
  import { approvalNeedsReview, ownerReviewLabel, homeHref, setupChipClass, setupPullStep, setupRuleStatus, setupStepState, type HomeSetup } from '../lib/home';

  let { setup, repo, showOwnerAction = true }: { setup: HomeSetup; repo: Repo; showOwnerAction?: boolean } = $props();
  const steps = [
    { title: 'Add the policy', text: 'Your architecture rules live in .archfit.yaml, in the repository.' },
    { title: 'Policy and workflow pull request', text: 'Rules reach the default branch through review, so the approval binds to exact file contents.' },
    { title: 'Owner approval', text: 'The owner signs the policy with a passkey. Change one byte and the approval no longer counts.' },
    { title: 'Require the gate', text: 'In GitHub, require archfit/gate on the default branch. The App reads these settings; an administrator changes them.' },
  ];
  const passkeyReason = 'The gate protects the rules themselves. A password or a GitHub review can be replayed by a compromised account, and in a one-person repository a self-review proves nothing. A passkey stays on your device, works only on this site, and signs the exact bytes of the policy and workflow. Any later edit needs a new approval.';
  let state = $derived(setupStepState(setup));
  let done = $derived(state.done);
  let completed = $derived(state.completed);
  let reviewHref = $derived(approvalNeedsReview(setup) && setup.owner_action?.href ? homeHref(repo, setup.owner_action.href) : undefined);
  let pullStep = $derived(setupPullStep(setup));
  let approvalHref = $derived(setup.approval.href ? homeHref(repo, setup.approval.href) : undefined);
  let githubRepo = $derived(`https://github.com/${encodeURIComponent(repo.owner)}/${encodeURIComponent(repo.name)}`);
</script>

<section class="box setup" aria-labelledby="setup-title">
  <div class="box-head"><div><h2 class="title-box" id="setup-title">Set up Archfit</h2><p>{state.next}</p><p>Policy approval and required-check settings are separate. Complete the applicable steps below.</p></div></div>
  <div class="box-body">
    <div class="progress" aria-label="{completed} of 4 steps done"><span>{completed} of 4 done</span><span class="bar" aria-hidden="true"><span style="width: {completed * 25}%"></span></span></div>
    <ol class="steps">
      {#each steps as step, i (step.title)}
        {@const status = i === 0 ? done[i] ? 'Done' : 'Action needed' : i === 1 ? pullStep.status : i === 2 ? state.approval : state.gate}
        <li class:done={done[i]} class:now={!done[i] && (i === 0 || done[i - 1])}>
          <span class="number" aria-hidden="true">{done[i] ? '✓' : i + 1}</span>
          <div class="step-copy"><h3 class="title-box">{i + 1} · {step.title}</h3><p>{step.text}</p></div>
          <span class="chip" class:good={setupChipClass(status) === 'good'} class:attention={setupChipClass(status) === 'attention'} class:neutral={setupChipClass(status) === 'neutral'}>{status}</span>
          <div class="step-body">
            {#if i === 0}
              {#if setup.policy}<p>The policy is on the default branch. <a href={repoPage(repo.fullName, '/policy')}>View the policy</a></p>
              {:else if setup.first_pull}<p>A version 3 policy is proposed in PR #{setup.first_pull}; it is not yet on the default branch.</p>
              {:else}<p>The default branch has no version 3 policy.</p><pre class="command"><code>archfit init</code></pre><p>This creates the policy (or converts v2 files) and adds its agent block to AGENTS.md. It does not write the workflow.</p>{/if}
              {#if !setup.policy && !setup.first_pull}<p>Commit the policy and workflow, then open a pull request.</p>{/if}
            {:else if i === 1}
              {#if pullStep.kind === 'review'}
                <p><a href={repoPage(repo.fullName, '/pull/' + pullStep.number)}>PR #{pullStep.number}</a> adds the workflow.</p>
                <a class="btn" href={`${githubRepo}/pull/${pullStep.number}`}>View PR #{pullStep.number} on GitHub</a>
              {:else if pullStep.kind === 'first-policy'}
                <p>PR #{pullStep.number} proposes the first policy and workflow.</p>
                <a class="btn" href={repoPage(repo.fullName, '/pull/' + pullStep.number)}>View PR #{pullStep.number}</a>
              {:else if pullStep.kind === 'current'}
                <p>The policy and workflow are present on the default branch. No setup pull request is needed.</p>
                <a class="btn" href={repoPage(repo.fullName, '/policy')}>View policy</a>
              {:else}
                <p>Open one pull request that adds the missing policy or workflow.</p>
                <a class="btn" href={`${githubRepo}/compare`}>Create a pull request on GitHub</a>
              {/if}
              {#if !setup.workflow && setup.open_pull === 0 && !setup.first_pull}<p>The pull request should also add <code>.github/workflows/archfit.yaml</code>.</p>{/if}
            {:else if i === 2}
              <p>{state.approvalText}</p>
              {#if approvalHref}<p><a href={approvalHref}>View PR #{setup.approval.pull_request} and approval record</a></p>{/if}
              {#if approvalNeedsReview(setup) && setup.owner_action}
                {#if reviewHref}{#if showOwnerAction}<a class="btn" href={reviewHref}>{ownerReviewLabel(setup)}</a>{/if}
                {:else}<p>{setup.owner_action.label}</p>{/if}
              {/if}
              {#if !setup.passkey_known}
                <p>Could not read your passkeys. Review is still available; try again before approving.</p>
              {:else if !setup.passkey && approvalNeedsReview(setup)}
                <a class="btn" href={repoPage(repo.fullName, '/passkeys')}>Register a passkey to approve</a>
                <p>A passkey is needed to approve, but not to read the review.</p>
              {:else if setup.passkey}
                <p><a href={repoPage(repo.fullName, '/passkeys')}>Manage passkeys</a></p>
              {/if}
              <details><summary>Why a passkey?</summary><p>{passkeyReason}</p></details>
            {:else}
              <p>This step turns green when GitHub requires pull requests and archfit/gate on the default branch. Bypass and policy-push advice does not block this step; Verify yourself means the App has not checked that setting.</p>
              <div class="rules">
                <div class="rule-actions"><a class="btn" href={`${githubRepo}/settings/rules`}>Open ruleset settings</a><button class="btn" onclick={() => window.location.reload()}>Check</button></div>
                <p>After saving changes on GitHub, select Check to read the settings again. Check does not change any GitHub settings.</p>
                {#each setup.rules as rule, ri (`${rule.code}-${ri}`)}
                  {@const help = setupRuleHelp(rule, repo)}
                  <div class="rule">
                    <div class="rule-head">
                      <h4 class="title-box">{help.title}</h4>
                      <span class="chip" class:good={rule.state === 'ok'} class:attention={rule.state !== 'ok' && rule.state !== 'not_applicable'} class:neutral={rule.state === 'not_applicable'}>{setupRuleStatus(rule.state, rule.code)}</span>
                    </div>
                    <p>{help.explanation}</p>
                    {#each help.instructions as instruction (instruction)}<p>{instruction}</p>{/each}
                    <div class="rule-links">
                      {#each help.links as link (link.href)}<a href={link.href}>{link.label}</a>{/each}
                    </div>
                  </div>
                {:else}<p>No ruleset settings are available.</p>{/each}
              </div>
            {/if}
          </div>
        </li>
      {/each}
    </ol>
  </div>
</section>

<style>
  .box-head p { margin: 4px 0 0; color: var(--fg-muted); }
  .progress { display: flex; align-items: center; gap: 10px; margin-bottom: 16px; color: var(--fg-muted); font-size: 13px; }
  .bar { flex: 1; height: 6px; background: var(--border); border-radius: 999px; overflow: hidden; }
  .bar span { display: block; height: 100%; background: var(--success); }
  .steps { list-style: none; padding: 0; margin: 0; }
  .steps li { display: grid; grid-template-columns: 34px minmax(0, 1fr) auto; gap: 12px; padding: 14px 0; border-top: 1px solid var(--border); }
  .number { width: 28px; height: 28px; border: 2px solid var(--border); border-radius: 50%; display: grid; place-items: center; color: var(--fg-muted); }
  .done .number { background: var(--success-emphasis); border-color: var(--success-emphasis); color: var(--fg-onEmphasis); }
  .step-copy h3 { margin: 0 0 4px; }
  .step-copy p, .step-body p { margin: 0; color: var(--fg-muted); }
  .chip { align-self: start; border: 1px solid var(--border); border-radius: 999px; padding: 3px 9px; font-size: 12px; font-weight: 600; background: var(--bg-subtle); }
  .chip.good { background: var(--success-subtle); color: var(--success); border-color: var(--success-border); }
  .chip.attention { background: var(--attention-subtle); color: var(--attention); border-color: var(--attention-border); }
  .chip.neutral { background: var(--neutral-subtle); color: var(--fg-muted); }
  .step-body { grid-column: 2 / 4; margin-top: 4px; display: flex; flex-direction: column; gap: 8px; }
  .step-body > .btn { align-self: flex-start; }
  details { margin-top: 8px; }
  summary { cursor: pointer; }
  .step-body details p { margin-top: 8px; }
  .command { margin: 8px 0; padding: 10px 12px; border-radius: var(--radius); background: var(--bg-subtle); overflow-x: auto; }
  .rules { border-top: 1px solid var(--border); margin-top: 8px; }
  .rule { padding: 14px 0; border-bottom: 1px dashed var(--border); overflow-wrap: anywhere; }
  .rule-head, .rule-actions { display: flex; align-items: baseline; flex-wrap: wrap; gap: 8px 12px; }
  .rule-head { justify-content: space-between; }
  .rule h4 { margin: 0; }
  .rule-links { display: flex; flex-wrap: wrap; gap: 6px 16px; margin-top: 10px; font-size: 13px; }
  .rule-head .chip { flex-shrink: 0; }
  .rule:last-child { border-bottom: 0; }
  .rule p { font-size: 13px; margin-top: 3px; }
  @media (max-width: 640px) { .steps li { grid-template-columns: 30px minmax(0, 1fr); } .steps li > .chip { grid-column: 2; grid-row: 2; justify-self: start; } .step-body { grid-column: 2; } }
</style>

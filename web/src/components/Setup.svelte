<script lang="ts">
  import { repoPage, type Repo } from '../lib/route';
  import { setupPasskeyActions, setupPullStep, setupRuleStatus, setupStepState, type HomeSetup } from '../lib/home';

  let { setup, repo }: { setup: HomeSetup; repo: Repo } = $props();
  const steps = [
    { title: 'Add the policy', text: 'Your architecture rules live in .archfit.yaml, in the repository.' },
    { title: 'Open a pull request', text: 'Rules reach the default branch through review, so the approval binds to exact file contents.' },
    { title: 'Approve the first policy', text: 'The owner signs the policy with a passkey. Change one byte and the approval no longer counts.' },
    { title: 'Require the gate', text: 'Turn on the ruleset so the default branch cannot change unless archfit/gate passes.' },
  ];
  const passkeyReason = 'The gate protects the rules themselves. A password or a GitHub review can be replayed by a compromised account, and in a one-person repository a self-review proves nothing. A passkey stays on your device, works only on this site, and signs the exact bytes of the policy and workflow. Any later edit needs a new approval.';
  let state = $derived(setupStepState(setup));
  let done = $derived(state.done);
  let completed = $derived(state.completed);
  let passkeyActions = $derived(setupPasskeyActions(setup));
  let pullStep = $derived(setupPullStep(setup));
  let githubRepo = $derived(`https://github.com/${encodeURIComponent(repo.owner)}/${encodeURIComponent(repo.name)}`);
  function ruleExplanation(rule: HomeSetup['rules'][number]): string {
    if (rule.state === 'unknown' && rule.code === 'administrator_bypass') return 'The App cannot read administrator bypass without Administration permission. An admin must confirm the bypass list is intentional.';
    if (rule.state === 'not_applicable') return 'Not available: this applies only to organization-owned private or internal repositories.';
    return rule.text;
  }
</script>

<section class="box setup" aria-labelledby="setup-title">
  <div class="box-head"><div><h2 id="setup-title">Set up Archfit</h2><p>Four steps to enforcement. Until step 4, Archfit only advises.</p></div></div>
  <div class="box-body">
    <div class="progress" aria-label="{completed} of 4 steps done"><span>{completed} of 4 done</span><span class="bar" aria-hidden="true"><span style="width: {completed * 25}%"></span></span></div>
    <ol class="steps">
      {#each steps as step, i (step.title)}
        {@const status = i === 0 ? done[i] ? 'Done' : 'Action needed' : i === 1 ? pullStep.status : i === 2 ? state.approval : state.gate}
        <li class:done={done[i]} class:now={!done[i] && (i === 0 || done[i - 1])}>
          <span class="number" aria-hidden="true">{done[i] ? '✓' : i + 1}</span>
          <div class="step-copy"><h3>{i + 1} · {step.title}</h3><p>{step.text}</p></div>
          <span class="chip" class:good={done[i]}>{status}</span>
          <div class="step-body">
            {#if i === 0}
              {#if setup.policy}<p>The policy is on the default branch. <a href={repoPage(repo.fullName, '/policy')}>View the policy</a></p>
              {:else if setup.first_pull}<p>The policy is in the first pull request.</p>
              {:else}<pre class="command"><code>archfit init</code></pre><p>This creates the policy (or converts v2 files) and adds its agent block to AGENTS.md. It does not write the workflow.</p>{/if}
              {#if !setup.policy && !setup.first_pull}<p>Commit the policy and workflow, then open a pull request.</p>{/if}
            {:else if i === 1}
              {#if pullStep.kind === 'review'}
                <p><a href={repoPage(repo.fullName, '/pull/' + pullStep.number)}>PR #{pullStep.number}</a> adds the workflow — review it.</p>
                <a class="btn ghost" href={`${githubRepo}/pull/${pullStep.number}`}>Open PR #{pullStep.number}</a>
              {:else if pullStep.kind === 'first-policy'}
                <p>First policy: #{setup.first_pull}, open and waiting for approval.</p>
                <a class="btn ghost" href={repoPage(repo.fullName, '/pull/' + setup.first_pull)}>Open the first pull request</a>
              {:else}
                <p>Open one pull request that adds the policy and workflow.</p>
                <a class="btn ghost" href={`${githubRepo}/compare`}>Open a pull request on GitHub</a>
              {/if}
              {#if !setup.workflow}<p>The pull request should also add <code>.github/workflows/archfit.yaml</code>.</p>{/if}
            {:else if i === 2}
              {#if passkeyActions.register}
                <a class="btn primary" href={repoPage(repo.fullName, '/passkeys')}>Register a passkey</a>
                {#if passkeyActions.approvalBlocked}<a class="btn ghost disabled" aria-disabled="true" tabindex="-1" href={repoPage(repo.fullName, '/owner/first' + (setup.first_pull ? '?pull=' + setup.first_pull : ''))} onclick={(event) => event.preventDefault()}>Approve the first policy</a><p>Register a passkey first.</p>{/if}
              {:else}
                {#if passkeyActions.approve}<a class="btn primary" href={repoPage(repo.fullName, '/owner/first' + (setup.first_pull ? '?pull=' + setup.first_pull : ''))}>Approve the first policy</a>{/if}
                <a class="btn ghost" href={repoPage(repo.fullName, '/passkeys')}>Manage passkeys</a>
              {/if}
              <details><summary>Why a passkey?</summary><p>{passkeyReason}</p></details>
            {:else}
              <div class="rules">
                <a class="btn ghost" href={`${githubRepo}/settings/rules`}>Open ruleset settings</a>
                {#each setup.rules as rule, ri (`${rule.code}-${ri}`)}
                  <div class="rule"><div><div class="rule-title">{rule.code === 'pull_requests' ? 'Default-branch changes require a pull request' : rule.code === 'gate_check' ? "The App’s archfit/gate check is required" : rule.code === 'administrator_bypass' ? 'Administrator bypass' : rule.code === 'policy_push' ? 'Policy-push restriction' : rule.text}</div>{#if rule.state !== 'ok'}<p>{ruleExplanation(rule)}</p>{/if}</div><span class="chip" class:good={rule.state === 'ok'}>{setupRuleStatus(rule.state)}</span></div>
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
  .setup { margin-top: 16px; }
  .setup a { text-decoration: underline; text-underline-offset: 2px; }
  .box-head p { margin: 4px 0 0; color: var(--fg-muted); }
  .progress { display: flex; align-items: center; gap: 10px; margin-bottom: 16px; color: var(--fg-muted); font-size: 13px; }
  .bar { flex: 1; height: 6px; background: var(--border); border-radius: 999px; overflow: hidden; }
  .bar span { display: block; height: 100%; background: var(--success); }
  .steps { list-style: none; padding: 0; margin: 0; }
  .steps li { display: grid; grid-template-columns: 34px minmax(0, 1fr) auto; gap: 12px; padding: 14px 0; border-top: 1px solid var(--border); }
  .number { width: 28px; height: 28px; border: 2px solid var(--border); border-radius: 50%; display: grid; place-items: center; color: var(--fg-muted); }
  .done .number { background: var(--success); color: white; }
  .step-copy h3 { margin: 0 0 3px; font-size: 15px; }
  .step-copy p, .step-body p { margin: 0; color: var(--fg-muted); }
  .chip { align-self: start; border-radius: 999px; padding: 3px 9px; font-size: 12px; font-weight: 600; background: var(--bg-subtle); }
  .chip.good { background: var(--success-subtle); color: var(--success); }
  .step-body { grid-column: 2 / 4; margin-top: 4px; }
  .step-body p + p { margin-top: 8px; }
  .btn { display: inline-block; margin: 4px 8px 4px 0; }
  .btn.disabled { opacity: .55; cursor: not-allowed; }
  details { margin-top: 8px; }
  summary { cursor: pointer; }
  details p { margin-top: 8px !important; }
  .command { margin: 8px 0; padding: 10px 12px; border-radius: var(--radius); background: var(--bg-subtle); overflow-x: auto; }
  .rules { border-top: 1px solid var(--border); margin-top: 8px; }
  .rule { display: grid; grid-template-columns: minmax(0, 1fr) auto; gap: 12px; padding: 9px 0; border-bottom: 1px dashed var(--border); }
  .rule:last-child { border-bottom: 0; }
  .rule p { font-size: 13px; margin-top: 3px; }
  @media (max-width: 520px) { .steps li { grid-template-columns: 30px minmax(0, 1fr); } .steps li > .chip { grid-column: 2; grid-row: 2; justify-self: start; } .step-body { grid-column: 2; } }
</style>

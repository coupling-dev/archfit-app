<script lang="ts">
  import Disclosure from './ui/Disclosure.svelte';
  import Diff from './ui/Diff.svelte';
  import StatusLabel from './ui/StatusLabel.svelte';
  import { explain, postJSON, repoAPI, type ApiError, type Answered, type Session } from '../lib/api';
  import { confirm } from '../lib/passkey';
  import { passkeyFailure } from '../lib/webauthn';
  import { repoPage, type Repo } from '../lib/route';
  import { setupRuleHelp } from '../lib/setup-rules';
  import { approvalNeedsReview, ownerReviewLabel, homeHref, setupCanRequireGate, setupPullStep, setupRuleStatus, setupStepChip, setupStepState, type HomeSetup } from '../lib/home';

  let { setup, repo, showOwnerAction = true, primaryHref = '', session, here }: { setup: HomeSetup; repo: Repo; showOwnerAction?: boolean; primaryHref?: string; session: Session; here: string } = $props();
  const steps = [
    { title: 'Add the policy', text: 'Your architecture rules live in .archfit.yaml, in the repository.' },
    { title: 'Open a pull request with both files', text: 'Rules reach the default branch through review, so the approval binds to exact file contents.' },
    { title: 'Owner approval', text: 'The owner approves the policy with a passkey. The approval covers these exact files; any later edit needs a new approval.' },
    { title: 'Require the archfit/gate check', text: 'GitHub does not merge a change until archfit/gate passes on the default branch.' },
  ];
  const passkeyReason = 'The check protects the rules themselves. A password or a GitHub review can be replayed by a compromised account, and in a one-person repository a self-review proves nothing. A passkey stays on your device, works only on this site, and approves the exact bytes of the policy and workflow. Any later edit needs a new approval.';
  let progress = $derived(setupStepState(setup));
  let done = $derived(progress.done);
  let completed = $derived(progress.completed);
  let active = $derived(done.indexOf(false));
  // Complete means every step is done and the approval is active: nothing left for the owner to do.
  let complete = $derived(completed === 4 && setup.approval.status === 'active');
  let reviewHref = $derived(approvalNeedsReview(setup) && setup.owner_action?.href ? homeHref(repo, setup.owner_action.href) : undefined);
  let pullStep = $derived(setupPullStep(setup));
  let approvalHref = $derived(setup.approval.href ? homeHref(repo, setup.approval.href) : undefined);
  let githubRepo = $derived(`https://github.com/${encodeURIComponent(repo.owner)}/${encodeURIComponent(repo.name)}`);
  let rules = $derived(setup.rules.filter((rule) => !(rule.code === 'policy_push' && rule.state === 'not_applicable')));
  // The required-gate flow: preview the App's change, approve it with the passkey, then show the
  // pull request. The App computes the policy; this page never sends policy bytes.
  const api = (suffix: string) => repoAPI(repo.owner, repo.name, suffix);
  let requiredPreview = $state<{ digest: string; diff: string; against?: string } | null>(null);
  let requiredBusy = $state(false);
  let requiredError = $state('');
  let requiredDone = $state(false);
  let requiredPull = $state<number | null>(null);
  let requiredNotice = $state('');

  async function previewRequired() {
    requiredBusy = true; requiredError = '';
    try {
      const p = await postJSON<{ digest?: string; diff: string; against?: string }>(api('setup/required/preview'), session.csrf, {});
      if (typeof p.diff !== 'string' || !/^sha256:[0-9a-f]{64}$/.test(p.digest ?? '')) throw new Error('The preview could not be read.');
      requiredPreview = { digest: p.digest!, diff: p.diff, ...(p.against === undefined ? {} : { against: p.against }) };
    } catch (e) { requiredError = explain(e as ApiError, here); }
    finally { requiredBusy = false; }
  }

  async function applyRequired() {
    const p = requiredPreview;
    if (!p) return;
    requiredBusy = true; requiredError = '';
    try {
      const token = await confirm(api, session.csrf, { action: 'make-required', digest: p.digest });
      const out = await postJSON<Answered>(api('setup/required'), session.csrf, { token });
      requiredDone = true;
      requiredPull = out.pull_request ?? null;
      requiredNotice = out.notice;
      requiredPreview = null;
    } catch (e) { requiredError = passkeyFailure(e) ?? explain(e as ApiError, here); }
    finally { requiredBusy = false; }
  }
</script>

{#snippet stepBody(i: number)}
  {#if i === 0}
    {#if setup.policy}<p>The policy is on the default branch.</p>
    {:else if setup.first_pull !== undefined}<p>Pull request #{setup.first_pull} proposes the first policy.</p>
    {:else}
      <p>Run the setup command in your repository. It writes the policy and workflow, then opens a pull request or prints a link to open one.</p>
      <pre class="command"><code>archfit setup</code></pre>
    {/if}
  {:else if i === 1}
    {#if pullStep.kind === 'review'}
      <p><a href={repoPage(repo.fullName, '/pull/' + pullStep.number)}>Pull request #{pullStep.number}</a> adds the workflow.</p>
      {#if primaryHref !== `${githubRepo}/pull/${pullStep.number}`}<a class="btn" href={`${githubRepo}/pull/${pullStep.number}`}>View pull request #{pullStep.number} on GitHub</a>{/if}
    {:else if pullStep.kind === 'first-policy'}
      <p>Pull request #{pullStep.number} proposes the first policy and workflow.</p>
      <a class="btn" href={repoPage(repo.fullName, '/pull/' + pullStep.number)}>View pull request #{pullStep.number}</a>
    {:else if pullStep.kind === 'current'}
      <p>The policy and workflow are on the default branch. No setup pull request is needed.</p>
    {:else}
      <p>Open one pull request that adds the missing policy or workflow.</p>
      {#if primaryHref !== `${githubRepo}/compare`}<a class="btn" href={`${githubRepo}/compare`}>Create a pull request on GitHub</a>{/if}
    {/if}
  {:else if i === 2}
    <p>{progress.approvalText}</p>
    {#if approvalHref}<p><a href={approvalHref}>View pull request #{setup.approval.pull_request} and approval record</a></p>{/if}
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
    <Disclosure summary="Why a passkey?"><p>{passkeyReason}</p></Disclosure>
  {:else}
    {#if setupCanRequireGate(setup)}
      <div class="required">
        <p>Make the check required when the owners agree. It starts with one approval of the policy and the workflow.</p>
        <p class="muted">Without the ruleset, a fail does not block a merge, even when the policy says required.</p>
        {#if requiredError}<p class="flash error" role="alert">{requiredError}</p>{/if}
        {#if requiredDone}
          <p class="flash success" role="status">{requiredNotice}</p>
          {#if requiredPull}<a class="btn" href={`${githubRepo}/pull/${requiredPull}`}>Pull request #{requiredPull} on GitHub</a>{/if}
        {:else if requiredPreview}
          <Diff text={requiredPreview.diff} label={`The policy change in .archfit.yaml against ${requiredPreview.against ?? 'the default branch'}`} />
          {#if setup.passkey}
            <button class="btn primary" disabled={requiredBusy} onclick={applyRequired}>{requiredBusy ? 'Confirming…' : 'Approve with your passkey'}</button>
          {:else}
            <a class="btn" href={repoPage(repo.fullName, '/passkeys')}>Register a passkey to approve</a>
          {/if}
        {:else}
          <button class="btn primary" disabled={requiredBusy} onclick={previewRequired}>{requiredBusy ? 'Checking…' : 'Make the check required'}</button>
        {/if}
        <p class="muted">The App opens a pull request that sets <code>gate: required</code> in the policy and merges it when the checks pass. Then add the ruleset below.</p>
      </div>
    {/if}
    <p>Done when GitHub requires a pull request and the archfit/gate check on the default branch. Items marked optional are not needed for this step.</p>
    <div class="rules">
      <p>In GitHub, open Settings → Rules → Rulesets, then create or edit a branch ruleset for the default branch. Set Enforcement status to Active, then tick what each row names below.</p>
      <div class="rule-actions">
        {#if primaryHref !== `${githubRepo}/settings/rules`}<a class="btn" href={`${githubRepo}/settings/rules`}>Open ruleset settings</a>{/if}
        <button class="btn" onclick={() => window.location.reload()}>Check again</button>
      </div>
      <p class="muted">After you save on GitHub, select Check again to read the settings again. It changes nothing in GitHub.</p>
      {#each rules as rule, ri (`${rule.code}-${ri}`)}
        {@const help = setupRuleHelp(rule, repo)}
        <div class="rule">
          <div class="rule-head">
            <h4 class="title-box">{help.title}</h4>
            <StatusLabel label={setupRuleStatus(rule.state, rule.code)} />
          </div>
          <p>{help.explanation}</p>
          {#each help.instructions as instruction (instruction)}<p>{instruction}</p>{/each}
          {#if help.links.length}
            <div class="rule-links">
              {#each help.links as link (link.href)}<a href={link.href}>{link.label}</a>{/each}
            </div>
          {/if}
        </div>
      {:else}<p>No ruleset settings are available.</p>{/each}
    </div>
  {/if}
{/snippet}

{#snippet stepList()}
  <ol class="steps">
    {#each steps as step, i (step.title)}
      <li class:active={i === active} class:done-line={done[i]}>
        <div class="step-head">
          <span class="number" aria-hidden="true">{done[i] ? '✓' : i + 1}</span>
          <div class="step-copy">
            <h3 class="title-box">{i + 1} · {step.title}</h3>
            {#if i === 3 && setup.weekly?.known}
              <p>This week: {setup.weekly.checked} checked, {setup.weekly.quiet} quiet, {setup.weekly.asked} asked</p>
              <p>{setup.weekly.caveat}</p>
            {:else if i === 3 && setup.weekly}
              <p>This week: counts unavailable</p>
            {/if}
            {#if !done[i]}<p>{step.text}</p>{/if}
          </div>
          <StatusLabel label={setupStepChip(done, i)} />
        </div>
        {#if i === active}
          <div class="step-body">{@render stepBody(i)}</div>
        {:else}
          <Disclosure summary={done[i] ? 'Details' : 'Show this step'}>
            <div class="step-body">{@render stepBody(i)}</div>
          </Disclosure>
        {/if}
      </li>
    {/each}
  </ol>
{/snippet}

<section class="box setup" aria-labelledby="setup-title">
  <div class="box-head">
    <h2 class="title-box" id="setup-title">Set up Archfit</h2>
    <p>{progress.next}</p>
  </div>
  <div class="box-body">
    {#if complete}
      {#if setup.weekly?.known}<p>This week: {setup.weekly.checked} checked, {setup.weekly.quiet} quiet, {setup.weekly.asked} asked</p><p>{setup.weekly.caveat}</p>{:else if setup.weekly}<p>This week: counts unavailable</p>{/if}
      <Disclosure summary="Setup details">{@render stepList()}</Disclosure>
    {:else}
      <div class="progress" aria-label="{completed} of 4 steps done"><span>{completed} of 4 done</span><span class="bar" aria-hidden="true"><span style="width: {completed * 25}%"></span></span></div>
      {@render stepList()}
    {/if}
  </div>
</section>

<style>
  .box-head p { margin: 4px 0 0; color: var(--fg-muted); }
  .progress { display: flex; align-items: center; gap: 10px; margin-bottom: 8px; color: var(--fg-muted); font-size: 13px; }
  .bar { flex: 1; height: 6px; background: var(--border); border-radius: 999px; overflow: hidden; }
  .bar span { display: block; height: 100%; background: var(--success); }
  .steps { list-style: none; padding: 0; margin: 0; }
  .steps li { padding: 10px 0; border-top: 1px solid var(--border); }
  .steps li.done-line { padding: 6px 0; }
  .done-line .step-head { align-items: center; }
  .done-line .step-copy h3 { margin: 0; font-weight: 500; color: var(--fg-muted); }
  .step-head { display: grid; grid-template-columns: 28px minmax(0, 1fr) auto; gap: 12px; align-items: start; }
  .number { width: 24px; height: 24px; border: 2px solid var(--border); border-radius: 50%; display: grid; place-items: center; font-size: 12px; color: var(--fg-muted); }
  .done-line .number { background: var(--success-emphasis); border-color: var(--success-emphasis); color: var(--fg-onEmphasis); }
  .step-copy h3 { margin: 0 0 4px; }
  .step-copy p { margin: 0; color: var(--fg-muted); }
  .step-body { margin: 8px 0 0 40px; display: flex; flex-direction: column; gap: 8px; }
  .step-body > p { margin: 0; color: var(--fg-muted); }
  .step-body > .btn { align-self: flex-start; }
  .command { margin: 0; padding: 10px 12px; border-radius: var(--radius); background: var(--bg-subtle); overflow-x: auto; }
  .rules { border-top: 1px solid var(--border); padding-top: 8px; display: flex; flex-direction: column; gap: 8px; }
  .rule-actions { display: flex; flex-wrap: wrap; gap: 8px; }
  .rule { padding: 12px 0; border-bottom: 1px dashed var(--border); overflow-wrap: anywhere; }
  .rule-head { display: flex; align-items: baseline; justify-content: space-between; flex-wrap: wrap; gap: 8px; }
  .rule h4 { margin: 0; }
  .rule p { font-size: 13px; margin-top: 3px; }
  .rule-links { display: flex; flex-wrap: wrap; gap: 6px 16px; margin-top: 8px; font-size: 13px; }
  .rule:last-child { border-bottom: 0; }
  @media (max-width: 640px) { .step-body { margin-left: 0; } }
</style>

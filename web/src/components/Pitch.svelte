<script lang="ts">
  import type { Snippet } from 'svelte';

  import StatusLabel from './ui/StatusLabel.svelte';

  let { top, status }: { top: Snippet; status?: Snippet } = $props();
</script>

<div class="pitch">
  <div class="pitch-hero">
    {@render top()}
    <h1>Check pull requests against your architecture policy</h1>
    <p>
      Your policy defines modules, allowed dependencies, rules and exceptions.
      Authors fix errors or open a request. Owners approve or reject requests.
    </p>
  </div>

  {@render status?.()}

  <section class="stack-sm" aria-labelledby="pitch-how">
    <h2 id="pitch-how">How it works</h2>
    <ol class="pitch-flow">
      <li>
        <span class="ic" aria-hidden="true">1</span><strong>Write the policy</strong>
        <span>Keep modules, allowed dependencies, rules and exceptions in <code>.archfit.yaml</code>.</span>
      </li>
      <li>
        <span class="ic" aria-hidden="true">2</span><strong>Check each change</strong>
        <span>Your CI runs Archfit. The check and comment show each error and the next step.</span>
      </li>
      <li>
        <span class="ic" aria-hidden="true">3</span><strong>Ask the owner</strong>
        <span>A request is a GitHub issue. An owner answers in Archfit; approval changes the policy by pull request.</span>
      </li>
    </ol>
    <p class="pitch-privacy">CI checks your code. The App reads reports and policy files to show results and owner requests.</p>
  </section>

  <section class="stack-sm" aria-labelledby="pitch-status">
    <h2 id="pitch-status">What the check says</h2>
    <div class="box">
      <div class="box-row pitch-check"><StatusLabel label="pass" /><span>The checked change meets the policy.</span></div>
      <div class="box-row pitch-check"><StatusLabel label="fail" /><span>Fix the code, or ask, as the error says.</span></div>
      <div class="box-row pitch-check"><StatusLabel label="waiting" /><span>An open request covers the errors. Wait for the owner.</span></div>
      <div class="box-row pitch-check"><StatusLabel label="unknown" /><span>Evidence is missing or the policy differs from the protected one. This is not a pass.</span></div>
    </div>
  </section>

  <details>
    <summary>How the policy stays protected</summary>
    <p>
      An owner uses a passkey to approve a policy change. The policy changes when its pull request merges.
      A policy changed outside this flow needs an owner to confirm or revert it.
      An expired exception does not allow an error in a checked change.
    </p>
  </details>
</div>

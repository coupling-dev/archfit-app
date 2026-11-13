<script lang="ts">
  import type { Snippet } from 'svelte';

  let { top, status }: { top: Snippet; status?: Snippet } = $props();
</script>

<div class="pitch">
  <div class="pitch-hero">
    {@render top()}
    <h1>Architecture checks on every pull request</h1>
    <p>
      Archfit checks each change against your policy: modules, allowed dependencies, rules and exceptions.
      Workers fix errors or ask with a request. Owners approve or reject.
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
    <p class="pitch-privacy"><strong>Your code never leaves your CI.</strong></p>
  </section>

  <section class="stack-sm" aria-labelledby="pitch-status">
    <h2 id="pitch-status">One status for each change</h2>
    <div class="box">
      <div class="box-row pitch-check"><span class="status success">pass</span><span>The change adds nothing a rule forbids.</span></div>
      <div class="box-row pitch-check"><span class="status danger">fail</span><span>Fix the code, or ask, as the error says.</span></div>
      <div class="box-row pitch-check"><span class="status attention">waiting</span><span>An open request covers the errors. Wait for the owner.</span></div>
      <div class="box-row pitch-check"><span class="status neutral">unknown</span><span>No verdict: evidence is missing or the policy differs from the protected one.</span></div>
    </div>
  </section>

  <details>
    <summary>How the policy stays protected</summary>
    <p>
      Only owners can loosen the policy. Approval needs a passkey, and takes effect when the policy pull request merges.
      A policy changed outside the flow makes every change unknown.
      An expired exception that still covers an error makes every change fail.
    </p>
  </details>
</div>

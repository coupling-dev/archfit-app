<script lang="ts">
  import type { Snippet } from 'svelte';

  // Pitch says what Archfit does and why, on one page: the signed-out landing page and the Waiting page of an account
  // no operator approved yet. top is the status or the sign-in under the headline; status is the box under the hero.
  let { top, status }: { top: Snippet; status?: Snippet } = $props();
</script>

<div class="pitch">
  <div class="pitch-hero">
    {@render top()}
    <h1>Architecture checks on every pull request</h1>
    <p>
      Archfit keeps your modules apart the way you designed them. It flags a pull request that crosses a boundary or
      tightens the wrong coupling, before it merges.
    </p>
  </div>

  {@render status?.()}

  <section class="stack-sm" aria-labelledby="pitch-how">
    <h2 id="pitch-how">How it works</h2>
    <ol class="pitch-flow">
      <li>
        <span class="ic" aria-hidden="true">1</span><strong>Your CI runs the engine</strong>
        <span>A workflow you add once. It reads your code where it already is.</span>
      </li>
      <li class="mid">
        <span class="ic" aria-hidden="true">2</span><strong>A report, no source code</strong>
        <span>Modules, dependencies and findings. Signed by GitHub, so nobody can fake it.</span>
      </li>
      <li class="end">
        <span class="ic" aria-hidden="true">✓</span><strong>A check on the pull request</strong>
        <span>One check and one comment that say what changed and how to fix it.</span>
      </li>
    </ol>
    <p class="pitch-privacy"><strong>Your code never leaves your CI.</strong></p>
  </section>

  <section class="stack-sm" aria-labelledby="pitch-get">
    <h2 id="pitch-get">What you get</h2>
    <div class="pitch-cards">
      <div>
        <span class="k">Drift</span><strong>Catch boundary breaks in review</strong>
        <span>A pull request that makes <code>api</code> reach into <code>billing</code> internals is flagged before merge.</span>
      </div>
      <div>
        <span class="k">Policy as code</span><strong>Rules live in your repository</strong>
        <span><code>.archfit.yaml</code> is reviewed like any other change.</span>
      </div>
      <div>
        <span class="k">Owners</span><strong>Only owners loosen the rules</strong>
        <span>A change to the policy needs an architecture owner's approval on that exact commit.</span>
      </div>
      <div>
        <span class="k">Baseline</span><strong>Old debt does not block</strong>
        <span>Existing problems are recorded once. Only new ones count.</span>
      </div>
    </div>
  </section>

  <section class="stack-sm" aria-labelledby="pitch-sample">
    <h2 id="pitch-sample">What it looks like <span class="label">sample</span></h2>
    <div class="box">
      <div class="box-row pitch-check">
        <span class="sev danger" aria-hidden="true">✕</span><code>archfit/state</code>
        <span class="muted">1 rule violation: <code>api → billing</code> crosses a layer boundary</span>
      </div>
      <div class="box-row pitch-check">
        <span class="sev attention" aria-hidden="true">●</span><code>archfit/gate</code>
        <span class="muted">Advisory: does not block merging</span>
      </div>
    </div>
  </section>
</div>

<script lang="ts">
  import type { PullHead } from '../lib/api';
  import { safeHref } from '../lib/route';
  let { head }: { head: PullHead } = $props();
  const check = $derived(head.check);
  // Build the run link from the authorized repository's GitHub URL, never Summary Markdown.
  const runURL = $derived(
    head.url.startsWith('https://github.com/') && check?.run_id
      ? safeHref(head.url.split('/pull/')[0] + '/actions/runs/' + check.run_id +
        (check.run_attempt ? '/attempts/' + check.run_attempt : ''))
      : '',
  );
  const missing = 'Not recorded';
</script>

<section class="box analysis" aria-labelledby="analysis">
  <div class="box-head"><h2 id="analysis">PR analysis</h2></div>
  <div class="box-body stack-sm">
    <p><strong>Reason:</strong> {check?.reason || (head.unavailable ? 'No diagnostic reason recorded in the check.' : head.headline) || 'No result recorded for this commit.'}</p>
    {#if check?.detail}<p>{check.detail}</p>{/if}
    <p><strong>Next:</strong> {check?.next || (head.unavailable
      ? 'Read the check diagnostics on GitHub. Re-run the workflow after resolving the reported cause.'
      : 'No recovery action recorded. Review the findings and coverage below.')}</p>
    {#if check?.also.length}
      <ul>{#each check.also as reason, i (i)}<li>{reason}</li>{/each}</ul>
    {/if}
    <p><strong>Analysis producer (claimed):</strong> {check?.producer || missing}</p>
    <div class="row small">
      <span><strong>Action version / SHA:</strong> {missing}</span>
      <span><strong>Analysed revision:</strong> <code>{check?.head_sha || missing}</code></span>
      <span><strong>Analysis time (UTC):</strong> {missing}</span>
    </div>
    <p class="small">
      {#if runURL}<a class="inline" href={runURL} rel="noreferrer">Run {check?.run_id}{check?.run_attempt ? ' · attempt ' + check.run_attempt : ''}</a>
      {:else}Run: {check?.run_id || missing}{/if}
      {#if safeHref(head.checks_url)} · <a class="inline" href={safeHref(head.checks_url)} rel="noreferrer">Checks on GitHub</a>{/if}
    </p>
    <p class="muted small">
      Recorded analysis identity is separate from this App's build. Provenance is not verification of report content.
      Missing historical fields are not inferred from current approved versions.
    </p>
    {#if check?.approved}
      <div class="flash warn stack-sm">
        <strong>Current approved workflow pins — not the producer of this analysis</strong>
        <p>Engine {check.approved.engine_version} · {check.approved.platform}</p>
        <p>Image <code>{check.approved.image_digest}</code></p>
        <p>Action SHA <code>{check.approved.action_ref}</code></p>
      </div>
    {/if}
    <details class="disclosure">
      <summary>Analysis details</summary>
      <dl class="analysis-details">
        <dt>Report schema</dt><dd>{check?.schema || missing}</dd>
        <dt>Analysis base revision</dt><dd>{missing}</dd>
        <dt>Report digest</dt><dd><code>{check?.report_digest || missing}</code></dd>
        <dt>Policy digest (check record)</dt><dd><code>{check?.policy_digest || missing}</code></dd>
        <dt>Baseline digest (check record)</dt><dd><code>{check?.baseline_digest || missing}</code></dd>
        <dt>Labels digest (check record)</dt><dd><code>{check?.labels_digest || missing}</code></dd>
        <dt>Measurement profile</dt><dd>{check?.profile || missing}</dd>
        <dt>Check started (not analysis time)</dt><dd>{check?.check_started_at || missing}</dd>
      </dl>
    </details>
  </div>
</section>

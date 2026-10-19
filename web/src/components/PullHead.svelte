<script lang="ts">
  import type { PullHead } from '../lib/api';
  import { repoPage, safeHref } from '../lib/route';

  // The header the pull request page and its graph share: which pull request, its result and the two views.
  let { head, fullName, current }: { head: PullHead; fullName: string; current: 'findings' | 'graph' } = $props();

  const stateTone: Record<string, string> = { open: 'success', draft: 'neutral', merged: 'done', closed: 'danger' };
  const page = $derived(repoPage(fullName, `/pull/${head.number}`));
</script>

<div class="page-head">
  <div class="stack-sm">
    <h1>{head.title || 'Pull request'} <span class="muted">#{head.number}</span></h1>
    <div class="row">
      <span class="state {stateTone[head.state] ?? 'neutral'}">{head.state}</span>
      {#if head.author}<span class="muted">{head.author} wants to merge</span>{/if}
      <code>{head.head_ref}</code><span class="muted">into</span><code>{head.base_ref}</code>
      <span class="muted small mono" title="Head commit">{head.head_sha}</span>
      {#if safeHref(head.url)}<a href={safeHref(head.url)} rel="noreferrer">View on GitHub</a>{/if}
    </div>
  </div>
</div>

<nav class="unav sub" aria-label="Pull request">
  <a href={page} aria-current={current === 'findings' ? 'page' : undefined}>Findings</a>
  <a href={page + '/graph'} aria-current={current === 'graph' ? 'page' : undefined}>Graph</a>
</nav>

{#if head.result}
  <section class="box result" aria-labelledby="result">
    <div class="box-body row">
      <span class="state {head.tone}" id="result">{head.result}</span>
      <strong>{head.headline}</strong>
    </div>
    <div class="box-foot row">
      {#if head.required}
        <span class="label danger">gate: required</span>
      {:else}
        <span class="label">gate: advisory · does not block merging</span>
      {/if}
      {#if !head.unavailable}
        {#if head.no_baseline}<span class="label attention">no baseline</span>{/if}
        {#if head.compared}<span class="label accent">compared with base</span>{:else}<span class="label"
            >PR attribution unavailable</span
          >{/if}
        <span>customer-attested · run {head.run_id}</span>
      {/if}
    </div>
  </section>
{/if}

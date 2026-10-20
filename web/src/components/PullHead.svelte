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
        <span class="label danger" title="The settings require archfit/gate. A failing result blocks merging only where a GitHub ruleset requires that check.">Required gate</span>
      {:else}
        <span class="label" title="The settings on the base branch do not require archfit/gate.">Advisory: does not block merging</span>
      {/if}
      {#if !head.unavailable}
        {#if head.no_baseline}
          <span class="label attention" title="Without a baseline every finding counts as new, including older debt.">No baseline</span>
        {/if}
        {#if head.compared}
          <span class="label accent" title="The engine compared this head with the base branch, so each finding says whether this pull request added it.">Compared with base branch</span>
        {:else}
          <span class="label" title="This run did not compare with the base branch, so nothing can be called added by this pull request.">Not compared with base branch</span>
        {/if}
        <span class="muted" title="The report comes from your CI run. Archfit authenticates who produced it; it does not check the content.">
          {#if safeHref(head.checks_url)}<a class="inline" href={safeHref(head.checks_url)} rel="noreferrer">Run {head.run_id}</a>{:else}Run {head.run_id}{/if}
          · customer-attested
        </span>
      {/if}
    </div>
  </section>
{/if}

<script lang="ts">
  import type { Findings } from '../lib/api';
  import CycleCard from './CycleCard.svelte';
  import FindingCard from './FindingCard.svelte';
  import FindingTable from './FindingTable.svelte';
  import { pairHref } from '../lib/feedback';

  // A report's findings in the PR comment's groups: the pull request page and the overview show them alike.
  // gatesFail is the engine's hard gates failing: then no violation listed never reads as nothing wrong.
  let { f, compared, gatesFail = false, graphPage = '' }: { f: Findings; compared: boolean; gatesFail?: boolean; graphPage?: string } = $props();

  const listed = $derived(
    (f.cycles?.length ?? 0) +
      (f.other?.length ?? 0) +
      (f.advisories?.length ?? 0) +
      (f.accepted?.length ?? 0) +
      (f.fixed?.length ?? 0),
  );
</script>

<section class="stack" aria-labelledby="violations">
  <h2 id="violations">Rule violations <span class="counter">{f.violation_count}</span></h2>
  {#if f.violation_count === 0}
    <div class="box blankslate">
      <p>
        {#if gatesFail}No rule violation is listed, but the engine's hard gates fail: the engine verdict above says
          why.{:else}No rule violations in this run.{/if}
      </p>
    </div>
  {/if}
  {#each f.cycles ?? [] as c, i (i)}
    <CycleCard {c} {compared} />
    {#if graphPage && c.directions[0]?.from && c.directions[0]?.to}
      <a class="inline small" href={pairHref(graphPage, c.directions[0].from, c.directions[0].to)}>Inspect this cycle in the authenticated graph</a>
    {/if}
  {/each}
  {#each f.other ?? [] as finding, i (i)}
    <FindingCard f={finding} />
    {#if graphPage && finding.from && finding.to}
      <a class="inline small" href={pairHref(graphPage, finding.from, finding.to)}>Inspect {finding.from} → {finding.to} in the authenticated graph</a>
    {/if}
  {/each}
</section>

{#if f.advisories?.length}
  <FindingTable id="advisories" title={f.advisories_elsewhere ? 'Advisories on other dependencies · never block' : 'Advisories · never block'} count={f.advisory_count} rows={f.advisories} {compared} moves />
{/if}
{#if f.accepted?.length}
  <FindingTable
    id="accepted"
    title="Known issues accepted earlier"
    count={f.accepted_count}
    rows={f.accepted}
    {compared}
  />
{/if}
{#if f.fixed?.length}
  <section class="box" id="fixed" aria-labelledby="fixed-title">
    <div class="box-head"><h2 id="fixed-title">Previously accepted findings no longer observed <span class="counter">{f.fixed_count}</span></h2></div>
    <div class="box-body stack-sm">
      <p>Previously accepted finding no longer observed; resolution not established.</p>
      <p class="muted small">Historical severity, endpoints and source sites are not established by this lifecycle entry. No removed edge or source-repair task is inferred.</p>
      <ul>{#each f.fixed as item, i (i)}<li>Rule <code>{item.rule_id}</code></li>{/each}</ul>
    </div>
  </section>
{/if}
{#if f.omitted}
  <p class="muted small">{f.omitted} more findings are not listed: each group shows at most 50.</p>
{/if}
{#if listed === 0 && f.violation_count === 0}
  <p class="muted small">The report lists no finding to show.</p>
{/if}

<script lang="ts">
  import type { Findings } from '../lib/api';
  import CycleCard from './CycleCard.svelte';
  import FindingCard from './FindingCard.svelte';
  import FindingTable from './FindingTable.svelte';

  // A report's findings in the PR comment's groups: the pull request page and the overview show them alike.
  // gatesFail is the engine's hard gates failing: then no violation listed never reads as nothing wrong.
  let { f, compared, gatesFail = false }: { f: Findings; compared: boolean; gatesFail?: boolean } = $props();

  const listed = $derived(
    (f.cycles?.length ?? 0) +
      (f.other?.length ?? 0) +
      (f.advisories?.length ?? 0) +
      (f.accepted?.length ?? 0) +
      (f.fixed?.length ?? 0),
  );
  // An advisory on a violation's dependency joined that row, so the rest are on other dependencies, as on the comment.
  const covered = $derived(
    [...(f.cycles ?? []).flatMap((c) => c.directions), ...(f.other ?? [])].some((r) => r.also?.length),
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
  {/each}
  {#each f.other ?? [] as finding, i (i)}
    <FindingCard f={finding} />
  {/each}
</section>

{#if f.advisories?.length}
  <FindingTable id="advisories" title={covered ? 'Advisories on other dependencies · never block' : 'Advisories · never block'} count={f.advisory_count} rows={f.advisories} {compared} moves />
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
  <FindingTable id="fixed" title="Known issues fixed" count={f.fixed_count} rows={f.fixed} compared={false} />
{/if}
{#if f.omitted}
  <p class="muted small">{f.omitted} more findings are not listed: each group shows at most 50.</p>
{/if}
{#if listed === 0 && f.violation_count === 0}
  <p class="muted small">The report lists no finding to show.</p>
{/if}

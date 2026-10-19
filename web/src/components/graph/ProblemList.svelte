<script lang="ts">
  import { linkKey, statusWord, type ProblemRow } from '../../lib/graph';
  import { pickLink, ui } from '../../lib/selection.svelte';

  // The problems above the canvas: violations, advisories and accepted dependencies, a cycle as one row.
  let { rows }: { rows: ProblemRow[] } = $props();

  const selected = (r: ProblemRow) =>
    ui.sel?.kind === 'link' && (ui.sel.key === r.key || (!!r.back && ui.sel.key === linkKey(r.back)));
</script>

<section class="box g-problems" aria-labelledby="problems">
  <div class="box-head"><h2 id="problems">Problems <span class="counter">{rows.length}</span></h2></div>
  {#if rows.length}
    <ul class="g-rows">
      {#each rows as r (r.key)}
        <li>
          <button type="button" class="g-row {r.link.status}" class:sel={selected(r)} onclick={() => pickLink(r.key)}>
            <i aria-hidden="true"></i>
            <span class="g-row-pair">
              <code>{r.link.from}</code>
              {r.back ? '⇄' : '→'}
              <code>{r.link.to}</code>
              {#if r.added}<span class="g-tag added">added</span>{/if}
            </span>
            <span class="muted small">
              {statusWord[r.link.status]} · {r.link.count || '·'}{r.back ? ` · ${r.back.count || '·'}` : ''}
            </span>
          </button>
        </li>
      {/each}
    </ul>
  {:else}
    <p class="box-row muted small">No violation, advisory or accepted dependency is shown.</p>
  {/if}
</section>

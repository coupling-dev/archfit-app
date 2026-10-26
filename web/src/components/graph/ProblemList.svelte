<script lang="ts">
  import { countWord, linkStatusWord, type GLink, type ProblemRow } from '../../lib/graph';
  import { pickLink, ui } from '../../lib/selection.svelte';
  import { linkKey } from '../../lib/graph';

  // The problems above the canvas: the violations and advisories, one row per dependency (an ordered pair of modules).
  // Accepted debt is no problem: it sits in its own collapsed group under them.
  let { rows, accepted }: { rows: ProblemRow[]; accepted: GLink[] } = $props();

  const selected = (key: string) => ui.sel?.kind === 'link' && ui.sel.key === key;
</script>

<section class="box g-problems" aria-labelledby="problems">
  <div class="box-head">
    <h2 id="problems">Problems <span class="counter">{rows.length}</span></h2>
    <span class="muted small">dependencies, each one module → another</span>
  </div>
  {#if rows.length}
    <ul class="g-rows">
      {#each rows as r (r.key)}
        <li>
          <button type="button" class="g-row {r.link.status}" class:sel={selected(r.key)} onclick={() => pickLink(r.key)}>
            <i aria-hidden="true"></i>
            <span class="g-row-pair">
              <code>{r.link.from}</code> → <code>{r.link.to}</code>
              {#if r.cycle}<span class="g-tag">cycle</span>{/if}
              {#if r.added}<span class="g-tag added">introduced finding</span>{/if}
            </span>
            <span class="muted small">{linkStatusWord(r.link)} · {countWord(r.link)}</span>
          </button>
        </li>
      {/each}
    </ul>
  {:else}
    <p class="box-row muted small">No violation or advisory is shown.</p>
  {/if}
  {#if accepted.length}
    <details class="g-accepted">
      <summary>Accepted earlier <span class="counter">{accepted.length}</span></summary>
      <ul class="g-rows">
        {#each accepted as l (linkKey(l))}
          <li>
            <button type="button" class="g-row accepted" class:sel={selected(linkKey(l))} onclick={() => pickLink(linkKey(l))}>
              <i aria-hidden="true"></i>
              <span class="g-row-pair"><code>{l.from}</code> → <code>{l.to}</code></span>
              <span class="muted small">{linkStatusWord(l)} · {countWord(l)}</span>
            </button>
          </li>
        {/each}
      </ul>
    </details>
  {/if}
</section>

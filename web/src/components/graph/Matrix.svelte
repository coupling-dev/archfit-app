<script lang="ts">
  import { isAdded, linkKey, order, statusWord, type GLink, type GNode, type Status } from '../../lib/graph';
  import { pickLink, ui } from '../../lib/selection.svelte';

  // The full picture as a dependency structure matrix: rows depend on columns. Focus mode never narrows it.
  let { nodes, links, pull, show }: { nodes: GNode[]; links: GLink[]; pull: boolean; show: Record<Status, boolean> } =
    $props();

  const names = $derived(order(nodes).map((n) => n.name));
  const cell = $derived(new Map(links.map((l) => [linkKey(l), l])));
</script>

<div class="box g-matrix">
  <p class="box-row muted small">Rows depend on columns. Position does not establish permission.</p>
  <div class="g-scroll">
    <table class="g-dsm">
      <thead>
        <tr>
          <td></td>
          {#each names as c (c)}<th scope="col"><span>{c}</span></th>{/each}
        </tr>
      </thead>
      <tbody>
        {#each names as r (r)}
          <tr>
            <th scope="row">{r}</th>
            {#each names as c (c)}
              {@const l = cell.get(JSON.stringify([r, c]))}
              <td class:self={r === c}>
                {#if l && show[l.status]}
                  <button
                    type="button"
                    class="g-cell {l.status}"
                    class:added={ui.emphasis && isAdded(l, pull)}
                    class:sel={ui.sel?.kind === 'link' && ui.sel.key === linkKey(l)}
                    aria-label="{r} to {c}, {l.count || 'named by a finding'}, {statusWord[l.status]}{isAdded(l, pull)
                      ? ', added by this PR'
                      : ''}"
                    onclick={() => pickLink(linkKey(l))}>{l.count || '·'}</button
                  >
                {/if}
              </td>
            {/each}
          </tr>
        {/each}
      </tbody>
    </table>
  </div>
</div>

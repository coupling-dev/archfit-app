<script lang="ts">
  import {
    balanceWord,
    countWord,
    isAdded,
    linkKey,
    linkStatusWord,
    linkVolatility,
    measured,
    moduleVolatility,
    order,
    strengthWord,
    type View,
  } from '../../lib/graph';
  import { safeHref } from '../../lib/route';

  // The graph as two tables: for screen readers, and the whole view when there are too many modules to draw.
  let { view, pull, policyHref, open }: { view: View; pull: boolean; policyHref: string; open: boolean } = $props();
  let filter = $state('');

  const match = (...parts: string[]) => !filter || parts.some((p) => p.toLowerCase().includes(filter.toLowerCase()));
</script>

<details class="disclosure g-tables" {open}>
  <summary>Modules and dependencies as tables</summary>
  <div class="details-body">
    <label class="small">
      Filter <input type="search" bind:value={filter} placeholder="Module name" />
    </label>
    <div class="box">
      <table class="data">
        <caption class="sr-only">Modules</caption>
        <thead><tr><th scope="col">Module</th><th scope="col">Layer</th><th scope="col">Declared</th></tr></thead>
        <tbody>
          {#each order(view.nodes).filter((n) => match(n.name, n.layer)) as n (n.name)}
            <tr>
              <td>
                {#if n.policy_line && safeHref(`${policyHref}#L${n.policy_line}`)}
                  <a href={safeHref(`${policyHref}#L${n.policy_line}`)}><code>{n.name}</code></a>
                {:else}
                  <code>{n.name}</code> <span class="muted small">not in the policy</span>
                {/if}
              </td>
              <td>{n.layer}</td>
              <td>{moduleVolatility(n)}</td>
            </tr>
          {/each}
        </tbody>
      </table>
    </div>
    <div class="box">
      <table class="data">
        <caption class="sr-only">Dependencies</caption>
        <thead>
          <tr>
            <th scope="col">From</th>
            <th scope="col">To</th>
            <th scope="col">Status</th>
            <th scope="col" class="num">Imports</th>
            <th scope="col">Strength · distance · volatility · balance</th>
          </tr>
        </thead>
        <tbody>
          {#each view.links.filter((l) => match(l.from, l.to)) as l (linkKey(l))}
            <tr>
              <td><code>{l.from}</code></td>
              <td><code>{l.to}</code></td>
              <td>{linkStatusWord(l)}{isAdded(l, pull) ? ' · added by this PR' : ''}</td>
              <td class="num">{l.count || countWord(l)}</td>
              <td>
                {measured(l) ? strengthWord[l.strength] : 'not measured'} · {l.distance || '—'} · {linkVolatility(l) || '—'} ·
                {balanceWord(l)}
              </td>
            </tr>
          {/each}
        </tbody>
      </table>
    </div>
  </div>
</details>

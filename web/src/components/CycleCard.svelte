<script lang="ts">
  import type { Cycle } from '../lib/api';
  import CycleDiagram from './CycleDiagram.svelte';
  import Locations from './Locations.svelte';
  import Origin from './Origin.svelte';
  import Reasons from './Reasons.svelte';

  let { c, compared }: { c: Cycle; compared: boolean } = $props();

  const between = $derived(
    c.modules.length === 2 ? `between ${c.modules[0]} and ${c.modules[1]}` : `among ${c.modules.join(', ')}`,
  );
  const coupling = $derived(c.directions.some((d) => d.strength || d.distance || d.volatility));
</script>

<article class="box finding">
  <div class="box-head">
    <div class="row">
      {#if c.violation}<span class="label solid danger">violation</span>{/if}
      <h3>Dependency cycle {between}</h3>
    </div>
    <span class="row muted small"><strong title={c.rule_id}>{c.rule}</strong> · {c.meta}</span>
  </div>
  <div class="box-body stack">
    <CycleDiagram modules={c.modules} directions={c.directions} />
    <div class="table-wrap">
      <table class="data">
        <thead>
          <tr>
            <th scope="col">Dependency</th>
            {#if compared}<th scope="col">This PR</th>{/if}
            <th scope="col">Where</th>
            {#if coupling}
              <th scope="col">Strength</th>
              <th scope="col">Distance</th>
              <th scope="col">Volatility</th>
            {/if}
          </tr>
        </thead>
        <tbody>
          {#each c.directions as d, i (i)}
            <tr>
              <td class="nowrap"><code>{d.from}</code> → <code>{d.to}</code></td>
              {#if compared}<td><Origin origin={d.origin} /></td>{/if}
              <td><Locations locations={d.locations} /></td>
              {#if coupling}
                <td>{d.strength}</td>
                <td>{d.distance}</td>
                <td>{d.volatility}</td>
              {/if}
            </tr>
          {/each}
        </tbody>
      </table>
    </div>
    <Reasons why={c.why} fixes={c.fixes ?? []} alternatives={c.alternatives ?? []} />
  </div>
</article>

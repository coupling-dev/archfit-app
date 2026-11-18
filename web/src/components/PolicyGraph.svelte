<script lang="ts">
  import type { Policy } from '../lib/api';
  import { policyMap } from '../lib/policy';

  let { modules, layers, inspect }: {
    modules: Policy['modules'];
    layers: Policy['layers'];
    inspect: (index: number) => void;
  } = $props();

  // The declared dependencies, drawn with the Home map's component.
  const graph = $derived(policyMap(modules, layers));
  let focused = $state('');
  const index = $derived(modules.findIndex((m) => m.name === focused));
  const current = $derived(index < 0 ? null : modules[index]);
  const dependents = $derived(modules.filter((m) => current && m.depends_on.includes(current.name)).map((m) => m.name));
</script>

{#await import('./map/Diagram.svelte')}
  <p role="status">Placing the modules…</p>
{:then component}
  <component.default
    model={graph.model}
    detailView="table"
    kinds={['allowed']}
    label="Allowed module dependencies"
    onfocus={(name: string) => (focused = name)}
  />
{:catch}
  <p class="flash error" role="alert">The graph could not load. The table below lists every dependency.</p>
{/await}
{#if current}
  <section class="graph-detail" aria-label="Selected module" aria-live="polite">
    <h3>{current.name}</h3>
    <p class="small">{current.layer || 'Layer not provided'} · Volatility: {current.volatility || 'Not provided'}</p>
    <p class="small">Depends on: {current.depends_on.length ? current.depends_on.join(', ') : 'nothing declared'}</p>
    <p class="small">Used by: {dependents.length ? dependents.join(', ') : 'no module in this policy'}</p>
    <button class="btn sm" type="button" onclick={() => inspect(index)}>Show in the table</button>
  </section>
{/if}
{#if graph.missing}
  <p class="small muted">{graph.missing} {graph.missing === 1 ? 'dependency target is' : 'dependency targets are'} not in the module list. See the table for all declared dependencies.</p>
{/if}

<style>
  .graph-detail { margin-top: 12px; padding: 12px; border: 1px solid var(--border-muted); border-radius: var(--radius); }
  .graph-detail h3 { margin: 0 0 4px; font: 600 14px var(--mono); }
  .graph-detail p { margin: 4px 0; overflow-wrap: anywhere; }
</style>

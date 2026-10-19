<script lang="ts">
  import type { Finding } from '../lib/api';
  import Locations from './Locations.svelte';
  import Origin from './Origin.svelte';
  import Reasons from './Reasons.svelte';

  let { f }: { f: Finding } = $props();
</script>

<article class="box finding">
  <div class="box-head">
    <div class="row">
      {#if f.violation}<span class="label solid danger">violation</span>{/if}
      <strong title={f.rule_id}>{f.rule}</strong>
      <span class="muted small">{f.meta}</span>
    </div>
    <Origin origin={f.origin} />
  </div>
  <div class="box-body stack-sm">
    {#if f.from || f.to}
      <p class="row"><code>{f.from}</code><span aria-label="depends on">→</span><code>{f.to}</code></p>
    {/if}
    <Reasons why={f.why} fixes={f.fix ? [f.fix] : []} alternatives={f.alternatives ?? []} />
    <div class="row"><span class="muted small">Where:</span> <Locations locations={f.locations} /></div>
  </div>
</article>

<script lang="ts">
  // A group that never blocks (advisories, accepted debt): one fold, a table, then each distinct reason once.
  import type { Finding } from '../lib/api';
  import { ownerDecisionTitle } from '../lib/seam';
  import Locations from './Locations.svelte';
  import Origin from './Origin.svelte';

  let {
    id,
    title,
    count,
    rows,
    compared,
  }: { id: string; title: string; count: number; rows: Finding[]; compared: boolean } = $props();

  const reasons = $derived.by(() => {
    const seen: Record<string, true> = {}; // a local, not state: a plain record
    const out: { rule: string; label: string; text: string }[] = [];
    for (const f of rows) {
      for (const [label, text] of [
        ['Why', f.why],
        ['Fix', f.fix],
        ['Suggested move', f.move ? `${f.move}.` : ''],
      ]) {
        const key = `${f.rule_id}\u0000${label}\u0000${text}`;
        if (text && !seen[key]) {
          seen[key] = true;
          out.push({ rule: f.rule, label, text });
        }
      }
    }
    return out;
  });
</script>

<details class="box fold" {id}>
  <summary class="box-head"><h2>{title}</h2><span class="counter">{count}</span></summary>
  <div class="table-wrap">
    <table class="data">
      <thead>
        <tr>
          <th scope="col">Rule</th>
          <th scope="col">Dependency</th>
          {#if compared}<th scope="col">This PR</th>{/if}
          <th scope="col">Where</th>
        </tr>
      </thead>
      <tbody>
        {#each rows as f, i (i)}
          <tr>
            <td>
              <span title={f.rule_id}>{f.rule}</span> <span class="muted small">· {f.meta}</span>
              {#if f.owner_decision}<span class="label" title={ownerDecisionTitle}>owner decision</span>{/if}
            </td>
            <td class="nowrap">
              {#if f.from || f.to}<code>{f.from}</code> → <code>{f.to}</code>{:else}<span class="muted small"
                  >{f.metric || 'no dependency'}</span
                >{/if}
            </td>
            {#if compared}<td><Origin origin={f.origin} /></td>{/if}
            <td><Locations locations={f.locations} total={f.locations_total} /></td>
          </tr>
        {/each}
      </tbody>
    </table>
  </div>
  {#if reasons.length}
    <div class="box-body stack-sm">
      {#each reasons as r, i (i)}<p><strong>{r.rule} · {r.label}:</strong> {r.text}</p>{/each}
    </div>
  {/if}
</details>

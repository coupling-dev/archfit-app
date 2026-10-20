<script lang="ts">
  import { Handle, Position } from '@xyflow/svelte';
  import { moduleVolatility, type GNode, type Status } from '../../lib/graph';
  import { pickNode, ui } from '../../lib/selection.svelte';

  // One module: its name, its layer and declared volatility, a border in its worst problem's colour, dashed when the
  // policy does not declare it.
  let { data }: { data: { n: GNode; worst: Status | ''; dim: boolean } } = $props();

  const n = $derived(data.n);
  const declared = $derived(n.policy_line > 0);
  const short = $derived(n.name.length > 22 ? '…' + n.name.slice(n.name.length - 21) : n.name);

  function onkey(e: KeyboardEvent) {
    if (e.key !== 'Enter' && e.key !== ' ') return;
    e.preventDefault();
    pickNode(n.name);
  }
</script>

<Handle type="target" position={Position.Top} isConnectable={false} class="g-handle" />
<div
  class="g-node {data.worst}"
  class:undeclared={!declared}
  class:dim={data.dim}
  class:sel={ui.sel?.kind === 'node' && ui.sel.name === n.name}
  tabindex="0"
  role="button"
  aria-label="Module {n.name}{declared ? '' : ', not in the policy'}{data.worst ? `, ${data.worst}` : ''}"
  title={n.name}
  onclick={(e) => {
    e.stopPropagation();
    pickNode(n.name);
  }}
  onkeydown={onkey}
>
  <span class="g-node-name">{short}</span>
  {#if n.layer || moduleVolatility(n)}
    <span class="g-node-facts">
      {#if n.layer}<span>{n.layer}</span>{/if}
      {#if moduleVolatility(n)}<span>{moduleVolatility(n)}</span>{/if}
    </span>
  {/if}
</div>
<Handle type="source" position={Position.Bottom} isConnectable={false} class="g-handle" />

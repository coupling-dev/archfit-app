<script lang="ts">
  import {
    balanceWord,
    countWord,
    findingsOf,
    isAcceptedEarlier,
    isAdded,
    isProblem,
    linkKey,
    linkStatusWord,
    linkVolatility,
    meaning,
    measured,
    moduleVolatility,
    strengthWord,
    type GLink,
    type View,
  } from '../../lib/graph';
  import { safeHref } from '../../lib/route';
  import InspectorSeam from './InspectorSeam.svelte';
  import { pickLink, ui } from '../../lib/selection.svelte';

  // The selected dependency or module, in words: status, then coupling, then the findings behind it.
  let { view, pull, policyHref }: { view: View; pull: boolean; policyHref: string } = $props();

  const link = $derived(ui.sel?.kind === 'link' ? view.links.find((l) => linkKey(l) === (ui.sel as { key: string }).key) : undefined);
  const back = $derived(link ? view.links.find((l) => l.from === link.to && l.to === link.from && l.from !== l.to) : undefined);
  const node = $derived(ui.sel?.kind === 'node' ? view.nodes.find((n) => n.name === (ui.sel as { name: string }).name) : undefined);
  const outs = $derived(node ? view.links.filter((l) => l.from === node.name && l.to !== node.name) : []);
  const ins = $derived(node ? view.links.filter((l) => l.to === node.name && l.from !== node.name) : []);
  const lineOf = (name: string) => view.nodes.find((n) => n.name === name)?.policy_line ?? 0;
  const policyLine = (line: number) => (line ? safeHref(`${policyHref}#L${line}`) : undefined);
</script>

<aside class="box g-inspector" aria-labelledby="inspector-title" aria-live="polite">
  {#if link}
    <div class="box-head"><h2 id="inspector-title">Dependency</h2></div>
    <div class="box-body stack-sm">
      {@render dependency(link, true)}
      {#if back}
        <h3 class="g-sub">Other direction · a cycle</h3>
        {@render dependency(back, false)}
      {/if}
      {#if policyLine(lineOf(link.from))}<a href={policyLine(lineOf(link.from))}>Open <code>{link.from}</code> in the policy</a>{/if}
    </div>
  {:else if node}
    <div class="box-head"><h2 id="inspector-title">Module</h2></div>
    <div class="box-body stack-sm">
      <p><code class="g-strong">{node.name}</code></p>
      <dl class="g-kv">
        <dt>Layer</dt>
        <dd>{node.layer || 'none declared'}</dd>
        <dt>Declared</dt>
        <dd>
          {moduleVolatility(node) ||
            (view.stale ? 'unknown: the policy is not the one the report used' : 'nothing: not in the policy')}
        </dd>
        <dt>Policy</dt>
        <dd>
          {#if policyLine(node.policy_line)}<a href={policyLine(node.policy_line)}>Open in the policy</a>{:else}not in the
            policy{/if}
        </dd>
      </dl>
      <h3 class="g-sub">Depends on <span class="counter">{outs.length}</span></h3>
      {@render picks(outs, 'to')}
      <h3 class="g-sub">Used by <span class="counter">{ins.length}</span></h3>
      {@render picks(ins, 'from')}
    </div>
  {:else}
    <div class="box-head"><h2 id="inspector-title">Nothing selected</h2></div>
    <div class="box-body stack-sm">
      <p class="muted small">Select a problem, a dependency, a cell or a module. Tab and Enter work too.</p>
      <dl class="g-kv">
        <dt>Modules</dt>
        <dd>{view.nodes.length}</dd>
        <dt>Dependencies</dt>
        <dd>{view.links.length}</dd>
        <dt>Problems</dt>
        <dd>{view.links.filter((l) => isProblem(l, pull)).length}</dd>
        <dt>Accepted earlier</dt>
        <dd>{view.links.filter((l) => isAcceptedEarlier(l, pull)).length}</dd>
        {#if pull}<dt>Added in this PR</dt>
          <dd>{view.links.filter((l) => isAdded(l, pull)).length}</dd>{/if}
      </dl>
    </div>
  {/if}
</aside>

{#snippet picks(ls: GLink[], end: 'from' | 'to')}
  {#if ls.length}
    <ul class="g-picks">
      {#each ls as l (linkKey(l))}
        <li>
          <button type="button" class="g-pick {l.status}" onclick={() => pickLink(linkKey(l))}>
            <span><i aria-hidden="true"></i><code>{l[end]}</code></span>
            <span class="muted small">{linkStatusWord(l)} · {l.count || '·'}</span>
          </button>
        </li>
      {/each}
    </ul>
  {:else}
    <p class="muted small">None.</p>
  {/if}
{/snippet}

{#snippet dependency(l: GLink, full: boolean)}
  <p class="g-pair"><code>{l.from}</code> → <code>{l.to}</code></p>
  <div class="row">
    <span class="g-tag {l.status}">{linkStatusWord(l)}</span>
    {#if isAdded(l, pull)}<span class="g-tag added">added by this PR</span>{/if}
    <span class="muted small">{countWord(l)}</span>
  </div>
  {#if full}<p class="small">{meaning[l.status]}</p>{/if}
  <dl class="g-kv">
    <dt>Integration strength</dt>
    <dd>{measured(l) ? strengthWord[l.strength] : 'not measured'}</dd>
    <dt>Distance</dt>
    <dd>{l.distance || 'not measured'}</dd>
    <dt>Volatility</dt>
    <dd>{linkVolatility(l) || 'not measured'}</dd>
    <dt>Balance</dt>
    <dd>{balanceWord(l)}</dd>
  </dl>
  <InspectorSeam link={l} />
  {#each findingsOf(view, l) as f, i (i)}
    <div class="g-finding">
      <p>
        <code>{f.rule_id || '(no rule)'}</code>
        <span class="muted small">{f.kind}, {f.status}</span>
      </p>
      {#if f.why}<p class="small">{f.why}</p>{/if}
      {#if f.more}<p class="muted small">and {f.more} more on the archfit/state check</p>{/if}
    </div>
  {/each}
{/snippet}

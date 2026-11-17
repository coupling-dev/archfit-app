<script lang="ts">
  import { tick } from 'svelte';
  import type { Policy } from '../lib/api';
  import { exceptionExpiry, filterPolicy, groupExceptions, policyCounts, ruleMode } from '../lib/policy';
  import { safeHref } from '../lib/route';
  import PolicyGraph from './PolicyGraph.svelte';

  let { policy, home, showSource = $bindable(false) }: { policy: Policy; home: string; showSource?: boolean } = $props();
  let query = $state('');
  let copyNotice = $state('');
  const counts = $derived(policyCounts(policy));
  const filtered = $derived(filterPolicy(policy, query));
  // Exceptions are grouped by rule and shown a page at a time. A column is drawn only when some row has its fact.
  const previewRows = 20;
  let showAll = $state(false);
  const groups = $derived(groupExceptions(filtered.exceptions));
  // Every rule keeps its header and count; the row budget is spent in order, so a rule past it shows no rows.
  const view = $derived.by(() => {
    let budget = showAll ? Infinity : previewRows;
    return groups.map((group) => {
      const take = Math.min(budget, group.items.length);
      budget -= take;
      return { group, items: group.items.slice(0, take) };
    });
  });
  const shownRows = $derived(view.flatMap((g) => g.items));
  const total = $derived(filtered.exceptions.length);
  const hasEnd = $derived(shownRows.some((e) => !!e.expires));
  const hasRequest = $derived(shownRows.some((e) => !!e.request && !!safeHref(e.request_href)));
  const hasBars = $derived(shownRows.some((e) => e.days_left != null));
  const noEnd = $derived(filtered.exceptions.filter((e) => !e.expires).length);
  const matches = $derived(filtered.modules.length + filtered.rules.length + filtered.exceptions.length);
  const hasNotes = $derived(policy.rules.some((rule) => rule.not_checked));
  // The gate key says what a failing rule does to archfit/gate; it does not say whether GitHub requires the check. Any other value is shown as it is.
  const gateMeaning: Record<string, string> = {
    advisory: 'rules are reported and archfit/gate does not fail',
    required: 'a failing rule makes archfit/gate fail',
  };

  async function inspectModule(index: number) {
    query = '';
    await tick();
    const detail = document.getElementById('policy-module-' + index);
    if (detail instanceof HTMLDetailsElement) {
      detail.open = true;
      detail.querySelector('summary')?.focus();
      detail.scrollIntoView({ block: 'center' });
    }
  }

  async function copyDigest() {
    try {
      await navigator.clipboard.writeText(policy.digest);
      copyNotice = 'Digest copied.';
    } catch {
      copyNotice = 'Clipboard unavailable. Select the digest to copy it.';
    }
  }
</script>

<section class="facts" aria-label="Policy summary">
  <dl>
    <div><dt>Owners</dt><dd>{policy.owners?.join(', ') || 'None'}</dd></div>
    <div><dt>Layers, inner first</dt><dd>{policy.layers?.join(', ') || 'None'}</dd></div>
    <div><dt>The check</dt><dd>{policy.gate || 'unknown'}{#if gateMeaning[policy.gate]}<span class="meaning">{' — ' + gateMeaning[policy.gate]}</span>{/if}</dd></div>
  </dl>
  <p class="small muted">Whether GitHub requires the check is shown on <a href={home}>Home</a>.</p>
</section>

<div class="stats" aria-label="Policy counts">
  <a href="#modules"><span class="n">{counts.modules}</span><span class="t">Modules</span><span class="s">Declared boundaries</span></a>
  <a href="#rules"><span class="n">{counts.askDeny}</span><span class="t">Ask and deny rules</span><span class="s">of {counts.rules} rules</span></a>
  <a href="#exceptions"><span class="n">{counts.exceptions}</span><span class="t">Exceptions</span>
    <span class="s" class:expired={counts.expired > 0}>{counts.expired ? `${counts.expired} already expired` : 'No recorded expiry has passed'}</span></a>
  <a href="#exceptions"><span class="n">{counts.expiring}</span><span class="t">Expiring in 30 days</span><span class="s">Includes today</span></a>
</div>

<div class="search-row">
  <label class="search"><span class="sr-only">Search policy</span><input class="form-control" type="search" bind:value={query} placeholder="Search modules, rules, exceptions…" /></label>
  <p class="small muted" role="status">{query.trim() ? matches ? `${matches} matching items` : 'No matches. Try a module name or rule ID.' : 'Expand a row for details.'}</p>
</div>

<section class="box" aria-labelledby="rules">
  <div class="box-head">
    <div><h2 class="title-box" id="rules">Rules</h2><p>Expand a rule to see why it exists and what to do instead.</p>{#if hasNotes}<p>A note is not checked. It tells the reader what to do.</p>{/if}</div>
    <a href="#raw" onclick={() => showSource = true}>View source</a>
  </div>
  {#each filtered.rules as rule, i (i)}
    <details class="rule">
      <summary>
        <strong class="rule-id">{rule.id}</strong>
        <span class="label" class:danger={rule.mode === 'deny'} class:attention={rule.mode === 'ask'}>{ruleMode(rule.mode)}</span>
      </summary>
      <div class="rule-body stack-sm">
        {#if rule.summary}<p>{rule.summary}</p>{/if}
        {#if rule.why || rule.instead}
          <dl class="kv">
            {#if rule.why}<dt>Why</dt><dd>{rule.why}</dd>{/if}
            {#if rule.instead}<dt>Instead</dt><dd>{rule.instead}</dd>{/if}
          </dl>
        {/if}
        {#if rule.mode === 'ask'}<p class="small muted">An error asks the owner for a change.</p>
        {:else if rule.mode === 'deny'}<p class="small muted">An error must be fixed before the check can pass.</p>
        {:else if rule.mode === 'off'}<p class="small muted">This rule is off.</p>{/if}
      </div>
    </details>
  {:else}
    <p class="box-body muted">No rules match this search.</p>
  {/each}
</section>

<section class="box" aria-labelledby="exceptions">
  <div class="box-head"><div><h2 class="title-box" id="exceptions">Exceptions</h2><p>Follow the linked request to renew or replace an exception.</p></div></div>
  {#if total}
    {#if noEnd > 0}<p class="box-body small muted">{noEnd === 1 ? '1 exception has' : `${noEnd} exceptions have`} no end date.</p>{/if}
    {#each view as section (section.group.rule)}
      <details class="exception-group" open={section.items.length > 0}>
        <summary>{section.group.rule} · {section.group.items.length}</summary>
        {#if section.items.length}
          <table class="data cards exceptions">
            <thead><tr>
              <th>Rule / boundary</th>
              {#if hasEnd}<th>Expiry</th>{/if}
              {#if hasRequest}<th>Request</th>{/if}
            </tr></thead>
            <tbody>
              {#each section.items as exception, i (i)}
              <tr>
                <td><p class="boundary">{exception.from}{exception.to ? ` → ${exception.to}` : ''}</p>
                  {#if exception.why}<details><summary>Reason</summary><p>{exception.why}</p></details>{/if}
                </td>
                {#if hasEnd}
                  {@const expiry = exceptionExpiry(exception)}
                  <td data-label="Expiry">
                    <div class="expiry">
                      <span class={'label ' + expiry.tone}>{expiry.label}</span>
                      {#if exception.expires}<time datetime={exception.expires}>{exception.expires}</time>{/if}
                      {#if expiry.remaining !== null}
                        <progress max="30" value={expiry.remaining} class:expired={expiry.tone === 'danger'} aria-label="Days remaining, capped at 30"></progress>
                      {/if}
                    </div>
                  </td>
                {/if}
                {#if hasRequest}
                  <td data-label="Request">
                    {#if exception.request && safeHref(exception.request_href)}
                      <a href={safeHref(exception.request_href)}>Request #{exception.request}</a>
                    {:else}<span class="muted">None</span>{/if}
                  </td>
                {/if}
              </tr>
              {/each}
            </tbody>
          </table>
        {:else}
          <p class="muted">Show all to see these exceptions.</p>
        {/if}
      </details>
    {/each}
    {#if total > previewRows && !showAll}
      <div class="box-foot row-between"><span class="small muted">Showing {previewRows} of {total}</span><button class="btn sm" type="button" onclick={() => showAll = true}>Show all</button></div>
    {/if}
    {#if hasBars}<div class="box-foot">Bars show days remaining within a 30-day window, not exception age. Expired records are not active exceptions.</div>{/if}
  {:else}<p class="box-body muted">{query.trim() ? 'No exceptions match this search.' : 'No exceptions.'}</p>{/if}
</section>

<section class="box" aria-labelledby="modules">
  <div class="box-head"><div><h2 class="title-box" id="modules">Modules</h2><p>Paths and allowed dependencies of each module. Volatility is how often a module is expected to change. Subdomain is its business role.</p></div></div>
  {#if filtered.modules.length}
    <table class="data cards modules">
      <thead><tr><th>Module / paths</th><th>Layer</th><th>Allowed dependencies</th></tr></thead>
      <tbody>
        {#each filtered.modules as module (module.name)}
          <tr>
            <td>
              <details id={'policy-module-' + policy.modules.indexOf(module)}>
                <summary><strong>{module.name}</strong></summary>
                <dl class="kv module-facts">
                  <dt>Public</dt><dd>{module.public === null ? 'Whole module' : module.public.join(', ') || 'None'}</dd>
                  <dt>Volatility</dt><dd>{module.volatility || 'Not provided'}</dd>
                  <dt>Subdomain</dt><dd>{module.subdomain || 'Not provided'}</dd>
                </dl>
              </details>
              <p class="boundary">{module.paths?.join(', ') || 'Not provided'}</p>
            </td>
            <td data-label="Layer"><span class="label">{module.layer || 'Not provided'}</span></td>
            <td data-label="Allowed dependencies"><span class="boundary">{module.depends_on.join(', ') || 'None'}</span></td>
          </tr>
        {/each}
      </tbody>
    </table>
  {:else}<p class="box-body muted">{query.trim() ? 'No modules match this search.' : 'No modules.'}</p>{/if}
  {#if policy.modules.length}
    <details class="graph-disclosure">
      <summary>Show graph <span class="small muted">All {policy.modules.length} modules</span></summary>
      <div class="graph-body">
        <p class="small muted">Allowed dependencies, not observed imports. Select a module to inspect it. Search filters the table, not the graph.</p>
        <PolicyGraph modules={policy.modules} layers={policy.layers} inspect={inspectModule} />
      </div>
    </details>
  {/if}
</section>

<section class="box" aria-labelledby="raw">
  <div class="box-head"><h2 class="title-box" id="raw">Policy file</h2><button class="btn sm" onclick={copyDigest} disabled={!policy.digest}>Copy digest</button></div>
  <div class="box-body stack-sm">
    <p class="small analysis">Digest: <code>{policy.digest || 'Not provided'}</code></p>
    {#if copyNotice}<p class="small" role="status">{copyNotice}</p>{/if}
    <details class="disclosure" bind:open={showSource}><summary>Show raw YAML</summary><pre>{policy.yaml}</pre></details>
  </div>
</section>

<style>
  .facts dl { display: grid; grid-template-columns: repeat(3, minmax(0, 1fr)); gap: 16px; margin: 0 0 8px; }
  .facts dt { font-size: 12px; color: var(--fg-muted); margin-bottom: 4px; }
  .facts dd { margin: 0; font-size: 13px; font-weight: 600; overflow-wrap: anywhere; }
  .facts .meaning { font-weight: 400; color: var(--fg-muted); }
  .stats a:hover { background: var(--bg-subtle); text-decoration: none; }
  .stats .s.expired { color: var(--danger); }
  .search-row { display: flex; flex-wrap: wrap; align-items: center; gap: 8px 16px; }
  .search { flex: 1; min-width: 220px; }
  .box-head p { font-size: 13px; color: var(--fg-muted); margin-top: 4px; }
  .box-head a { font-size: 13px; }
  .rule { border-top: 1px solid var(--border-muted); padding: 0 16px; }
  .box-head + .rule { border-top: 0; }
  .rule summary { display: flex; align-items: center; gap: 10px; padding: 11px 0; cursor: pointer; list-style: none; }
  .rule summary::-webkit-details-marker { display: none; }
  .rule summary::before { content: '›'; color: var(--fg-muted); font-size: 20px; line-height: 1; }
  .rule[open] summary::before { transform: rotate(90deg); }
  .rule summary:hover strong { color: var(--accent); }
  .rule-id { font: 600 13px var(--mono); min-width: 0; overflow-wrap: anywhere; }
  .rule summary .label { margin-left: auto; flex: none; }
  .rule-body { padding: 0 0 14px 22px; font-size: 13px; overflow-wrap: anywhere; }
  table { table-layout: fixed; }
  table td { overflow-wrap: anywhere; padding: 12px; }
  .exceptions th:first-child { width: 50%; }
  .exception-group > summary { cursor: pointer; padding: 10px 16px; font-size: 13px; font-weight: 600; overflow-wrap: anywhere; border-top: 1px solid var(--border-muted); background: var(--bg-subtle); }
  .exception-group > p { padding: 10px 16px; font-size: 13px; margin: 0; }
  .modules .label { max-width: 100%; white-space: normal; overflow-wrap: anywhere; }
  .modules th:first-child { width: 45%; }
  .modules th:nth-child(2) { width: 20%; }
  td summary { cursor: pointer; color: var(--accent); font-size: 12px; }
  td summary strong { font: 600 12px var(--mono); }
  td details p { margin-top: 6px; font-size: 12px; }
  .boundary { font: 12px/1.5 var(--mono); margin-top: 4px; overflow-wrap: anywhere; }
  .module-facts { margin-block: 8px; }
  .expiry { display: inline-flex; flex-direction: column; align-items: flex-start; gap: 5px; font-size: 12px; }
  .expiry .label { white-space: normal; }
  progress { appearance: none; display: block; height: 4px; width: 100%; max-width: 180px; border: 0; border-radius: 3px; overflow: hidden; background: var(--border-muted); color: var(--attention); }
  progress::-webkit-progress-bar { background: var(--border-muted); }
  progress::-webkit-progress-value { background: var(--attention); }
  progress::-moz-progress-bar { background: var(--attention); }
  progress.expired { border-left: 3px solid var(--danger); }
  .graph-disclosure { border-top: 1px solid var(--border-muted); }
  .graph-disclosure > summary { cursor: pointer; padding: 12px 16px; font-weight: 600; }
  .graph-disclosure > summary span { margin-left: 8px; font-weight: 400; }
  .graph-body { padding: 0 16px 16px; }
  pre { white-space: pre-wrap; overflow-wrap: anywhere; }
  @media (max-width: 640px) {
    .facts dl { grid-template-columns: minmax(0, 1fr); }
    .search { flex-basis: 100%; min-width: 0; }
    .rule summary { align-items: flex-start; }
    .rule summary::before { margin-top: 1px; }
    .expiry { display: inline-flex; vertical-align: top; }
  }
</style>

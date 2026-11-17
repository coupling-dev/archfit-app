<script lang="ts">
  import { tick } from 'svelte';
  import { repoPage } from '../../lib/route';
  import { mapLayers, type MapModel } from './model';
  import { breakable, cellView, compactFrom, filtersFrom, isNavKey, nextCell, pairIndex, pairKey, statusLabel, symbols, visibleModules, type Cell } from './dsm';

  let { model, repo }: { model: MapModel; repo: string } = $props();
  const id = $props.id();
  const index = $derived(pairIndex(model.lines));
  let search = $state('');
  let focus = $state('');
  let direction = $state<'inbound' | 'outbound'>('outbound');
  let attention = $state(false);
  let collapsed = $state<string[]>([]);
  const compact = $derived(model.modules.length >= compactFrom);
  const modules = $derived(visibleModules(model, { search, focus, direction, attention, collapsed }));
  let selected = $state<{ from: string; to: string } | null>(null);
  let dialog: HTMLDialogElement;
  let table = $state<HTMLTableElement>();
  // The active cell is plain DOM state, not reactive: moving it must touch two rows and two columns, not every cell.
  let active: { from: string; to: string } | null = null;
  let stop: HTMLButtonElement | null = null; // The one cell in the tab order.
  function setStop(next: HTMLButtonElement | null) {
    stop?.setAttribute('tabindex', '-1');
    stop = next;
    next?.setAttribute('tabindex', '0');
  }

  // A column starts a layer band when the module before it is in another layer.
  const bandStart = (list: { layer: string }[], col: number) => col > 0 && list[col - 1].layer !== list[col].layer;
  const hasLines = (from: string, to: string) => index.has(pairKey(from, to));
  const open = (cell: Cell) =>
    cell.row !== cell.col || hasLines(modules[cell.row].name, modules[cell.row].name);
  const button = (row: number, col: number) =>
    table?.tBodies[0].rows[row]?.cells[col + 1]?.querySelector('button') ?? null;
  function cellOf(event: Event): { el: HTMLButtonElement; row: number; col: number } | null {
    const el = event.target instanceof Element ? event.target.closest<HTMLButtonElement>('td > button') : null;
    return el ? { el, row: Number(el.dataset.row), col: Number(el.dataset.col) } : null;
  }
  function highlight(row: number, col: number, on: boolean) {
    const rows = table?.tBodies[0].rows;
    if (!rows?.[row] || !table) return;
    for (const cell of rows[row].cells) cell.classList.toggle('highlight', on);
    table.tHead?.rows[0].cells[col + 1]?.classList.toggle('highlight', on);
    for (const r of rows) r.cells[col + 1]?.classList.toggle('highlight', on);
  }
  function indexOfActive() {
    const row = modules.findIndex((m) => m.name === active?.from);
    const col = modules.findIndex((m) => m.name === active?.to);
    return row >= 0 && col >= 0 ? { row, col } : null;
  }
  function activate(from: string, to: string, row: number, col: number) {
    const old = indexOfActive();
    if (old) highlight(old.row, old.col, false);
    active = { from, to };
    setStop(button(row, col));
    highlight(row, col, true);
  }
  // After the axes change, the cells are new: keep the active cell if it is still drawn, else the first reachable one.
  $effect(() => {
    if (!table) return;
    for (const b of table.querySelectorAll('td > button')) b.setAttribute('tabindex', '-1');
    stop = null;
    table.querySelectorAll('.highlight').forEach((el) => el.classList.remove('highlight'));
    const found = indexOfActive();
    if (found && open(found)) return activate(modules[found.row].name, modules[found.col].name, found.row, found.col);
    active = null;
    for (let row = 0; row < modules.length; row++) {
      const col = modules.findIndex((_, c) => open({ row, col: c }));
      if (col >= 0) return setStop(button(row, col));
    }
  });

  async function details(from: string, to: string) {
    selected = { from, to };
    await tick();
    dialog.showModal();
  }
  function onfocusin(event: FocusEvent) {
    const cell = cellOf(event);
    if (cell) activate(modules[cell.row].name, modules[cell.col].name, cell.row, cell.col);
  }
  function onkeydown(event: KeyboardEvent) {
    const cell = cellOf(event);
    if (!cell || !isNavKey(event.key)) return;
    event.preventDefault();
    const to = nextCell(event.key, event.ctrlKey, cell, modules.length, open);
    button(to.row, to.col)?.focus();
  }
  function onclick(event: MouseEvent) {
    const cell = cellOf(event);
    if (cell) void details(modules[cell.row].name, modules[cell.col].name);
  }
  function reset() { search = ''; focus = ''; direction = 'outbound'; attention = false; collapsed = []; }
  function toggleLayer(layer: string) {
    collapsed = collapsed.includes(layer) ? collapsed.filter((l) => l !== layer) : [...collapsed, layer];
  }
</script>

<div class="dsm">
  {#if model.modules.length === 1 && !model.lines.length}
    <p>One module is declared, so there is nothing to draw.</p>
  {:else}
  <p class="orientation"><strong>Policy and review status</strong> · row depends on column.</p>
  <ul class="legend" aria-label="Matrix legend">
    <li>✓ An allowed dependency</li><li>× One a rule denies</li><li>? A requested dependency</li><li>! Drift</li><li>◷ An exception</li>
  </ul>
  <p class="muted small" id={id + '-scope'}>Blank: the policy says nothing about this pair. It does not mean the dependency is allowed.</p>
  <details class="about small">
    <summary>About this view</summary>
    <p>Deny status is only known for touched, requested or drift pairs. Full deny evaluation is not available.</p>
  </details>

  {#if model.modules.length >= filtersFrom}
    <div class="filters">
      <label>Search modules or layers<input class="form-control" type="search" bind:value={search} /></label>
      <label>Focus module<select class="form-control" bind:value={focus}>
        <option value="">All modules</option>
        {#each visibleModules(model, { search: '', focus: '', direction, attention: false, collapsed: [] }) as m (m.name)}
          <option value={m.name}>{m.name}</option>
        {/each}
      </select></label>
      <label>Direction<select class="form-control" bind:value={direction}>
        <option value="outbound">Outbound (source → targets)</option><option value="inbound">Inbound (sources → target)</option>
      </select></label>
      <label class="attention"><input type="checkbox" bind:checked={attention} /> With denies, requests, drift or exceptions</label>
      <button class="btn" onclick={reset}>Reset filters</button>
    </div>
    <p class="muted small">Focus follows explicit policy relationships only.</p>
    <div class="layers" role="group" aria-label="Visible layers">
      {#each mapLayers(model) as layer (layer)}
        <button class="btn" aria-expanded={!collapsed.includes(layer)} onclick={() => toggleLayer(layer)}>
          {collapsed.includes(layer) ? '▸' : '▾'} {layer || 'No layer'} ({model.modules.filter((m) => m.layer === layer).length} modules)
        </button>
      {/each}
    </div>
    <p class="small" role="status">Showing {modules.length} of {model.modules.length} modules on both axes.</p>
  {/if}

  {#if !model.modules.length}<p>No modules.</p>
  {:else if !modules.length}<p>No modules match these filters. Reset filters to see all modules.</p>
  {:else}
    <p class="muted small">Select a cell for details. Use arrow keys within the table; Enter opens details.</p>
    <div class="viewport" role="region" aria-label="Scrollable relationship matrix">
      <!-- The buttons are the interactive elements; the table only delegates their focus, key and click events. -->
      <!-- svelte-ignore a11y_no_noninteractive_element_interactions -->
      <table bind:this={table} class:compact aria-describedby={id + '-scope'} {onfocusin} {onkeydown} {onclick}>
        <caption class="sr-only">Policy and review status matrix</caption>
        <thead><tr>
          <th class="corner" scope="col">Source ↓<br />Target →</th>
          {#each modules as target, col (target.name)}
            <th scope="col" class="col" class:col-start={bandStart(modules, col)} title={target.name}>
              <span class="layer">{target.layer || 'No layer'}</span><span class="name">{#each breakable(target.name) as part, i (i)}{part}<wbr />{/each}</span>
            </th>
          {/each}
        </tr></thead>
        <tbody>
          {#each modules as source, row (source.name)}
            <tr class:layer-start={row > 0 && modules[row - 1].layer !== source.layer}>
              <th scope="row" title={source.name}>
                <span class="layer" class:repeat={row > 0 && modules[row - 1].layer === source.layer}>{source.layer || 'No layer'}</span><span class="name">{#each breakable(source.name) as part, i (i)}{part}<wbr />{/each}</span>
              </th>
              {#each modules as target, col (target.name)}
                {@const lines = index.get(pairKey(source.name, target.name)) ?? []}
                {@const view = cellView(lines)}
                {#if row === col && !lines.length}
                  <td class="self" class:col-start={bandStart(modules, col)}></td>
                {:else}
                  <td class:col-start={bandStart(modules, col)}>
                    <button
                      tabindex="-1"
                      data-row={row}
                      data-col={col}
                      aria-label={source.name + ' to ' + target.name + ': ' + (lines.length ? lines.map(statusLabel).join('; ') : 'no rule')}
                      aria-haspopup="dialog"
                    >
                      {#if view}
                        <span class={'mark ' + view.kind} class:soon={view.soon} class:quiet={view.quiet} aria-hidden="true">{symbols[view.kind]}</span>
                        {#if view.count > 1}<span class="count" aria-hidden="true">{view.count}</span>{/if}
                      {/if}
                    </button>
                  </td>
                {/if}
              {/each}
            </tr>
          {/each}
        </tbody>
      </table>
    </div>
  {/if}
  {/if}
</div>

<dialog bind:this={dialog} aria-labelledby={id + '-detail-title'}>
  {#if selected}
    <div class="detail-head">
      <h3 id={id + '-detail-title'}>{selected.from} → {selected.to}</h3>
      <button class="btn" onclick={() => dialog.close()}>Close details</button>
    </div>
    <dl><dt>Source</dt><dd>{selected.from}</dd><dt>Target</dt><dd>{selected.to}</dd></dl>
    <h4>Statuses</h4>
    <ul class="statuses">
      {#each index.get(pairKey(selected.from, selected.to)) ?? [] as line, i (i)}
        <li>
          <span aria-hidden="true">{symbols[line.kind]}</span> {statusLabel(line)}
          {#if line.request !== undefined}
            <a href={repoPage(repo, `/requests/${line.request}`)}>Open request #{line.request}</a>
          {/if}
        </li>
      {:else}<li>The policy says nothing about this pair.</li>{/each}
    </ul>
    <h4>About this pair</h4>
    <p>Allowed entries are policy declarations, not observed imports. Exception dates are the policy's own; an exception
      without a date shows no date.</p>
  {/if}
</dialog>

<style>
  .dsm { min-width: 0; }
  .orientation { margin-bottom: 8px; }
  .legend { display: flex; flex-wrap: wrap; list-style: none; gap: 6px 16px; padding: 0; margin: 8px 0; font-size: 13px; }
  .small { margin-block: 8px; }
  .about summary { cursor: pointer; }
  .viewport { max-height: 65vh; max-width: 100%; overflow: auto; border: 1px solid var(--border); border-radius: var(--radius); }
  table { border-collapse: separate; border-spacing: 0; width: max-content; min-width: 100%; font-size: 13px; }
  th, td { padding: 0; border-right: 1px solid var(--border-muted); border-bottom: 1px solid var(--border-muted); text-align: center; }
  /* Names break only where the script put a <wbr> (after _ and -), never inside a word. */
  th { background: var(--bg-subtle); padding: 8px; min-width: 80px; overflow-wrap: normal; }
  thead th { position: sticky; top: 0; z-index: 2; }
  tbody th, .corner { position: sticky; left: 0; text-align: left; min-width: 105px; }
  tbody th { z-index: 1; }
  .corner { z-index: 3; }
  .layer { display: block; font-size: 11px; font-weight: 400; color: var(--fg-muted); }
  .layer-start > * { border-top: 2px solid var(--border); }
  td button { display: flex; align-items: center; justify-content: center; flex-wrap: wrap; gap: 4px; padding: 8px; width: 100%; min-height: 48px; min-width: 64px; border: 0; border-radius: 0; background: transparent; color: var(--fg); cursor: pointer; }
  td button:hover { background: var(--accent-subtle); }
  td button:focus-visible { outline-offset: -3px; }
  :where(td.self) { background: var(--bg-subtle); }
  /* Set by script on the active row and column, so Svelte cannot see the class. */
  table :global(.highlight) { background: var(--accent-subtle); }
  table :global(th.highlight) { background: var(--bg-subtle); box-shadow: inset 0 -3px var(--accent); }
  .col-start { border-left: 2px solid var(--border); }
  .mark { font-size: 18px; font-weight: 700; line-height: 1; }
  .mark.quiet { color: var(--fg-muted); font-weight: 400; }
  .mark.soon { display: grid; place-items: center; width: 24px; height: 24px; border: 2px solid var(--attention); border-radius: 50%; font-size: 14px; }
  .count { font-size: 11px; font-weight: 600; color: var(--fg-muted); }
  /* Compact: 32 px symbol cells, a 220 px row header, and vertical column names. */
  table.compact { font-size: 12px; min-width: 0; }
  table.compact td, table.compact th.col { width: 32px; min-width: 32px; max-width: 32px; }
  table.compact td button { width: 32px; height: 32px; min-width: 0; min-height: 0; padding: 0; gap: 2px; flex-wrap: nowrap; }
  table.compact .mark { font-size: 16px; }
  table.compact .mark.soon { width: 20px; height: 20px; font-size: 12px; }
  table.compact th.col { padding: 6px 0; vertical-align: bottom; }
  table.compact th.col .name { display: block; margin-inline: auto; writing-mode: vertical-rl; transform: rotate(180deg); white-space: nowrap; max-height: 12em; overflow: hidden; text-overflow: ellipsis; }
  table.compact tbody th, table.compact .corner { width: 220px; min-width: 220px; max-width: 220px; padding: 2px 8px; }
  table.compact th.col .layer, table.compact .layer.repeat { position: absolute; width: 1px; height: 1px; overflow: hidden; clip-path: inset(50%); }
  @media (width < 720px) {
    table.compact tbody th, table.compact .corner { width: 140px; min-width: 140px; max-width: 140px; }
  }
  .allowed { color: var(--success); }
  .deny, .drift { color: var(--danger); }
  .requested, .exception { color: var(--attention); }
  .filters { display: flex; flex-wrap: wrap; gap: 12px; align-items: end; margin-top: 16px; }
  .filters label { display: grid; gap: 4px; min-width: 0; max-width: 100%; }
  .filters input, .filters select { max-width: 100%; min-height: 44px; }
  .filters .attention { display: flex; align-items: center; min-height: 44px; }
  .layers { display: flex; flex-wrap: wrap; gap: 8px; margin-block: 12px; }
  .btn { min-height: 44px; }
  dialog { color: var(--fg); background: var(--bg); border: 1px solid var(--border); border-radius: var(--radius); padding: 20px; width: min(600px, calc(100% - 32px)); max-height: 85vh; overflow: auto; overflow-wrap: anywhere; margin: auto; }
  dialog::backdrop { background: #0008; }
  .detail-head { display: flex; justify-content: space-between; align-items: start; gap: 16px; }
  .detail-head h3 { min-width: 0; }
  .detail-head button { flex-shrink: 0; }
  dl { display: grid; grid-template-columns: auto 1fr; gap: 4px 12px; margin-block: 16px; }
  dt { font-weight: 600; }
  dd { margin: 0; }
  .statuses { padding-left: 20px; }
  .statuses li { margin-block: 8px; }
  .statuses a { display: inline-block; padding: 4px; text-decoration: underline; }
  h4 { margin-top: 16px; }
  dialog p { margin-block: 8px; }
</style>

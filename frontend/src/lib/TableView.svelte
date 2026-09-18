<script>
  // DataGrip-style data view for one table: WHERE / ORDER BY filters, paging, sortable headers,
  // and the grid's editing actions in a toolbar. The SQL is generated, shown read-only.
  import { createEventDispatcher, tick } from 'svelte'
  import ResultGrid from './ResultGrid.svelte'
  import { icons } from './icons.js'
  import { OPS, opLabel, tableSQL, filterSQL } from './filters.js'
  import { highlightSQL } from './highlight.js'
  export let tab            // { table, result, running, applying, error, status, where, orderBy, page, pageSize }
  export let active = true
  export let driver = 'mysql'   // identifier quoting / LIKE casting for column filters
  const dispatch = createEventDispatcher()
  let grid, changeCount = 0, hasSelection = false
  let where = tab.where || '', orderBy = tab.orderBy || ''
  let showSQL = false

  $: sql = tableSQL(tab, driver)
  $: filters = tab.filters || {}

  // ---- column filter popover ----
  let pop = null            // { col, op, value, x, y }
  let popInput, popEl
  async function openFilter({ col, x, y }) {
    const f = filters[col]
    pop = { col, op: f?.op || 'eq', value: f?.value ?? '', x, y }
    await tick()
    if (popEl) { const r = popEl.getBoundingClientRect(); if (r.right > innerWidth) popEl.style.left = Math.max(0, innerWidth - r.width - 8) + 'px' }
    popInput?.focus(); popInput?.select()
  }
  $: popNeedsValue = pop ? OPS.find(o => o.id === pop.op)?.needsValue : false
  function applyFilter() {
    if (!pop) return
    const nf = { ...filters }
    if (popNeedsValue && pop.value === '') delete nf[pop.col]
    else nf[pop.col] = { op: pop.op, value: popNeedsValue ? pop.value : '' }
    pop = null
    reload({ where, orderBy, filters: nf })
  }
  function clearFilter(col) {
    const nf = { ...filters }; delete nf[col]
    pop = null
    reload({ where, orderBy, filters: nf })
  }
  function clearAllFilters() { pop = null; reload({ where, orderBy, filters: {} }) }
  function popKey(e) {
    if (e.key === 'Enter') { e.preventDefault(); applyFilter() }
    else if (e.key === 'Escape') { e.preventDefault(); pop = null }
  }
  $: sort = parseSort(tab.orderBy)
  function parseSort(o) {
    const m = /^\s*([\w.]+)\s*(asc|desc)?\s*$/i.exec(o || '')
    return m ? { col: m[1].split('.').pop(), dir: (m[2] || 'asc').toLowerCase() } : null
  }
  $: start = (tab.page || 0) * (tab.pageSize || 500)
  $: count = tab.result?.rows?.length || 0
  $: more = !!tab.result?.truncated
  $: rangeText = tab.result ? (count ? `${start + 1}-${start + count} of ${start + count}${more ? '+' : ''}` : (start ? 'no more rows' : '0 rows')) : ''

  async function reload(p = {}) {
    if (changeCount && !(await ask('Discard pending changes and reload?', { title: 'Pending changes', ok: 'Discard', key: 'discardEdits' }))) return
    dispatch('change', { where, orderBy, page: 0, ...p })
  }
  const ask = (m, o) => window.durusqlAsk ? window.durusqlAsk(m, o) : Promise.resolve(confirm(m))
  function apply() { reload({ where, orderBy }) }
  function sortBy(col, dir) {
    const cur = parseSort(tab.orderBy)
    orderBy = dir ? `${col}${dir === 'desc' ? ' DESC' : ''}` : (cur?.col === col && cur.dir === 'asc' ? `${col} DESC` : col)
    pop = null
    reload({ orderBy })
  }
  function setPage(p) { if (p >= 0) reload({ where, orderBy, page: p }) }
  function setPageSize(e) { reload({ where, orderBy, page: 0, pageSize: Number(e.target.value) }) }
  // first load happens when the tab is (or becomes) visible; restored tabs wait until shown
  let loaded = false
  $: if (active && !loaded && !tab.running) { loaded = true; if (!tab.result && !tab.loadedOnce) dispatch('change', { where, orderBy, page: tab.page || 0 }) }
</script>

<div class="view">
  <div class="tools">
    <button class="icon" title="Reload" disabled={tab.running} on:click={() => reload({ where, orderBy, page: tab.page || 0 })}>{@html icons.refresh}</button>
    <span class="sep"></span>
    <button class="icon" title="Add row (Alt+Insert)" on:click={() => grid?.addRow()}>{@html icons.plus}</button>
    <button class="icon" title="Delete selected rows (Ctrl+Y)" disabled={!hasSelection} on:click={() => grid?.deleteSelectedRows()}>{@html icons.x}</button>
    <span class="sep"></span>
    <button class="submit" class:primary={changeCount > 0} disabled={!changeCount || tab.applying || tab.running} on:click={() => grid?.submit()} title="Submit changes (Ctrl+Enter in the grid)">
      {@html icons.import} Submit{#if changeCount}<span class="badge">{changeCount}</span>{/if}
    </button>
    <button class="ghost" disabled={!changeCount} on:click={() => grid?.revertAll()}>Revert</button>
    <span class="sep"></span>
    <button class="ghost" on:click={() => grid?.copyCSV()} title="Copy this page as CSV to the clipboard">Copy CSV</button>
    <button class="ghost" on:click={e => dispatch('exportMenu', { x: e.clientX, y: e.clientY })} title="Export all rows matching the current filters (CSV, JSON or Excel)">Export ▾</button>
    <button class="ghost" on:click={() => dispatch('console', sql)} title="Open this query in a new console">Console</button>
    <button class="ghost" class:on={showSQL} on:click={() => showSQL = !showSQL} title="Show the generated query">SQL</button>
    <span class="spacer"></span>
    <span class="muted small mono">{tab.table}</span>
    {#if tab.result && !tab.result.keys?.length}<span class="warn small" title="No primary key: rows are matched on all columns">no key</span>{/if}
  </div>

  {#if Object.keys(filters).length}
    <div class="chips">
      {#each Object.entries(filters) as [col, f] (col)}
        <button class="chip mono" title={filterSQL(col, f, driver)} on:click={e => openFilter({ col, x: e.clientX, y: e.clientY })}>
          <b>{col}</b> {opLabel(f.op)} {f.value}<span class="chipx" role="button" tabindex="-1" on:click|stopPropagation={() => clearFilter(col)}>×</span>
        </button>
      {/each}
      <button class="ghost small" on:click={clearAllFilters}>Clear all</button>
    </div>
  {/if}
  <div class="filters">
    <label class="f"><span class="kw">WHERE</span><input class="mono" placeholder="id > 100 AND status = 'open'" bind:value={where} on:keydown={e => e.key === 'Enter' && apply()} /></label>
    <label class="f order"><span class="kw">ORDER BY</span><input class="mono" placeholder="updated DESC" bind:value={orderBy} on:keydown={e => e.key === 'Enter' && apply()} /></label>
    <button class="ghost small" on:click={apply} disabled={tab.running}>Apply</button>
    {#if where !== (tab.where || '') || orderBy !== (tab.orderBy || '')}<span class="muted small">press Enter</span>{/if}
  </div>
  {#if showSQL}<div class="sqlline mono">{@html highlightSQL(sql)}</div>{/if}

  <div class="statusbar">
    {#if tab.error}<span class="err mono">{tab.error}</span>{:else}<span class="muted">{tab.running ? 'Loading…' : tab.status}</span>{/if}
  </div>

  <ResultGrid bind:this={grid} result={tab.result} busy={tab.applying || tab.running} toolbar={false} sortable {sort} filterable {filters}
              bind:changeCount bind:hasSelection on:submit on:sort={e => sortBy(e.detail)} on:filter={e => openFilter(e.detail)} on:exportMenu />

  <div class="pager">
    <button class="icon" disabled={!tab.page} on:click={() => setPage(0)} title="First page">⏮</button>
    <button class="icon" disabled={!tab.page} on:click={() => setPage((tab.page || 0) - 1)} title="Previous page">◀</button>
    <span class="range">{rangeText}</span>
    <button class="icon" disabled={!more} on:click={() => setPage((tab.page || 0) + 1)} title="Next page">▶</button>
    <select class="ps" value={tab.pageSize || 500} on:change={setPageSize} title="Rows per page">
      {#each [100, 200, 500, 1000, 5000] as n}<option value={n}>{n}</option>{/each}
    </select>
  </div>
</div>

<svelte:window on:mousedown={e => { if (pop && popEl && !popEl.contains(e.target)) pop = null }} />
{#if pop}
  <div class="pop" bind:this={popEl} style="left:{pop.x}px; top:{pop.y}px" role="dialog" on:keydown={popKey}>
    <div class="poph mono">{pop.col}</div>
    <div class="poprow">
      <select bind:value={pop.op}>{#each OPS as o}<option value={o.id}>{o.label}</option>{/each}</select>
    </div>
    {#if popNeedsValue}
      <div class="poprow"><input class="mono" bind:this={popInput} bind:value={pop.value} placeholder={pop.op === 'in' ? 'a, b, c' : 'value'} /></div>
    {/if}
    <div class="poprow actions">
      <button class="primary small" on:click={applyFilter}>Apply</button>
      <button class="small" disabled={!filters[pop.col]} on:click={() => clearFilter(pop.col)}>Clear</button>
      <span class="spacer"></span>
      <button class="ghost small" title="Sort ascending" on:click={() => sortBy(pop.col, 'asc')}>▲ Sort</button>
      <button class="ghost small" title="Sort descending" on:click={() => sortBy(pop.col, 'desc')}>▼ Sort</button>
    </div>
  </div>
{/if}

<style>
  .pop { position: fixed; z-index: 1000; width: 300px; background: var(--bg3); border: 1px solid var(--line); border-radius: 0; box-shadow: 0 8px 24px rgba(0,0,0,.45); padding: 8px; }
  .poph { font-size: 12px; color: var(--fg2); margin-bottom: 6px; }
  .poprow { margin-bottom: 6px; }
  .poprow select, .poprow input { padding: 3px 6px; font-size: 12px; }
  .actions { display: flex; gap: 6px; align-items: center; margin-bottom: 0; }
  button.small { padding: 2px 8px; font-size: 12px; }
  .chips { display: flex; flex-wrap: wrap; align-items: center; gap: 4px; padding: 3px 8px 0; background: var(--bg2); flex: none; }
  .chip { display: inline-flex; align-items: center; gap: 4px; padding: 1px 4px 1px 8px; font-size: 11px; border-radius: 10px; border: 1px solid #e7b64a; color: #e7b64a; background: rgba(231,182,74,.1); }
  .chip:hover { background: rgba(231,182,74,.2); }
  .chipx { padding: 0 4px; border-radius: 8px; font-size: 13px; line-height: 1; }
  .chipx:hover { background: #e7b64a; color: #1e1f22; }
  .view { display: flex; flex-direction: column; min-height: 0; height: 100%; }
  .tools { display: flex; align-items: center; gap: 4px; padding: 3px 8px; border-bottom: 1px solid var(--line); background: var(--bg2); flex: none; }
  .tools .sep { width: 1px; height: 16px; background: var(--line); margin: 0 4px; }
  .spacer { flex: 1; }
  .small { font-size: 11px; }
  .warn { color: #e7b64a; border: 1px solid #e7b64a; border-radius: 10px; padding: 0 6px; }
  .submit { display: inline-flex; align-items: center; gap: 5px; padding: 2px 8px; }
  .badge { background: rgba(255,255,255,.2); border-radius: 8px; padding: 0 6px; font-size: 11px; }
  button.on { color: var(--acc2); }
  .filters { display: flex; align-items: center; gap: 8px; padding: 4px 8px; border-bottom: 1px solid var(--line); background: var(--bg2); flex: none; }
  .f { display: flex; align-items: center; gap: 6px; flex: 2; margin: 0; }
  .f.order { flex: 1; }
  .kw { font-size: 11px; font-weight: 600; color: var(--fg2); letter-spacing: .03em; white-space: nowrap; }
  .f input { padding: 2px 8px; font-size: 12px; }
  .sqlline { padding: 3px 12px; font-size: 12px; color: var(--fg2); background: var(--bg); border-bottom: 1px solid var(--line); white-space: pre-wrap; user-select: text; flex: none; }
  .statusbar { padding: 2px 12px; background: var(--bg2); border-bottom: 1px solid var(--line); min-height: 22px; white-space: pre-wrap; font-size: 12px; flex: none; }
  .view > :global(.wrap) { flex: 1; min-height: 0; }
  .pager { display: flex; align-items: center; justify-content: center; gap: 6px; padding: 2px 8px; border-top: 1px solid var(--line); background: var(--bg2); flex: none; font-size: 12px; }
  .range { color: var(--fg2); min-width: 120px; text-align: center; }
  .ps { width: auto; padding: 1px 4px; font-size: 11px; }
</style>

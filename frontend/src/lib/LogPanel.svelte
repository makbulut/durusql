<script>
  import { createEventDispatcher } from 'svelte'
  import { icons } from './icons.js'
  import { highlightSQL } from './highlight.js'
  export let log = []          // session output, newest last: { at: Date, conn, sql, ms, rows, err }
  export let open = true
  export let mode = 'output'   // 'output' | 'history'
  export let history = []      // persisted history of the active connection, newest first: { at, sql, ms, rows, err }
  export let historyConn = ''  // name of the connection the history belongs to
  const dispatch = createEventDispatcher()
  let el, filter = ''
  $: if (el && mode === 'output' && log.length) requestAnimationFrame(() => el.scrollTop = el.scrollHeight)
  const t = d => new Date(d).toTimeString().slice(0, 8)
  const day = d => new Date(d).toISOString().slice(0, 10)
  const one = s => s.replace(/\s+/g, ' ').trim()
  $: q = filter.trim().toLowerCase()
  $: shown = history.filter(h => !q || h.sql.toLowerCase().includes(q) || (h.err || '').toLowerCase().includes(q))
  const ctx = (e, h) => { e.preventDefault(); dispatch('menu', { x: e.clientX, y: e.clientY, entry: h }) }
</script>

<section class="log">
  <div class="head">
    <button class="ghost tgl" on:click={() => dispatch('toggle')} title={open ? 'Hide panel' : 'Show panel'}>{open ? '▾' : '▸'}</button>
    <button class="tab" class:on={mode === 'output'} on:click={() => dispatch('mode', 'output')}>Output{#if log.length}<span class="cnt">{log.length}</span>{/if}</button>
    <button class="tab" class:on={mode === 'history'} on:click={() => dispatch('mode', 'history')}>History{#if historyConn}<span class="cnt">{historyConn}</span>{/if}</button>
    {#if open && mode === 'history'}
      <input class="hfilter" placeholder="Search history…" bind:value={filter} />
    {/if}
    <span class="spacer"></span>
    {#if open && mode === 'output'}<button class="ghost" disabled={!log.length} on:click={() => dispatch('clear')}>Clear</button>{/if}
    {#if open && mode === 'history'}
      <button class="icon" title="Refresh" on:click={() => dispatch('refreshHistory')}>{@html icons.refresh}</button>
      <button class="ghost" disabled={!history.length} on:click={() => dispatch('clearHistory')}>Clear history</button>
    {/if}
  </div>
  {#if open && mode === 'output'}
  <div class="rows mono" bind:this={el}>
    {#if !log.length}<p class="muted pad">Executed statements, timings and errors show up here.</p>{/if}
    {#each log as e}
      <div class="row" class:bad={e.err} on:dblclick={() => dispatch('load', e.sql)} title="Double-click to load into editor">
        <span class="ts">{t(e.at)}</span>
        <span class="st">{@html e.err ? icons.err : icons.ok}</span>
        <span class="cn">{e.conn}&gt;</span>
        <span class="sql">{@html highlightSQL(one(e.sql))}</span>
        <span class="meta">{e.err ? e.err : `${e.rows ?? 0} rows · ${e.ms} ms`}</span>
      </div>
    {/each}
  </div>
  {:else if open}
  <div class="rows mono">
    {#if !historyConn}<p class="muted pad">Select a connection to see its query history.</p>
    {:else if !shown.length}<p class="muted pad">{history.length ? 'No matches.' : 'No queries run on this connection yet.'}</p>{/if}
    {#each shown as h, i}
      {#if i === 0 || day(h.at) !== day(shown[i - 1].at)}<div class="day">{day(h.at)}</div>{/if}
      <div class="row" class:bad={h.err} on:dblclick={() => dispatch('load', h.sql)} on:contextmenu={e => ctx(e, h)} title={h.sql.length > 200 ? h.sql.slice(0, 200) + '…' : h.sql}>
        <span class="ts">{t(h.at)}</span>
        <span class="st">{@html h.err ? icons.err : icons.ok}</span>
        <span class="sql">{@html highlightSQL(one(h.sql))}</span>
        <span class="meta">{h.err ? h.err : `${h.rows ?? 0} rows · ${h.ms} ms`}</span>
      </div>
    {/each}
  </div>
  {/if}
</section>

<style>
  .log { display: flex; flex-direction: column; min-height: 0; height: 100%; background: var(--bg); }
  .head { display: flex; align-items: center; gap: 8px; padding: 2px 10px; background: var(--bg2); border-bottom: 1px solid var(--line); min-height: 26px; }
  .tab { background: none; border: 0; padding: 2px 6px; font-weight: 600; font-size: 12px; color: var(--fg2); }
  .tab.on { color: var(--fg); box-shadow: inset 0 -2px var(--acc); }
  .tgl { padding: 0 4px; color: var(--fg2); }
  .cnt { font-size: 11px; margin-left: 5px; color: var(--fg2); font-weight: 400; }
  .hfilter { width: 260px; padding: 2px 8px; font-size: 12px; }
  .spacer { flex: 1; }
  .rows { overflow: auto; flex: 1; font-size: 12px; }
  .row { display: flex; gap: 10px; align-items: center; padding: 2px 10px; white-space: nowrap; }
  .row:hover { background: var(--hover); }
  .day { padding: 4px 10px 1px; color: var(--fg2); font-size: 11px; font-family: var(--font); }
  .ts { color: var(--fg2); }
  .st { display: inline-flex; color: var(--ok); }
  .bad .st { color: var(--err); }
  .cn { color: var(--acc); }
  .sql { flex: 1; overflow: hidden; text-overflow: ellipsis; }
  .meta { color: var(--fg2); }
  .bad .meta { color: var(--err); max-width: 50%; overflow: hidden; text-overflow: ellipsis; }
  .pad { padding: 8px 10px; }
</style>

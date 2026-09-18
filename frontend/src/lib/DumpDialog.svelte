<script>
  // "Export with mysqldump…" dialog, modelled on DataGrip's.
  import { createEventDispatcher, onMount } from 'svelte'
  import * as api from './api.js'
  export let connId, connName = '', database = '', table = ''
  const dispatch = createEventDispatcher()
  let o = null, driver = 'mysql', preview = '', running = false, msg = '', err = '', done = null
  let tablesText = ''
  const key = () => 'durusql.dump.' + driver

  onMount(async () => {
    const d = await api.dumpDefaults(connId, database, table)
    driver = d.driver
    o = d.options
    try { // remember executable / output template / options between runs (per driver)
      const saved = JSON.parse(localStorage.getItem(key()) || 'null')
      if (saved) { const { database: _d, tables: _t, ...rest } = saved; o = { ...o, ...rest } }
    } catch {}
    tablesText = (o.tables || []).join(', ')
    refresh()
  })
  let timer
  function refresh() {
    clearTimeout(timer)
    timer = setTimeout(async () => {
      if (!o) return
      o.tables = tablesText.split(/[,\s]+/).map(s => s.trim()).filter(Boolean)
      try { preview = await api.dumpPreview(connId, o) } catch (e) { preview = String(e) }
    }, 120)
  }
  $: o && tablesText !== undefined && refresh()
  async function pickExe() { const p = await api.pickFile('Path to dump executable'); if (p) { o.executable = p; refresh() } }
  async function pickOut() { const p = await api.pickSavePath('Output file', o.outPath.replace(/\{[^}]+\}/g, 'x')); if (p) { o.outPath = p; refresh() } }
  async function run() {
    running = true; err = ''; msg = 'Running…'; done = null
    o.tables = tablesText.split(/[,\s]+/).map(s => s.trim()).filter(Boolean)
    try {
      const r = await api.runDump(connId, o)
      done = r
      msg = `Done: ${r.path} (${(r.rows / 1048576).toFixed(2)} MB)`
      try { localStorage.setItem(key(), JSON.stringify(o)) } catch {}
      dispatch('done', r)
    } catch (e) { err = String(e); msg = '' } finally { running = false }
  }
  const MY = [
    ['addDropTable', 'Add DROP TABLE before CREATE TABLE'], ['completeInsert', 'Include column names in each INSERT'],
    ['disableKeys', 'Add DISABLE KEYS before each INSERT'], ['createOptions', 'Include all table options in CREATE TABLE'],
    ['lockTables', 'Add LOCK TABLES before each table dump'], ['routines', 'Include stored routines in the dump'],
    ['addDropTrigger', 'Add DROP TRIGGER before CREATE TRIGGER'], ['lockAllTables', 'Lock all tables for the duration of export'],
    ['schemaOnly', 'Export schema without data'], ['events', 'Include events in the dump'],
    ['noTablespaces', 'Export schema without tablespaces'], ['extendedInsert', 'Use single INSERT for multiple rows'],
    ['dataOnly', 'Export without table creation'], ['triggers', 'Include triggers'],
    ['singleTransaction', 'Single transaction (consistent snapshot)'], ['quick', 'Quick (stream rows, low memory)'],
    ['addDropDatabase', 'Add DROP DATABASE before CREATE DATABASE'],
  ]
  const PG = [
    ['schemaOnly', 'Export schema without data'], ['clean', 'Add DROP before CREATE'],
    ['dataOnly', 'Export data only'], ['ifExists', 'Use IF EXISTS with DROP'],
    ['inserts', 'Use INSERT instead of COPY'], ['columnInserts', 'Include column names in each INSERT'],
    ['noOwner', 'Skip ownership and privileges'],
  ]
</script>

<div class="backdrop" on:mousedown|self={() => !running && dispatch('close')} role="presentation">
  <div class="modal" role="dialog" aria-label="Export with dump tool">
    <h2>Export with {driver === 'postgres' ? 'pg_dump' : 'mysqldump'}… <span class="muted">({connName})</span></h2>
    {#if o}
      <div class="grid">
        <label for="exe">Path to executable:</label>
        <div class="row"><input id="exe" class="mono" bind:value={o.executable} on:input={refresh} /><button class="icon" title="Browse…" on:click={pickExe}>📁</button></div>
        <label for="out">Output result to:</label>
        <div class="row"><input id="out" class="mono" bind:value={o.outPath} on:input={refresh} /><button class="icon" title="Browse…" on:click={pickOut}>📁</button></div>
        <span></span><span class="hint">Allowed substitution patterns: {'{timestamp}'}, {'{data_source}'}, {'{database}'}, {'{table}'}</span>
      </div>

      <div class="section">Options</div>
      <div class="grid">
        <label for="db">{driver === 'postgres' ? 'Schema to dump:' : 'Database to dump:'}</label>
        <input id="db" class="mono" bind:value={o.database} on:input={refresh} />
        <label for="tb">Tables to dump:</label>
        <input id="tb" class="mono" bind:value={tablesText} on:input={refresh} placeholder="empty = whole database; separate with commas" />
        <label for="ex">Extra arguments:</label>
        <input id="ex" class="mono" bind:value={o.extra} on:input={refresh} placeholder="--ignore-table=db.big_log …" />
      </div>

      <div class="checks">
        {#each (driver === 'postgres' ? PG : MY) as [k, label]}
          <label class="check"><input type="checkbox" bind:checked={o[k]} on:change={refresh} /> {label}</label>
        {/each}
      </div>

      <pre class="preview mono">{preview}</pre>

      <div class="actions">
        <span class="msg" class:err={!!err}>{err || msg}</span>
        <button on:click={() => dispatch('close')} disabled={running}>{done ? 'Close' : 'Cancel'}</button>
        <button class="primary" on:click={run} disabled={running || !o.database}>{running ? 'Running…' : 'Run'}</button>
      </div>
    {:else}
      <p class="muted">Loading…</p>
    {/if}
  </div>
</div>

<style>
  .backdrop { position: fixed; inset: 0; background: rgba(0,0,0,.55); display: grid; place-items: center; z-index: 950; }
  .modal { background: var(--bg2); border: 1px solid var(--line); padding: 16px 20px; width: 860px; max-width: 96vw; max-height: 92vh; overflow: auto; }
  h2 { margin: 0 0 14px; font-size: 15px; }
  .grid { display: grid; grid-template-columns: 150px 1fr; gap: 8px 12px; align-items: center; }
  .grid label { margin: 0; text-align: right; color: var(--fg); font-size: 12.5px; }
  .row { display: flex; gap: 6px; }
  .hint { color: var(--fg2); font-size: 11.5px; }
  .section { margin: 16px 0 8px; font-weight: 600; font-size: 12.5px; border-bottom: 1px solid var(--line); padding-bottom: 4px; }
  .checks { display: grid; grid-template-columns: 1fr 1fr; gap: 4px 20px; margin: 12px 0 12px 20px; }
  .check { display: flex; align-items: center; gap: 8px; color: var(--fg); font-size: 12.5px; margin: 0; }
  .check input { width: auto; }
  .preview { background: var(--bg); border: 1px solid var(--line); padding: 8px 10px; font-size: 12px; white-space: pre-wrap; word-break: break-all; min-height: 60px; max-height: 160px; overflow: auto; margin: 0; user-select: text; }
  .actions { display: flex; gap: 8px; align-items: center; margin-top: 14px; }
  .msg { flex: 1; font-size: 12px; color: var(--fg2); overflow: hidden; text-overflow: ellipsis; }
  .msg.err { color: var(--err); }
</style>

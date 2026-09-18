<script>
  // "Restore with mysql / psql…" dialog: import a .sql (or .sql.gz) dump into a database.
  import { createEventDispatcher, onMount } from 'svelte'
  import * as api from './api.js'
  export let connId, connName = '', database = '', databases = []
  const dispatch = createEventDispatcher()
  let o = null, driver = 'mysql', preview = '', running = false, msg = '', err = '', done = false
  const key = () => 'durusql.restore.' + driver

  onMount(async () => {
    const d = await api.restoreDefaults(connId, database)
    driver = d.driver
    o = d.options
    try {
      const saved = JSON.parse(localStorage.getItem(key()) || 'null')
      if (saved) { o = { ...o, executable: saved.executable || o.executable, inPath: saved.inPath || '', force: !!saved.force, disableFKChecks: saved.disableFKChecks ?? o.disableFKChecks, onErrorStop: saved.onErrorStop ?? o.onErrorStop, singleTransaction: !!saved.singleTransaction } }
    } catch {}
    refresh()
  })
  let timer
  function refresh() {
    clearTimeout(timer)
    timer = setTimeout(async () => { if (o) { try { preview = await api.restorePreview(connId, o) } catch (e) { preview = String(e) } } }, 120)
  }
  async function pickExe() { const p = await api.pickFile('Path to client executable'); if (p) { o.executable = p; refresh() } }
  async function pickIn() { const p = await api.pickOpenFile('Dump file to import', '*.sql;*.sql.gz;*.gz;*.dump'); if (p) { o.inPath = p; refresh() } }
  async function run() {
    const q = `${o.inPath}\n→ ${o.database || '(connection default)'} on ${connName}\n\nStatements in the file run as-is; a dump with DROP TABLE replaces existing tables.`
    if (!(await (window.durusqlAsk ? window.durusqlAsk(q, { title: 'Import dump?', ok: 'Import', danger: true }) : Promise.resolve(confirm(q))))) return
    running = true; err = ''; msg = 'Importing… this can take a while for big dumps'
    try {
      const r = await api.runRestore(connId, o)
      done = true
      msg = `Done in ${(r.ms / 1000).toFixed(1)} s` + (r.warnings ? ` · ${r.warnings.split('\n').length} message(s) from the client` : '')
      if (r.warnings) preview = r.warnings
      try { localStorage.setItem(key(), JSON.stringify(o)) } catch {}
      dispatch('done', { ...r, database: o.database })
    } catch (e) { err = String(e); msg = '' } finally { running = false }
  }
</script>

<div class="backdrop" on:mousedown|self={() => !running && dispatch('close')} role="presentation">
  <div class="modal" role="dialog" aria-label="Restore dump">
    <h2>Restore with {driver === 'postgres' ? 'psql' : 'mysql'}… <span class="muted">({connName})</span></h2>
    {#if o}
      <div class="grid">
        <label for="exe">Path to executable:</label>
        <div class="row"><input id="exe" class="mono" bind:value={o.executable} on:input={refresh} /><button class="icon" title="Browse…" on:click={pickExe}>📁</button></div>
        <label for="in">Dump file:</label>
        <div class="row"><input id="in" class="mono" bind:value={o.inPath} on:input={refresh} placeholder="/path/to/dump.sql or .sql.gz" /><button class="icon" title="Browse…" on:click={pickIn}>📁</button></div>
        <label for="db">{driver === 'postgres' ? 'Target database:' : 'Target database:'}</label>
        <div class="row">
          <input id="db" class="mono" bind:value={o.database} on:input={refresh} list="dblist" placeholder="database name" />
          <datalist id="dblist">{#each databases as d}<option value={d} />{/each}</datalist>
        </div>
        <label for="ex">Extra arguments:</label>
        <input id="ex" class="mono" bind:value={o.extra} on:input={refresh} placeholder="--max-allowed-packet=1G …" />
      </div>

      <div class="section">Options</div>
      <div class="checks">
        {#if driver === 'postgres'}
          <label class="check"><input type="checkbox" bind:checked={o.onErrorStop} on:change={refresh} /> Stop at the first error (ON_ERROR_STOP)</label>
          <label class="check"><input type="checkbox" bind:checked={o.singleTransaction} on:change={refresh} /> Run as a single transaction</label>
        {:else}
          <label class="check"><input type="checkbox" bind:checked={o.createDatabase} on:change={refresh} /> Create the database if it does not exist</label>
          <label class="check"><input type="checkbox" bind:checked={o.disableFKChecks} on:change={refresh} /> Disable foreign key and unique checks during import</label>
          <label class="check"><input type="checkbox" bind:checked={o.force} on:change={refresh} /> Continue after SQL errors (--force)</label>
        {/if}
      </div>

      <pre class="preview mono">{preview}</pre>

      <div class="actions">
        <span class="msg" class:err={!!err}>{err || msg}</span>
        <button on:click={() => dispatch('close')} disabled={running}>{done ? 'Close' : 'Cancel'}</button>
        <button class="primary" on:click={run} disabled={running || !o.inPath || (driver !== 'postgres' && !o.database)}>{running ? 'Importing…' : 'Run'}</button>
      </div>
    {:else}
      <p class="muted">Loading…</p>
    {/if}
  </div>
</div>

<style>
  .backdrop { position: fixed; inset: 0; background: rgba(0,0,0,.55); display: grid; place-items: center; z-index: 950; }
  .modal { background: var(--bg2); border: 1px solid var(--line); padding: 16px 20px; width: 800px; max-width: 96vw; max-height: 92vh; overflow: auto; }
  h2 { margin: 0 0 14px; font-size: 15px; }
  .grid { display: grid; grid-template-columns: 150px 1fr; gap: 8px 12px; align-items: center; }
  .grid label { margin: 0; text-align: right; color: var(--fg); font-size: 12.5px; }
  .row { display: flex; gap: 6px; }
  .section { margin: 16px 0 8px; font-weight: 600; font-size: 12.5px; border-bottom: 1px solid var(--line); padding-bottom: 4px; }
  .checks { display: grid; grid-template-columns: 1fr; gap: 4px 20px; margin: 10px 0 12px 20px; }
  .check { display: flex; align-items: center; gap: 8px; color: var(--fg); font-size: 12.5px; margin: 0; }
  .check input { width: auto; }
  .preview { background: var(--bg); border: 1px solid var(--line); padding: 8px 10px; font-size: 12px; white-space: pre-wrap; word-break: break-all; min-height: 48px; max-height: 200px; overflow: auto; margin: 0; user-select: text; }
  .actions { display: flex; gap: 8px; align-items: center; margin-top: 14px; }
  .msg { flex: 1; font-size: 12px; color: var(--fg2); overflow: hidden; text-overflow: ellipsis; }
  .msg.err { color: var(--err); }
</style>

<script>
  import { onMount, tick } from 'svelte'
  import Explorer from './lib/Explorer.svelte'
  import Editor from './lib/Editor.svelte'
  import ResultGrid from './lib/ResultGrid.svelte'
  import ConnectionForm from './lib/ConnectionForm.svelte'
  import ContextMenu from './lib/ContextMenu.svelte'
  import LogPanel from './lib/LogPanel.svelte'
  import TableView from './lib/TableView.svelte'
  import FilesPanel from './lib/FilesPanel.svelte'
  import DumpDialog from './lib/DumpDialog.svelte'
  import RestoreDialog from './lib/RestoreDialog.svelte'
  import ImportDialog from './lib/ImportDialog.svelte'
  import ModifyDialog from './lib/ModifyDialog.svelte'
  import Confirm from './lib/Confirm.svelte'
  import SearchDialog from './lib/SearchDialog.svelte'
  import UpdateDialog from './lib/UpdateDialog.svelte'
  import { tableSQL, tableSQLAll, tableRef } from './lib/filters.js'
  import { icons } from './lib/icons.js'
  import { splitter, clamp, remember, persist } from './lib/splitter.js'
  import * as api from './lib/api.js'
  import { WindowMinimise, WindowToggleMaximise, Quit, WindowIsMaximised } from './wailsjs/runtime/runtime.js'

  // one-time migration of browser storage written under the old product name
  try {
    for (const k of Object.keys(localStorage)) if (k.startsWith('dbtool.')) {
      const nk = 'durusql.' + k.slice('dbtool.'.length)
      if (localStorage.getItem(nk) === null) localStorage.setItem(nk, localStorage.getItem(k))
      localStorage.removeItem(k)
    }
  } catch {}

  let connections = []
  let state = {}              // per-connection tree state, see Explorer.svelte
  let editing = null          // connection being edited, or {} for new
  let log = []
  let menu = null
  let files = {}              // saved queries per connection, for the Files panel
  let dumpDlg = null          // { connId, database, table } while the dump dialog is open
  let restoreDlg = null       // { connId, connName, database, databases } while the restore dialog is open
  let importDlg = false
  let tableEd = null          // { connId, connName, driver, database, table } while the table editor is open
  let confirmDlg = null
  let searchDlg = false
  let updateDlg = false, updateStatus = null
  async function autoCheckUpdates() {
    try {
      const st = await api.getSettings()
      if (!st.autoCheck || !st.updateURL) return
      const last = st.lastCheck ? new Date(st.lastCheck).getTime() : 0
      if (Date.now() - last < 20 * 3600 * 1000) return
      const r = await api.checkUpdate(false)
      if (!r.error) updateStatus = r
    } catch {}
  }
  // a search hit was chosen: open (Enter) or just select in the explorer (Shift+Enter)
  async function searchPick({ hit, alt }) {
    searchDlg = false
    const id = hit.conn.id
    if (hit.kind === 'connection') { if (!state[id]?.open) await toggle(id); else bind(id); return }
    const table = hit.kind === 'column' ? `${hit.schema}.${hit.table}` : `${hit.schema}.${hit.name}`
    // reveal in the explorer: expand connection and database
    if (!state[id]?.open) patch(id, { open: true })
    if (!state[id]?.dbs?.[hit.schema]?.tables) { patchDb(id, hit.schema, { open: true, loading: true }); await loadDb(id, hit.schema) } else patchDb(id, hit.schema, { open: true })
    if (hit.kind === 'routine') { bind(id, hit.schema); patchDb(id, hit.schema, { folders: { ...(state[id]?.dbs?.[hit.schema]?.folders || {}), routines: true } }); if (!alt) showRoutine({ id, db: hit.schema, routine: { name: hit.name, type: hit.detail?.toUpperCase() || 'FUNCTION' } }); return }
    if (hit.kind === 'view') patchDb(id, hit.schema, { folders: { ...(state[id]?.dbs?.[hit.schema]?.folders || {}), views: true } })
    selectTable({ id, db: hit.schema, table })
    if (!alt) openTable({ id, table })
    if (hit.kind === 'column' && alt) { patchTable(id, hit.schema, hit.table, { open: true }); toggleTable({ id, db: hit.schema, table: hit.table }) }
  }
  // in-app confirm; `key` enables "Don't ask again" (remembered in localStorage)
  function ask(message, { title = '', ok = 'OK', cancel = 'Cancel', danger = false, key = '' } = {}) {
    if (key) { try { if (localStorage.getItem('durusql.noConfirm.' + key) === '1') return Promise.resolve(true) } catch {} }
    return new Promise(resolve => { confirmDlg = { title, message, ok, cancel, danger, dontAskKey: key, resolve: v => { confirmDlg = null; resolve(v) } } })
  }
  let stats = null            // memory / process usage for the status bar
  let version = ''
  api.version().then(v => version = v).catch(() => {})
  let logMode = 'output'      // bottom panel: 'output' | 'history'
  let history = []            // persisted history of the active connection
  let historyFor = null       // connection id the loaded history belongs to
  async function loadHistory(force = false) {
    if (!activeId) { history = []; historyFor = null; return }
    if (!force && historyFor === activeId) return
    try { history = await api.listHistory(activeId, 1000); historyFor = activeId } catch { history = [] }
  }
  $: if (logMode === 'history') { activeId; loadHistory() }
  async function clearHistory() {
    if (!activeId || !(await ask(`Clear the query history of ${connName(activeId)}?`, { title: 'Clear history', ok: 'Clear', danger: true }))) return
    await api.clearHistory(activeId); history = []
  }
  function historyMenu({ x, y, entry }) {
    menu = { x, y, items: [
      { label: 'Load into console', action: () => tab && upd(tab.id, { sql: entry.sql }) },
      { label: 'Open in new console', action: () => newTab(activeId, { sql: entry.sql, db: tab?.db || '' }) },
      { label: 'Run in new console', action: () => runIn(newTab(activeId, { sql: entry.sql, db: tab?.db || '' }), entry.sql) },
      { sep: true },
      { label: 'Copy SQL', action: () => navigator.clipboard.writeText(entry.sql) },
    ]}
  }
  let maximised = false
  async function pollStats() { try { stats = await api.stats() } catch {} }
  const fmtMB = mb => mb >= 1024 ? (mb / 1024).toFixed(2) + ' GB' : Math.round(mb) + ' MB'
  async function toggleMax() { await WindowToggleMaximise(); maximised = await WindowIsMaximised() }
  // breadcrumb for the status bar: connection › database › table
  $: crumbs = (() => {
    if (!active) return []
    const out = [active.name]
    const db = tab?.kind === 'table' ? tab.table.split('.')[0] : (tab?.db || active.database)
    if (db) out.push(db)
    if (tab?.kind === 'table') out.push('tables', tab.table.split('.').slice(1).join('.') || tab.table)
    return out
  })()
  let filesOpen = (() => { try { return localStorage.getItem('durusql.filesOpen') !== '0' } catch { return true } })()
  let filesW = remember('durusql.filesW', 260)
  $: try { localStorage.setItem('durusql.filesOpen', filesOpen ? '1' : '0') } catch {}

  // ---- tabs: each one is a console (or a table view) with its own connection, SQL and result ----
  // { id, kind: 'console'|'table', title, connId, table, sql, result, lastRun, status, error, running, applying }
  let tabs = []
  let activeTab = null
  $: tab = tabs.find(t => t.id === activeTab) || null
  $: activeId = tab?.connId || null
  $: active = connections.find(c => c.id === activeId)
  $: activeState = state[activeId] || {}
  $: connected = !!activeState.connected
  $: persistTabs(tabs, activeTab)

  const uid = () => 't' + Date.now().toString(36) + Math.random().toString(36).slice(2, 6)
  function newTab(connId = null, opts = {}) {
    const t = { id: uid(), kind: 'console', title: '', table: null, sql: '', result: null, lastRun: null, status: '', error: '', running: false, applying: false, ...opts, connId }
    tabs = [...tabs, t]; activeTab = t.id
    return t
  }
  function upd(id, p) { tabs = tabs.map(t => t.id === id ? { ...t, ...p } : t) }
  async function closeTab(id) {
    const i = tabs.findIndex(t => t.id === id)
    if (i < 0) return
    const t = tabs[i]
    const unsaved = t.kind === 'console' && (t.queryName ? dirty(t) : !!t.sql?.trim())
    if (unsaved && !(await ask(t.queryName ? `"${tabTitle(t)}" has unsaved changes. Close it anyway?` : `Close "${tabTitle(t)}"? Its SQL is not saved.`,
        { title: 'Close tab', ok: 'Close', key: 'closeTab' }))) return
    tabs = tabs.filter(x => x.id !== id)
    if (activeTab === id) activeTab = (tabs[i] || tabs[i - 1] || tabs[0])?.id || null
    if (!tabs.length) newTab(t.connId)
  }
  function closeOthers(id) { tabs = tabs.filter(t => t.id === id); activeTab = id }
  function renameTab(id) {
    const t = tabs.find(x => x.id === id)
    const name = prompt('Tab name', t.title || tabTitle(t))
    if (name !== null) upd(id, { title: name.trim() })
  }
  const connName = id => connections.find(c => c.id === id)?.name || ''
  const tabTitle = t => t.title || (t.kind === 'table' ? t.table.split('.').pop() : 'console')
  // DataGrip-style middle truncation for long table names
  const dirty = t => !!t.queryName && (t.sql || '') !== (t.savedSql || '')
  async function setTabDb(db) {
    if (!tab) return
    upd(tab.id, { db })
    if (db && tab.connId) {
      patch(tab.connId, { activeDb: db })
      if (state[tab.connId]?.connected) {
        if (!state[tab.connId]?.dbs?.[db]?.tables) { patchDb(tab.connId, db, { loading: true }); await loadDb(tab.connId, db) }
        else loadColumns(tab.connId, db)
      }
    }
  }
  async function dbListFocus() {
    // populate the list on first use: connect if needed
    if (tab?.connId && !state[tab.connId]?.connected && !state[tab.connId]?.loading) await connect(tab.connId)
  }
  const short = (s, max = 26) => s.length <= max ? s : s.slice(0, Math.ceil(max / 2) - 1) + '…' + s.slice(-(Math.floor(max / 2) - 1))

  // ---- tab strip overflow: arrows, dropdown of all tabs, keep the active tab visible ----
  let strip, overflow = false, tabList = false, tabListEl
  function checkOverflow() { if (strip) overflow = strip.scrollWidth > strip.clientWidth + 1 }
  const scrollStrip = dx => strip?.scrollBy({ left: dx, behavior: 'smooth' })
  $: if (strip && tabs) tick().then(checkOverflow)
  $: if (strip && activeTab) tick().then(() => strip.querySelector(`[data-tab="${activeTab}"]`)?.scrollIntoView({ inline: 'nearest', block: 'nearest' }))
  onMount(() => {
    const ro = new ResizeObserver(checkOverflow)
    if (strip) ro.observe(strip)
    return () => ro.disconnect()
  })
  function nextTab(dir) {
    if (tabs.length < 2) return
    const i = tabs.findIndex(t => t.id === activeTab)
    activeTab = tabs[(i + dir + tabs.length) % tabs.length].id
  }
  function tabMenu(e, t) {
    e.preventDefault()
    menu = { x: e.clientX, y: e.clientY, items: [
      { label: 'Rename…', action: () => renameTab(t.id) },
      { label: 'New console', hint: 'Ctrl+T', action: () => newTab(t.connId) },
      { sep: true },
      { label: 'Close', hint: 'Ctrl+W', action: () => closeTab(t.id) },
      { label: 'Close others', action: () => closeOthers(t.id), disabled: tabs.length < 2 },
      { label: 'Close all', action: async () => { if (tabs.some(x => x.kind === 'console' && (x.queryName ? dirty(x) : !!x.sql?.trim())) && !(await ask('Some consoles have unsaved SQL. Close all tabs anyway?', { title: 'Close all tabs', ok: 'Close all', key: 'closeAll' }))) return; tabs = []; newTab(t.connId) } },
    ]}
  }

  // tabs survive restarts (SQL and binding only, results are not kept)
  let restoring = true
  function persistTabs(ts, act) {
    if (restoring) return
    try {
      localStorage.setItem('durusql.tabs', JSON.stringify(ts.map(t => ({ id: t.id, kind: t.kind, title: t.title, connId: t.connId, table: t.table, sql: t.sql, db: t.db, queryName: t.queryName, savedSql: t.savedSql, where: t.where, orderBy: t.orderBy, filters: t.filters, page: t.page, pageSize: t.pageSize }))))
      localStorage.setItem('durusql.activeTab', act || '')
    } catch {}
  }
  function restoreTabs() {
    try {
      const saved = JSON.parse(localStorage.getItem('durusql.tabs') || '[]')
      for (const s of saved) if (s && s.id) tabs = [...tabs, { result: null, lastRun: null, status: '', error: '', running: false, applying: false, ...s }]
      const act = localStorage.getItem('durusql.activeTab')
      activeTab = tabs.find(t => t.id === act)?.id || tabs[0]?.id || null
    } catch {}
    if (!tabs.length) newTab(null)
    restoring = false; tabs = tabs
  }

  // pane sizes (persisted)
  let sidebarW = remember('durusql.sidebarW', 300)
  let resultsH = remember('durusql.resultsH', 320)
  let logH = remember('durusql.logH', 150)
  let logOpen = true

  onMount(async () => {
    window.durusqlAsk = ask   // child components use the same dialog
    await refreshConnections(); restoreTabs()
    pollStats(); const iv = setInterval(pollStats, 3000)
    setTimeout(autoCheckUpdates, 5000); const uiv = setInterval(autoCheckUpdates, 6 * 3600 * 1000)
    WindowIsMaximised().then(v => maximised = v).catch(() => {})
    return () => { clearInterval(iv); clearInterval(uiv) }
  })

  async function refreshConnections() {
    connections = await api.listConnections()
    for (const c of connections) state[c.id] ||= { folders: {} }
    state = state
    loadFiles()
  }
  async function loadFiles() {
    const out = {}
    await Promise.all(connections.map(async c => { try { out[c.id] = await api.listQueries(c.id) } catch { out[c.id] = [] } }))
    files = out
  }
  function patch(id, p) { state[id] = { ...(state[id] || { folders: {} }), ...p }; state = state }

  // ---- connections ----
  async function connect(id) {
    const s = state[id] || {}
    if (s.connected || s.loading) return
    patch(id, { loading: true, error: '' })
    try {
      await api.connect(id)
      const [dbNames, favorites, queries] = await Promise.all([api.listDatabases(id), api.getFavorites(id), api.listQueries(id)])
      patch(id, { connected: true, loading: false, dbNames, dbs: {}, favorites, queries })
      api.getTxState(id).then(tx => patch(id, { tx })).catch(() => {})
      const c = connections.find(x => x.id === id)
      if (c?.driver === 'postgres') api.listDatabaseObjects(id).then(o => patch(id, { dbObjects: o })).catch(() => {})
      // only the database list is shown; databases expand on demand
    } catch (e) {
      patch(id, { connected: false, loading: false, error: String(e) })
    }
  }
  async function disconnect(id) {
    await api.disconnect(id)
    patch(id, { connected: false, dbNames: [], dbs: {}, favorites: [], queries: [], error: '' })
  }
  function patchDb(id, db, p) {
    const dbs = { ...(state[id]?.dbs || {}) }
    dbs[db] = { ...(dbs[db] || {}), ...p }
    patch(id, { dbs })
  }
  async function toggleDb({ id, db }) {
    const ds = state[id]?.dbs?.[db] || {}
    bind(id, db)
    if (ds.open) return patchDb(id, db, { open: false })
    patchDb(id, db, { open: true, loading: !ds.tables, error: '' })
    if (ds.tables) return
    await loadDb(id, db)
  }
  async function loadDb(id, db) {
    try {
      const o = await api.listSchemaObjects(id, db)
      patchDb(id, db, { ...o, loading: false, folders: state[id]?.dbs?.[db]?.folders || { tables: true }, td: {} })
      loadColumns(id, db)
    } catch (e) { patchDb(id, db, { loading: false, error: String(e) }) }
  }
  // column names of every table in a database, for editor autocompletion (one query, cached)
  async function loadColumns(id, db) {
    const ds = state[id]?.dbs?.[db] || {}
    if (ds.columns || ds.columnsLoading || !db) return
    patchDb(id, db, { columnsLoading: true })
    try { patchDb(id, db, { columns: await api.listSchemaColumns(id, db), columnsLoading: false }) }
    catch { patchDb(id, db, { columnsLoading: false }) }
  }
  // completion schema for a connection: { db: { table: [ {label, detail} ] } }
  const schemaVersion = s => (s?.connected ? 1 : 0) + '|' + (s?.dbNames?.length || 0) + '|' + Object.values(s?.dbs || {}).map(d => (d.tables?.length || 0) + ':' + (d.columns ? Object.keys(d.columns).length : -1)).join(',')
  function completionSchema(id) {
    const s = state[id]
    if (!s?.connected) return null
    const out = {}
    for (const db of s.dbNames || []) {
      const ds = s.dbs?.[db]
      if (!ds?.tables) { out[db] = {}; continue }
      const ns = {}
      for (const t of [...(ds.tables || []), ...(ds.views || [])]) {
        ns[t] = (ds.columns?.[t] || []).map(c => ({ label: c.name, detail: c.type, type: 'property' }))
      }
      out[db] = ns
    }
    return out
  }
  $: schemaKey = activeId ? schemaVersion(state[activeId]) : ''
  let editorSchema = null
  $: { schemaKey; editorSchema = completionSchema(activeId) }
  function toggleDbFolder({ id, db, folder }) {
    const f = { ...(state[id]?.dbs?.[db]?.folders || {}) }
    f[folder] = !f[folder]
    patchDb(id, db, { folders: f })
  }
  function patchTable(id, db, table, p) {
    const td = { ...(state[id]?.dbs?.[db]?.td || {}) }
    td[table] = { ...(td[table] || {}), ...p }
    patchDb(id, db, { td })
  }
  async function toggleTable({ id, db, table }) {
    const td = state[id]?.dbs?.[db]?.td?.[table] || {}
    if (td.open) return patchTable(id, db, table, { open: false })
    patchTable(id, db, table, { open: true, loading: !td.details, error: '' })
    if (td.details) return
    try { patchTable(id, db, table, { details: await api.tableDetails(id, db + '.' + table), loading: false }) }
    catch (e) { patchTable(id, db, table, { loading: false, error: String(e) }) }
  }
  function toggleTableFolder({ id, db, table, folder }) {
    const f = { ...(state[id]?.dbs?.[db]?.td?.[table]?.folders || {}) }
    // columns/keys default open, the rest default closed
    const cur = f[folder] === undefined ? (folder === 'columns' || folder === 'keys') : f[folder]
    f[folder] = !cur
    patchTable(id, db, table, { folders: f })
  }
  async function toggle(id) {
    const s = state[id] || {}
    bind(id)
    if (!s.open) { patch(id, { open: true }); await connect(id) }
    else patch(id, { open: false })
  }
  async function refresh(id) {
    if (!id) return
    if (!state[id]?.connected) return connect(id)
    patch(id, { loading: true })
    try {
      const [dbNames, favorites, queries] = await Promise.all([api.listDatabases(id), api.getFavorites(id), api.listQueries(id)])
      const dbs = {}
      for (const d of dbNames) {
        const prev = state[id]?.dbs?.[d]
        dbs[d] = prev?.open ? { open: true, folders: prev.folders, td: {}, ...(await api.listSchemaObjects(id, d)) } : {}
      }
      patch(id, { loading: false, dbNames, dbs, favorites, queries, error: '' })
    } catch (e) { patch(id, { loading: false, error: String(e) }) }
  }
  // bind the active console to a connection (and remember the selected database)
  // single click on a table: select it (bind the console to its database), no data load
  function selectTable({ id, db, table }) {
    bind(id, db)
    patch(id, { activeTable: table })
  }
  function bind(id, db) {
    patch(id, { activeDb: db || state[id]?.activeDb || null, activeTable: null })
    const d = state[id]?.activeDb
    if (d && state[id]?.connected && state[id]?.dbs?.[d]?.tables) loadColumns(id, d)
    if (!tab) newTab(id, { db: db || '' })
    else if (tab.kind === 'console') upd(tab.id, { connId: id, error: '', ...(db ? { db } : tab.connId !== id ? { db: '' } : {}) })
    else if (tab.connId !== id) newTab(id, { db: db || '' })
  }
  function toggleFolder({ id, folder }) {
    const f = { ...(state[id]?.folders || {}) }
    // favorites/queries default open (undefined = open); the rest default closed
    const defOpen = folder === 'favorites' || folder === 'queries'
    const cur = f[folder] === undefined ? defOpen : f[folder]
    f[folder] = !cur
    patch(id, { folders: f })
  }

  // ---- queries ----
  function addLog(e) { log = [...log.slice(-499), { at: new Date(), ...e }] }

  const statusOf = r => r.columns.length
    ? `${r.rows.length}${r.truncated ? '+' : ''} rows · ${r.ms} ms`
    : `${r.rowsAffected} rows affected · ${r.ms} ms`

  // table tabs: one statement, one result (unchanged path)
  async function runIn(t, text, limit = 500) {
    if (t.kind === 'console') return runScriptIn(t, text, limit)
    const q = (text ?? t.sql).trim()
    if (!q || !t.connId) return
    if (!state[t.connId]?.connected) await connect(t.connId)
    if (!state[t.connId]?.connected) { upd(t.id, { error: state[t.connId]?.error || 'not connected' }); return }
    upd(t.id, { running: true, error: '' })
    const name = connName(t.connId)
    try {
      const result = await api.runQuery(t.connId, q, limit, '')
      upd(t.id, { result, lastRun: { sql: q, limit }, status: statusOf(result), running: false })
      addLog({ conn: name, sql: q, ms: result.ms, rows: result.columns.length ? result.rows.length : result.rowsAffected })
    } catch (e) {
      upd(t.id, { error: String(e), result: null, running: false })
      addLog({ conn: name, sql: q, err: String(e) })
    }
    if (logMode === 'history' && historyFor === t.connId) loadHistory(true)
  }

  // console tabs: run the whole editor (or the selection) as a script, one result per statement
  async function runScriptIn(t, text, limit = 500) {
    const q = (text ?? t.sql).trim()
    if (!q || !t.connId) return
    if (!state[t.connId]?.connected) await connect(t.connId)
    if (!state[t.connId]?.connected) { upd(t.id, { error: state[t.connId]?.error || 'not connected' }); return }
    upd(t.id, { running: true, error: '', runId: t.id + ':' + Date.now() })
    const runId = tabs.find(x => x.id === t.id)?.runId
    const name = connName(t.connId)
    try {
      const r = await api.runScript(t.connId, t.db || '', q, limit, runId)
      const results = r.results || []
      for (const res of results) addLog({ conn: name, sql: res.sql, ms: res.ms, rows: res.columns.length ? res.rows.length : res.rowsAffected })
      if (r.error) addLog({ conn: name, sql: r.errorSql || q, err: r.error })
      const withRows = results.map((res, i) => i).filter(i => results[i].columns.length)
      const idx = withRows.length ? withRows[withRows.length - 1] : Math.max(results.length - 1, 0)
      const last = results[idx]
      const status = r.cancelled ? 'Cancelled' : results.length > 1
        ? `${results.length} statements · ${results.reduce((a, x) => a + x.ms, 0)} ms` + (last ? ` · last: ${statusOf(last)}` : '')
        : last ? statusOf(last) : ''
      upd(t.id, { results, resultIdx: idx, result: last || null, lastRun: { sql: q, limit }, status, running: false, runId: null,
                  error: r.error && !r.cancelled ? `Statement ${r.errorIdx + 1} failed: ${r.error}` : '' })
      patch(t.connId, { tx: r.tx })
      if (results.some(res => !res.columns.length && /^\s*(create|drop|alter|rename|truncate)\b/i.test(res.sql))) {
        const d = t.db || state[t.connId]?.activeDb
        if (d && state[t.connId]?.dbs?.[d]?.open) loadDb(t.connId, d)
      }
    } catch (e) {
      upd(t.id, { error: String(e), results: [], result: null, running: false, runId: null })
      addLog({ conn: name, sql: q, err: String(e) })
    }
    if (logMode === 'history' && historyFor === t.connId) loadHistory(true)
  }
  const run = text => tab && runIn(tab, text)
  function cancelRun() { if (tab?.runId) api.cancelQuery(tab.runId) }
  function selectResult(t, i) { upd(t.id, { resultIdx: i, result: t.results[i], status: statusOf(t.results[i]) }) }

  // ---- transactions ----
  async function setTxMode(id, mode) {
    try { patch(id, { tx: await api.setTxMode(id, mode) }) } catch (e) { note(String(e), true) }
  }
  async function commitTx(id) {
    try { patch(id, { tx: await api.commit(id) }); note('Committed'); addLog({ conn: connName(id), sql: 'COMMIT', ms: 0, rows: 0 }); reloadTables(id) }
    catch (e) { note(String(e), true) }
  }
  async function rollbackTx(id) {
    try { patch(id, { tx: await api.rollback(id) }); note('Rolled back'); addLog({ conn: connName(id), sql: 'ROLLBACK', ms: 0, rows: 0 }); reloadTables(id) }
    catch (e) { note(String(e), true) }
  }
  function reloadTables(id) { for (const t of tabs) if (t.kind === 'table' && t.connId === id && t.result) tableChange(t, {}) }

  async function applyChanges(t, { table, changes, count }) {
    if (!t.connId || t.applying) return
    upd(t.id, { applying: true, error: '' })
    const name = connName(t.connId)
    try {
      const n = await api.applyChanges(t.connId, table, changes)
      upd(t.id, { status: `${n} row(s) written to ${table}` })
      addLog({ conn: name, sql: changes.map(describe).join('; '), ms: 0, rows: n })
      if (t.kind === 'console' && t.result?.sql) {
        const fresh = await api.runQuery(t.connId, t.result.sql, t.lastRun?.limit || 500, t.db || '')
        const results = (t.results || []).map((r, i) => i === t.resultIdx ? fresh : r)
        upd(t.id, { results, result: fresh, status: `${n} row(s) written to ${table}` })
      } else if (t.lastRun) upd(t.id, { result: await api.runQuery(t.connId, t.lastRun.sql, t.lastRun.limit, ''), status: `${n} row(s) written to ${table}` })
      patch(t.connId, { tx: await api.getTxState(t.connId) })
    } catch (e) {
      upd(t.id, { error: String(e) })
      addLog({ conn: name, sql: `-- submit ${count} change(s) to ${table}`, err: String(e) })
    } finally { upd(t.id, { applying: false }) }
  }
  const describe = ch => {
    const kv = o => Object.entries(o || {}).map(([k, v]) => `${k}=${v === null ? 'NULL' : JSON.stringify(v)}`).join(', ')
    if (ch.op === 'update') return `UPDATE SET ${kv(ch.values)} WHERE ${kv(ch.key)}`
    if (ch.op === 'delete') return `DELETE WHERE ${kv(ch.key)}`
    return `INSERT ${kv(ch.values)}`
  }

  // a table opens in its own data-view tab (reused when already open), like DataGrip
  function openTable({ id, table }) {
    patch(id, { activeTable: table, activeDb: table.split('.')[0] })
    const t = tabs.find(x => x.kind === 'table' && x.connId === id && x.table === table)
    if (t) { activeTab = t.id; tableChange(t, { page: t.page || 0 }) }
    else { const nt = newTab(id, { kind: 'table', table, where: '', orderBy: '', filters: {}, page: 0, pageSize: 500 }); tableChange(nt, { page: 0 }) }
  }
  const driverOf = id => connections.find(c => c.id === id)?.driver || 'mysql'
  const isDoc = id => driverOf(id) === 'opensearch'   // OpenSearch / Elasticsearch: read-only indices, no SQL DDL
  const ref = (id, table) => tableRef(table, driverOf(id))
  // filters / paging / sort changed in a table tab: store them and reload
  function tableChange(t, p) {
    upd(t.id, { ...p, loadedOnce: true })
    const nt = tabs.find(x => x.id === t.id)
    runIn(nt, tableSQL(nt, driverOf(nt.connId), true), nt.pageSize || 500)
  }
  function insertTable({ id, table }) {
    const sql = `SELECT * FROM ${ref(id, table)} LIMIT 100`
    if (tab && tab.kind === 'console') upd(tab.id, { connId: id, sql })
    else newTab(id, { sql })
  }
  function loadQuery({ id, query }) {
    const open = tabs.find(t => t.kind === 'console' && t.connId === id && t.queryName === query.name)
    if (open) { activeTab = open.id; return }
    if (tab && tab.kind === 'console' && !tab.sql?.trim() && !tab.queryName) upd(tab.id, { connId: id, sql: query.sql, title: query.name, queryName: query.name, savedSql: query.sql, db: '' })
    else newTab(id, { sql: query.sql, title: query.name, queryName: query.name, savedSql: query.sql, db: '' })
  }
  async function newQueryFile(id) {
    const name = prompt('Query name')
    if (!name) return
    await api.saveQuery(id, name, '')
    await loadFiles()
    newTab(id, { sql: '', title: name, queryName: name, savedSql: '', db: '' })
  }
  async function renameQuery(id, query) {
    const name = prompt('New name', query.name)
    if (!name || name === query.name) return
    await api.saveQuery(id, name, query.sql)
    await api.deleteQuery(id, query.name)
    tabs = tabs.map(t => t.connId === id && t.queryName === query.name ? { ...t, queryName: name, title: name } : t)
    await loadFiles()
  }

  async function toggleFavTable(id, t) { patch(id, { favorites: await api.toggleFavorite(id, t) }) }
  // Ctrl+S: save back to the file the tab came from, or ask for a name the first time
  async function saveQuery(saveAs = false) {
    if (!tab?.connId || tab.kind !== 'console') return
    let name = tab.queryName
    if (!name || saveAs) { name = prompt('Query name', tab.queryName || tab.title); if (!name) return }
    await api.saveQuery(tab.connId, name, tab.sql)
    upd(tab.id, { title: name, queryName: name, savedSql: tab.sql })
    await loadFiles()
  }
  async function deleteQuery(id, name) {
    if (!(await ask(`Delete query "${name}"?`, { title: 'Delete query', ok: 'Delete', danger: true, key: 'deleteQuery' }))) return
    await api.deleteQuery(id, name)
    tabs = tabs.map(t => t.connId === id && t.queryName === name ? { ...t, queryName: null, savedSql: null } : t)
    await loadFiles()
  }
  // copy (or move) a saved query to one or all other connections
  async function copyQuery(fromId, query, targetIds, move = false) {
    let done = 0
    for (const id of targetIds) {
      const exists = (files[id] || []).some(f => f.name === query.name)
      if (exists && !(await ask(`"${query.name}.sql" already exists in ${connName(id)}. Overwrite?`, { title: 'Overwrite file', ok: 'Overwrite' }))) continue
      await api.saveQuery(id, query.name, query.sql)
      done++
    }
    if (move && done) {
      await api.deleteQuery(fromId, query.name)
      tabs = tabs.map(t => t.connId === fromId && t.queryName === query.name ? { ...t, connId: targetIds[0] } : t)
    }
    await loadFiles()
    if (tab) upd(tab.id, { status: `${move ? 'Moved' : 'Copied'} "${query.name}.sql" to ${done} connection(s)` })
  }
  function pickTargets({ x, y, conn, query }, move) {
    const others = connections.filter(c => c.id !== conn.id)
    menu = { x, y, items: [
      { label: `${move ? 'Move' : 'Copy'} "${query.name}.sql" to…`, disabled: true },
      { sep: true },
      ...(move ? [] : [{ label: 'All other connections', action: () => copyQuery(conn.id, query, others.map(c => c.id)) }, { sep: true }]),
      ...others.map(c => ({ label: c.name, hint: c.group || '', action: () => copyQuery(conn.id, query, [c.id], move) })),
    ]}
  }
  function fileMenu({ x, y, conn, query }) {
    menu = { x, y, items: query ? [
      { label: 'Open', action: () => loadQuery({ id: conn.id, query }) },
      { label: 'Run', action: () => runIn(newTab(conn.id, { sql: query.sql, title: query.name, queryName: query.name, savedSql: query.sql }), query.sql) },
      { sep: true },
      { label: 'Copy to connection…', action: () => pickTargets({ x, y, conn, query }, false), disabled: connections.length < 2 },
      { label: 'Move to connection…', action: () => pickTargets({ x, y, conn, query }, true), disabled: connections.length < 2 },
      { label: 'Rename…', action: () => renameQuery(conn.id, query) },
      { label: 'Delete', danger: true, action: () => deleteQuery(conn.id, query.name) },
    ] : [
      { label: 'New query…', action: () => newQueryFile(conn.id) },
      { label: 'Refresh', action: loadFiles },
    ]}
  }

  // ---- editing / import ----
  async function onSaved(e) {
    editing = null
    await refreshConnections()
    if (e.detail?.id) { patch(e.detail.id, { connected: false, open: false }); bind(e.detail.id) }
  }
  async function deleteConnection(id) {
    const c = connections.find(x => x.id === id)
    if (!(await ask(`Delete "${c?.name}" and its saved queries?`, { title: 'Delete connection', ok: 'Delete', danger: true }))) return
    await api.deleteConnection(id)
    delete state[id]
    tabs = tabs.map(t => t.connId === id ? { ...t, connId: null, result: null } : t)
    await refreshConnections()
  }
  async function toggleConnFavorite(c) {
    await api.saveConnection({ ...c, favorite: !c.favorite })
    await refreshConnections()
  }
  function importDataGrip() { importDlg = true }
  async function imported(r) {
    await refreshConnections()
    for (const id of r.ids || []) patch(id, { connected: false, open: false })
    if (tab) upd(tab.id, { status: `${r.imported} connection(s) imported` })
  }

  // ---- table operations ----
  const note = (msg, err) => { if (tab) upd(tab.id, err ? { error: msg } : { status: msg, error: '' }) }
  async function exportCSV(id, schema, sql, name) {
    note(`Exporting ${name}…`)
    try {
      const r = await api.exportCSV(id, schema, sql, name + '.csv')
      if (!r.path) { note(''); return }
      note(`Exported ${r.rows} rows to ${r.path}`)
      addLog({ conn: connName(id), sql: `-- export CSV ${r.path}`, ms: 0, rows: r.rows })
    } catch (e) { note(String(e), true); addLog({ conn: connName(id), sql: `-- export CSV ${name}`, err: String(e) }) }
  }
  function openDump(conn, database, table = '') { dumpDlg = { connId: conn.id, connName: conn.name, database, table } }
  function openRestore(conn, database = '') { restoreDlg = { connId: conn.id, connName: conn.name, database, databases: state[conn.id]?.dbNames || [] } }
  async function restoreDone(r) {
    const id = restoreDlg?.connId
    note(`Imported into ${r.database} in ${(r.ms / 1000).toFixed(1)} s`)
    addLog({ conn: connName(id), sql: `-- restore → ${r.database}`, ms: r.ms, rows: 0 })
    // the backend dropped the pooled connections; reconnect and refresh the tree
    patch(id, { connected: false })
    await connect(id)
    if (r.database && state[id]?.dbs?.[r.database]?.open) loadDb(id, r.database)
  }
  function dumpDone(r) {
    note(`Dumped to ${r.path} (${(r.rows / 1048576).toFixed(2)} MB)`)
    addLog({ conn: connName(dumpDlg?.connId), sql: `-- ${r.info || 'dump'} → ${r.path}`, ms: 0, rows: 0 })
  }
  async function truncateTable(id, table) {
    if (!(await ask(`This deletes ALL rows of ${table} on ${connName(id)} and cannot be undone.`, { title: `TRUNCATE TABLE ${table}`, ok: 'Truncate', danger: true }))) return
    try {
      await api.truncateTable(id, table)
      note(`Truncated ${table}`)
      addLog({ conn: connName(id), sql: `TRUNCATE TABLE ${table}`, ms: 0, rows: 0 })
      for (const t of tabs) if (t.kind === 'table' && t.connId === id && t.table === table) tableChange(t, { page: 0 })
    } catch (e) { note(String(e), true); addLog({ conn: connName(id), sql: `TRUNCATE TABLE ${table}`, err: String(e) }) }
  }
  async function showDDL(id, table) {
    note(`Loading DDL of ${table}…`)
    try { newTab(id, { sql: await api.tableDDLAny(id, table), title: table.split('.').pop() + ' DDL', db: table.split('.')[0] }); note('') }
    catch (e) { note(String(e), true) }
  }
  async function showViewDDL(id, view) {
    try { newTab(id, { sql: await api.viewDDL(id, view), title: view.split('.').pop() + ' DDL', db: view.split('.')[0] }) }
    catch (e) { note(String(e), true) }
  }
  async function showRoutine({ id, db, routine }) {
    try { newTab(id, { sql: await api.routineSource(id, db, routine.name, routine.type), title: routine.name, db }) }
    catch (e) { note(String(e), true) }
  }
  function openTableEditor(conn, database, table = '', select = null) {
    const tables = (state[conn.id]?.dbs?.[database]?.tables || [])
    tableEd = { connId: conn.id, connName: conn.name, driver: driverOf(conn.id), database, table, select, tables }
  }
  // right-click on a column / key / index / foreign key (or its folder) in the explorer
  function detailMenu({ x, y, conn, table, kind, item, folder }) {
    const [db] = table.split('.')
    const kindOf = { column: 'column', key: 'key', fk: 'fk', index: 'index' }[kind]
    const labels = { column: 'column', key: 'key', fk: 'foreign key', index: 'index' }
    const items = []
    if (isDoc(conn.id)) {
      if (!folder && item) items.push({ label: 'Copy name', action: () => navigator.clipboard.writeText(item.name) })
      items.push({ label: 'Mapping in console', action: () => showDDL(conn.id, table) })
      menu = { x, y, items }
      return
    }
    if (kindOf) {
      if (!folder && item) items.push({ label: `Modify ${labels[kindOf]}…`, hint: 'dbl-click', action: () => openTableEditor(conn, db, table, { kind: kindOf, name: item.name }) })
      items.push({ label: `Add ${labels[kindOf]}…`, action: () => openTableEditor(conn, db, table, { kind: kindOf, name: '__new__' }) })
      if (!folder && item) items.push({ sep: true }, { label: `Drop ${labels[kindOf]} ${item.name}…`, danger: true, action: () => dropDetail(conn, table, kindOf, item) })
    }
    items.push({ sep: true }, { label: 'Modify table…', action: () => openTableEditor(conn, db, table) })
    if (!folder && item) items.push({ label: 'Copy name', action: () => navigator.clipboard.writeText(item.name) })
    menu = { x, y, items }
  }
  async function dropDetail(conn, table, kind, item) {
    const pg = driverOf(conn.id) === 'postgres'
    const q = s => pg ? `"${s}"` : '`' + s + '`'
    const [db] = table.split('.')
    const t = table.split('.').map(q).join('.')
    let sql
    if (kind === 'column') sql = `ALTER TABLE ${t} DROP COLUMN ${q(item.name)}`
    else if (kind === 'fk') sql = pg ? `ALTER TABLE ${t} DROP CONSTRAINT ${q(item.name)}` : `ALTER TABLE ${t} DROP FOREIGN KEY ${q(item.name)}`
    else if (kind === 'key') sql = item.type === 'PRIMARY KEY' && !pg ? `ALTER TABLE ${t} DROP PRIMARY KEY` : pg ? `ALTER TABLE ${t} DROP CONSTRAINT ${q(item.name)}` : `ALTER TABLE ${t} DROP INDEX ${q(item.name)}`
    else sql = pg ? `DROP INDEX ${q(db)}.${q(item.name)}` : `ALTER TABLE ${t} DROP INDEX ${q(item.name)}`
    if (!(await ask(sql, { title: `Drop ${kind === 'fk' ? 'foreign key' : kind} on ${conn.name}?`, ok: 'Drop', danger: true }))) return
    try {
      const r = await api.runScript(conn.id, db, sql, 1, '')
      if (r.error) throw new Error(r.error)
      addLog({ conn: conn.name, sql, ms: 0, rows: 0 })
      const bare = table.split('.').slice(1).join('.')
      patchTable(conn.id, db, bare, { details: null, open: false }); toggleTable({ id: conn.id, db, table: bare })
    } catch (e) { note(String(e), true); addLog({ conn: conn.name, sql, err: String(e) }) }
  }
  async function tableEdited({ table, created }) {
    const id = tableEd?.connId, db = table.split('.')[0], bare = table.split('.').slice(1).join('.')
    const prevTable = tableEd?.table
    tableEd = null
    addLog({ conn: connName(id), sql: `-- ${created ? 'created' : 'modified'} ${table}`, ms: 0, rows: 0 })
    if (state[id]?.dbs?.[db]) {
      const wasOpen = !!state[id]?.dbs?.[db]?.td?.[bare]?.open || !!state[id]?.dbs?.[db]?.td?.[prevTable?.split('.').pop()]?.open
      patchDb(id, db, { loading: true }); await loadDb(id, db)
      if (wasOpen) toggleTable({ id, db, table: bare })
    }
    for (const t of tabs) if (t.kind === 'table' && t.connId === id && (t.table === table || t.table === prevTable)) tableChange(t, { table })
  }
  async function renameTable(conn, table) {
    const [db, ...rest] = table.split('.'); const bare = rest.join('.') || table
    const name = prompt('New table name', bare)
    if (!name || name === bare) return
    const pg = driverOf(conn.id) === 'postgres'
    const q = s => pg ? `"${s}"` : '`' + s + '`'
    const sql = pg ? `alter table ${q(bare)} rename to ${q(name)}` : `rename table ${q(bare)} to ${q(name)}`
    try {
      const r = await api.runScript(conn.id, db, sql, 1, '')
      if (r.error) throw new Error(r.error)
      addLog({ conn: conn.name, sql, ms: 0, rows: 0 })
      tabs = tabs.map(t => t.kind === 'table' && t.connId === conn.id && t.table === table ? { ...t, table: db + '.' + name, title: '' } : t)
      patchDb(conn.id, db, { loading: true }); await loadDb(conn.id, db)
    } catch (e) { note(String(e), true); addLog({ conn: conn.name, sql, err: String(e) }) }
  }
  async function dropTable(conn, table) {
    const bare = table.split('.').pop()
    const typed = prompt(`DROP TABLE ${table} on ${conn.name}\n\nThis deletes the table and all its data and cannot be undone.\nType the table name to confirm:`)
    if (typed !== bare) { if (typed !== null) note('Name did not match, nothing dropped'); return }
    const [db] = table.split('.')
    const sql = `DROP TABLE ${table}`
    try {
      const r = await api.runScript(conn.id, db, sql, 1, '')
      if (r.error) throw new Error(r.error)
      addLog({ conn: conn.name, sql, ms: 0, rows: 0 })
      tabs = tabs.filter(t => !(t.kind === 'table' && t.connId === conn.id && t.table === table))
      if (!tabs.length) newTab(conn.id)
      if (!tabs.find(t => t.id === activeTab)) activeTab = tabs[0].id
      patchDb(conn.id, db, { loading: true }); await loadDb(conn.id, db)
    } catch (e) { note(String(e), true); addLog({ conn: conn.name, sql, err: String(e) }) }
  }
  function exportMenu({ x, y }, t) {
    const sql = t.kind === 'table' ? tableSQLAll(t, driverOf(t.connId)) : t.result?.sql || t.lastRun?.sql
    if (!sql) return
    const schema = t.kind === 'console' ? (t.db || '') : ''
    const name = t.kind === 'table' ? t.table.split('.').pop() : (t.result?.table?.split('.').pop() || 'result')
    const doExport = async fmt => {
      note(`Exporting ${name} as ${fmt.toUpperCase()}…`)
      try {
        const r = await api.exportQuery(t.connId, schema, sql, fmt, name)
        if (!r.path) { note(''); return }
        note(`Exported ${r.rows} rows to ${r.path}`); addLog({ conn: connName(t.connId), sql: `-- export ${fmt} ${r.path}`, ms: 0, rows: r.rows })
      } catch (e) { note(String(e), true) }
    }
    menu = { x, y, items: [
      { label: 'Export as CSV…', action: () => doExport('csv') },
      { label: 'Export as JSON…', action: () => doExport('json') },
      { label: 'Export as Excel (.xlsx)…', action: () => doExport('xlsx') },
    ]}
  }
  function viewMenu({ x, y, conn, view }) {
    menu = { x, y, items: [
      { label: 'Open view', action: () => openTable({ id: conn.id, table: view }) },
      { label: 'SELECT in console', action: () => insertTable({ id: conn.id, table: view }) },
      { label: isDoc(conn.id) ? 'Alias definition in console' : 'DDL in console', action: () => showViewDDL(conn.id, view) },
      { sep: true },
      { label: 'Copy name', action: () => navigator.clipboard.writeText(view.split('.').pop()) },
      { label: 'Export to CSV…', action: () => exportCSV(conn.id, '', `SELECT * FROM ${ref(conn.id, view)}`, view.split('.').pop()) },
    ]}
  }
  function routineMenu({ x, y, conn, db, routine }) {
    menu = { x, y, items: [
      { label: 'Source in console', action: () => showRoutine({ id: conn.id, db, routine }) },
      { label: routine.type === 'PROCEDURE' ? 'CALL in console' : 'SELECT in console', action: () => newTab(conn.id, { sql: routine.type === 'PROCEDURE' ? `CALL ${routine.name}();` : `SELECT ${routine.name}();`, db }) },
      { sep: true },
      { label: 'Copy name', action: () => navigator.clipboard.writeText(routine.name) },
    ]}
  }

  // ---- context menus ----
  function connMenu({ x, y, conn }) {
    const s = state[conn.id] || {}
    bind(conn.id)
    menu = { x, y, items: [
      s.connected ? { label: 'Disconnect', action: () => disconnect(conn.id) }
                  : { label: 'Connect', action: () => toggle(conn.id) },
      { label: 'Refresh', action: () => refresh(conn.id), disabled: !s.connected },
      { label: 'Collapse all databases', action: () => { const dbs = {}; for (const [d, v] of Object.entries(s.dbs || {})) dbs[d] = { ...v, open: false }; patch(conn.id, { dbs }) }, disabled: !s.connected },
      { label: 'New query console', hint: 'Ctrl+T', action: () => newTab(conn.id) },
      { sep: true },
      { label: conn.favorite ? 'Remove from favorites' : 'Add to favorites', action: () => toggleConnFavorite(conn) },
      ...(isDoc(conn.id) ? [] : [{ label: `Restore with ${driverOf(conn.id) === 'postgres' ? 'psql' : 'mysql'}…`, hint: 'import dump', action: () => openRestore(conn, conn.database || '') }]),
      { label: 'Edit…', hint: 'properties', action: () => editing = conn },
      { label: 'Duplicate', action: () => editing = { ...conn, id: '', name: conn.name + ' copy' } },
      { sep: true },
      { label: 'Delete', danger: true, hint: 'Del', action: () => deleteConnection(conn.id) },
    ]}
  }
  function dbMenu({ x, y, conn, db }) {
    const ds = state[conn.id]?.dbs?.[db] || {}
    bind(conn.id, db)
    menu = { x, y, items: [
      { label: ds.open ? 'Collapse' : 'Expand', action: () => toggleDb({ id: conn.id, db }) },
      { label: 'Refresh', action: () => { patchDb(conn.id, db, { loading: true }); loadDb(conn.id, db) }, disabled: !ds.open },
      { sep: true },
      { label: 'New query console', hint: 'Ctrl+T', action: () => newTab(conn.id, { db }) },
      ...(isDoc(conn.id) ? [] : [{ label: 'New table…', action: () => openTableEditor(conn, db) }]),
      { label: 'Copy name', action: () => navigator.clipboard.writeText(db) },
      ...(isDoc(conn.id) ? [] : [
        { sep: true },
        { label: `Export with ${driverOf(conn.id) === 'postgres' ? 'pg_dump' : 'mysqldump'}…`, action: () => openDump(conn, db) },
        { label: `Restore with ${driverOf(conn.id) === 'postgres' ? 'psql' : 'mysql'}…`, action: () => openRestore(conn, db) },
      ]),
    ]}
  }
  function tableMenu({ x, y, conn, table }) {
    const fav = state[conn.id]?.favorites?.includes(table)
    const [db, ...rest] = table.split('.')
    const bare = rest.join('.') || table
    const pg = driverOf(conn.id) === 'postgres'
    if (isDoc(conn.id)) {
      const count = `SELECT COUNT(*) FROM ${ref(conn.id, table)}`
      menu = { x, y, items: [
        { label: 'Open index', hint: 'dbl-click', action: () => openTable({ id: conn.id, table }) },
        { label: 'SELECT in console', action: () => insertTable({ id: conn.id, table }) },
        { label: 'Search in console', action: () => newTab(conn.id, { sql: `GET /${bare}/_search\n{\n  "size": 100,\n  "query": { "match_all": {} }\n}`, db }) },
        { label: 'Count documents', action: () => runIn(newTab(conn.id, { sql: count, db }), count) },
        { label: 'Mapping in console', hint: 'settings + mappings', action: () => showDDL(conn.id, table) },
        { sep: true },
        { label: fav ? 'Remove from favorites' : 'Add to favorites', action: () => toggleFavTable(conn.id, table) },
        { label: 'Copy name', action: () => navigator.clipboard.writeText(bare) },
        { sep: true },
        { label: 'Export to CSV…', action: () => exportCSV(conn.id, '', `SELECT * FROM ${ref(conn.id, table)} LIMIT 10000`, bare) },
        { sep: true },
        { label: 'Delete all documents…', danger: true, action: () => truncateTable(conn.id, table) },
      ]}
      return
    }
    menu = { x, y, items: [
      { label: 'Open table', hint: 'dbl-click', action: () => openTable({ id: conn.id, table }) },
      { label: 'SELECT in console', action: () => insertTable({ id: conn.id, table }) },
      { label: 'Count rows', action: () => runIn(newTab(conn.id, { sql: `SELECT COUNT(*) FROM ${table}`, db }), `SELECT COUNT(*) FROM ${table}`) },
      { label: 'DDL in console', action: () => showDDL(conn.id, table) },
      { sep: true },
      { label: 'Modify table…', action: () => openTableEditor(conn, db, table) },
      { label: 'Add column…', action: () => openTableEditor(conn, db, table, { kind: 'column', name: '__new__' }) },
      { label: 'Add index…', action: () => openTableEditor(conn, db, table, { kind: 'index', name: '__new__' }) },
      { label: 'Rename table…', action: () => renameTable(conn, table) },
      { sep: true },
      { label: fav ? 'Remove from favorites' : 'Add to favorites', action: () => toggleFavTable(conn.id, table) },
      { label: 'Copy name', action: () => navigator.clipboard.writeText(bare) },
      { label: 'Copy qualified name', action: () => navigator.clipboard.writeText(table) },
      { sep: true },
      { label: 'Export to CSV…', action: () => exportCSV(conn.id, '', `SELECT * FROM ${table}`, bare) },
      { label: `Export with ${pg ? 'pg_dump' : 'mysqldump'}…`, action: () => openDump(conn, db, rest.length ? bare : '') },
      { sep: true },
      { label: 'Truncate table…', danger: true, action: () => truncateTable(conn.id, table) },
      { label: 'Drop table…', danger: true, action: () => dropTable(conn, table) },
    ]}
  }
  function globalKeys(e) {
    if (e.key === 'Escape' && tabList) { tabList = false; return }
    if (!e.ctrlKey || e.altKey) return
    if (e.shiftKey && (e.key === 'f' || e.key === 'F')) { e.preventDefault(); searchDlg = true }
    else if (e.key === 'k' || e.key === 'K' || e.key === 'p' || e.key === 'P') { e.preventDefault(); searchDlg = true }
    else if (e.key === 's' || e.key === 'S') { e.preventDefault(); saveQuery(e.shiftKey) }
    else if (e.key === 't' || e.key === 'T') { e.preventDefault(); newTab(activeId, { db: state[activeId]?.activeDb || '' }) }
    else if (e.key === 'w' || e.key === 'W') { e.preventDefault(); if (tab) closeTab(tab.id) }
    else if (e.key === 'PageDown' || (e.key === 'Tab' && !e.shiftKey)) { e.preventDefault(); nextTab(1) }
    else if (e.key === 'PageUp' || (e.key === 'Tab' && e.shiftKey)) { e.preventDefault(); nextTab(-1) }
  }
</script>

<svelte:window on:keydown={globalKeys} on:mousedown={e => { if (tabList && tabListEl && !tabListEl.contains(e.target) && !e.target.closest('.tabctl')) tabList = false }} />

<div class="layout" style="grid-template-columns: {sidebarW}px 0 1fr 0 {filesOpen ? filesW : 0}px; grid-template-rows: 34px 1fr 0 {logOpen ? logH : 27}px 24px">
  <div class="titlebar" style="--wails-draggable: drag" on:dblclick={toggleMax} role="banner">
    <span class="logo">{@html icons.db}</span>
    <span class="appname" title="DuruSQL {version}">DuruSQL</span>
    {#if active}<span class="tb-sep">›</span><span class="tb-conn"><span class="dot" style="background:{active.color || 'var(--acc)'}"></span>{active.name}</span>{/if}
    <span class="spacer"></span>
    <span class="winbtns" style="--wails-draggable: no-drag">
      <button class="wb" title="Minimise" on:click={() => WindowMinimise()}>&#x2013;</button>
      <button class="wb" title={maximised ? 'Restore' : 'Maximise'} on:click={toggleMax}>{maximised ? '❐' : '☐'}</button>
      <button class="wb close" title="Close" on:click={() => Quit()}>✕</button>
    </span>
  </div>
  <Explorer {connections} {state} {activeId}
    on:select={e => bind(e.detail.id, e.detail.db)}
    on:toggle={e => toggle(e.detail)}
    on:toggleDb={e => toggleDb(e.detail)}
    on:dbFolder={e => toggleDbFolder(e.detail)}
    on:toggleTable={e => toggleTable(e.detail)}
    on:tableFolder={e => toggleTableFolder(e.detail)}
    on:folder={e => toggleFolder(e.detail)}
    on:refresh={e => refresh(e.detail)}
    on:new={() => editing = {}}
    on:import={importDataGrip}
    on:search={() => searchDlg = true}
    on:delete={e => deleteConnection(e.detail)}
    on:table={e => openTable(e.detail)}
    on:selectTable={e => selectTable(e.detail)}
    on:insertTable={e => insertTable(e.detail)}
    on:menuConn={e => connMenu(e.detail)}
    on:menuDb={e => dbMenu(e.detail)}
    on:menuTable={e => tableMenu(e.detail)}
    on:menuView={e => viewMenu(e.detail)}
    on:menuRoutine={e => routineMenu(e.detail)}
    on:routine={e => showRoutine(e.detail)}
    on:menuDetail={e => detailMenu(e.detail)}
    on:modify={e => { if (isDoc(e.detail.id)) return; const conn = connections.find(c => c.id === e.detail.id); openTableEditor(conn, e.detail.table.split('.')[0], e.detail.table, { kind: { column: 'column', key: 'key', fk: 'fk', index: 'index' }[e.detail.kind], name: e.detail.item.name }) }}
  />

  <div class="split v" use:splitter={{ axis: 'x', get: () => sidebarW, set: v => sidebarW = clamp(v, 180, 700), done: () => persist('durusql.sidebarW', sidebarW) }}></div>

  <main>
    <div class="tabbar">
      <div class="strip" role="tablist" bind:this={strip} on:wheel={e => { if (strip.scrollWidth > strip.clientWidth) { e.preventDefault(); strip.scrollLeft += e.deltaY || e.deltaX } }}>
        {#each tabs as t (t.id)}
          <div class="tab" data-tab={t.id} class:on={t.id === activeTab} class:table={t.kind === 'table'} role="tab" aria-selected={t.id === activeTab}
               on:mousedown={e => { if (e.button === 1) { e.preventDefault(); closeTab(t.id) } else if (e.button === 0) activeTab = t.id }}
               on:dblclick={() => renameTab(t.id)} on:contextmenu={e => tabMenu(e, t)} title={(t.kind === 'table' ? t.table : (t.sql || '').split('\n')[0]) + (t.connId ? ` [${connName(t.connId)}]` : '')}>
            <span class="ic">{@html t.kind === 'table' ? icons.table : icons.query}</span>
            <span class="title">{short(tabTitle(t))}{#if dirty(t)}<span class="dirty" title="Unsaved changes (Ctrl+S)">●</span>{/if}</span>
            {#if t.connId}<span class="conn">[{connName(t.connId)}]</span>{/if}
            {#if t.running || t.applying}<span class="ic spin">{@html icons.spinner}</span>{/if}
            <button class="close" title="Close (Ctrl+W)" on:mousedown|stopPropagation on:click={() => closeTab(t.id)}>{@html icons.x}</button>
          </div>
        {/each}
        <button class="icon add" title="New console (Ctrl+T)" on:click={() => newTab(activeId)}>{@html icons.plus}</button>
      </div>
      <div class="tabctl">
        {#if overflow}
          <button class="icon" title="Scroll tabs left" on:click={() => scrollStrip(-240)}>◀</button>
          <button class="icon" title="Scroll tabs right" on:click={() => scrollStrip(240)}>▶</button>
        {/if}
        <button class="icon" class:on={tabList} title="Show all tabs" on:click={() => tabList = !tabList}>⌄<span class="cnt">{tabs.length}</span></button>
        {#if !filesOpen}<button class="icon" title="Show Files panel" on:click={() => filesOpen = true}>{@html icons.query}</button>{/if}
      </div>
    </div>
    {#if tabList}
      <div class="tablist" bind:this={tabListEl} role="menu">
        {#each tabs as t (t.id)}
          <div class="tlrow" class:on={t.id === activeTab} role="menuitem" on:click={() => { activeTab = t.id; tabList = false }}>
            <span class="ic" class:tbl={t.kind === 'table'}>{@html t.kind === 'table' ? icons.table : icons.query}</span>
            <span class="tltitle">{t.kind === 'table' ? t.table : tabTitle(t)}</span>
            {#if t.connId}<span class="conn">[{connName(t.connId)}]</span>{/if}
            <button class="close" title="Close" on:click|stopPropagation={() => closeTab(t.id)}>{@html icons.x}</button>
          </div>
        {/each}
        <div class="tlfoot">
          <button class="ghost" on:click={() => { tabList = false; newTab(activeId) }}>New console</button>
          <span class="spacer"></span>
          <button class="ghost" on:click={() => { if (tab) closeOthers(tab.id); tabList = false }} disabled={tabs.length < 2}>Close others</button>
          <button class="ghost" title="Show all confirmation dialogs again" on:click={() => { try { for (const k of Object.keys(localStorage)) if (k.startsWith('durusql.noConfirm.')) localStorage.removeItem(k) } catch {}; tabList = false; note('Confirmation dialogs reset') }}>Reset "don't ask again"</button>
        </div>
      </div>
    {/if}

    {#if tab?.kind !== 'table'}
    <header>
      {#if tab?.running && tab?.runId}
        <button class="icon stop" on:click={cancelRun} title="Cancel the running statement">{@html icons.stop}</button>
      {:else}
        <button class="icon run" disabled={!activeId || tab?.running} on:click={() => run()} title="Run (Ctrl+Enter) — runs the selection when there is one; several statements run one after another">{@html icons.play}</button>
      {/if}
      <button class="icon" disabled={!activeId} on:click={() => { logMode = 'history'; logOpen = true; loadHistory(true) }} title="Query history of this connection">{@html icons.event}</button>
      <button class="icon" class:dirty-btn={tab && dirty(tab)} disabled={!activeId || !tab?.sql?.trim()} on:click={() => saveQuery(false)} title={tab?.queryName ? `Save to ${tab.queryName}.sql (Ctrl+S) · Ctrl+Shift+S saves as…` : 'Save as… (Ctrl+S)'}>{@html icons.save}</button>
      <span class="hsep"></span>
      <select class="txsel" title="Transaction mode: Auto commits every statement; Manual keeps a transaction open until you commit or roll back"
              value={activeState.tx?.mode || 'auto'} on:change={e => setTxMode(activeId, e.target.value)} disabled={!activeId}>
        <option value="auto">Tx: Auto</option><option value="manual">Tx: Manual</option>
      </select>
      {#if activeState.tx?.mode === 'manual'}
        <button class="icon ok" disabled={!activeState.tx?.open} on:click={() => commitTx(activeId)} title="Commit ({activeState.tx?.count || 0} statement(s) pending)">{@html icons.ok}{#if activeState.tx?.open}<span class="txcnt">{activeState.tx.count}</span>{/if}</button>
        <button class="icon bad" disabled={!activeState.tx?.open} on:click={() => rollbackTx(activeId)} title="Rollback">{@html icons.x}</button>
      {/if}
      {#if !active}<span class="muted">Pick a connection in the explorer to bind this console</span>{/if}
      <span class="spacer"></span>
      {#if active}
        <span class="ic dbic" title="Database the console runs against">{@html icons.db}</span>
        <select class="dbsel" title="Database the console runs against (unqualified table names resolve here)"
                value={tab.db || ''} on:mousedown={dbListFocus} on:change={e => setTabDb(e.target.value)}>
          <option value="">{active.database ? `‹ ${active.database} ›` : '‹ no database ›'}</option>
          {#each (activeState.dbNames || (tab.db ? [tab.db] : [])) as d}<option value={d}>{d}</option>{/each}
          {#if !connected && !activeState.dbNames?.length}<option disabled>connect to list databases…</option>{/if}
        </select>
      {/if}
    </header>
    {/if}

    {#each tabs as t (t.id)}
      <div class="pane" class:hidden={t.id !== activeTab}>
        {#if t.kind === 'table'}
          <TableView tab={t} active={t.id === activeTab} driver={driverOf(t.connId)}
            on:change={e => tableChange(t, e.detail)}
            on:submit={e => applyChanges(t, e.detail)}
            on:console={e => newTab(t.connId, { sql: e.detail })}
            on:exportMenu={e => exportMenu(e.detail, t)} />
        {:else}
        <div class="editorwrap"><Editor bind:value={t.sql} active={t.id === activeTab}
          schema={t.id === activeTab ? editorSchema : null}
          defaultSchema={t.id === activeTab ? (t.db || connections.find(c => c.id === t.connId)?.database || '') : ''}
          dialect={driverOf(t.connId)}
          on:run={e => runIn(t, e.detail)} /></div>
        <div class="split h" use:splitter={{ axis: 'y', invert: true, get: () => resultsH, set: v => resultsH = clamp(v, 80, innerHeight - 260), done: () => persist('durusql.resultsH', resultsH) }}></div>
        <div class="results" style="height:{resultsH}px">
          {#if (t.results || []).length > 1}
            <div class="rtabs">
              {#each t.results as r, i}
                <button class="rtab" class:on={i === t.resultIdx} on:click={() => selectResult(t, i)} title={r.sql}>
                  {i + 1}. {r.columns.length ? `${r.rows.length}${r.truncated ? '+' : ''} rows` : `${r.rowsAffected} affected`} <span class="rsql mono">{(r.sql || '').replace(/\s+/g, ' ').slice(0, 40)}</span>
                </button>
              {/each}
            </div>
          {/if}
          <div class="statusbar">
            {#if t.error}<span class="err mono">{t.error}</span>{:else}<span class="muted">{t.running ? 'Running…' : t.status}</span>{/if}
          </div>
          <ResultGrid result={t.result} busy={t.applying || t.running} on:submit={e => applyChanges(t, e.detail)} on:exportMenu={e => exportMenu(e.detail, t)} />
        </div>
        {/if}
      </div>
    {/each}
  </main>

  {#if filesOpen}
    <div class="split v right" use:splitter={{ axis: 'x', invert: true, get: () => filesW, set: v => filesW = clamp(v, 160, 600), done: () => persist('durusql.filesW', filesW) }}></div>
    <div class="filespane">
      <FilesPanel {connections} {files} {activeId} openName={tab?.queryName || ''}
        on:open={e => loadQuery(e.detail)} on:new={e => newQueryFile(e.detail.id)} on:refresh={loadFiles}
        on:hide={() => filesOpen = false} on:menu={e => fileMenu(e.detail)} />
    </div>
  {:else}
    <div class="split v right" style="width:0"></div><div class="filespane"></div>
  {/if}

  <div class="split h full" use:splitter={{ axis: 'y', invert: true, get: () => logH, set: v => { logOpen = true; logH = clamp(v, 60, innerHeight - 200) }, done: () => persist('durusql.logH', logH) }}></div>

  <div class="bottom">
    <LogPanel {log} open={logOpen} mode={logMode} {history} historyConn={activeId ? connName(activeId) : ''}
      on:toggle={() => logOpen = !logOpen} on:mode={e => { logMode = e.detail; logOpen = true }} on:clear={() => log = []}
      on:load={e => tab && upd(tab.id, { sql: e.detail })} on:refreshHistory={() => loadHistory(true)} on:clearHistory={clearHistory} on:menu={e => historyMenu(e.detail)} />
  </div>

  <div class="statusbar-app">
    {#if crumbs.length}
      <span class="crumb-sep">Database</span>
      {#each crumbs as c}<span class="crumb-sep">›</span><span class="crumb">{c}</span>{/each}
      <span class="crumb-sep">·</span><span class:ok={connected}>{activeState.loading ? 'connecting…' : connected ? 'connected' : 'disconnected'}</span>
    {:else}
      <span>No connection selected</span>
    {/if}
    <span class="st-right">
      {#if active}
        <span class="mono" title="Driver · host (via SSH host) · port {active.port}">{active.driver} · {active.host}{active.ssh?.host ? ' via ' + active.ssh.host : ''}</span>
      {/if}
      <button class="stbtn" class:avail={updateStatus?.available} on:click={() => updateDlg = true} title={updateStatus?.available ? `Update ${updateStatus.latest} available — click for details` : 'Version · updates and settings'}>{updateStatus?.available ? `⬆ ${updateStatus.latest} available` : version || 'dev'}</button>
      <span title="{tabs.length} tabs open">{tabs.length} tabs</span>
      {#if stats}
        <span title="Resident memory of DuruSQL and its WebKit processes ({stats.processes} processes) · Go heap {fmtMB(stats.heapMB)} · {stats.goroutines} goroutines">{fmtMB(stats.rssMB)}</span>
      {/if}
    </span>
  </div>
</div>

{#if editing}
  <ConnectionForm conn={editing} on:saved={onSaved} on:close={() => editing = null} />
{/if}
{#if dumpDlg}
  <DumpDialog {...dumpDlg} on:done={e => dumpDone(e.detail)} on:close={() => dumpDlg = null} />
{/if}
{#if restoreDlg}
  <RestoreDialog {...restoreDlg} on:done={e => restoreDone(e.detail)} on:close={() => restoreDlg = null} />
{/if}
{#if importDlg}
  <ImportDialog on:imported={e => imported(e.detail)} on:close={() => importDlg = false} />
{/if}
{#if confirmDlg}
  <Confirm dlg={confirmDlg} />
{/if}
{#if updateDlg}
  <UpdateDialog {version} status={updateStatus} on:status={e => updateStatus = e.detail} on:close={() => updateDlg = false} />
{/if}
{#if searchDlg}
  <SearchDialog {connections} {state} on:pick={e => searchPick(e.detail)} on:close={() => searchDlg = false} />
{/if}
{#if tableEd}
  <ModifyDialog {...tableEd} on:done={e => tableEdited(e.detail)} on:console={e => { newTab(tableEd.connId, { sql: e.detail, db: tableEd.database }); tableEd = null }} on:close={() => tableEd = null} />
{/if}
{#if menu}
  <ContextMenu {menu} close={() => menu = null} />
{/if}

<style>
  .layout { display: grid; height: 100%; grid-template-areas: "top top top top top" "side vs main vf files" "hs hs hs hs hs" "log log log log log" "status status status status status"; }
  .titlebar { grid-area: top; display: flex; align-items: center; gap: 8px; padding: 0 0 0 10px; background: var(--bg2); border-bottom: 1px solid var(--line); user-select: none; -webkit-user-select: none; }
  .logo { display: inline-flex; color: var(--acc2); }
  .appname { font-weight: 600; }
  .tb-sep { color: var(--fg2); }
  .tb-conn { display: inline-flex; align-items: center; gap: 6px; color: var(--fg2); }
  .winbtns { display: flex; align-self: stretch; }
  .wb { background: transparent; border: 0; border-radius: 0; width: 44px; color: var(--fg2); font-size: 13px; }
  .wb:hover { background: var(--hover); color: var(--fg); }
  .wb.close:hover { background: #c42b1c; color: #fff; }
  .statusbar-app { grid-area: status; display: flex; align-items: center; gap: 6px; padding: 0 10px; background: var(--bg2); border-top: 1px solid var(--line); font-size: 12px; color: var(--fg2); white-space: nowrap; overflow: hidden; }
  .crumb { color: var(--fg); }
  .crumb-sep { opacity: .6; }
  .st-right { margin-left: auto; display: flex; gap: 14px; }
  .st-right span[title] { cursor: help; }
  .stbtn { background: none; border: 0; padding: 0 4px; color: var(--fg2); font-size: 12px; }
  .stbtn:hover { color: var(--fg); }
  .stbtn.avail { color: var(--ok); font-weight: 600; }
  .split.v.right { grid-area: vf; }
  .filespane { grid-area: files; min-width: 0; min-height: 0; overflow: hidden; }
  .layout > :global(aside) { grid-area: side; }
  .split.v { grid-area: vs; }
  .split.h.full { grid-area: hs; }
  main { grid-area: main; display: flex; flex-direction: column; min-width: 0; min-height: 0; border-left: 1px solid var(--line); }
  .bottom { grid-area: log; border-top: 1px solid var(--line); min-height: 0; }

  .tabbar { display: flex; align-items: stretch; background: var(--bg2); border-bottom: 1px solid var(--line); flex: none; min-height: 32px; position: relative; }
  .strip { display: flex; align-items: stretch; flex: 1; min-width: 0; overflow-x: auto; overflow-y: hidden; scrollbar-width: none; }
  .strip::-webkit-scrollbar { display: none; }
  .tabctl { display: flex; align-items: center; gap: 2px; padding: 0 6px; border-left: 1px solid var(--line); flex: none; }
  .tabctl .icon { font-size: 10px; padding: 3px 5px; }
  .tabctl .icon.on { background: var(--hover); color: var(--fg); }
  .cnt { font-size: 10px; margin-left: 3px; color: var(--fg2); }
  .tablist { position: absolute; right: 8px; top: 32px; z-index: 900; min-width: 320px; max-width: 520px; max-height: 60vh; overflow: auto; background: var(--bg3); border: 1px solid var(--line); border-radius: 0; box-shadow: 0 8px 24px rgba(0,0,0,.45); padding: 4px; }
  .tlrow { display: flex; align-items: center; gap: 8px; padding: 4px 8px; border-radius: 0; cursor: default; white-space: nowrap; }
  .tlrow:hover { background: var(--hover); }
  .tlrow.on { background: var(--sel); }
  .tlrow .ic.tbl { color: #7aa2f7; }
  .tltitle { flex: 1; overflow: hidden; text-overflow: ellipsis; }
  .tlrow .close { opacity: .6; }
  .tlrow .close:hover { opacity: 1; }
  .tlfoot { display: flex; gap: 6px; padding: 6px 4px 2px; border-top: 1px solid var(--line); margin-top: 4px; }
  .tab { display: flex; align-items: center; gap: 6px; padding: 0 6px 0 10px; border-right: 1px solid var(--line); color: var(--fg2); cursor: default; user-select: none; white-space: nowrap; position: relative; flex: none; }
  .tab:hover { background: var(--hover); color: var(--fg); }
  .tab.on { color: var(--fg); background: var(--bg); box-shadow: inset 0 -2px var(--acc); }
  .tab .ic { display: inline-flex; color: var(--fg2); }
  .tab.table .ic { color: #7aa2f7; }
  .tab .conn { font-size: 11px; color: var(--fg2); }
  .dirty { color: var(--acc2); margin-left: 4px; font-size: 10px; }
  .tab .close { display: inline-flex; background: none; border: 0; padding: 2px; color: var(--fg2); border-radius: 0; opacity: 0; }
  .tab:hover .close, .tab.on .close { opacity: 1; }
  .tab .close:hover { background: var(--bg3); color: var(--fg); }
  .add { align-self: center; margin: 0 4px; }
  .spin { animation: spin 1s linear infinite; }
  @keyframes spin { to { transform: rotate(360deg); } }

  header { display: flex; align-items: center; gap: 8px; padding: 4px 12px; height: 34px; background: var(--bg2); border-bottom: 1px solid var(--line); flex: none; white-space: nowrap; overflow: hidden; }
  header > .muted { overflow: hidden; text-overflow: ellipsis; flex: 0 1 auto; min-width: 0; }
  header .icon { font-size: 0; padding: 4px; color: var(--fg); }
  header .icon.run { color: var(--ok); }
  header .icon.stop { color: var(--err); }
  header .icon.ok { color: var(--ok); font-size: 12px; display: inline-flex; align-items: center; gap: 3px; }
  header .icon.bad { color: var(--err); }
  .txcnt { font-size: 11px; }
  .hsep { width: 1px; height: 18px; background: var(--line); margin: 0 4px; }
  header .dbic { display: inline-flex; color: #7aa2f7; }
  .txsel { width: auto; min-width: 96px; padding: 2px 22px 2px 6px; font-size: 12px; flex: none; }
  header .icon.run:hover:not(:disabled) { background: rgba(95,184,101,.15); }
  header .icon.dirty-btn { color: var(--acc2); }
  .spacer { flex: 1; }
  .dot { width: 10px; height: 10px; border-radius: 50%; display: inline-block; }
  .state { font-size: 11px; color: var(--fg2); padding: 1px 6px; border: 1px solid var(--line); border-radius: 10px; }
  .state.on { color: var(--ok); border-color: var(--ok); }
  .ok { color: var(--ok); }
  .kbd { font-size: 11px; opacity: .8; margin-left: 4px; }
  .dbsel { width: auto; min-width: 110px; max-width: 220px; padding: 2px 24px 2px 8px; font-size: 12px; flex: 0 1 auto; overflow: hidden; text-overflow: ellipsis; }
  header .spacer { flex: 1 0 8px; }
  header strong, header .state, header .dot { flex: none; }

  .pane { flex: 1; display: flex; flex-direction: column; min-height: 0; }
  .pane.hidden { display: none; }
  .editorwrap { flex: 1; min-height: 80px; overflow: hidden; display: flex; flex-direction: column; }
  .editorwrap > :global(.editor) { flex: 1; }
  .results { display: flex; flex-direction: column; min-height: 0; flex: none; }
  .results > :global(.wrap) { flex: 1; min-height: 0; }
  .rtabs { display: flex; overflow-x: auto; background: var(--bg2); border-top: 1px solid var(--line); flex: none; scrollbar-width: none; }
  .rtabs::-webkit-scrollbar { display: none; }
  .rtab { background: none; border: 0; border-right: 1px solid var(--line); padding: 3px 10px; font-size: 12px; color: var(--fg2); white-space: nowrap; }
  .rtab.on { color: var(--fg); box-shadow: inset 0 -2px var(--acc); }
  .rsql { color: var(--fg2); font-size: 11px; margin-left: 6px; }
  .statusbar { padding: 3px 12px; background: var(--bg2); border-top: 1px solid var(--line); border-bottom: 1px solid var(--line); min-height: 24px; white-space: pre-wrap; font-size: 12px; flex: none; }
</style>

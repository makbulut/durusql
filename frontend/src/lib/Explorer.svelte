<script>
  import { createEventDispatcher } from 'svelte'
  import { icons } from './icons.js'
  // state[id] = { open, connected, loading, error, dbNames: [], activeDb,
  //   dbs: { [db]: { open, loading, error, tables, views, routines, events, folders: {tables,views,routines,events},
  //                  td: { [table]: { open, loading, error, details, folders: {columns,keys,foreignKeys,indexes,triggers,partitions} } } } },
  //   favorites: ['db.table'], queries: [{name, sql}], folders: { favorites, queries } }
  export let connections = [], state = {}, activeId = null
  const dispatch = createEventDispatcher()
  let filter = ''

  $: groups = Object.entries(
    connections.reduce((g, c) => ((g[c.group || ''] ||= []).push(c), g), {})
  ).sort(([a], [b]) => a.localeCompare(b))
  $: q = filter.trim().toLowerCase()
  const has = (s) => !q || (s || '').toLowerCase().includes(q)
  const dbObjects = ds => [...(ds?.tables || []), ...(ds?.views || []), ...(ds?.routines || []).map(r => r.name), ...(ds?.events || [])]
  $: matchesConn = (c) => {
    if (!q || has(c.name)) return true
    const s = state[c.id] || {}
    return (s.dbNames || []).some(d => has(d) || dbObjects(s.dbs?.[d]).some(has))
  }
  $: matchesDb = (c, d) => !q || has(c.name) || has(d) || dbObjects(state[c.id]?.dbs?.[d]).some(has)
  $: listOf = (c, d, items) => (items || []).filter(t => !q || has(c.name) || has(d) || has(typeof t === 'string' ? t : t.name))

  const ctx = (name) => (e, detail) => { e.preventDefault(); dispatch(name, { x: e.clientX, y: e.clientY, ...detail }) }
  const menuConn = ctx('menuConn'), menuDb = ctx('menuDb'), menuTable = ctx('menuTable'), menuQuery = ctx('menuQuery'), menuView = ctx('menuView'), menuRoutine = ctx('menuRoutine'), menuDetail = ctx('menuDetail')
  const pad = depth => `padding-left:${depth * 20 + 4}px`
  const isOpen = (folders, name) => folders?.[name] !== false
  const TABLE_FOLDERS = [
    ['columns', 'columns', 'column'], ['keys', 'keys', 'key'], ['foreignKeys', 'foreign keys', 'fk'],
    ['indexes', 'indexes', 'index'], ['triggers', 'triggers', 'trigger'], ['partitions', 'partitions', 'partition'],
  ]
</script>

<aside>
  <div class="head">
    <span class="title">Database Explorer</span>
    <span class="tools">
      <button class="icon" title="Search everywhere: tables, views, routines, columns, connections (Ctrl+K / Ctrl+Shift+F)" on:click={() => dispatch('search')}>{@html icons.search}</button>
      <button class="icon" title="New connection" on:click={() => dispatch('new')}>{@html icons.plus}</button>
      <button class="icon" title="Import connections from DataGrip / PhpStorm / other JetBrains IDEs" on:click={() => dispatch('import')}>{@html icons.import}</button>
      <button class="icon" title="Refresh" disabled={!activeId} on:click={() => dispatch('refresh', activeId)}>{@html icons.refresh}</button>
    </span>
  </div>
  <div class="filter"><input placeholder="Filter connections, databases, tables…" bind:value={filter} /></div>

  <div class="tree" role="tree">
    {#if !connections.length}
      <p class="muted pad">No connections yet.<br />Add one with <b>+</b> or import them from DataGrip.</p>
    {/if}
    {#each groups as [group, list]}
      {#if group}<div class="group">{group}</div>{/if}
      {#each list.filter(matchesConn) as c (c.id)}
        {@const s = state[c.id] || {}}
        <!-- connection -->
        <div class="node conn" style={pad(0)} class:active={c.id === activeId && !s.activeDb && !s.activeTable} class:connected={s.connected}
             role="treeitem" aria-expanded={!!s.open} tabindex="0"
             on:click={() => dispatch('select', { id: c.id })}
             on:dblclick={() => dispatch('toggle', c.id)}
             on:contextmenu={e => menuConn(e, { conn: c })}
             on:keydown={e => { if (e.key === 'Enter') dispatch('toggle', c.id); if (e.key === 'Delete') dispatch('delete', c.id) }}>
          <button class="chev" class:open={s.open} on:click|stopPropagation={() => dispatch('toggle', c.id)} tabindex="-1" aria-label="expand">{@html icons.chevron}</button>
          <span class="ic" style="color:{c.color || 'var(--fg2)'}">{@html s.loading ? icons.spinner : icons.plug}</span>
          <span class="name">{c.name}</span>
          {#if c.favorite}<span class="fav">{@html icons.starOn}</span>{/if}
          {#if s.connected}<span class="count">{s.dbNames?.length ?? 0}</span>{/if}
          {#if s.error}<span class="bad" title={s.error}>{@html icons.err}</span>{/if}
          <span class="sub">{c.driver === 'postgres' ? 'pg' : 'my'}{c.ssh?.host ? ' · ssh' : ''}</span>
        </div>

        {#if s.open}
          {#if s.error}
            <div class="node errline mono" style={pad(1)} title={s.error}>{s.error}</div>
          {:else if s.connected}
            <!-- favorites -->
            {#if s.favorites?.length}
              <div class="node folder" style={pad(1)} role="treeitem" on:click={() => dispatch('folder', { id: c.id, folder: 'favorites' })}>
                <span class="chev" class:open={isOpen(s.folders, 'favorites')}>{@html icons.chevron}</span>
                <span class="ic star">{@html icons.starOn}</span><span class="name">favorites</span><span class="count">{s.favorites.length}</span>
              </div>
              {#if isOpen(s.folders, 'favorites')}
                {#each s.favorites as t (t)}
                  <div class="node leaf" style={pad(2)} role="treeitem" class:active={c.id === activeId && s.activeTable === t} on:click={() => dispatch('selectTable', { id: c.id, db: t.split('.')[0], table: t })} on:dblclick={() => dispatch('table', { id: c.id, table: t })} on:contextmenu={e => menuTable(e, { conn: c, table: t })}>
                    <span class="ic">{@html icons.table}</span><span class="name mono">{t}</span>
                  </div>
                {/each}
              {/if}
            {/if}

            {@const pg = c.driver === 'postgres'}
            {#if pg}
              <div class="node db pgdb" style={pad(1)} role="treeitem" title="Connected PostgreSQL database; schemas are listed below">
                <span class="chev open">{@html icons.chevron}</span>
                <span class="ic dbic">{@html icons.db}</span>
                <span class="name">{s.dbObjects?.database || c.database || 'database'}</span><span class="count">{s.dbNames?.length ?? 0} schemas</span>
              </div>
            {/if}
            <!-- databases / schemas -->
            {#each (s.dbNames || []).filter(d => matchesDb(c, d)) as d (d)}
              {@const ds = s.dbs?.[d] || {}}
              {@const lvl = pg ? 1 : 0}
              <div class="node db" style={pad(1 + lvl)} class:active={c.id === activeId && s.activeDb === d && !s.activeTable} class:def={d === c.database} role="treeitem" aria-expanded={!!ds.open}
                   on:click={() => dispatch('select', { id: c.id, db: d })}
                   on:dblclick={() => dispatch('toggleDb', { id: c.id, db: d })}
                   on:contextmenu={e => menuDb(e, { conn: c, db: d })}>
                <button class="chev" class:open={ds.open} on:click|stopPropagation={() => dispatch('toggleDb', { id: c.id, db: d })} tabindex="-1" aria-label="expand">{@html icons.chevron}</button>
                <span class="ic dbic">{@html ds.loading ? icons.spinner : icons.db}</span>
                <span class="name">{d}</span>
                {#if ds.tables}<span class="count">{ds.tables.length}</span>{/if}
                {#if ds.error}<span class="bad" title={ds.error}>{@html icons.err}</span>{/if}
              </div>

              {#if ds.open && ds.error}
                <div class="node errline mono" style={pad(2 + lvl)}>{ds.error}</div>
              {:else if ds.open && ds.tables}
                <!-- tables -->
                <div class="node folder" style={pad(2 + lvl)} role="treeitem" on:click={() => dispatch('dbFolder', { id: c.id, db: d, folder: 'tables' })}>
                  <span class="chev" class:open={isOpen(ds.folders, 'tables')}>{@html icons.chevron}</span>
                  <span class="ic">{@html icons.folder}</span><span class="name">tables</span><span class="count">{ds.tables.length}</span>
                </div>
                {#if isOpen(ds.folders, 'tables')}
                  {#each listOf(c, d, ds.tables) as t (t)}
                    {@const qn = d + '.' + t}
                    {@const td = ds.td?.[t] || {}}
                    <div class="node tbl" style={pad(3 + lvl)} role="treeitem" aria-expanded={!!td.open} class:active={c.id === activeId && s.activeTable === qn}
                         on:click={() => dispatch('selectTable', { id: c.id, db: d, table: qn })}
                         on:dblclick={() => dispatch('table', { id: c.id, table: qn })}
                         on:contextmenu={e => menuTable(e, { conn: c, table: qn })}>
                      <button class="chev" class:open={td.open} on:click|stopPropagation={() => dispatch('toggleTable', { id: c.id, db: d, table: t })} tabindex="-1" aria-label="expand">{@html icons.chevron}</button>
                      <span class="ic">{@html td.loading ? icons.spinner : icons.table}</span><span class="name mono">{t}</span>
                      {#if s.favorites?.includes(qn)}<span class="fav">{@html icons.starOn}</span>{/if}
                      {#if td.error}<span class="bad" title={td.error}>{@html icons.err}</span>{/if}
                    </div>
                    {#if td.open && td.details}
                      {#each TABLE_FOLDERS as [key, label, kind] (key)}
                        {@const items = td.details[key] || []}
                        {#if items.length || key === 'columns'}
                          <div class="node folder" style={pad(4 + lvl)} role="treeitem" on:click|stopPropagation={() => dispatch('tableFolder', { id: c.id, db: d, table: t, folder: key })} on:contextmenu|stopPropagation={e => menuDetail(e, { conn: c, table: qn, kind, folder: true })}>
                            <span class="chev" class:open={isOpen(td.folders, key) && (key === 'columns' || key === 'keys' || td.folders?.[key])}>{@html icons.chevron}</span>
                            <span class="ic">{@html icons.folder}</span><span class="name">{label}</span><span class="count">{items.length}</span>
                          </div>
                          {#if isOpen(td.folders, key) && (key === 'columns' || key === 'keys' || td.folders?.[key])}
                            {#each items as it (it.name)}
                              <div class="node leaf detail" style={pad(5 + lvl)} role="treeitem" title={it.definition || it.expression || it.type || ''}
                                   on:click|stopPropagation={() => dispatch('detail', { id: c.id, kind, item: it, table: qn })}
                                   on:dblclick|stopPropagation={() => dispatch('modify', { id: c.id, table: qn, kind, item: it })}
                                   on:contextmenu|stopPropagation={e => menuDetail(e, { conn: c, table: qn, kind, item: it })}>
                                {#if kind === 'column'}
                                  <span class="ic" class:pk={it.key === 'PRI'}>{@html it.key === 'PRI' ? icons.keyIcon : icons.column}</span>
                                  <span class="name mono">{it.name}</span>
                                  <span class="meta mono">{it.type}{it.nullable ? '' : ' not null'}{it.extra ? ' ' + it.extra.replace(/^auto_increment$/, '(auto increment)') : ''}{it.default && it.default !== 'NULL' && !/auto_increment/.test(it.extra) ? ' = ' + it.default : ''}</span>
                                {:else if kind === 'key'}
                                  <span class="ic pk">{@html icons.keyIcon}</span>
                                  <span class="name mono">{it.name}</span><span class="meta mono">({it.columns.join(', ')}){it.type === 'UNIQUE' ? ' unique' : ''}</span>
                                {:else if kind === 'fk'}
                                  <span class="ic">{@html icons.link}</span>
                                  <span class="name mono">{it.name}</span><span class="meta mono">{it.definition}</span>
                                {:else if kind === 'index'}
                                  <span class="ic">{@html icons.index}</span>
                                  <span class="name mono">{it.name}</span><span class="meta mono">{it.definition}{it.unique ? ' unique' : ''}</span>
                                {:else if kind === 'trigger'}
                                  <span class="ic">{@html icons.trigger}</span>
                                  <span class="name mono">{it.name}</span><span class="meta mono">{it.timing} {it.event}</span>
                                {:else}
                                  <span class="ic">{@html icons.partition}</span>
                                  <span class="name mono">{it.name}</span><span class="meta mono">{it.method} {it.expression} {it.description}</span>
                                {/if}
                              </div>
                            {/each}
                          {/if}
                        {/if}
                      {/each}
                    {:else if td.open && td.error}
                      <div class="node errline mono" style={pad(4 + lvl)}>{td.error}</div>
                    {/if}
                  {/each}
                {/if}

                <!-- views -->
                {#if ds.views?.length}
                  <div class="node folder" style={pad(2 + lvl)} role="treeitem" on:click={() => dispatch('dbFolder', { id: c.id, db: d, folder: 'views' })}>
                    <span class="chev" class:open={ds.folders?.views}>{@html icons.chevron}</span>
                    <span class="ic">{@html icons.folder}</span><span class="name">views</span><span class="count">{ds.views.length}</span>
                  </div>
                  {#if ds.folders?.views}
                    {#each listOf(c, d, ds.views) as v (v)}
                      {@const qn = d + '.' + v}
                      <div class="node leaf" style={pad(3 + lvl)} role="treeitem" class:active={c.id === activeId && s.activeTable === qn} on:click={() => dispatch('selectTable', { id: c.id, db: d, table: qn })} on:dblclick={() => dispatch('table', { id: c.id, table: qn })} on:contextmenu={e => menuView(e, { conn: c, view: qn })}>
                        <span class="ic">{@html icons.view}</span><span class="name mono">{v}</span>
                      </div>
                    {/each}
                  {/if}
                {/if}

                <!-- routines -->
                {#if ds.routines?.length}
                  <div class="node folder" style={pad(2 + lvl)} role="treeitem" on:click={() => dispatch('dbFolder', { id: c.id, db: d, folder: 'routines' })}>
                    <span class="chev" class:open={ds.folders?.routines}>{@html icons.chevron}</span>
                    <span class="ic">{@html icons.folder}</span><span class="name">routines</span><span class="count">{ds.routines.length}</span>
                  </div>
                  {#if ds.folders?.routines}
                    {#each listOf(c, d, ds.routines) as r (r.name + r.type)}
                      <div class="node leaf" style={pad(3 + lvl)} role="treeitem" title="{r.type} · right-click for source" on:contextmenu={e => menuRoutine(e, { conn: c, db: d, routine: r })} on:dblclick={() => dispatch('routine', { id: c.id, db: d, routine: r })}>
                        <span class="ic">{@html icons.routine}</span><span class="name mono">{r.name}</span><span class="meta">{r.type.toLowerCase()}</span>
                      </div>
                    {/each}
                  {/if}
                {/if}

                <!-- sequences (PostgreSQL) -->
                {#if ds.sequences?.length}
                  <div class="node folder" style={pad(2 + lvl)} role="treeitem" on:click={() => dispatch('dbFolder', { id: c.id, db: d, folder: 'sequences' })}>
                    <span class="chev" class:open={ds.folders?.sequences}>{@html icons.chevron}</span>
                    <span class="ic">{@html icons.folder}</span><span class="name">sequences</span><span class="count">{ds.sequences.length}</span>
                  </div>
                  {#if ds.folders?.sequences}
                    {#each listOf(c, d, ds.sequences) as sq (sq.name)}
                      <div class="node leaf" style={pad(3 + lvl)} role="treeitem"><span class="ic">{@html icons.seq}</span><span class="name mono">{sq.name}</span><span class="meta mono">{sq.type}</span></div>
                    {/each}
                  {/if}
                {/if}
                <!-- object types (PostgreSQL) -->
                {#if ds.types?.length}
                  <div class="node folder" style={pad(2 + lvl)} role="treeitem" on:click={() => dispatch('dbFolder', { id: c.id, db: d, folder: 'types' })}>
                    <span class="chev" class:open={ds.folders?.types}>{@html icons.chevron}</span>
                    <span class="ic">{@html icons.folder}</span><span class="name">object types</span><span class="count">{ds.types.length}</span>
                  </div>
                  {#if ds.folders?.types}
                    {#each listOf(c, d, ds.types) as ty (ty.name)}
                      <div class="node" style={pad(3 + lvl)} role="treeitem" on:click={() => dispatch('dbFolder', { id: c.id, db: d, folder: 'type:' + ty.name })} title={ty.kind}>
                        <span class="chev" class:open={ds.folders?.['type:' + ty.name]} class:hidden={!ty.values?.length}>{@html icons.chevron}</span>
                        <span class="ic">{@html icons.enumT}</span><span class="name mono">{ty.name}</span><span class="meta">{ty.kind}</span>
                      </div>
                      {#if ds.folders?.['type:' + ty.name]}
                        {#each ty.values as v}
                          <div class="node leaf" style={pad(4 + lvl)} role="treeitem"><span class="ic">{@html icons.column}</span><span class="name mono">{v}</span></div>
                        {/each}
                      {/if}
                    {/each}
                  {/if}
                {/if}
                <!-- events (MySQL) -->
                {#if ds.events?.length}
                  <div class="node folder" style={pad(2 + lvl)} role="treeitem" on:click={() => dispatch('dbFolder', { id: c.id, db: d, folder: 'events' })}>
                    <span class="chev" class:open={ds.folders?.events}>{@html icons.chevron}</span>
                    <span class="ic">{@html icons.folder}</span><span class="name">events</span><span class="count">{ds.events.length}</span>
                  </div>
                  {#if ds.folders?.events}
                    {#each listOf(c, d, ds.events) as ev (ev)}
                      <div class="node leaf" style={pad(3 + lvl)} role="treeitem"><span class="ic">{@html icons.event}</span><span class="name mono">{ev}</span></div>
                    {/each}
                  {/if}
                {/if}
              {/if}
            {/each}

            {#if pg && s.dbObjects}
              <div class="node folder" style={pad(1)} role="treeitem" on:click={() => dispatch('folder', { id: c.id, folder: 'dbObjects' })}>
                <span class="chev" class:open={s.folders?.dbObjects}>{@html icons.chevron}</span>
                <span class="ic">{@html icons.folder}</span><span class="name">Database Objects</span>
              </div>
              {#if s.folders?.dbObjects}
                <div class="node folder" style={pad(2)} role="treeitem" on:click={() => dispatch('folder', { id: c.id, folder: 'extensions' })}>
                  <span class="chev" class:open={s.folders?.extensions}>{@html icons.chevron}</span>
                  <span class="ic">{@html icons.folder}</span><span class="name">extensions</span><span class="count">{s.dbObjects.extensions.length}</span>
                </div>
                {#if s.folders?.extensions}
                  {#each s.dbObjects.extensions as ex (ex.name)}
                    <div class="node leaf" style={pad(3)} role="treeitem"><span class="ic">{@html icons.ext}</span><span class="name mono">{ex.name}</span><span class="meta">{ex.version}</span></div>
                  {/each}
                {/if}
                <div class="node folder" style={pad(2)} role="treeitem" on:click={() => dispatch('folder', { id: c.id, folder: 'languages' })}>
                  <span class="chev" class:open={s.folders?.languages}>{@html icons.chevron}</span>
                  <span class="ic">{@html icons.folder}</span><span class="name">languages</span><span class="count">{s.dbObjects.languages.length}</span>
                </div>
                {#if s.folders?.languages}
                  {#each s.dbObjects.languages as l (l)}
                    <div class="node leaf" style={pad(3)} role="treeitem"><span class="ic">{@html icons.routine}</span><span class="name mono">{l}</span></div>
                  {/each}
                {/if}
              {/if}
            {/if}
          {:else if !s.loading}
            <div class="node muted" style={pad(1)}>not connected</div>
          {/if}
        {/if}
      {/each}
    {/each}
  </div>
</aside>

<style>
  aside { display: flex; flex-direction: column; min-height: 0; min-width: 0; background: var(--bg2); height: 100%; }
  .head { display: flex; align-items: center; justify-content: space-between; padding: 6px 8px 4px 10px; }
  .title { font-weight: 600; font-size: 12.5px; }
  .tools { display: flex; gap: 2px; }
  .filter { padding: 0 8px 6px; }
  .filter input { padding: 3px 8px; font-size: 12px; }
  .tree { overflow: auto; flex: 1; padding-bottom: 12px; }
  .group { padding: 8px 10px 2px; font-size: 11px; text-transform: uppercase; letter-spacing: .04em; color: var(--fg2); }
  .node { display: flex; align-items: center; gap: 4px; height: 23px; padding-right: 8px; cursor: default; user-select: none; white-space: nowrap; }
  .node:hover { background: var(--hover); }
  .node.active { background: var(--sel); }
  .leaf { padding-left: 20px; }
  .chev { display: inline-flex; width: 16px; height: 16px; align-items: center; justify-content: center; color: var(--fg2); background: none; border: 0; padding: 0; transition: transform .12s; flex: none; }
  .chev.open { transform: rotate(90deg); }
  .chev.hidden { visibility: hidden; }
  .pgdb .name { font-weight: 600; }
  .ic { display: inline-flex; color: var(--fg2); flex: none; }
  .dbic { color: #7aa2f7; }
  .ic.pk { color: #e7b64a; }
  .ic.star, .fav { color: #e7b64a; display: inline-flex; }
  .name { overflow: hidden; text-overflow: ellipsis; }
  .conn .name { font-weight: 500; }
  .db.def .name { font-weight: 600; }
  .count { color: var(--fg2); font-size: 11px; margin-left: 4px; }
  .meta { color: var(--fg2); font-size: 11px; margin-left: 6px; overflow: hidden; text-overflow: ellipsis; }
  .sub { margin-left: auto; color: var(--fg2); font-size: 10.5px; opacity: .7; }
  .bad { color: var(--err); display: inline-flex; }
  .errline { color: var(--err); font-size: 11.5px; overflow: hidden; text-overflow: ellipsis; height: auto; padding-top: 2px; padding-bottom: 2px; white-space: normal; }
  .folder .name { color: var(--fg); }
  .pad { padding: 10px; }
  :global(.spin) { animation: spin 1s linear infinite; transform-origin: 8px 8px; }
  @keyframes spin { to { transform: rotate(360deg); } }
</style>

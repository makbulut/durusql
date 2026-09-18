<script>
  // DataGrip-style "Modify table": object tree on the left, property form on the right,
  // generated SQL preview at the bottom. Also creates new tables (table = '').
  import { createEventDispatcher, onMount } from 'svelte'
  import * as api from './api.js'
  import { icons } from './icons.js'
  import { highlightSQL } from './highlight.js'
  export let connId, connName = '', driver = 'mysql', database = '', table = '', tables = []
  export let select = null     // { kind: 'column'|'key'|'fk'|'index', name } to preselect
  const dispatch = createEventDispatcher()
  const pg = driver === 'postgres'
  const tblName = table ? table.split('.').pop() : ''
  let m = null                 // model, see load()
  let sel = { kind: 'table' }  // { kind, id }
  let loading = !!table, err = '', msg = '', busy = false
  let open = { columns: true, keys: true, fks: true, indexes: true }
  let nextId = 1
  const uid = () => 'o' + nextId++
  const TYPES = pg
    ? ['integer', 'bigint', 'smallint', 'serial', 'bigserial', 'numeric(12,2)', 'real', 'double precision', 'boolean', 'varchar(255)', 'text', 'char(1)', 'uuid', 'date', 'timestamp', 'timestamptz', 'time', 'json', 'jsonb', 'bytea']
    : ['int', 'int unsigned', 'bigint', 'bigint unsigned', 'smallint', 'tinyint', 'tinyint(1)', 'decimal(12,2)', 'float', 'double', 'varchar(255)', 'text', 'mediumtext', 'longtext', 'char(1)', 'date', 'datetime', 'timestamp', 'time', 'json', 'blob', "enum('a','b')"]
  const ENGINES = ['InnoDB', 'MyISAM', 'MEMORY', 'ARCHIVE', 'CSV']
  const COLLATIONS = ['utf8mb4_general_ci', 'utf8mb4_unicode_ci', 'utf8mb4_0900_ai_ci', 'utf8mb4_bin', 'utf8mb3_general_ci', 'latin1_swedish_ci', 'ascii_general_ci']
  const FK_ACTIONS = ['NO ACTION', 'RESTRICT', 'CASCADE', 'SET NULL', 'SET DEFAULT']

  const snap = o => JSON.parse(JSON.stringify(o))
  function newColumn() { return { id: uid(), name: '', type: 'varchar(255)', nullable: true, def: '', auto: false, comment: '', kind: 'NORMAL', genExpr: '', onUpdate: '', hidden: false, collation: '', orig: null, dropped: false } }
  const COL_COLLATIONS = ['', 'utf8mb4_general_ci', 'utf8mb4_unicode_ci', 'utf8mb4_0900_ai_ci', 'utf8mb4_bin', 'utf8mb3_general_ci', 'latin1_swedish_ci', 'ascii_general_ci', 'ascii_bin']
  function newKey(type = 'UNIQUE') { return { id: uid(), name: type === 'PRIMARY KEY' ? 'PRIMARY' : '', type, columns: [], orig: null, dropped: false } }
  function newFk() { return { id: uid(), name: '', columns: [], refTable: '', refColumns: [], onDelete: 'NO ACTION', onUpdate: 'NO ACTION', orig: null, dropped: false } }
  function newIndex() { return { id: uid(), name: '', columns: [], unique: false, orig: null, dropped: false } }

  onMount(load)
  async function load() {
    if (!table) {
      const id = { ...newColumn(), name: 'id', type: pg ? 'serial' : 'int', nullable: false, auto: !pg }
      m = { name: '', comment: '', engine: 'InnoDB', collation: 'utf8mb4_general_ci', autoIncrement: 0, columns: [id], keys: [{ ...newKey('PRIMARY KEY'), columns: ['id'] }], fks: [], indexes: [] }
      m.orig = null
      return
    }
    try {
      const d = await api.tableDetails(connId, table)
      const columns = d.columns.map(c => {
        const auto = /auto_increment/i.test(c.extra) || /nextval\(/i.test(c.default) || !!c.identity
        const col = { id: uid(), name: c.name, type: c.type, nullable: c.nullable, def: auto || c.default === 'NULL' ? '' : c.default, auto, comment: c.comment || '',
          kind: c.genKind ? 'GENERATED ' + c.genKind : 'NORMAL', genExpr: c.generation || '', onUpdate: c.onUpdate || '', hidden: !!c.hidden, collation: c.collation || '', dropped: false }
        col.orig = snap(col); return col
      })
      const keys = (d.keys || []).map(k => { const o = { id: uid(), name: k.name, type: k.type, columns: [...k.columns], dropped: false }; o.orig = snap(o); return o })
      const fks = (d.foreignKeys || []).map(f => {
        let cols = f.columns || [], ref = f.refTable || '', refCols = f.refColumns || [], onDelete = 'NO ACTION', onUpdate = 'NO ACTION'
        const mm = /FOREIGN KEY \(([^)]*)\) REFERENCES ([^\s(]+)\s*\(([^)]*)\)(.*)$/i.exec(f.definition || '')
        if (mm && !cols.length) { cols = mm[1].split(',').map(x => x.trim().replace(/"/g, '')); ref = mm[2].replace(/"/g, ''); refCols = mm[3].split(',').map(x => x.trim().replace(/"/g, '')) }
        const od = /ON DELETE (SET NULL|SET DEFAULT|CASCADE|RESTRICT|NO ACTION)/i.exec(f.definition || ''); if (od) onDelete = od[1].toUpperCase()
        const ou = /ON UPDATE (SET NULL|SET DEFAULT|CASCADE|RESTRICT|NO ACTION)/i.exec(f.definition || ''); if (ou) onUpdate = ou[1].toUpperCase()
        const o = { id: uid(), name: f.name, columns: cols, refTable: ref, refColumns: refCols, onDelete, onUpdate, dropped: false }; o.orig = snap(o); return o
      })
      const uniqueNames = new Set(keys.map(k => k.name))
      const indexes = (d.indexes || []).filter(i => !uniqueNames.has(i.name) && i.name !== 'PRIMARY').map(i => {
        const o = { id: uid(), name: i.name, columns: [...(i.columns || [])], unique: !!i.unique, dropped: false }; o.orig = snap(o); return o
      })
      origOrder = columns.map(c => c.name)
      m = { name: tblName, comment: d.info?.comment || '', engine: d.info?.engine || '', collation: d.info?.collation || '', autoIncrement: d.info?.autoIncrement || 0, columns, keys, fks, indexes }
      m.orig = { name: m.name, comment: m.comment, engine: m.engine, collation: m.collation, autoIncrement: m.autoIncrement }
      if (select?.name === '__new__') add(select.kind)
      else if (select) {
        const list = { column: m.columns, key: m.keys, fk: m.fks, index: m.indexes }[select.kind] || []
        const it = list.find(x => x.name === select.name)
        if (it) sel = { kind: select.kind, id: it.id }
      }
    } catch (e) { err = String(e) } finally { loading = false }
  }

  // ---- helpers (DataGrip style: lowercase keywords, quote identifiers only when needed,
  //      no database prefix because the statements run with the database selected) ----
  const RESERVED = new Set(('add all alter and as asc between by case check column constraint create cross database default delete desc distinct drop else end exists foreign from group having in index inner insert into is join key left like limit not null on or order outer primary references right select set table then to union unique update using values where with user order group key index name type value date time timestamp year month day desc asc status comment level position result role session text int integer char').split(' '))
  const q = s => /^[a-z_][a-z0-9_]*$/.test(s) && !RESERVED.has(s) ? s : (pg ? `"${s.replaceAll('"', '""')}"` : '`' + s.replaceAll('`', '``') + '`')
  const qt = () => q(m.name)
  const qtOrig = () => q(tblName || m.name)
  const lit = s => `'${s.replaceAll("'", "''")}'`
  const defSQL = d => { const t = d.trim(); if (!t) return ''; if (/^(current_timestamp|now\(\)|null|true|false|current_date|localtimestamp)/i.test(t) || /^-?\d+(\.\d+)?$/.test(t) || /^'.*'$/.test(t) || /^[a-z_]+\(.*\)$/i.test(t)) return t; return lit(t) }
  const live = arr => arr.filter(x => !x.dropped)
  const idxSpec = i => '(' + i.columns.filter(Boolean).map(q).join(', ') + ')'
  function colDef(c) {
    const gen = c.kind && c.kind !== 'NORMAL' && c.genExpr.trim()
    let s = q(c.name) + ' ' + c.type
    if (c.collation && !pg) s += ' COLLATE ' + c.collation
    if (c.collation && pg) s += ' COLLATE ' + lit(c.collation).replace(/^'|'$/g, '"')
    if (gen) s += ` GENERATED ALWAYS AS (${c.genExpr.trim()}) ${c.kind === 'GENERATED VIRTUAL' && !pg ? 'VIRTUAL' : 'STORED'}`
    else {
      if (pg && c.auto && !/serial/i.test(c.type)) s += ' GENERATED BY DEFAULT AS IDENTITY'
      s += c.nullable && !isPk(c.name) ? ' NULL' : ' NOT NULL'
      if (c.def.trim() && !c.auto) s += ' DEFAULT ' + defSQL(c.def)
      if (!pg && c.onUpdate.trim()) s += ' ON UPDATE ' + c.onUpdate.trim()
      if (!pg && c.auto) s += ' AUTO_INCREMENT'
    }
    if (!pg && c.hidden) s += ' INVISIBLE'
    if (!pg && c.comment) s += ' COMMENT ' + lit(c.comment)
    return s
  }
  $: isPk = name => !!m && live(m.keys).some(k => k.type === 'PRIMARY KEY' && k.columns.includes(name))
  const fkDef = f => `FOREIGN KEY (${f.columns.filter(Boolean).map(q).join(', ')}) REFERENCES ${f.refTable.split('.').map(q).join('.')} (${f.refColumns.filter(Boolean).map(q).join(', ')})` + (f.onDelete !== 'NO ACTION' ? ' ON DELETE ' + f.onDelete : '') + (f.onUpdate !== 'NO ACTION' ? ' ON UPDATE ' + f.onUpdate : '')
  const keyDef = k => k.type === 'PRIMARY KEY' ? `PRIMARY KEY ${idxSpec(k)}` : `${pg ? 'CONSTRAINT ' + q(k.name || (m.name + '_' + k.columns.join('_') + '_key')) + ' ' : ''}UNIQUE ${pg ? '' : (k.name ? q(k.name) + ' ' : '')}${idxSpec(k)}`
  const changed = (a, b) => JSON.stringify(a) !== JSON.stringify(b)
  const strip = o => { const { orig, dropped, id, ...rest } = o; return rest }

  const KW = ['RENAME TABLE', 'MODIFY ', 'CHANGE ', 'GENERATED ALWAYS AS', 'VIRTUAL', 'STORED', 'INVISIBLE', 'ADD GENERATED BY DEFAULT AS IDENTITY', 'DROP IDENTITY', 'DROP EXPRESSION', 'AUTO_INCREMENT=', 'ALTER TABLE', 'CREATE TABLE', 'CREATE UNIQUE INDEX', 'CREATE INDEX', 'DROP INDEX', 'DROP COLUMN', 'DROP FOREIGN KEY', 'DROP PRIMARY KEY', 'DROP CONSTRAINT', 'ADD COLUMN', 'ADD CONSTRAINT', 'ADD UNIQUE INDEX', 'ADD INDEX', 'ADD PRIMARY KEY', 'ADD UNIQUE', 'ADD ', 'CHANGE COLUMN', 'RENAME COLUMN', 'RENAME TO', 'ALTER COLUMN', 'SET NOT NULL', 'DROP NOT NULL', 'SET DEFAULT', 'DROP DEFAULT', 'NOT NULL', 'NULL', 'DEFAULT', 'AUTO_INCREMENT', 'COMMENT ON COLUMN', 'COMMENT ON TABLE', 'COMMENT', 'PRIMARY KEY', 'FOREIGN KEY', 'REFERENCES', 'ON DELETE', 'ON UPDATE', 'CASCADE', 'SET NULL', 'RESTRICT', 'NO ACTION', 'UNIQUE', 'INDEX', 'AFTER', 'FIRST', 'TYPE', 'GENERATED BY DEFAULT AS IDENTITY', 'ENGINE', 'COLLATE', 'DEFAULT CHARSET', ' IS ', ' ON ', ' TO ']
  const lower = s => KW.reduce((acc, k) => acc.replaceAll(k, k.toLowerCase()), s)
  $: sql = m && origOrder ? lower(table ? alterSQL() : createSQL()) : ''
  function createSQL() {
    if (!m.name) return '-- give the table a name'
    const lines = live(m.columns).filter(c => c.name).map(c => '  ' + colDef(c))
    for (const k of live(m.keys)) if (k.columns.length) lines.push('  ' + keyDef(k))
    for (const f of live(m.fks)) if (f.columns.length && f.refTable) lines.push('  ' + (f.name ? 'CONSTRAINT ' + q(f.name) + ' ' : '') + fkDef(f))
    if (!pg) for (const i of live(m.indexes)) if (i.columns.length) lines.push(`  ${i.unique ? 'UNIQUE ' : ''}INDEX ${q(i.name || 'idx_' + i.columns.join('_'))} ${idxSpec(i)}`)
    let s = `CREATE TABLE ${qt()} (\n${lines.join(',\n')}\n)`
    if (!pg) { if (m.engine) s += ` ENGINE=${m.engine}`; s += ' DEFAULT CHARSET=utf8mb4'; if (m.collation) s += ` COLLATE=${m.collation}`; if (m.comment) s += ` COMMENT=${lit(m.comment)}` }
    s += ';'
    if (pg) { for (const i of live(m.indexes)) if (i.columns.length) s += `\nCREATE ${i.unique ? 'UNIQUE ' : ''}INDEX ${q(i.name || m.name + '_' + i.columns.join('_') + '_idx')} ON ${qt()} ${idxSpec(i)};`; if (m.comment) s += `\nCOMMENT ON TABLE ${qt()} IS ${lit(m.comment)};`; for (const c of live(m.columns)) if (c.comment) s += `\nCOMMENT ON COLUMN ${qt()}.${q(c.name)} IS ${lit(c.comment)};` }
    return s
  }
  function alterSQL() {
    const t = qtOrig(); const out = []
    const A = s => out.push(`ALTER TABLE ${t} ${s};`)
    // foreign keys and keys are dropped before column changes and re-added after
    for (const f of m.fks) if (f.orig && (f.dropped || changed(strip(f), strip(f.orig)))) A(pg ? `DROP CONSTRAINT ${q(f.orig.name)}` : `DROP FOREIGN KEY ${q(f.orig.name)}`)
    for (const k of m.keys) if (k.orig && (k.dropped || changed(strip(k), strip(k.orig)))) A(k.orig.type === 'PRIMARY KEY' ? (pg ? `DROP CONSTRAINT ${q(k.orig.name)}` : 'DROP PRIMARY KEY') : (pg ? `DROP CONSTRAINT ${q(k.orig.name)}` : `DROP INDEX ${q(k.orig.name)}`))
    for (const i of m.indexes) if (i.orig && (i.dropped || changed(strip(i), strip(i.orig)))) out.push(pg ? `DROP INDEX ${q(i.orig.name)};` : `DROP INDEX ${q(i.orig.name)} ON ${t};`)
    // columns
    let prev = null
    for (const c of m.columns) {
      if (c.dropped) { if (c.orig) A(`DROP COLUMN ${q(c.orig.name)}`); continue }
      if (!c.orig) { if (c.name) A((pg ? 'ADD COLUMN ' : 'ADD ') + colDef(c) + (!pg && prev ? ` AFTER ${q(prev)}` : '')); prev = c.name; continue }
      const o = c.orig
      if (pg) {
        if (c.name !== o.name) A(`RENAME COLUMN ${q(o.name)} TO ${q(c.name)}`)
        if (c.type !== o.type) A(`ALTER COLUMN ${q(c.name)} TYPE ${c.type}`)
        if (c.nullable !== o.nullable) A(`ALTER COLUMN ${q(c.name)} ${c.nullable ? 'DROP' : 'SET'} NOT NULL`)
        if (c.def !== o.def) A(c.def.trim() ? `ALTER COLUMN ${q(c.name)} SET DEFAULT ${defSQL(c.def)}` : `ALTER COLUMN ${q(c.name)} DROP DEFAULT`)
        if (c.comment !== o.comment) out.push(`COMMENT ON COLUMN ${t}.${q(c.name)} IS ${c.comment ? lit(c.comment) : 'NULL'};`)
        if (c.auto !== o.auto && !/serial/i.test(c.type)) A(c.auto ? `ALTER COLUMN ${q(c.name)} ADD GENERATED BY DEFAULT AS IDENTITY` : `ALTER COLUMN ${q(c.name)} DROP IDENTITY`)
        if (c.collation !== o.collation && c.type === o.type) A(`ALTER COLUMN ${q(c.name)} TYPE ${c.type}${c.collation ? ' COLLATE "' + c.collation + '"' : ''}`)
        if ((c.kind !== o.kind || c.genExpr !== o.genExpr) && c.kind === 'NORMAL' && o.kind !== 'NORMAL') A(`ALTER COLUMN ${q(c.name)} DROP EXPRESSION`)
      } else {
        const moved = posChanged(c)
        if (changed(strip(c), strip(o)) || moved) A((c.name !== o.name ? `CHANGE ${q(o.name)} ` : 'MODIFY ') + colDef(c) + (moved ? (prev ? ` AFTER ${q(prev)}` : ' FIRST') : ''))
      }
      prev = c.name
    }
    // keys / indexes / fks (re)added
    for (const k of live(m.keys)) if ((!k.orig || changed(strip(k), strip(k.orig))) && k.columns.length) A(`ADD ${keyDef(k)}`)
    for (const i of live(m.indexes)) if ((!i.orig || changed(strip(i), strip(i.orig))) && i.columns.length) out.push(`CREATE ${i.unique ? 'UNIQUE ' : ''}INDEX ${q(i.name || (pg ? m.name + '_' + i.columns.join('_') + '_idx' : 'idx_' + i.columns.join('_')))} ON ${t} ${idxSpec(i)};`)
    for (const f of live(m.fks)) if ((!f.orig || changed(strip(f), strip(f.orig))) && f.columns.length && f.refTable) A(`ADD ${f.name ? 'CONSTRAINT ' + q(f.name) + ' ' : ''}${fkDef(f)}`)
    // table properties
    if (!pg) {
      const opts = []
      if (m.engine !== m.orig.engine && m.engine) opts.push(`ENGINE=${m.engine}`)
      if (m.collation !== m.orig.collation && m.collation) opts.push(`COLLATE=${m.collation}`)
      if (m.comment !== m.orig.comment) opts.push(`COMMENT=${lit(m.comment)}`)
      if (Number(m.autoIncrement) !== Number(m.orig.autoIncrement) && Number(m.autoIncrement) > 0) opts.push(`AUTO_INCREMENT=${Number(m.autoIncrement)}`)
      if (opts.length) A(opts.join(' '))
    } else if (m.comment !== m.orig.comment) out.push(`COMMENT ON TABLE ${t} IS ${m.comment ? lit(m.comment) : 'NULL'};`)
    if (m.name !== tblName) out.push(pg ? `ALTER TABLE ${t} RENAME TO ${q(m.name)};` : `RENAME TABLE ${t} TO ${q(m.name)};`)
    if (!out.length) return '-- no changes'
    // merge consecutive "ALTER TABLE t X;" lines into one statement with indented clauses
    const merged = []
    for (const line of out) {
      const mm = new RegExp(`^ALTER TABLE ${t.replace(/[.*+?^${}()|[\]\\]/g, '\\$&')} (.*);$`).exec(line)
      const last = merged[merged.length - 1]
      if (mm && last && last.t === t) last.clauses.push(mm[1])
      else if (mm) merged.push({ t, clauses: [mm[1]] })
      else merged.push({ raw: line })
    }
    return merged.map(x => x.raw ? x.raw : `ALTER TABLE ${x.t}\n    ${x.clauses.join(',\n    ')};`).join('\n')
  }
  // a column "moved" when the live column before it differs from the one before it originally
  let origOrder = []
  function posChanged(c) {
    if (!origOrder.length) return false
    const cur = m.columns.filter(x => !x.dropped)
    const i = cur.indexOf(c)
    const nowBefore = i > 0 ? (cur[i - 1].orig?.name || cur[i - 1].name) : null
    const oi = origOrder.indexOf(c.orig.name)
    const before = oi > 0 ? origOrder[oi - 1] : null
    return nowBefore !== before
  }

  // ---- tree actions ----
  $: current = m && sel.kind !== 'table' ? ({ column: m.columns, key: m.keys, fk: m.fks, index: m.indexes }[sel.kind] || []).find(x => x.id === sel.id) : null
  function add(kind) {
    const it = kind === 'column' ? newColumn() : kind === 'key' ? newKey(live(m.keys).some(k => k.type === 'PRIMARY KEY') ? 'UNIQUE' : 'PRIMARY KEY') : kind === 'fk' ? newFk() : newIndex()
    const list = { column: 'columns', key: 'keys', fk: 'fks', index: 'indexes' }[kind]
    m[list] = [...m[list], it]; open[list] = true; sel = { kind, id: it.id }
  }
  function remove() {
    if (!current) return
    const list = { column: 'columns', key: 'keys', fk: 'fks', index: 'indexes' }[sel.kind]
    if (current.orig) { current.dropped = !current.dropped; m = m } else { m[list] = m[list].filter(x => x !== current); sel = { kind: 'table' } }
  }
  function move(d) {
    if (sel.kind !== 'column' || !current) return
    const i = m.columns.indexOf(current), j = i + d
    if (j < 0 || j >= m.columns.length) return
    const c = [...m.columns]; [c[i], c[j]] = [c[j], c[i]]; m.columns = c
  }
  const colNames = () => live(m.columns).map(c => c.name).filter(Boolean)
  function toggleCol(list, name) { const i = list.indexOf(name); if (i >= 0) list.splice(i, 1); else list.push(name); m = m }
  let refCols = []
  async function loadRefCols(ref) {
    if (!ref) { refCols = []; return }
    try { const d = await api.tableDetails(connId, ref.includes('.') ? ref : (database ? database + '.' : '') + ref); refCols = d.columns.map(c => c.name) } catch { refCols = [] }
  }
  $: if (current && sel.kind === 'fk') loadRefCols(current.refTable)

  async function run() {
    if (!sql || sql.startsWith('--')) return
    if (table && !(await (window.durusqlAsk ? window.durusqlAsk(sql, { title: `Run on ${connName}?`, ok: 'Run', danger: true }) : Promise.resolve(confirm(sql))))) return
    busy = true; err = ''; msg = 'Running…'
    try {
      const r = await api.runScript(connId, database, sql, 1, '')
      if (r.error) throw new Error(`Statement ${r.errorIdx + 1} failed: ${r.error}`)
      msg = 'Done'
      dispatch('done', { table: (database ? database + '.' : '') + m.name, created: !table })
    } catch (e) { err = String(e); msg = '' } finally { busy = false }
  }
  const label = it => it.name || '(new)'
</script>

<div class="backdrop" on:mousedown|self={() => !busy && dispatch('close')} role="presentation">
  <div class="modal" role="dialog" aria-label="Modify table">
    <div class="title"><b>{table ? 'Modify' : 'New table'}</b><span class="muted small">{connName}{database ? ' · ' + database : ''}</span><span class="spacer"></span><button class="icon" on:click={() => dispatch('close')} title="Close">{@html icons.x}</button></div>
    {#if loading}<p class="muted pad">Loading structure…</p>
    {:else if m}
    <div class="body">
      <div class="left">
        <div class="tools">
          <button class="icon" title="Add column" on:click={() => add('column')}>{@html icons.plus}</button>
          <button class="icon small" title="Add key (primary / unique)" on:click={() => add('key')}>🔑</button>
          <button class="icon small" title="Add foreign key" on:click={() => add('fk')}>{@html icons.link}</button>
          <button class="icon small" title="Add index" on:click={() => add('index')}>{@html icons.index}</button>
          <span class="sep"></span>
          <button class="icon" title={current?.dropped ? 'Undo drop' : 'Drop / remove selected'} disabled={!current} on:click={remove}>{current?.dropped ? '↺' : '−'}</button>
          <button class="icon" title="Move column up" disabled={sel.kind !== 'column'} on:click={() => move(-1)}>▲</button>
          <button class="icon" title="Move column down" disabled={sel.kind !== 'column'} on:click={() => move(1)}>▼</button>
        </div>
        <div class="tree">
          <div class="node root" class:on={sel.kind === 'table'} on:click={() => sel = { kind: 'table' }}><span class="ic">{@html icons.table}</span><span class="mono">{m.name || '(table)'}</span></div>
          {#each [['columns', 'columns', 'column', icons.column], ['keys', 'keys', 'key', icons.keyIcon], ['fks', 'foreign keys', 'fk', icons.link], ['indexes', 'indexes', 'index', icons.index]] as [list, lbl, kind, ic]}
            <div class="node folder" on:click={() => open[list] = !open[list]}><span class="chev" class:open={open[list]}>{@html icons.chevron}</span><span class="ic">{@html icons.folder}</span>{lbl}<span class="cnt">{live(m[list]).length}</span></div>
            {#if open[list]}
              {#each m[list] as it (it.id)}
                <div class="node leaf" class:on={sel.kind === kind && sel.id === it.id} class:dropped={it.dropped} class:added={!it.orig} on:click={() => sel = { kind, id: it.id }}>
                  <span class="ic" class:pk={kind === 'column' && isPk(it.name)}>{@html kind === 'column' && isPk(it.name) ? icons.keyIcon : ic}</span>
                  <span class="mono">{label(it)}</span>
                  <span class="meta mono">{kind === 'column' ? it.type + (it.kind !== 'NORMAL' ? ' generated' : '') + (it.nullable || it.kind !== 'NORMAL' ? '' : ' not null') + (it.auto ? ' (auto increment)' : '') + (it.def ? ' = ' + it.def : '') + (it.hidden ? ' hidden' : '') : kind === 'fk' ? `(${it.columns.join(', ')}) → ${it.refTable}` : `(${it.columns.join(', ')})${it.unique ? ' unique' : ''}`}</span>
                </div>
              {/each}
            {/if}
          {/each}
        </div>
      </div>

      <div class="right">
        {#if sel.kind === 'table'}
          <div class="form">
            <label>Name</label><input class="mono" bind:value={m.name} />
            <label>Comment</label><input bind:value={m.comment} />
            {#if !pg}
              <label>Engine</label><input list="engines" bind:value={m.engine} /><datalist id="engines">{#each ENGINES as e}<option value={e} />{/each}</datalist>
              <label>Collation</label><input list="collations" bind:value={m.collation} /><datalist id="collations">{#each COLLATIONS as c}<option value={c} />{/each}</datalist>
            {/if}
          </div>
        {:else if current && sel.kind === 'column'}
          {@const gen = current.kind !== 'NORMAL'}
          <div class="form">
            <label>Name</label><input class="mono" bind:value={current.name} disabled={current.dropped} />
            <label>Comment</label><input bind:value={current.comment} disabled={current.dropped} />
            <label>Data type</label><input class="mono" list="types" bind:value={current.type} disabled={current.dropped} /><datalist id="types">{#each TYPES as t}<option value={t} />{/each}</datalist>
            <label></label><div class="inline">
              <label class="check"><input type="checkbox" checked={!current.nullable} disabled={current.dropped || isPk(current.name) || gen} on:change={e => { current.nullable = !e.target.checked; m = m }} /> Not null</label>
              <label class="check"><input type="checkbox" bind:checked={current.auto} disabled={current.dropped || gen} /> {pg ? 'Identity' : 'Auto increment'}</label>
              {#if !pg && current.auto}<input class="mono ai" type="number" min="1" bind:value={m.autoIncrement} placeholder="next value" title="Next auto-increment value (table level)" />{/if}
              <label class="check"><input type="checkbox" checked={isPk(current.name)} disabled={current.dropped} on:change={e => { let pk = m.keys.find(k => k.type === 'PRIMARY KEY' && !k.dropped); if (e.target.checked) { if (!pk) { pk = newKey('PRIMARY KEY'); m.keys = [...m.keys, pk] } if (!pk.columns.includes(current.name)) pk.columns = [...pk.columns, current.name]; current.nullable = false } else if (pk) { pk.columns = pk.columns.filter(x => x !== current.name); if (!pk.columns.length) { if (pk.orig) pk.dropped = true; else m.keys = m.keys.filter(k => k !== pk) } } m = m }} /> Primary key</label>
            </div>
            <label>Column kind</label><select bind:value={current.kind} disabled={current.dropped}><option>NORMAL</option><option>GENERATED STORED</option>{#if !pg}<option>GENERATED VIRTUAL</option>{/if}</select>
            {#if gen}
              <label>Expression</label><input class="mono" bind:value={current.genExpr} placeholder="e.g. price * qty" disabled={current.dropped} />
            {:else}
              <label>Default expression</label><input class="mono" bind:value={current.def} placeholder="0 · 'text' · CURRENT_TIMESTAMP" disabled={current.dropped || current.auto} />
            {/if}
            {#if !pg}
              <label></label><label class="check"><input type="checkbox" bind:checked={current.hidden} disabled={current.dropped} /> Hidden (INVISIBLE, MySQL 8 / MariaDB 10.3+)</label>
              <label>On update</label><input class="mono" bind:value={current.onUpdate} placeholder="CURRENT_TIMESTAMP" disabled={current.dropped || gen} />
            {/if}
            <label>Collation</label><input list="colcoll" bind:value={current.collation} placeholder="table default" disabled={current.dropped} /><datalist id="colcoll">{#each COL_COLLATIONS as c}<option value={c} />{/each}</datalist>
          </div>
        {:else if current && sel.kind === 'key'}
          <div class="form">
            <label>Type</label><select bind:value={current.type} disabled={current.dropped || !!current.orig}><option>PRIMARY KEY</option><option>UNIQUE</option></select>
            <label>Name</label><input class="mono" bind:value={current.name} disabled={current.dropped || current.type === 'PRIMARY KEY'} placeholder={current.type === 'PRIMARY KEY' ? 'PRIMARY' : 'uk_name'} />
            <label>Columns</label><div class="cols">{#each colNames() as cn}<label class="check"><input type="checkbox" checked={current.columns.includes(cn)} disabled={current.dropped} on:change={() => toggleCol(current.columns, cn)} /> <span class="mono">{cn}</span></label>{/each}
              <div class="muted small">order: {current.columns.join(', ') || '—'}</div></div>
          </div>
        {:else if current && sel.kind === 'index'}
          <div class="form">
            <label>Name</label><input class="mono" bind:value={current.name} disabled={current.dropped} placeholder="idx_name" />
            <label>Unique</label><div><input type="checkbox" bind:checked={current.unique} disabled={current.dropped} /></div>
            <label>Columns</label><div class="cols">{#each colNames() as cn}<label class="check"><input type="checkbox" checked={current.columns.includes(cn)} disabled={current.dropped} on:change={() => toggleCol(current.columns, cn)} /> <span class="mono">{cn}</span></label>{/each}
              <div class="muted small">order: {current.columns.join(', ') || '—'}</div></div>
          </div>
        {:else if current && sel.kind === 'fk'}
          <div class="form">
            <label>Name</label><input class="mono" bind:value={current.name} disabled={current.dropped} placeholder="fk_name" />
            <label>Columns</label><div class="cols">{#each colNames() as cn}<label class="check"><input type="checkbox" checked={current.columns.includes(cn)} disabled={current.dropped} on:change={() => toggleCol(current.columns, cn)} /> <span class="mono">{cn}</span></label>{/each}</div>
            <label>Target table</label><input class="mono" list="reftables" bind:value={current.refTable} disabled={current.dropped} placeholder="table or db.table" /><datalist id="reftables">{#each tables as t}<option value={t} />{/each}</datalist>
            <label>Target columns</label><div class="cols">{#if refCols.length}{#each refCols as cn}<label class="check"><input type="checkbox" checked={current.refColumns.includes(cn)} disabled={current.dropped} on:change={() => toggleCol(current.refColumns, cn)} /> <span class="mono">{cn}</span></label>{/each}{:else}<input class="mono" value={current.refColumns.join(', ')} on:input={e => current.refColumns = e.target.value.split(',').map(x => x.trim()).filter(Boolean)} placeholder="id" />{/if}</div>
            <label>On delete</label><select bind:value={current.onDelete} disabled={current.dropped}>{#each FK_ACTIONS as a}<option>{a}</option>{/each}</select>
            <label>On update</label><select bind:value={current.onUpdate} disabled={current.dropped}>{#each FK_ACTIONS as a}<option>{a}</option>{/each}</select>
          </div>
        {/if}
      </div>
    </div>
    <div class="ptitle">Preview</div>
    <pre class="preview mono">{@html highlightSQL(sql)}</pre>
    <div class="actions">
      <span class="msg" class:err={!!err}>{err || msg}</span>
      <button on:click={() => dispatch('console', sql)} disabled={!sql || sql.startsWith('--')}>Open in console</button>
      <button on:click={() => dispatch('close')} disabled={busy}>Cancel</button>
      <button class="primary" on:click={run} disabled={busy || !sql || sql.startsWith('--')}>OK</button>
    </div>
    {:else}<p class="err pad">{err}</p>{/if}
  </div>
</div>

<style>
  .backdrop { position: fixed; inset: 0; background: rgba(0,0,0,.55); display: grid; place-items: center; z-index: 950; }
  .modal { background: var(--bg2); border: 1px solid var(--line); width: 980px; max-width: 96vw; height: 88vh; display: flex; flex-direction: column; }
  .title { display: flex; align-items: center; gap: 10px; padding: 8px 12px; border-bottom: 1px solid var(--line); }
  .spacer { flex: 1; } .small { font-size: 12px; } .pad { padding: 12px; }
  .body { display: grid; grid-template-columns: 380px 1fr; flex: 1; min-height: 0; }
  .left { border-right: 1px solid var(--line); display: flex; flex-direction: column; min-height: 0; }
  .tools { display: flex; gap: 2px; padding: 4px 6px; border-bottom: 1px solid var(--line); align-items: center; }
  .tools .sep { width: 1px; height: 16px; background: var(--line); margin: 0 4px; }
  .tools .small { font-size: 12px; }
  .tree { overflow: auto; flex: 1; font-size: 12.5px; }
  .node { display: flex; align-items: center; gap: 5px; height: 23px; padding: 0 8px; white-space: nowrap; cursor: default; user-select: none; }
  .node:hover { background: var(--hover); } .node.on { background: var(--sel); }
  .node.folder { padding-left: 14px; } .node.leaf { padding-left: 44px; }
  .node.dropped { text-decoration: line-through; color: var(--err); } .node.added .mono { color: var(--ok); }
  .chev { display: inline-flex; width: 14px; transition: transform .12s; color: var(--fg2); } .chev.open { transform: rotate(90deg); }
  .ic { display: inline-flex; color: var(--fg2); } .ic.pk { color: #e7b64a; }
  .cnt, .meta { color: var(--fg2); font-size: 11px; margin-left: 4px; overflow: hidden; text-overflow: ellipsis; }
  .right { overflow: auto; padding: 14px 16px; }
  .form { display: grid; grid-template-columns: 120px 1fr; gap: 8px 12px; align-items: center; }
  .form > label { margin: 0; text-align: right; color: var(--fg); font-size: 12.5px; }
  .form input, .form select { padding: 4px 8px; font-size: 12.5px; }
  .checks { display: flex; flex-direction: column; gap: 4px; }
  .inline { display: flex; flex-wrap: wrap; gap: 6px 18px; align-items: center; }
  .ai { width: 120px; }
  .check { display: flex; align-items: center; gap: 6px; margin: 0; font-size: 12.5px; color: var(--fg); text-align: left; }
  .check input { width: auto; }
  .cols { display: flex; flex-wrap: wrap; gap: 4px 14px; }
  .ptitle { padding: 4px 12px; font-size: 12px; font-weight: 600; color: var(--fg2); border-top: 1px solid var(--line); background: var(--bg2); }
  .preview { margin: 0; height: 150px; overflow: auto; background: var(--bg); border-top: 1px solid var(--line); padding: 8px 12px; font-size: 12px; white-space: pre-wrap; user-select: text; }
  .actions { display: flex; gap: 8px; align-items: center; padding: 8px 12px; border-top: 1px solid var(--line); }
  .msg { flex: 1; font-size: 12px; color: var(--fg2); overflow: hidden; text-overflow: ellipsis; } .msg.err { color: var(--err); }
</style>

<script>
  import { createEventDispatcher, tick } from 'svelte'
  import ContextMenu from './ContextMenu.svelte'
  import { icons } from './icons.js'

  export let result = null      // { columns, rows, table, keys, truncated, ms, rowsAffected }
  export let busy = false
  export let toolbar = true     // false: the host (TableView) provides its own toolbar
  export let sortable = false   // header click dispatches 'sort'
  export let sort = null        // { col, dir: 'asc'|'desc' } shown in the header
  export let filterable = false // header funnel dispatches 'filter' { col, x, y }
  export let filters = {}       // { col: { op, value } } marks filtered headers
  export let changeCount = 0    // readable via bind:changeCount
  export let hasSelection = false
  const dispatch = createEventDispatcher()

  // ---- editing state (reset whenever a new result arrives) ----
  let edits = new Map()         // "r:c" -> value
  let deleted = new Set()       // row indexes
  let inserted = []             // arrays of values for new rows
  let focus = null              // { r, c }
  let anchor = null             // selection anchor { r, c }
  let rowMode = false           // selection covers whole rows
  let editing = null            // { r, c, value }
  let menu = null
  let grid, editInput
  let lastResult = null
  let widths = {}               // column name → px (resizable headers)
  let order = null              // display order of column indexes (drag to reorder)
  let viewer = null             // { r, c, text, editable } cell value viewer
  let detail = null             // row index shown in the row details panel
  let dragCol = null
  $: if (result !== lastResult) { const same = lastResult && result && lastResult.columns.join('|') === result.columns.join('|'); lastResult = result; reset(); if (!same) { widths = {}; order = null } }
  $: visible = order && order.length === cols.length ? order : cols.map((_, i) => i)

  function reset() {
    edits = new Map(); deleted = new Set(); inserted = []
    focus = anchor = editing = null; rowMode = false; viewer = null; detail = null
  }

  // ---- column resize / reorder ----
  function startResize(e, c) {
    e.preventDefault(); e.stopPropagation()
    const th = e.currentTarget.parentElement
    const startX = e.clientX, startW = th.getBoundingClientRect().width
    const move = ev => { widths[cols[c]] = Math.max(40, startW + ev.clientX - startX); widths = widths }
    const up = () => { window.removeEventListener('mousemove', move); window.removeEventListener('mouseup', up) }
    window.addEventListener('mousemove', move); window.addEventListener('mouseup', up)
  }
  function dropCol(target) {
    if (dragCol === null || dragCol === target) { dragCol = null; return }
    const o = [...visible]
    const from = o.indexOf(dragCol), to = o.indexOf(target)
    o.splice(from, 1); o.splice(to, 0, dragCol)
    order = o; dragCol = null
  }
  const autoFit = c => { delete widths[cols[c]]; widths = widths }

  // ---- cell viewer / row details ----
  function openViewer(r, c) {
    const v = value(r, c)
    let text = v === null || v === undefined ? '' : fmt(v)
    try { const j = JSON.parse(text); if (typeof j === 'object' && j !== null) text = JSON.stringify(j, null, 2) } catch {}
    viewer = { r, c, text, isNull: v === null || v === undefined, editable: editable && !deleted.has(r) }
  }
  function viewerApply() { if (viewer) { setCell(viewer.r, viewer.c, viewer.isNull ? null : viewer.text); viewer = null; grid?.focus() } }

  // ---- copy helpers ----
  const sqlLit = v => v === null || v === undefined ? 'NULL' : typeof v === 'number' || typeof v === 'boolean' ? String(v) : `'${String(fmt(v)).replaceAll("'", "''")}'`
  function selectedRows() { if (!sel) return []; const out = []; for (let r = sel.r1; r <= sel.r2; r++) if (!deleted.has(r)) out.push(r); return out }
  function copyAsInsert() {
    const rows = selectedRows(); if (!rows.length) return
    const tbl = result.table || 'table_name'
    const text = rows.map(r => `INSERT INTO ${tbl} (${cols.join(', ')}) VALUES (${cols.map((_, c) => sqlLit(value(r, c))).join(', ')});`).join('\n')
    navigator.clipboard.writeText(text)
  }
  function copyAsMarkdown() {
    const rows = selectedRows(); if (!rows.length) return
    const vc = visible
    const esc = v => String(v === null || v === undefined ? '' : fmt(v)).replaceAll('|', '\\|').replace(/\s+/g, ' ')
    const lines = ['| ' + vc.map(c => cols[c]).join(' | ') + ' |', '| ' + vc.map(() => '---').join(' | ') + ' |',
      ...rows.map(r => '| ' + vc.map(c => esc(value(r, c))).join(' | ') + ' |')]
    navigator.clipboard.writeText(lines.join('\n'))
  }
  function copyValue() { if (focus) navigator.clipboard.writeText(fmt(value(focus.r, focus.c)) ?? '') }

  $: cols = result?.columns || []
  $: base = result?.rows || []
  $: total = base.length + inserted.length
  $: editable = !!result?.table
  $: hasKeys = !!result?.keys?.length && result.keys.every(k => cols.includes(k))
  $: changeCount = edits.size + deleted.size + inserted.length
  $: sel = selection(focus, anchor, rowMode, cols.length)
  $: hasSelection = !!sel

  function selection(f, a, rows, ncols) {
    if (!f) return null
    const b = a || f
    return { r1: Math.min(f.r, b.r), r2: Math.max(f.r, b.r), c1: rows ? 0 : Math.min(f.c, b.c), c2: rows ? ncols - 1 : Math.max(f.c, b.c) }
  }
  // Reactive helpers: declared with `$:` so template expressions that call them re-evaluate
  // when the underlying state changes (a plain function would leave stale cells behind).
  const key = (r, c) => `${r}:${c}`
  $: inSel = (r, c) => sel && r >= sel.r1 && r <= sel.r2 && c >= sel.c1 && c <= sel.c2
  $: isNew = r => r >= base.length
  $: original = (r, c) => (r >= base.length ? null : base[r][c])
  $: value = (r, c) => {
    if (r >= base.length) return inserted[r - base.length]?.[c]
    return edits.has(key(r, c)) ? edits.get(key(r, c)) : base[r][c]
  }
  function fmt(v) {
    if (v === null || v === undefined) return null
    if (typeof v === 'object') return JSON.stringify(v)
    return String(v)
  }

  // ---- mutations ----
  function setCell(r, c, v) {
    if (!editable) return
    if (isNew(r)) { inserted[r - base.length][c] = v; inserted = inserted; return }
    const same = (a, b) => (a === null || a === undefined ? null : String(a)) === (b === null || b === undefined ? null : String(b))
    if (same(v, base[r][c])) edits.delete(key(r, c)); else edits.set(key(r, c), v)
    edits = edits
  }
  export function addRow() {
    if (!editable) return
    inserted = [...inserted, cols.map(() => null)]
    focus = anchor = { r: total, c: 0 }; rowMode = false
    tick().then(() => startEdit(focus.r, 0))
  }
  export function deleteSelectedRows() {
    if (!editable || !sel) return
    for (let r = sel.r1; r <= sel.r2; r++) {
      if (isNew(r)) continue
      deleted.add(r)
      for (let c = 0; c < cols.length; c++) edits.delete(key(r, c))
    }
    // drop new rows inside the selection (from the end so indexes stay valid)
    for (let r = sel.r2; r >= sel.r1; r--) if (isNew(r)) inserted.splice(r - base.length, 1)
    deleted = deleted; edits = edits; inserted = inserted
    if (focus && focus.r >= total) focus = anchor = total ? { r: total - 1, c: focus.c } : null
  }
  function undeleteSelectedRows() {
    if (!sel) return
    for (let r = sel.r1; r <= sel.r2; r++) deleted.delete(r)
    deleted = deleted
  }
  function revertSelection() {
    if (!sel) return
    for (let r = sel.r1; r <= sel.r2; r++) { for (let c = sel.c1; c <= sel.c2; c++) edits.delete(key(r, c)); deleted.delete(r) }
    edits = edits; deleted = deleted
  }
  function setNull() {
    if (!sel) return
    for (let r = sel.r1; r <= sel.r2; r++) for (let c = sel.c1; c <= sel.c2; c++) setCell(r, c, null)
  }
  export function revertAll() { reset() }

  function changes() {
    const keyOf = r => {
      const k = {}
      for (const name of hasKeys ? result.keys : cols) k[name] = original(r, cols.indexOf(name))
      return k
    }
    const out = []
    const touched = new Set()
    for (const k of edits.keys()) touched.add(Number(k.split(':')[0]))
    for (const r of touched) {
      if (deleted.has(r)) continue
      const values = {}
      for (let c = 0; c < cols.length; c++) if (edits.has(key(r, c))) values[cols[c]] = edits.get(key(r, c))
      out.push({ op: 'update', key: keyOf(r), values })
    }
    for (const r of deleted) out.push({ op: 'delete', key: keyOf(r) })
    for (const row of inserted) {
      const values = {}
      row.forEach((v, c) => { if (v !== null && v !== undefined) values[cols[c]] = v })
      if (Object.keys(values).length) out.push({ op: 'insert', values })
    }
    return out
  }
  export function submit() {
    if (!changeCount || busy) return
    dispatch('submit', { table: result.table, changes: changes(), count: changeCount })
  }

  // ---- editing ----
  async function startEdit(r, c, initial) {
    if (!editable || deleted.has(r)) return
    const v = value(r, c)
    editing = { r, c, value: initial !== undefined ? initial : (v === null || v === undefined ? '' : fmt(v)), wasNull: v === null || v === undefined }
    await tick(); editInput?.focus(); if (initial === undefined) editInput?.select()
  }
  function commitEdit(move) {
    if (!editing) return
    const { r, c, value: v, wasNull } = editing
    editing = null
    setCell(r, c, v === '' && wasNull ? null : v)
    grid?.focus()
    if (move === 'down') moveFocus(1, 0); else if (move === 'right') moveFocus(0, 1)
  }
  function cancelEdit() { editing = null; grid?.focus() }
  function moveFocus(dr, dc, extend = false) {
    if (!focus) { focus = anchor = { r: 0, c: 0 }; return }
    const r = Math.min(Math.max(focus.r + dr, 0), Math.max(total - 1, 0))
    const c = Math.min(Math.max(focus.c + dc, 0), Math.max(cols.length - 1, 0))
    focus = { r, c }; if (!extend) { anchor = focus; rowMode = false }
    tick().then(() => grid?.querySelector('td.focus')?.scrollIntoView({ block: 'nearest', inline: 'nearest' }))
  }

  // ---- mouse ----
  function clickCell(e, r, c) {
    if (editing) commitEdit()
    if (e.shiftKey && anchor) { focus = { r, c }; rowMode = false }
    else { focus = anchor = { r, c }; rowMode = false }
    grid?.focus()
  }
  function clickRow(e, r) {
    if (editing) commitEdit()
    if (e.shiftKey && anchor) focus = { r, c: cols.length - 1 }
    else { anchor = { r, c: 0 }; focus = { r, c: cols.length - 1 } }
    rowMode = true
    grid?.focus()
  }
  function contextMenu(e, r, c) {
    e.preventDefault()
    if (!inSel(r, c)) { focus = anchor = { r, c }; rowMode = false }
    const rowsSel = sel ? sel.r2 - sel.r1 + 1 : 0
    const anyDeleted = sel && [...Array(rowsSel)].some((_, i) => deleted.has(sel.r1 + i))
    const x = e.clientX, y = e.clientY
    menu = { x, y, items: [
      { label: 'Edit cell', hint: 'F2', action: () => startEdit(r, c), disabled: !editable },
      { label: 'View / edit value…', hint: 'Shift+Enter', action: () => openViewer(r, c) },
      { label: 'Row details…', action: () => detail = r },
      { label: 'Set NULL', action: setNull, disabled: !editable },
      { sep: true },
      { label: 'Copy', hint: 'Ctrl+C', action: () => document.execCommand('copy') },
      { label: 'Copy as INSERT', action: copyAsInsert },
      { label: 'Copy as Markdown table', action: copyAsMarkdown },
      { label: 'Export result…', action: () => dispatch('exportMenu', { x, y: y + 4 }) },
      { label: 'Paste', hint: 'Ctrl+V', action: () => navigator.clipboard?.readText?.().then(t => pasteText(t)).catch(() => {}), disabled: !editable },
      { sep: true },
      { label: 'Add row', hint: 'Alt+Ins', action: addRow, disabled: !editable },
      { label: `Delete ${rowsSel > 1 ? rowsSel + ' rows' : 'row'}`, hint: 'Ctrl+Y', danger: true, action: deleteSelectedRows, disabled: !editable },
      ...(anyDeleted ? [{ label: 'Undelete row(s)', action: undeleteSelectedRows }] : []),
      { label: 'Revert selection', action: revertSelection, disabled: !changeCount },
      { sep: true },
      { label: 'Submit changes', hint: 'Ctrl+Enter', action: submit, disabled: !changeCount },
    ]}
  }

  // ---- keyboard ----
  function keydown(e) {
    if (viewer || detail !== null) { if (e.key === 'Escape') { e.preventDefault(); viewer = null; detail = null } ; return }
    if (editing) {
      if (e.key === 'Enter') { e.preventDefault(); commitEdit('down') }
      else if (e.key === 'Tab') { e.preventDefault(); commitEdit('right') }
      else if (e.key === 'Escape') { e.preventDefault(); cancelEdit() }
      return
    }
    if (e.ctrlKey && e.key === 'Enter') { e.preventDefault(); submit(); return }
    if (e.ctrlKey && (e.key === 'y' || e.key === 'Y')) { e.preventDefault(); deleteSelectedRows(); return }
    if (e.altKey && e.key === 'Insert') { e.preventDefault(); addRow(); return }
    if (e.ctrlKey && e.key === 'a') { e.preventDefault(); anchor = { r: 0, c: 0 }; focus = { r: total - 1, c: cols.length - 1 }; rowMode = true; return }
    if (e.ctrlKey || e.metaKey) return   // copy/paste arrive as events
    if (!focus) { if (e.key.startsWith('Arrow')) { e.preventDefault(); moveFocus(0, 0) } ; return }
    switch (e.key) {
      case 'ArrowUp': e.preventDefault(); moveFocus(-1, 0, e.shiftKey); break
      case 'ArrowDown': e.preventDefault(); moveFocus(1, 0, e.shiftKey); break
      case 'ArrowLeft': e.preventDefault(); moveFocus(0, -1, e.shiftKey); break
      case 'ArrowRight': e.preventDefault(); moveFocus(0, 1, e.shiftKey); break
      case 'Tab': e.preventDefault(); moveFocus(0, e.shiftKey ? -1 : 1); break
      case 'Home': e.preventDefault(); focus = { r: focus.r, c: 0 }; if (!e.shiftKey) anchor = focus; break
      case 'End': e.preventDefault(); focus = { r: focus.r, c: cols.length - 1 }; if (!e.shiftKey) anchor = focus; break
      case 'PageDown': e.preventDefault(); moveFocus(20, 0, e.shiftKey); break
      case 'PageUp': e.preventDefault(); moveFocus(-20, 0, e.shiftKey); break
      case 'Enter': case 'F2': e.preventDefault(); if (e.shiftKey) openViewer(focus.r, focus.c); else startEdit(focus.r, focus.c); break
      case 'Escape': e.preventDefault(); anchor = focus; rowMode = false; break
      case 'Delete': case 'Backspace':
        e.preventDefault()
        if (rowMode) deleteSelectedRows(); else setNull()
        break
      default:
        if (e.key.length === 1 && !e.altKey) { e.preventDefault(); startEdit(focus.r, focus.c, e.key) }
    }
  }
  function onCopy(e) {
    if (editing || !sel) return
    e.preventDefault()
    const lines = []
    for (let r = sel.r1; r <= sel.r2; r++) {
      const cells = []
      for (let c = sel.c1; c <= sel.c2; c++) { const v = value(r, c); cells.push(v === null || v === undefined ? '' : fmt(v)) }
      lines.push(cells.join('\t'))
    }
    e.clipboardData.setData('text/plain', lines.join('\n'))
  }
  function onPaste(e) {
    if (editing || !focus || !editable) return
    e.preventDefault()
    pasteText(e.clipboardData.getData('text/plain'))
  }
  function pasteText(text) {
    if (!focus || !text) return
    const rows = text.replace(/\r/g, '').replace(/\n$/, '').split('\n').map(l => l.split('\t'))
    // single value pasted onto a multi-cell selection fills the selection
    if (rows.length === 1 && rows[0].length === 1 && sel && (sel.r1 !== sel.r2 || sel.c1 !== sel.c2)) {
      for (let r = sel.r1; r <= sel.r2; r++) for (let c = sel.c1; c <= sel.c2; c++) setCell(r, c, rows[0][0])
      return
    }
    rows.forEach((line, i) => line.forEach((v, j) => {
      const r = focus.r + i, c = focus.c + j
      if (c >= cols.length) return
      while (r >= total) { inserted = [...inserted, cols.map(() => null)] }
      setCell(r, c, v)
    }))
    anchor = focus; focus = { r: Math.min(focus.r + rows.length - 1, total - 1), c: Math.min(focus.c + rows[0].length - 1, cols.length - 1) }
  }

  export function copyCSV() {
    if (!result) return
    const esc = s => `"${String(s ?? '').replaceAll('"', '""')}"`
    const lines = [cols.map(esc).join(',')]
    for (let r = 0; r < total; r++) if (!deleted.has(r)) lines.push(cols.map((_, c) => esc(value(r, c))).join(','))
    navigator.clipboard.writeText(lines.join('\n'))
  }
</script>

<div class="wrap">
  {#if result && cols.length}
    {#if toolbar}
    <div class="tools">
      <button class="icon" title="Add row (Alt+Insert)" disabled={!editable} on:click={addRow}>{@html icons.plus}</button>
      <button class="icon" title="Delete selected rows (Ctrl+Y)" disabled={!editable || !sel} on:click={deleteSelectedRows}>{@html icons.x}</button>
      <span class="sep"></span>
      <button class="submit" class:primary={changeCount > 0} disabled={!changeCount || busy} on:click={submit} title="Submit changes (Ctrl+Enter)">
        {@html icons.import} Submit{#if changeCount}<span class="badge">{changeCount}</span>{/if}
      </button>
      <button class="ghost" disabled={!changeCount} on:click={revertAll}>Revert</button>
      <span class="sep"></span>
      <button class="ghost" on:click={copyCSV}>Copy as CSV</button>
      <button class="ghost" on:click={e => dispatch('exportMenu', { x: e.clientX, y: e.clientY })} title="Export the full result set to a file">Export ▾</button>
      <span class="spacer"></span>
      {#if editable}
        <span class="muted small mono">{result.table}</span>
        {#if !hasKeys}<span class="warn small" title="No primary key in the result: rows are matched on all columns">no key</span>{/if}
      {:else}
        <span class="muted small" title="Only single-table SELECTs are editable">read-only</span>
      {/if}
    </div>
    {/if}
    <div class="scroll" bind:this={grid} tabindex="0" role="grid" on:keydown={keydown} on:copy={onCopy} on:paste={onPaste}>
      <table class="mono">
        <thead><tr><th class="n"></th>{#each visible as ci (ci)}{@const c = cols[ci]}
          <th class:key={result.keys?.includes(c)} class:sortable class:sorted={sort?.col === c} class:filtered={!!filters[c]} class:dragover={dragCol !== null && dragCol !== ci}
              style={widths[c] ? `width:${widths[c]}px; min-width:${widths[c]}px; max-width:${widths[c]}px` : ''}
              draggable="true" on:dragstart={() => dragCol = ci} on:dragover|preventDefault on:drop|preventDefault={() => dropCol(ci)} on:dragend={() => dragCol = null}
              on:click={() => sortable && dispatch('sort', c)} title={(sortable ? 'Click to sort · ' : '') + 'drag to reorder · drag the edge to resize'}>
            <span class="hname">{c}</span>{#if sort?.col === c}<span class="arrow">{sort.dir === 'desc' ? '▼' : '▲'}</span>{/if}
            {#if filterable}
              <button class="flt" class:on={!!filters[c]} title={filters[c] ? 'Edit filter' : 'Filter'} on:click|stopPropagation={e => { const r = e.currentTarget.getBoundingClientRect(); dispatch('filter', { col: c, x: r.left, y: r.bottom + 2 }) }}>{@html filters[c] ? icons.filterOn : icons.filter}</button>
            {/if}
            <span class="rz" on:mousedown={e => startResize(e, ci)} on:dblclick|stopPropagation={() => autoFit(ci)} on:click|stopPropagation></span>
          </th>{/each}</tr></thead>
        <tbody>
          {#each Array(total) as _, r}
            <tr class:deleted={deleted.has(r)} class:inserted={isNew(r)} class:rowsel={rowMode && sel && r >= sel.r1 && r <= sel.r2}>
              <td class="n" on:mousedown|preventDefault={e => clickRow(e, r)} on:contextmenu={e => contextMenu(e, r, focus?.c ?? 0)}>{isNew(r) ? '+' : r + 1}</td>
              {#each visible as c (c)}
                {@const v = fmt(value(r, c))}
                {@const changed = !isNew(r) && edits.has(key(r, c))}
                <td class:null={v === null} class:focus={focus?.r === r && focus?.c === c} class:sel={inSel(r, c)} class:changed
                    style={widths[cols[c]] ? `max-width:${widths[cols[c]]}px` : ''}
                    title={changed ? `was: ${fmt(original(r, c)) ?? 'NULL'}` : (v ?? 'NULL')}
                    on:mousedown={e => { if (e.button === 0) clickCell(e, r, c) }}
                    on:dblclick={() => startEdit(r, c)}
                    on:contextmenu={e => contextMenu(e, r, c)}>
                  {#if editing && editing.r === r && editing.c === c}
                    <input class="edit mono" bind:this={editInput} bind:value={editing.value} on:blur={() => commitEdit()} />
                  {:else}
                    {v ?? 'NULL'}
                  {/if}
                </td>
              {/each}
            </tr>
          {/each}
        </tbody>
      </table>
    </div>
  {:else if result}
    <p class="muted empty">Statement ran. {result.rowsAffected} rows affected.</p>
  {:else}
    <p class="muted empty">Results appear here. Run a query or click a table.</p>
  {/if}
</div>

{#if menu}<ContextMenu {menu} close={() => menu = null} />{/if}

{#if viewer}
  <div class="backdrop" on:mousedown|self={() => viewer = null} role="presentation">
    <div class="modal" role="dialog">
      <div class="mhead"><b class="mono">{cols[viewer.c]}</b><span class="muted small">row {viewer.r + 1}{viewer.editable ? '' : ' · read-only'}</span><span class="spacer"></span>
        <label class="check small"><input type="checkbox" bind:checked={viewer.isNull} disabled={!viewer.editable} /> NULL</label></div>
      <textarea class="mono" rows="16" bind:value={viewer.text} readonly={!viewer.editable || viewer.isNull} spellcheck="false"></textarea>
      <div class="mfoot">
        <span class="muted small">{viewer.text.length} chars · Esc closes</span><span class="spacer"></span>
        <button class="ghost" on:click={() => navigator.clipboard.writeText(viewer.text)}>Copy</button>
        <button on:click={() => viewer = null}>Close</button>
        {#if viewer.editable}<button class="primary" on:click={viewerApply}>Set value</button>{/if}
      </div>
    </div>
  </div>
{/if}
{#if detail !== null}
  <div class="backdrop" on:mousedown|self={() => detail = null} role="presentation">
    <div class="modal" role="dialog">
      <div class="mhead"><b>Row {detail + 1}</b>{#if result.table}<span class="muted small mono">{result.table}</span>{/if}<span class="spacer"></span></div>
      <div class="details">
        {#each cols as c, ci}
          <div class="dk mono" class:pk={result.keys?.includes(c)}>{c}</div>
          <div class="dv mono" class:null={value(detail, ci) === null || value(detail, ci) === undefined} on:dblclick={() => { const r = detail; detail = null; openViewer(r, ci) }} title="Double-click to view / edit">{fmt(value(detail, ci)) ?? 'NULL'}</div>
        {/each}
      </div>
      <div class="mfoot"><span class="spacer"></span><button class="ghost" on:click={() => { const r = detail; anchor = { r, c: 0 }; focus = { r, c: cols.length - 1 }; rowMode = true; copyAsMarkdown() }}>Copy as Markdown</button><button on:click={() => detail = null}>Close</button></div>
    </div>
  </div>
{/if}

<style>
  .wrap { display: flex; flex-direction: column; min-height: 0; }
  .tools { display: flex; align-items: center; gap: 4px; padding: 2px 8px; border-bottom: 1px solid var(--line); background: var(--bg2); }
  .tools .sep { width: 1px; height: 16px; background: var(--line); margin: 0 4px; }
  .spacer { flex: 1; }
  .small { font-size: 11px; }
  .warn { color: #e7b64a; border: 1px solid #e7b64a; border-radius: 10px; padding: 0 6px; }
  .submit { display: inline-flex; align-items: center; gap: 5px; padding: 2px 8px; }
  .badge { background: rgba(255,255,255,.2); border-radius: 8px; padding: 0 6px; font-size: 11px; }
  .scroll { overflow: auto; flex: 1; outline: none; }
  .scroll:focus-visible { outline: none; }
  table { border-collapse: collapse; font-size: 12px; }
  th, td { padding: 3px 10px; border-right: 1px solid var(--line); border-bottom: 1px solid var(--line); white-space: nowrap; max-width: 420px; overflow: hidden; text-overflow: ellipsis; height: 24px; }
  th { position: sticky; top: 0; background: var(--bg3); text-align: left; font-weight: 600; z-index: 1; }
  th.key::before { content: '🔑 '; font-size: 9px; }
  th.sortable { cursor: pointer; }
  th.sortable:hover { color: var(--acc2); }
  th.sorted { color: var(--acc2); }
  th.filtered .hname { color: #e7b64a; }
  .flt { display: inline-flex; background: none; border: 0; padding: 0 2px; margin-left: 4px; color: var(--fg2); opacity: 0; vertical-align: middle; border-radius: 0; }
  th:hover .flt, .flt.on { opacity: 1; }
  .flt.on { color: #e7b64a; }
  .flt:hover { background: var(--bg3); color: var(--fg); }
  .arrow { font-size: 9px; margin-left: 4px; }
  td.n, th.n { color: var(--fg2); background: var(--bg2); text-align: right; position: sticky; left: 0; cursor: pointer; user-select: none; z-index: 1; }
  th.n { z-index: 2; }
  td.null { color: var(--fg2); font-style: italic; }
  td { cursor: default; user-select: none; }
  td.sel { background: rgba(53,116,240,.22); }
  td.focus { outline: 2px solid var(--acc); outline-offset: -2px; }
  td.changed { background: rgba(231,182,74,.22); }
  td.changed.sel { background: rgba(231,182,74,.4); }
  tr.deleted td { text-decoration: line-through; color: var(--err); background: rgba(240,122,122,.12); }
  tr.inserted td { background: rgba(95,184,101,.12); }
  tr.rowsel td.n { background: var(--sel); color: var(--fg); }
  tr:hover td:not(.sel):not(.changed) { background: var(--hover); }
  .rz { position: absolute; right: -3px; top: 0; bottom: 0; width: 7px; cursor: col-resize; z-index: 2; }
  .rz:hover { background: var(--acc); opacity: .6; }
  th { position: sticky; }
  th.dragover { outline: 1px dashed var(--acc2); outline-offset: -2px; }
  .backdrop { position: fixed; inset: 0; background: rgba(0,0,0,.5); display: grid; place-items: center; z-index: 960; }
  .modal { background: var(--bg2); border: 1px solid var(--line); width: 720px; max-width: 94vw; max-height: 90vh; display: flex; flex-direction: column; padding: 12px 14px; }
  .mhead, .mfoot { display: flex; align-items: center; gap: 10px; }
  .mfoot { margin-top: 10px; }
  .modal textarea { margin-top: 8px; width: 100%; font-size: 12px; background: var(--bg); color: var(--fg); border: 1px solid var(--line); padding: 6px 8px; resize: vertical; }
  .small { font-size: 11px; }
  .spacer { flex: 1; }
  .check { display: flex; align-items: center; gap: 5px; margin: 0; }
  .check input { width: auto; }
  .details { display: grid; grid-template-columns: max-content 1fr; gap: 2px 14px; margin-top: 8px; overflow: auto; max-height: 60vh; font-size: 12px; }
  .dk { color: var(--fg2); text-align: right; padding: 2px 0; }
  .dk.pk::before { content: '🔑 '; font-size: 9px; }
  .dv { padding: 2px 6px; white-space: pre-wrap; word-break: break-all; max-height: 120px; overflow: auto; border-bottom: 1px solid var(--line); }
  .dv.null { color: var(--fg2); font-style: italic; }
  .edit { width: 100%; min-width: 120px; padding: 1px 4px; margin: -2px 0; height: 20px; font-size: 12px; border-color: var(--acc); background: var(--bg); }
  .empty { padding: 20px; }
</style>

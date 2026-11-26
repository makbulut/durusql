<script>
  import { onMount, onDestroy, createEventDispatcher } from 'svelte'
  import { EditorView, basicSetup } from 'codemirror'
  import { keymap, Decoration, gutter, GutterMarker } from '@codemirror/view'
  import { Prec, Compartment, StateField, StateEffect, RangeSet } from '@codemirror/state'
  import { status } from './icons.js'
  import { statements, statementAt, subqueriesAt } from './sqlsplit.js'
  import { sql as sqlLang, MySQL, PostgreSQL, MariaSQL } from '@codemirror/lang-sql'
  import { oneDark } from '@codemirror/theme-one-dark'

  export let value = ''
  export let active = true      // re-measure when a hidden tab becomes visible
  export let schema = null      // SQLNamespace: { db: { table: [column completions] } }
  export let defaultSchema = '' // database whose tables complete without a prefix
  export let dialect = 'mysql'  // mysql | postgres
  const dispatch = createEventDispatcher()
  let host, view
  const langConf = new Compartment()

  // highlighted range: the chooser's hovered candidate, or a short flash of what just ran
  const setHl = StateEffect.define()
  const hlField = StateField.define({
    create: () => Decoration.none,
    update(d, tr) {
      for (const e of tr.effects) if (e.is(setHl)) return e.value ? Decoration.set([Decoration.mark({ class: 'cm-runhl' }).range(e.value.from, e.value.to)]) : Decoration.none
      return d.map(tr.changes)
    },
    provide: f => EditorView.decorations.from(f),
  })
  // run-status gutter: spinner / tick / error next to the line number of each executed statement
  class StatusMarker extends GutterMarker {
    constructor(kind) { super(); this.kind = kind }
    eq(o) { return o.kind === this.kind }
    toDOM() { const s = document.createElement('span'); s.className = 'cm-runst ' + this.kind; s.innerHTML = status[this.kind]; return s }
  }
  const markers = { run: new StatusMarker('run'), ok: new StatusMarker('ok'), err: new StatusMarker('err') }
  const setSt = StateEffect.define()   // { pos, kind } or null = clear all
  const stField = StateField.define({
    create: () => RangeSet.empty,
    update(set, tr) {
      set = set.map(tr.changes)
      for (const e of tr.effects) if (e.is(setSt)) {
        if (!e.value) { set = RangeSet.empty; continue }
        const { pos, kind } = e.value
        const at = tr.state.doc.lineAt(Math.min(pos, tr.state.doc.length)).from
        set = set.update({ filter: from => from !== at, add: [markers[kind].range(at)] })
      }
      return set
    },
  })
  const stGutter = gutter({ class: 'cm-runGutter', markers: v => v.state.field(stField) })

  // statements of the last run: the backend reports them by index and text; find each in the run range
  let runFrom = 0, runTo = 0, runCursor = 0, stepPos = []
  const isBlankOrComment = l => /^\s*($|--|#|\/\*.*\*\/\s*$)/.test(l)
  function locate(sql) {
    const text = view.state.sliceDoc(runFrom, runTo), q = sql.trim()
    let i = q ? text.indexOf(q, runCursor) : -1
    if (i < 0) { const s = statements(text.slice(runCursor), dialect)[0]; if (!s) return -1; i = runCursor + s.from; runCursor = runCursor + s.to; return runFrom + i }
    runCursor = i + q.length
    // skip leading comment lines (the backend keeps them with the statement)
    let p = i
    for (const l of q.split('\n')) { if (!isBlankOrComment(l)) break; p += l.length + 1 }
    return runFrom + Math.min(p, i + q.length)
  }
  // called by the parent for each step of a run: { idx, sql?, kind: 'run'|'ok'|'err' }
  export function markStep(idx, kind, sql) {
    if (!view) return
    if (stepPos[idx] === undefined) { if (sql == null) return; stepPos[idx] = locate(sql) }
    if (stepPos[idx] < 0) return
    view.dispatch({ effects: setSt.of({ pos: stepPos[idx], kind }) })
  }
  function beginRun(range) { runFrom = range.from; runTo = range.to; runCursor = 0; stepPos = []; view.dispatch({ effects: setSt.of(null) }) }

  let flashTimer
  const highlight = range => { clearTimeout(flashTimer); view?.dispatch({ effects: setHl.of(range && range.to > range.from ? range : null) }) }
  function runRange(range) {
    if (!range) return
    highlight(range)
    beginRun(range)
    flashTimer = setTimeout(() => highlight(null), 500)
    dispatch('run', view.state.sliceDoc(range.from, range.to))
  }
  const doc = () => view.state.doc.toString()
  const allRange = () => ({ from: 0, to: view.state.doc.length })
  const label = (range, n = 70) => view.state.sliceDoc(range.from, range.to).replace(/\s+/g, ' ').trim().slice(0, n)

  // what Ctrl+Enter / the context menu can run at the cursor: subqueries (innermost first), the statement,
  // and the whole script when it has more than one statement
  function candidates(pos) {
    const text = doc(), stmt = statementAt(text, pos, dialect)
    if (!stmt) return []
    const list = [...subqueriesAt(text, stmt, pos, dialect).map(r => ({ ...r, kind: 'subquery' })), { ...stmt, kind: 'statement' }]
    if (statements(text, dialect).length > 1) list.push({ ...allRange(), kind: 'script' })
    return list
  }
  export function selectionText() {
    const sel = view?.state.selection.main
    return sel && !sel.empty ? view.state.sliceDoc(sel.from, sel.to) : ''
  }
  // Ctrl+Enter: the selection; else the statement at the cursor, or a chooser when the cursor is in a subquery
  export function runAtCursor() {
    const sel = view.state.selection.main
    if (!sel.empty) return runRange(sel)
    const list = candidates(sel.head)
    if (!list.length) return
    if (!list.some(c => c.kind === 'subquery')) return runRange(list[0])
    const at = view.coordsAtPos(sel.head) || { left: 100, bottom: 100 }
    chooser = { list, sel: 0, x: at.left, y: at.bottom + 4 }
    highlight(list[0])
  }
  export const runAll = () => runRange(allRange())

  // ---- statement chooser (DataGrip "Statements" popup) ----
  let chooser = null, chooserEl
  function choose(i) { const c = chooser.list[i]; chooser = null; runRange(c); view.focus() }
  function closeChooser() { if (!chooser) return; chooser = null; highlight(null); view.focus() }
  function chooserKeys(e) {
    if (!chooser) return
    const n = chooser.list.length
    if (e.key === 'ArrowDown' || e.key === 'ArrowUp') { chooser.sel = (chooser.sel + (e.key === 'ArrowDown' ? 1 : n - 1)) % n; highlight(chooser.list[chooser.sel]) }
    else if (e.key === 'Enter') choose(chooser.sel)
    else if (e.key === 'Escape') closeChooser()
    else return
    e.preventDefault(); e.stopPropagation()
  }
  $: if (chooserEl) { const r = chooserEl.getBoundingClientRect(); if (r.right > innerWidth) chooserEl.style.left = Math.max(0, innerWidth - r.width - 8) + 'px'; if (r.bottom > innerHeight) chooserEl.style.top = Math.max(0, chooser.y - r.height - 24) + 'px' }

  // right-click: keep a selection that was clicked into, otherwise move the cursor to the click first
  function contextMenu(e) {
    e.preventDefault()
    const pos = view.posAtCoords({ x: e.clientX, y: e.clientY })
    const sel = view.state.selection.main
    if (pos != null && (sel.empty || pos < sel.from || pos > sel.to)) view.dispatch({ selection: { anchor: pos } })
    const cur = view.state.selection.main
    const list = cur.empty ? candidates(cur.head) : []
    dispatch('menu', {
      x: e.clientX, y: e.clientY,
      selection: cur.empty ? '' : label(cur),
      runSelection: () => runRange(cur),
      targets: list.map(c => ({ kind: c.kind, label: label(c, 50), run: () => runRange(c) })),
      runAll,
      cut: () => { navigator.clipboard.writeText(view.state.sliceDoc(cur.from, cur.to)); view.dispatch({ changes: { from: cur.from, to: cur.to } }); view.focus() },
      copy: () => navigator.clipboard.writeText(view.state.sliceDoc(cur.from, cur.to)),
      paste: () => navigator.clipboard.readText().then(t => { view.dispatch(view.state.replaceSelection(t)); view.focus() }).catch(() => {}),
      selectAll: () => { view.dispatch({ selection: { anchor: 0, head: view.state.doc.length } }); view.focus() },
    })
  }

  const langExt = () => sqlLang({
    dialect: dialect === 'postgres' ? PostgreSQL : MariaSQL,
    schema: schema || undefined,
    defaultSchema: defaultSchema || undefined,
    upperCaseKeywords: true,
  })

  onMount(() => {
    view = new EditorView({
      doc: value,
      parent: host,
      extensions: [
        basicSetup,
        langConf.of(langExt()),
        oneDark,
        // Prec.highest: basicSetup binds Mod-Enter to "insert blank line", which would otherwise win.
        Prec.highest(keymap.of([
          { key: 'Ctrl-Enter', mac: 'Cmd-Enter', run: () => { runAtCursor(); return true } },
          { key: 'Ctrl-Shift-Enter', mac: 'Cmd-Shift-Enter', run: () => { runAll(); return true } },
        ])),
        hlField,
        stField,
        stGutter,
        EditorView.domEventHandlers({ contextmenu: e => { contextMenu(e); return true } }),
        EditorView.updateListener.of(u => { if (u.docChanged) value = u.state.doc.toString() }),
        EditorView.theme({ '&': { height: '100%', fontSize: '13px' }, '.cm-scroller': { fontFamily: 'var(--mono)' } })
      ]
    })
  })
  onDestroy(() => view?.destroy())

  $: if (active && view) { view.requestMeasure(); }

  // schema / dialect changed (connection bound, database expanded, columns loaded): reconfigure completion
  $: if (view && (schema || defaultSchema || dialect)) view.dispatch({ effects: langConf.reconfigure(langExt()) })

  // external updates (loading a saved query, table click)
  $: if (view && value !== view.state.doc.toString()) {
    view.dispatch({ changes: { from: 0, to: view.state.doc.length, insert: value } })
  }
</script>

<svelte:window on:keydown|capture={chooserKeys} on:mousedown={e => chooser && !chooserEl?.contains(e.target) && closeChooser()} />

<div class="editor" bind:this={host}></div>
{#if chooser}
  <div class="chooser" bind:this={chooserEl} style="left:{chooser.x}px; top:{chooser.y}px" role="listbox" aria-label="Statements">
    <div class="ch-title">Statements</div>
    {#each chooser.list as c, i}
      <div class="ch-opt mono" class:on={i === chooser.sel} role="option" tabindex="-1" aria-selected={i === chooser.sel}
           on:mousemove={() => { if (chooser.sel !== i) { chooser.sel = i; highlight(c) } }} on:mousedown|preventDefault={() => choose(i)}>
        <span class="ch-text">{label(c)}</span><span class="ch-kind">{c.kind === 'script' ? 'all' : c.kind}</span>
      </div>
    {/each}
  </div>
{/if}

<style>
  .editor { min-height: 160px; overflow: hidden; }
  .editor :global(.cm-editor) { height: 100%; }
  .editor :global(.cm-tooltip-autocomplete) { font-family: var(--mono); font-size: 12px; }
  .editor :global(.cm-runGutter) { width: 18px; }
  .editor :global(.cm-runGutter .cm-gutterElement) { display: flex; align-items: center; justify-content: center; }
  .editor :global(.cm-runst) { display: inline-flex; }
  .editor :global(.cm-runst .spin) { animation: spin 1s linear infinite; transform-origin: 50% 50%; }
  .editor :global(.cm-runhl) { background: rgba(122, 162, 247, .22); }
  .chooser { position: fixed; z-index: 1000; min-width: 260px; max-width: 520px; padding: 4px; background: var(--bg3); border: 1px solid var(--line); box-shadow: 0 8px 24px rgba(0,0,0,.45); }
  .ch-title { text-align: center; font-weight: 600; font-size: 12px; padding: 4px 0 6px; }
  .ch-opt { display: flex; gap: 12px; padding: 4px 8px; font-size: 12px; cursor: default; white-space: nowrap; }
  .ch-opt.on { background: var(--sel); }
  .ch-text { flex: 1; overflow: hidden; text-overflow: ellipsis; }
  .ch-kind { color: var(--fg2); font-size: 11px; }
  .editor :global(.cm-completionDetail) { color: var(--fg2); font-style: normal; margin-left: 8px; }
</style>

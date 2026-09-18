<script>
  import { onMount, onDestroy, createEventDispatcher } from 'svelte'
  import { EditorView, basicSetup } from 'codemirror'
  import { keymap } from '@codemirror/view'
  import { Prec, Compartment } from '@codemirror/state'
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
        Prec.highest(keymap.of([{
          key: 'Ctrl-Enter', mac: 'Cmd-Enter',
          run: v => {
            const sel = v.state.selection.main
            const text = sel.empty ? v.state.doc.toString() : v.state.sliceDoc(sel.from, sel.to)
            dispatch('run', text)
            return true
          }
        }])),
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

<div class="editor" bind:this={host}></div>

<style>
  .editor { min-height: 160px; overflow: hidden; }
  .editor :global(.cm-editor) { height: 100%; }
  .editor :global(.cm-tooltip-autocomplete) { font-family: var(--mono); font-size: 12px; }
  .editor :global(.cm-completionDetail) { color: var(--fg2); font-style: normal; margin-left: 8px; }
</style>

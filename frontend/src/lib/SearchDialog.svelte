<script>
  // "Search everywhere" for the explorer: connections by name (local), and tables / views /
  // routines / columns of every connected server (server-side, information_schema).
  import { createEventDispatcher, onMount, tick } from 'svelte'
  import * as api from './api.js'
  import { icons } from './icons.js'
  export let connections = [], state = {}
  const dispatch = createEventDispatcher()
  let term = '', input, listEl, hits = [], busy = false, cursor = 0, note = ''
  let timer, seq = 0
  onMount(() => input?.focus())
  $: connected = connections.filter(c => state[c.id]?.connected)
  $: q = term.trim().toLowerCase()
  $: connHits = q ? connections.filter(c => c.name.toLowerCase().includes(q)).map(c => ({ kind: 'connection', conn: c, name: c.name })) : []
  $: all = [...connHits, ...hits]
  $: if (cursor >= all.length) cursor = Math.max(0, all.length - 1)

  function onInput() {
    clearTimeout(timer)
    const t = term.trim()
    if (t.length < 2) { hits = []; note = t ? 'type at least 2 characters' : ''; return }
    timer = setTimeout(() => search(t), 220)
  }
  async function search(t) {
    const my = ++seq
    busy = true; note = connected.length ? '' : 'no connected connections to search; connect one in the explorer'
    const results = await Promise.all(connected.map(async c => {
      try { return (await api.searchObjects(c.id, t, 60)).map(h => ({ ...h, conn: c })) } catch (e) { return [{ kind: 'error', conn: c, name: String(e) }] }
    }))
    if (my !== seq) return
    const flat = results.flat()
    // rank: exact name first, then prefix, then contains; tables before columns
    const rank = h => (h.name.toLowerCase() === t.toLowerCase() ? 0 : h.name.toLowerCase().startsWith(t.toLowerCase()) ? 1 : 2) * 10 + ({ table: 0, view: 1, routine: 2, column: 3, error: 9 }[h.kind] ?? 5)
    hits = flat.sort((a, b) => rank(a) - rank(b) || a.name.localeCompare(b.name)).slice(0, 300)
    busy = false; cursor = 0
    if (!hits.length && connected.length) note = 'nothing found'
  }
  function pick(h, alt = false) {
    if (!h || h.kind === 'error') return
    dispatch('pick', { hit: h, alt })
  }
  function key(e) {
    if (e.key === 'ArrowDown') { e.preventDefault(); cursor = Math.min(cursor + 1, all.length - 1); scrollTo() }
    else if (e.key === 'ArrowUp') { e.preventDefault(); cursor = Math.max(cursor - 1, 0); scrollTo() }
    else if (e.key === 'Enter') { e.preventDefault(); pick(all[cursor], e.shiftKey) }
    else if (e.key === 'Escape') { e.preventDefault(); dispatch('close') }
  }
  async function scrollTo() { await tick(); listEl?.querySelector('.row.on')?.scrollIntoView({ block: 'nearest' }) }
  const ic = { connection: icons.plug, table: icons.table, view: icons.view, routine: icons.routine, column: icons.column, error: icons.err }
  const full = h => h.kind === 'connection' ? h.name : h.kind === 'column' ? `${h.schema}.${h.table}.${h.name}` : `${h.schema}.${h.name}`
  function mark(text) {
    if (!q) return text
    const i = text.toLowerCase().indexOf(q)
    if (i < 0) return text
    const esc = s => s.replace(/&/g, '&amp;').replace(/</g, '&lt;')
    return esc(text.slice(0, i)) + '<b>' + esc(text.slice(i, i + q.length)) + '</b>' + esc(text.slice(i + q.length))
  }
</script>

<div class="backdrop" on:mousedown|self={() => dispatch('close')} role="presentation">
  <div class="modal" role="dialog" aria-label="Search everywhere">
    <div class="inp">
      <span class="ic">{@html icons.search}</span>
      <input bind:this={input} bind:value={term} on:input={onInput} on:keydown={key} placeholder="Search tables, views, routines, columns and connections…" spellcheck="false" />
      {#if busy}<span class="muted small">searching…</span>{/if}
    </div>
    <div class="hint muted small">Searches {connected.length} connected connection{connected.length === 1 ? '' : 's'} · Enter opens (table data / console) · Shift+Enter selects in the explorer · Esc closes</div>
    <div class="list" bind:this={listEl}>
      {#if note}<p class="muted pad">{note}</p>{/if}
      {#each all as h, i (h.kind + (h.conn?.id || '') + full(h))}
        <div class="row" class:on={i === cursor} class:bad={h.kind === 'error'} on:mousemove={() => cursor = i} on:click={() => pick(h)} on:dblclick={() => pick(h)}>
          <span class="ic" class:tbl={h.kind === 'table' || h.kind === 'view'}>{@html ic[h.kind]}</span>
          <span class="name mono">{@html mark(full(h))}</span>
          {#if h.detail}<span class="detail mono">{h.detail}</span>{/if}
          <span class="kind">{h.kind}</span>
          <span class="conn">[{h.conn?.name || ''}]</span>
        </div>
      {/each}
    </div>
  </div>
</div>

<style>
  .backdrop { position: fixed; inset: 0; background: rgba(0,0,0,.45); display: flex; justify-content: center; align-items: flex-start; padding-top: 80px; z-index: 970; }
  .modal { background: var(--bg2); border: 1px solid var(--line); width: 760px; max-width: 94vw; max-height: 70vh; display: flex; flex-direction: column; box-shadow: 0 12px 32px rgba(0,0,0,.5); }
  .inp { display: flex; align-items: center; gap: 8px; padding: 8px 12px; border-bottom: 1px solid var(--line); }
  .inp input { font-size: 14px; padding: 6px 8px; border: 0; background: transparent; }
  .inp input:focus { outline: none; }
  .ic { display: inline-flex; color: var(--fg2); flex: none; } .ic.tbl { color: #7aa2f7; }
  .hint { padding: 4px 12px; border-bottom: 1px solid var(--line); }
  .small { font-size: 11px; }
  .list { overflow: auto; flex: 1; }
  .row { display: flex; align-items: center; gap: 8px; padding: 4px 12px; white-space: nowrap; cursor: default; font-size: 12.5px; }
  .row.on { background: var(--sel); }
  .row.bad { color: var(--err); }
  .name { flex: 1; overflow: hidden; text-overflow: ellipsis; }
  .name :global(b) { color: var(--acc2); font-weight: 600; }
  .detail, .kind, .conn { color: var(--fg2); font-size: 11px; }
  .pad { padding: 10px 12px; }
</style>

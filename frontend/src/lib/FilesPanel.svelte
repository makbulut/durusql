<script>
  // Right-side "Files" panel: saved queries per connection (plain .sql files on disk).
  import { createEventDispatcher } from 'svelte'
  import { icons } from './icons.js'
  export let connections = []
  export let files = {}        // { connId: [{ name, sql }] }
  export let activeId = null
  export let openName = ''     // queryName of the active tab, highlighted
  const dispatch = createEventDispatcher()
  let collapsed = {}
  let filter = ''
  $: q = filter.trim().toLowerCase()
  $: list = connections
    .map(c => ({ c, qs: (files[c.id] || []).filter(f => !q || f.name.toLowerCase().includes(q) || c.name.toLowerCase().includes(q)) }))
    .filter(x => x.qs.length || (!q && (files[x.c.id] || []).length))   // only connections that have saved queries
  const ctx = (e, detail) => { e.preventDefault(); dispatch('menu', { x: e.clientX, y: e.clientY, ...detail }) }
</script>

<section class="files">
  <div class="head">
    <span class="title">Files</span>
    <span class="tools">
      <button class="icon" title="New query for the active connection" disabled={!activeId} on:click={() => dispatch('new', { id: activeId })}>{@html icons.plus}</button>
      <button class="icon" title="Refresh" on:click={() => dispatch('refresh')}>{@html icons.refresh}</button>
      <button class="icon" title="Hide panel" on:click={() => dispatch('hide')}>{@html icons.x}</button>
    </span>
  </div>
  <div class="filter"><input placeholder="Filter queries…" bind:value={filter} /></div>
  <div class="tree">
    {#if !list.length}<p class="muted pad">No saved queries yet. Press Ctrl+S in a console, or use <b>+</b> to create one for the active connection.</p>{/if}
    {#each list as { c, qs } (c.id)}
      <div class="node conn" on:click={() => collapsed[c.id] = !collapsed[c.id]} on:contextmenu={e => ctx(e, { conn: c })}>
        <span class="chev" class:open={!collapsed[c.id]}>{@html icons.chevron}</span>
        <span class="ic" style="color:{c.color || 'var(--fg2)'}">{@html icons.folder}</span>
        <span class="name">{c.name}</span><span class="count">{qs.length}</span>
      </div>
      {#if !collapsed[c.id]}
        {#each qs as f (f.name)}
          <div class="node file" class:active={c.id === activeId && f.name === openName} title={f.sql.split('\n').slice(0, 6).join('\n')}
               on:click={() => dispatch('open', { id: c.id, query: f })} on:contextmenu={e => ctx(e, { conn: c, query: f })}>
            <span class="ic">{@html icons.query}</span>
            <span class="name mono">{f.name}.sql</span>
            <span class="sub">[{c.name}]</span>
          </div>
        {/each}
      {/if}
    {/each}
  </div>
</section>

<style>
  .files { display: flex; flex-direction: column; min-height: 0; height: 100%; background: var(--bg2); border-left: 1px solid var(--line); }
  .head { display: flex; align-items: center; justify-content: space-between; padding: 6px 8px 4px 10px; }
  .title { font-weight: 600; font-size: 12.5px; }
  .tools { display: flex; gap: 2px; }
  .filter { padding: 0 8px 6px; }
  .filter input { padding: 3px 8px; font-size: 12px; }
  .tree { overflow: auto; flex: 1; padding-bottom: 12px; }
  .node { display: flex; align-items: center; gap: 4px; height: 23px; padding: 0 8px 0 4px; cursor: default; user-select: none; white-space: nowrap; }
  .node:hover { background: var(--hover); }
  .node.active { background: var(--sel); }
  .file { padding-left: 24px; }
  .pad { padding: 10px; white-space: normal; }
  .chev { display: inline-flex; width: 16px; height: 16px; align-items: center; justify-content: center; color: var(--fg2); transition: transform .12s; flex: none; }
  .chev.open { transform: rotate(90deg); }
  .ic { display: inline-flex; color: var(--fg2); flex: none; }
  .name { overflow: hidden; text-overflow: ellipsis; }
  .conn .name { font-weight: 500; }
  .count { color: var(--fg2); font-size: 11px; margin-left: 4px; }
  .sub { margin-left: auto; color: var(--fg2); font-size: 10.5px; opacity: .7; }
</style>

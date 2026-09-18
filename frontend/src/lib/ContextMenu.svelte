<script>
  import { onMount } from 'svelte'
  // menu = { x, y, items: [{ label, action, danger, disabled } | { sep: true }] }
  export let menu = null
  export let close = () => {}
  let el
  onMount(() => {
    // keep the menu inside the window
    const r = el.getBoundingClientRect()
    if (r.right > innerWidth) el.style.left = Math.max(0, innerWidth - r.width - 4) + 'px'
    if (r.bottom > innerHeight) el.style.top = Math.max(0, innerHeight - r.height - 4) + 'px'
  })
  function pick(item) {
    if (item.disabled) return
    close(); item.action?.()
  }
</script>

<svelte:window on:mousedown={e => { if (!el?.contains(e.target)) close() }} on:keydown={e => e.key === 'Escape' && close()} on:blur={close} />

<div class="menu" bind:this={el} style="left:{menu.x}px; top:{menu.y}px" role="menu">
  {#each menu.items as item}
    {#if item.sep}
      <div class="sep"></div>
    {:else}
      <button role="menuitem" class:danger={item.danger} disabled={item.disabled} on:click={() => pick(item)}>
        <span class="lbl">{item.label}</span>{#if item.hint}<span class="hint">{item.hint}</span>{/if}
      </button>
    {/if}
  {/each}
</div>

<style>
  .menu { position: fixed; z-index: 1000; min-width: 190px; padding: 4px; background: var(--bg3); border: 1px solid var(--line); border-radius: 0; box-shadow: 0 8px 24px rgba(0,0,0,.45); }
  button { display: flex; width: 100%; gap: 12px; align-items: center; text-align: left; background: transparent; border: 0; border-radius: 0; padding: 5px 10px; color: var(--fg); }
  button:hover:not(:disabled) { background: var(--sel); }
  button:disabled { color: var(--fg2); cursor: default; }
  button.danger:hover:not(:disabled) { background: #5a2a2a; color: #ffb3b3; }
  .lbl { flex: 1; }
  .hint { color: var(--fg2); font-size: 11px; }
  .sep { height: 1px; background: var(--line); margin: 4px 6px; }
</style>

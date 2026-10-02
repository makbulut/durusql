<script>
  // single-line SQL fragment input (WHERE / ORDER BY) with column-name completion:
  // suggestions pop up while typing a word, Ctrl+Space forces them, ↑/↓ pick, Enter/Tab accept, Esc closes
  import { createEventDispatcher, tick } from 'svelte'
  export let value = ''
  export let columns = []    // [{ name, type? }]
  export let keywords = []   // offered after the columns
  export let placeholder = ''
  const dispatch = createEventDispatcher()
  let input, items = [], sel = 0, open = false, left = 0, word = { start: 0, end: 0 }

  // the identifier around the caret, unless the caret is inside a string literal
  function wordAt() {
    const pos = input.selectionStart ?? value.length
    const before = value.slice(0, pos)
    if ((before.match(/'/g) || []).length % 2) return null
    const start = pos - (before.match(/[\w$]*$/)[0].length)
    const end = pos + (value.slice(pos).match(/^[\w$]*/)[0].length)
    return { start, end, prefix: value.slice(start, pos) }
  }
  let measure
  function caretX(i) {
    measure ||= document.createElement('canvas').getContext('2d')
    const cs = getComputedStyle(input)
    measure.font = `${cs.fontSize} ${cs.fontFamily}`
    return Math.min(input.clientWidth - 40, parseFloat(cs.paddingLeft) + measure.measureText(value.slice(0, i)).width - input.scrollLeft)
  }
  function suggest(force = false) {
    const w = wordAt()
    if (!w || (!w.prefix && !force)) return close()
    const p = w.prefix.toLowerCase()
    const score = s => { const l = s.toLowerCase(); return l.startsWith(p) ? 0 : l.includes(p) ? 1 : -1 }
    const cols = columns.map(c => ({ ...c, s: score(c.name) })).filter(c => c.s >= 0 && c.name.toLowerCase() !== p)
    const kws = keywords.filter(k => p && k.toLowerCase().startsWith(p) && k.toLowerCase() !== p).map(k => ({ name: k, kw: true, s: 2 }))
    items = [...cols.sort((a, b) => a.s - b.s), ...kws].slice(0, 50)
    if (!items.length) return close()
    word = w; sel = 0; open = true; left = caretX(w.start)
  }
  function close() { open = false; items = [] }
  async function accept(it) {
    value = value.slice(0, word.start) + it.name + value.slice(word.end)
    close()
    await tick()
    const pos = word.start + it.name.length
    input.focus(); input.setSelectionRange(pos, pos)
  }
  function keydown(e) {
    if (e.key === ' ' && e.ctrlKey) { e.preventDefault(); suggest(true); return }
    if (open) {
      if (e.key === 'ArrowDown') { e.preventDefault(); sel = (sel + 1) % items.length; return }
      if (e.key === 'ArrowUp') { e.preventDefault(); sel = (sel - 1 + items.length) % items.length; return }
      if (e.key === 'Enter' || e.key === 'Tab') { e.preventDefault(); accept(items[sel]); return }
      if (e.key === 'Escape') { e.preventDefault(); e.stopPropagation(); close(); return }
    }
    if (e.key === 'Enter') dispatch('enter')
  }
</script>

<span class="wrap">
  <input class="mono" bind:this={input} bind:value {placeholder} spellcheck="false" autocomplete="off"
         on:keydown={keydown} on:input={() => suggest()} on:blur={close} on:click={close} />
  {#if open}
    <div class="pop" style="left:{left}px" role="listbox">
      {#each items as it, i}
        <!-- mousedown so the input keeps focus -->
        <div class="opt mono" class:on={i === sel} role="option" tabindex="-1" aria-selected={i === sel}
             on:mousedown|preventDefault={() => accept(it)} on:mousemove={() => sel = i}>
          <span class="n" class:kw={it.kw}>{it.name}</span>{#if it.type}<span class="t">{it.type}</span>{/if}
        </div>
      {/each}
    </div>
  {/if}
</span>

<style>
  .wrap { position: relative; flex: 1; display: flex; min-width: 0; }
  input { flex: 1; min-width: 0; padding: 2px 8px; font-size: 12px; }
  .pop { position: absolute; top: 100%; margin-top: 2px; z-index: 50; min-width: 200px; max-width: 420px; max-height: 260px; overflow: auto;
         background: var(--bg3); border: 1px solid var(--line); box-shadow: 0 8px 24px rgba(0,0,0,.45); padding: 2px 0; }
  .opt { display: flex; gap: 12px; padding: 2px 8px; font-size: 12px; cursor: default; white-space: nowrap; }
  .opt.on { background: var(--sel); }
  .n { flex: 1; overflow: hidden; text-overflow: ellipsis; }
  .n.kw { color: var(--fg2); }
  .t { color: var(--fg2); font-size: 11px; }
</style>

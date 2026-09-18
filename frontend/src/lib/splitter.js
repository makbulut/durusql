// Svelte action: drag to resize. `get` returns the current size, `set` receives the new one.
// `invert` flips the direction for panes anchored to the right/bottom edge.
export function splitter(node, opts) {
  let o = opts
  function down(e) {
    if (e.button !== 0) return
    e.preventDefault()
    const axis = o.axis === 'y' ? 'clientY' : 'clientX'
    const start = e[axis]
    const base = o.get()
    const sign = o.invert ? -1 : 1
    document.body.style.cursor = o.axis === 'y' ? 'row-resize' : 'col-resize'
    document.body.style.userSelect = 'none'
    node.classList.add('dragging')
    const move = ev => o.set(base + sign * (ev[axis] - start))
    const up = () => {
      window.removeEventListener('mousemove', move)
      window.removeEventListener('mouseup', up)
      document.body.style.cursor = ''
      document.body.style.userSelect = ''
      node.classList.remove('dragging')
      o.done?.()
    }
    window.addEventListener('mousemove', move)
    window.addEventListener('mouseup', up)
  }
  node.addEventListener('mousedown', down)
  return { update(n) { o = n }, destroy() { node.removeEventListener('mousedown', down) } }
}

export const clamp = (v, min, max) => Math.min(max, Math.max(min, v))

export function remember(key, fallback) {
  try { const v = Number(localStorage.getItem(key)); return v > 0 ? v : fallback } catch { return fallback }
}
export function persist(key, v) { try { localStorage.setItem(key, String(v)) } catch {} }

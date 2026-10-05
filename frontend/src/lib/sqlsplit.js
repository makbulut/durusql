// Statement ranges in an SQL script, mirroring internal/db/script.go SplitStatements:
// ';' outside strings, quoted identifiers and comments, MySQL DELIMITER, PostgreSQL $tag$ quotes.

// end index (exclusive) of the string / comment / dollar quote starting at i, or -1 when none starts there
function skip(text, i, mysql) {
  const c = text[i], n = text.length
  if (c === "'" || c === '"' || c === '`') {
    for (let j = i + 1; j < n; j++) {
      if (text[j] === '\\' && c !== '`' && mysql) { j++; continue }
      if (text[j] === c) { if (text[j + 1] === c) { j++; continue } return j + 1 }
    }
    return n
  }
  if ((c === '-' && text[i + 1] === '-') || (c === '#' && mysql)) { const e = text.indexOf('\n', i); return e < 0 ? n : e }
  if (c === '/' && text[i + 1] === '*') { const e = text.indexOf('*/', i + 2); return e < 0 ? n : e + 2 }
  if (c === '$' && !mysql) {
    const m = /^\$[A-Za-z_]?\w*\$/.exec(text.slice(i, i + 64))
    if (m) { const e = text.indexOf(m[0], i + m[0].length); return e < 0 ? n : e + m[0].length }
  }
  return -1
}

// [{ from, to }] of each non-empty statement (trimmed, without its delimiter)
export function statements(text, dialect = 'mysql') {
  const mysql = dialect !== 'postgres', out = [], n = text.length
  let start = 0, delim = ';', lineStart = true
  const push = end => {
    let a = start, b = end
    // leading whitespace and comments belong to no statement
    for (;;) {
      while (a < b && /\s/.test(text[a])) a++
      const isComment = text.startsWith('--', a) || text.startsWith('/*', a) || (mysql && text[a] === '#')
      if (!isComment || a >= b) break
      a = Math.min(skip(text, a, mysql), b)
    }
    while (b > a && /\s/.test(text[b - 1])) b--
    if (b > a) out.push({ from: a, to: b })
  }
  for (let i = 0; i < n;) {
    if (lineStart && mysql && /^delimiter[ \t]/i.test(text.slice(i, i + 10))) {
      push(i)
      let k = text.indexOf('\n', i); if (k < 0) k = n
      delim = text.slice(i + 10, k).trim() || ';'
      start = i = k
      continue
    }
    lineStart = text[i] === '\n'
    const e = skip(text, i, mysql)
    if (e > i) { i = e; continue }
    if (text.startsWith(delim, i)) { push(i); i += delim.length; start = i; continue }
    i++
  }
  push(n)
  return out
}

const lineOf = (text, pos) => { let l = 0; for (let i = 0; i < pos; i++) if (text[i] === '\n') l++; return l }

// the statement at the cursor; between statements the nearest one by line (the earlier one on a tie)
export function statementAt(text, pos, dialect = 'mysql') {
  const list = statements(text, dialect)
  const hit = list.find(s => pos >= s.from && pos <= s.to)
  if (hit || !list.length) return hit || null
  const line = lineOf(text, pos)
  let best = null, bestD = Infinity
  for (const s of list) {
    const d = pos < s.from ? lineOf(text, s.from) - line : line - lineOf(text, s.to)
    if (d < bestD) { best = s; bestD = d }
  }
  return best
}

// parenthesised SELECT / WITH queries inside [from, to) that contain pos, innermost first
export function subqueriesAt(text, stmt, pos, dialect = 'mysql') {
  const mysql = dialect !== 'postgres', stack = [], out = []
  for (let i = stmt.from; i < stmt.to;) {
    const e = skip(text, i, mysql)
    if (e > i) { i = e; continue }
    if (text[i] === '(') stack.push(i)
    else if (text[i] === ')' && stack.length) {
      const open = stack.pop()
      let a = open + 1, b = i
      while (a < b && /\s/.test(text[a])) a++
      while (b > a && /\s/.test(text[b - 1])) b--
      if (pos > open && pos <= i && /^(select|with)\b/i.test(text.slice(a, a + 7))) out.push({ from: a, to: b })
    }
    i++
  }
  return out.sort((x, y) => (x.to - x.from) - (y.to - y.from))
}

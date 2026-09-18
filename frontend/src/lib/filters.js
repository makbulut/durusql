// Column filters for the table data view. A filter is { op, value }; filters are ANDed together
// and with the free-text WHERE. The same code builds the SQL the view shows and the app runs.
export const OPS = [
  { id: 'eq',       label: '=',            needsValue: true },
  { id: 'ne',       label: '≠',            needsValue: true },
  { id: 'contains', label: 'contains',     needsValue: true },
  { id: 'starts',   label: 'starts with',  needsValue: true },
  { id: 'ends',     label: 'ends with',    needsValue: true },
  { id: 'gt',       label: '>',            needsValue: true },
  { id: 'ge',       label: '≥',            needsValue: true },
  { id: 'lt',       label: '<',            needsValue: true },
  { id: 'le',       label: '≤',            needsValue: true },
  { id: 'in',       label: 'in (a, b, c)', needsValue: true },
  { id: 'null',     label: 'is NULL',      needsValue: false },
  { id: 'notnull',  label: 'is not NULL',  needsValue: false },
  { id: 'empty',    label: 'is empty',     needsValue: false },
]
export const opLabel = id => OPS.find(o => o.id === id)?.label || id

const ident = (c, driver) => driver === 'postgres' ? `"${c.replaceAll('"', '""')}"` : '`' + c.replaceAll('`', '``') + '`'
const lit = v => `'${String(v).replaceAll("'", "''")}'`
const likeEsc = v => String(v).replace(/[\\%_]/g, m => '\\' + m)
const text = (c, driver) => driver === 'postgres' ? `CAST(${c} AS TEXT)` : c

export function filterSQL(col, f, driver) {
  const c = ident(col, driver)
  const v = f.value ?? ''
  switch (f.op) {
    case 'eq': return `${c} = ${lit(v)}`
    case 'ne': return `${c} <> ${lit(v)}`
    case 'contains': return `${text(c, driver)} LIKE ${lit('%' + likeEsc(v) + '%')}`
    case 'starts': return `${text(c, driver)} LIKE ${lit(likeEsc(v) + '%')}`
    case 'ends': return `${text(c, driver)} LIKE ${lit('%' + likeEsc(v))}`
    case 'gt': return `${c} > ${lit(v)}`
    case 'ge': return `${c} >= ${lit(v)}`
    case 'lt': return `${c} < ${lit(v)}`
    case 'le': return `${c} <= ${lit(v)}`
    case 'in': return `${c} IN (${String(v).split(',').map(s => lit(s.trim())).filter(s => s !== "''").join(', ') || 'NULL'})`
    case 'null': return `${c} IS NULL`
    case 'notnull': return `${c} IS NOT NULL`
    case 'empty': return `(${c} IS NULL OR ${text(c, driver)} = '')`
  }
  return ''
}

export function composeWhere(where, filters, driver) {
  const parts = []
  if (where?.trim()) parts.push(Object.keys(filters || {}).length ? `(${where.trim()})` : where.trim())
  for (const [col, f] of Object.entries(filters || {})) {
    const s = filterSQL(col, f, driver)
    if (s) parts.push(s)
  }
  return parts.join(' AND ')
}

// probe=true asks for one row more than the page so the backend can tell whether a next page
// exists (it stops at pageSize rows and flags the result as truncated).
export function tableSQL(t, driver, probe = false) {
  let q = `SELECT * FROM ${t.table}`
  const w = composeWhere(t.where, t.filters, driver)
  if (w) q += ` WHERE ${w}`
  if (t.orderBy?.trim()) q += ` ORDER BY ${t.orderBy.trim()}`
  q += ` LIMIT ${(t.pageSize || 500) + (probe ? 1 : 0)}`
  if (t.page) q += ` OFFSET ${t.page * (t.pageSize || 500)}`
  return q
}

// the same query without paging, for exports
export function tableSQLAll(t, driver) {
  let q = `SELECT * FROM ${t.table}`
  const w = composeWhere(t.where, t.filters, driver)
  if (w) q += ` WHERE ${w}`
  if (t.orderBy?.trim()) q += ` ORDER BY ${t.orderBy.trim()}`
  return q
}

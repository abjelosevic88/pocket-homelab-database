// What the pages share about databases: engines, status, cells, SQL words.
import type { Cell, Connection, Engine } from './types'
import { ago, fmtBytes } from './format'

export const ENGINES: { id: Engine; name: string; short: string; color: string; port: number; hint: string }[] = [
  { id: 'postgres', name: 'PostgreSQL', short: 'PG', color: '#336791', port: 5432, hint: 'Also TimescaleDB, PostGIS, Supabase, Immich' },
  { id: 'mysql', name: 'MySQL / MariaDB', short: 'My', color: '#c26a1b', port: 3306, hint: 'Also Percona' },
  { id: 'sqlite', name: 'SQLite', short: 'SQ', color: '#0f80cc', port: 0, hint: 'A database in one file' },
  { id: 'redis', name: 'Redis / Valkey', short: 'R', color: '#d82c20', port: 6379, hint: 'Also KeyDB, Dragonfly' },
]
export const engineOf = (e: string) => ENGINES.find(x => x.id === e) || { id: e as Engine, name: e, short: e.slice(0, 2).toUpperCase(), color: '#7a7a85', port: 0, hint: '' }
export const isSQL = (e: string) => e === 'postgres' || e === 'mysql' || e === 'sqlite'

export type Status = 'up' | 'down' | 'paused' | 'unknown'
export function statusOf(c: Connection): Status {
  if (c.paused || c.status === 'paused') return 'paused'
  if (c.status === 'up') return 'up'
  if (c.status === 'down') return 'down'
  return 'unknown'
}
export const STATUS_LABEL: Record<Status, string> = { up: 'Up', down: 'Down', paused: 'Paused for changes', unknown: 'Not checked yet' }

/** "PostgreSQL 16.4 · 3.2 GB" */
export function connLine(c: Connection): string {
  const parts = [c.version ? (c.engine === 'sqlite' ? `SQLite ${c.version}` : c.version.match(/^\d/) ? `${engineOf(c.engine).name.split(' ')[0]} ${c.version}` : c.version) : engineOf(c.engine).name]
  if (c.size_bytes >= 0) parts.push(fmtBytes(c.size_bytes))
  return parts.join(' · ')
}
export function statusLine(c: Connection): string {
  const s = statusOf(c)
  if (s === 'down') return `Down${c.down_since ? ' since ' + ago(c.down_since) : ''}${c.status_detail ? ': ' + c.status_detail : ''}`
  if (s === 'paused') return c.status_detail || 'Paused for changes'
  if (s === 'up') return `Up · checked ${ago(c.checked_at)}${c.latency_ms ? ` · ${c.latency_ms} ms` : ''}`
  return c.monitor ? 'Not checked yet' : 'Not watched'
}

// ---- cells ----

export const isNull = (v: Cell) => v === null
export function isBin(v: Cell): v is { $bin: string; size: number } { return typeof v === 'object' && v !== null && '$bin' in v }
export function isLong(v: Cell): v is { $trunc: string; size: number } { return typeof v === 'object' && v !== null && '$trunc' in v }

/** A cell as one line of text for the grid. */
export function cellText(v: Cell, nullText = 'NULL'): string {
  if (v === null) return nullText
  if (isBin(v)) return `binary · ${fmtBytes(v.size)}`
  if (isLong(v)) return v.$trunc
  if (typeof v === 'boolean') return v ? 'true' : 'false'
  return String(v)
}
/** A cell as the text an edit box starts with ("" for NULL). */
export function cellEdit(v: Cell): string {
  if (v === null) return ''
  if (isLong(v)) return v.$trunc
  if (isBin(v)) return ''
  return String(v)
}
export const isNumeric = (t: string) => /int|serial|numeric|decimal|real|double|float|money|number/i.test(t)

/** Pretty JSON when a text is JSON, else null. */
export function asJSON(s: string): string | null {
  const t = s.trim()
  if (!(t.startsWith('{') || t.startsWith('['))) return null
  try { return JSON.stringify(JSON.parse(t), null, 2) } catch { return null }
}

// ---- SQL ----

export const SQL_WORDS = ['SELECT', 'FROM', 'WHERE', 'AND', 'OR', 'NOT', 'NULL', 'IS', 'IN', 'LIKE', 'ILIKE', 'BETWEEN', 'EXISTS', 'AS', 'ON', 'JOIN', 'LEFT', 'RIGHT', 'INNER', 'OUTER', 'FULL', 'CROSS',
  'GROUP', 'BY', 'ORDER', 'HAVING', 'LIMIT', 'OFFSET', 'DISTINCT', 'UNION', 'ALL', 'INSERT', 'INTO', 'VALUES', 'UPDATE', 'SET', 'DELETE', 'CREATE', 'TABLE', 'VIEW', 'INDEX', 'DROP',
  'ALTER', 'ADD', 'COLUMN', 'PRIMARY', 'KEY', 'FOREIGN', 'REFERENCES', 'DEFAULT', 'UNIQUE', 'CHECK', 'CASE', 'WHEN', 'THEN', 'ELSE', 'END', 'WITH', 'RETURNING', 'ASC', 'DESC',
  'COUNT', 'SUM', 'AVG', 'MIN', 'MAX', 'COALESCE', 'CAST', 'TRUE', 'FALSE', 'EXPLAIN', 'ANALYZE', 'PRAGMA', 'SHOW', 'TRUNCATE', 'BEGIN', 'COMMIT', 'ROLLBACK', 'OVER', 'PARTITION',
  'NULLS', 'FIRST', 'LAST', 'INTERVAL', 'NOW', 'CURRENT_DATE', 'CURRENT_TIMESTAMP', 'LATERAL', 'USING', 'IF', 'REPLACE', 'CONFLICT', 'DO', 'NOTHING', 'ROUND', 'LOWER', 'UPPER']
const WORDS = new Set(SQL_WORDS)

/** Splits SQL into coloured tokens for the editor's highlight layer. */
export function highlight(sql: string): { t: string; c: string }[] {
  const out: { t: string; c: string }[] = []
  const re = /(--[^\n]*|\/\*[\s\S]*?(?:\*\/|$))|('(?:[^']|'')*'?)|("(?:[^"]|"")*"?|`[^`]*`?)|(\b\d+(?:\.\d+)?\b)|([A-Za-z_][A-Za-z0-9_]*)|(\s+)|(.)/g
  let m: RegExpExecArray | null
  while ((m = re.exec(sql))) {
    if (m[1]) out.push({ t: m[1], c: 'tk-com' })
    else if (m[2]) out.push({ t: m[2], c: 'tk-str' })
    else if (m[3]) out.push({ t: m[3], c: 'tk-id' })
    else if (m[4]) out.push({ t: m[4], c: 'tk-num' })
    else if (m[5]) out.push({ t: m[5], c: WORDS.has(m[5].toUpperCase()) ? 'tk-kw' : '' })
    else out.push({ t: m[0], c: '' })
  }
  return out
}

/** The statement under the cursor (between semicolons, ignoring quoted ones). */
export function statementAt(sql: string, pos: number): string {
  let start = 0, q = ''
  for (let i = 0; i < sql.length; i++) {
    const ch = sql[i]
    if (q) { if (ch === q) q = ''; continue }
    if (ch === "'" || ch === '"' || ch === '`') { q = ch; continue }
    if (ch === '-' && sql[i + 1] === '-') { const nl = sql.indexOf('\n', i); i = nl < 0 ? sql.length : nl; continue }
    if (ch === ';') {
      if (pos <= i) return sql.slice(start, i).trim()
      start = i + 1
    }
  }
  return sql.slice(start).trim() || sql.trim()
}

export const quoteIdent = (engine: string, n: string) => engine === 'mysql' ? '`' + n.replace(/`/g, '``') + '`' : /^[a-z_][a-z0-9_]*$/.test(n) ? n : '"' + n.replace(/"/g, '""') + '"'
export function tableSQL(engine: string, schema: string, name: string): string {
  const t = quoteIdent(engine, name)
  if (engine === 'postgres' && schema && schema !== 'public') return `${quoteIdent(engine, schema)}.${t}`
  return t
}

export const KIND_LABEL: Record<string, string> = { sql: 'SQL', edit: 'Edit', insert: 'New rows', delete: 'Deleted', key: 'Key', pause: 'Paused', resume: 'Started again', snapshot: 'Snapshot', restore: 'Restored' }
export const KIND_ICON: Record<string, string> = { sql: 'code', edit: 'edit', insert: 'plus', delete: 'trash', key: 'key', pause: 'pause', resume: 'play', snapshot: 'archive', restore: 'undo' }

export function fmtTTL(ms: number): string {
  if (ms < 0) return 'never'
  const s = Math.round(ms / 1000)
  if (s < 60) return `${s}s`
  if (s < 3600) return `${Math.round(s / 60)} min`
  if (s < 86400) return `${(s / 3600).toFixed(s < 36000 ? 1 : 0)} h`
  return `${Math.round(s / 86400)} d`
}

/** Rows as tab-separated text (for pasting into a spreadsheet). */
export function toTSV(cols: string[], rows: Cell[][]): string {
  const esc = (s: string) => /[\t\n"]/.test(s) ? '"' + s.replace(/"/g, '""') + '"' : s
  return [cols.map(esc).join('\t'), ...rows.map(r => r.map(c => esc(cellText(c, ''))).join('\t'))].join('\n')
}

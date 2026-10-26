// One table: its rows (sort, filter, search, page, edit, add, delete, export), its structure and its CREATE statement.
import { useEffect, useMemo, useState } from 'react'
import { useNavigate } from 'react-router-dom'
import { api, ApiError, V1 } from '../lib/api'
import { useApp } from '../lib/app-context'
import type { Cell, Filter, Page, TableInfo } from '../lib/types'
import { tableSQL } from '../lib/db'
import { fmtBytes } from '../lib/format'
import { Confirm, Loading, Modal, Popover, Segmented, copyText, rectOf, useAsync, useDebounce, useToast, type Anchor } from '../components/ui'
import { Icon } from '../components/icons'
import { Select } from '../components/Select'
import { Grid, RowSheet, pageLabel, type Edits } from '../components/Grid'
import { useWs } from './Connection'

const OPS: { value: string; label: string; needs: boolean }[] = [
  { value: 'eq', label: 'is', needs: true }, { value: 'ne', label: 'is not', needs: true }, { value: 'contains', label: 'contains', needs: true },
  { value: 'starts', label: 'starts with', needs: true }, { value: 'ends', label: 'ends with', needs: true }, { value: 'gt', label: '>', needs: true },
  { value: 'ge', label: '≥', needs: true }, { value: 'lt', label: '<', needs: true }, { value: 'le', label: '≤', needs: true },
  { value: 'null', label: 'is empty (NULL)', needs: false }, { value: 'notnull', label: 'is not empty', needs: false },
]

type Change = { op: 'insert' | 'update' | 'delete'; key?: Record<string, Cell>; values?: Record<string, string | null> }

export default function TableView({ name, schema }: { name: string; schema: string }) {
  const { conn, db, reloadSafety } = useWs()
  const { settings } = useApp()
  const { toast } = useToast()
  const nav = useNavigate()
  const ref = `db=${encodeURIComponent(db)}&schema=${encodeURIComponent(schema)}&name=${encodeURIComponent(name)}`
  const { data: info, error: infoErr } = useAsync(() => api.get<TableInfo>(`${V1}/connections/${conn.id}/table?${ref}`), [conn.id, ref])
  const [tab, setTab] = useState<'data' | 'structure' | 'ddl'>('data')
  const [sort, setSort] = useState<{ col: string; desc: boolean } | null>(null)
  const [filters, setFilters] = useState<Filter[]>([])
  const [search, setSearch] = useState('')
  const dsearch = useDebounce(search, 350)
  const [offset, setOffset] = useState(0)
  const limit = settings?.page_size || 100
  const [page, setPage] = useState<Page | null>(null)
  const [loading, setLoading] = useState(false)
  const [err, setErr] = useState('')
  const [edits, setEdits] = useState<Edits>({})
  const [selected, setSelected] = useState<Set<number>>(new Set())
  const [sheet, setSheet] = useState<{ row?: number; insert?: boolean } | null>(null)
  const [del, setDel] = useState(false)
  const [filterAt, setFilterAt] = useState<Anchor | null>(null)
  const [gate, setGate] = useState<{ code: string; msg: string } | null>(null)
  const [seq, setSeq] = useState(0)
  const key = useMemo(() => info ? (info.rowid ? ['__rowid'] : info.primary_key) : [], [info])
  const canEdit = !!info && info.kind === 'table' && conn.allow_writes && key.length > 0

  useEffect(() => { setOffset(0) }, [dsearch, filters, sort])
  useEffect(() => {
    if (!info) return
    let off = false
    setLoading(true)
    const p = new URLSearchParams({ db, schema, name, limit: String(limit), offset: String(offset) })
    if (sort) { p.set('sort', sort.col); if (sort.desc) p.set('desc', '1') }
    if (dsearch) p.set('search', dsearch)
    if (filters.length) p.set('filters', JSON.stringify(filters))
    api.get<Page>(`${V1}/connections/${conn.id}/rows?${p}`).then(r => { if (!off) { setPage(r); setErr(''); setEdits({}); setSelected(new Set()) } })
      .catch(e => { if (!off) setErr((e as Error).message) }).finally(() => { if (!off) setLoading(false) })
    return () => { off = true }
  }, [info, conn.id, db, schema, name, limit, offset, sort, dsearch, filters, seq])

  if (infoErr) return <div className="callout danger"><Icon name="alert" size={15} /><span>{infoErr}</span></div>
  if (!info) return <Loading />
  const cols = page?.columns || info.columns.map(c => c.name)
  const rowKey = (r: number): Record<string, Cell> => {
    const out: Record<string, Cell> = {}
    if (!page) return out
    key.forEach(k => { const i = page.columns.indexOf(k); out[k] = i >= 0 ? page.rows[r][i] : null })
    return out
  }
  const apply = async (changes: Change[], msg: string) => {
    try {
      const r = await api.post<{ summary: string }>(`${V1}/connections/${conn.id}/rows`, { db, schema, name, changes })
      toast(msg || r.summary); setSeq(s => s + 1); reloadSafety()
      return true
    } catch (e) {
      const ae = e as ApiError
      if (ae.data?.code) setGate({ code: String(ae.data.code), msg: ae.message })
      else toast(ae.message, 'err')
      return false
    }
  }
  const pendingCount = Object.values(edits).reduce((n, r) => n + Object.keys(r).length, 0)
  const saveEdits = () => apply(Object.entries(edits).map(([r, vals]) => ({ op: 'update' as const, key: rowKey(Number(r)), values: vals })), `Saved ${pendingCount} ${pendingCount === 1 ? 'change' : 'changes'}`)
  const sortBy = (col: string) => setSort(s => s?.col === col ? (s.desc ? null : { col, desc: true }) : { col, desc: false })
  const exportURL = (format: string) => `${V1}/connections/${conn.id}/export?${ref}&format=${format}`
  const sqlFor = () => `SELECT *\nFROM ${tableSQL(conn.engine, info.schema, info.name)}\nLIMIT 100;`
  return (
    <div className="table-view">
      <div className="tv-head">
        <div className="grow min0">
          <h2 className="tv-title ellipsis">{info.name}{info.kind !== 'table' && <span className="badge">{info.kind}</span>}</h2>
          <div className="muted small">{info.rows >= 0 ? `${info.rows.toLocaleString()} rows` : 'rows not counted'}{info.size_bytes > 0 ? ` · ${fmtBytes(info.size_bytes)}` : ''} · {info.columns.length} columns{info.comment ? ` · ${info.comment}` : ''}</div>
        </div>
        <Segmented size="sm" value={tab} onChange={setTab} options={[{ value: 'data', label: 'Data' }, { value: 'structure', label: 'Structure' }, { value: 'ddl', label: 'SQL' }]} />
      </div>
      {tab === 'data' && <>
        <div className="toolbar tv-tools">
          <div className="search-box"><Icon name="search" size={14} /><input value={search} onChange={e => setSearch(e.target.value)} placeholder="Search every column" aria-label="Search rows" /></div>
          <button type="button" className={`btn sm ${filters.length ? 'on' : ''}`} onClick={e => setFilterAt(rectOf(e.currentTarget))}><Icon name="filter" size={14} />Filter{filters.length ? ` · ${filters.length}` : ''}</button>
          <span className="spacer" />
          {canEdit && <button type="button" className="btn sm" onClick={() => setSheet({ insert: true })}><Icon name="plus" size={14} />Row</button>}
          {canEdit && selected.size > 0 && <button type="button" className="btn sm danger-text" onClick={() => setDel(true)}><Icon name="trash" size={14} />Delete {selected.size}</button>}
          <button type="button" className="btn sm ghost" title="Open in the SQL editor" onClick={() => nav(`/c/${conn.id}/sql?${new URLSearchParams({ ...(db ? { db } : {}), q: sqlFor() })}`)}><Icon name="code" size={14} /><span className="hide-sm">SQL</span></button>
          <a className="btn sm ghost" href={exportURL('csv')} title="Export everything as CSV"><Icon name="download" size={14} /><span className="hide-sm">CSV</span></a>
          <a className="btn sm ghost hide-sm" href={exportURL('json')} title="Export everything as JSON">JSON</a>
          <button type="button" className="btn sm ghost icon-only" title="Reload" aria-label="Reload" onClick={() => setSeq(s => s + 1)}><Icon name="refresh" size={14} /></button>
        </div>
        {filters.length > 0 && <div className="chips filter-chips">{filters.map((f, i) => <span key={i} className="chip">{f.column} {OPS.find(o => o.value === f.op)?.label} {OPS.find(o => o.value === f.op)?.needs ? <b>{f.value || '""'}</b> : null}<button type="button" aria-label="Remove the filter" onClick={() => setFilters(filters.filter((_, j) => j !== i))}><Icon name="x" size={12} /></button></span>)}<button type="button" className="link-btn small" onClick={() => setFilters([])}>Clear</button></div>}
        {!conn.allow_writes ? null : info.kind !== 'table' ? <p className="muted small">A {info.kind} cannot be edited row by row.</p> : key.length === 0 ? <p className="muted small">No primary key: single rows cannot be identified, so edit this table with SQL.</p> : null}
        {err && <div className="callout danger"><Icon name="alert" size={15} /><span>{err}</span></div>}
        {!page ? <Loading /> : (
          <div className={`tv-grid ${loading ? 'is-loading' : ''}`}>
            {page.rows.length === 0 ? <p className="muted pad">{dsearch || filters.length ? 'No rows match.' : 'This table is empty.'}</p>
              : <Grid columns={page.columns} types={page.types} rows={page.rows} start={offset} sort={sort?.col} desc={sort?.desc} onSort={sortBy}
                editable={canEdit} edits={edits} onEdit={(r, c, v) => setEdits(e => ({ ...e, [r]: { ...e[r], [c]: v } }))}
                selected={selected} onSelect={setSelected} onOpenRow={r => setSheet({ row: r })} density={settings?.density} hidden={info.rowid ? new Set(['__rowid']) : undefined} />}
          </div>
        )}
        {page && (
          <div className="pager">
            {pendingCount > 0 ? (
              <div className="pending-bar"><Icon name="edit" size={14} /><span>{pendingCount} unsaved {pendingCount === 1 ? 'change' : 'changes'}</span>
                <button type="button" className="btn sm" onClick={() => setEdits({})}>Discard</button><button type="button" className="btn sm primary" onClick={saveEdits}>Save</button></div>
            ) : <span className="muted small tnum">{pageLabel(offset, page.rows.length, page.total, page.total_approx)} · {page.duration_ms} ms</span>}
            <span className="spacer" />
            <button type="button" className="btn sm ghost icon-only" aria-label="First page" disabled={offset === 0} onClick={() => setOffset(0)}><Icon name="chevron-left" size={14} /><Icon name="chevron-left" size={14} /></button>
            <button type="button" className="btn sm" disabled={offset === 0} onClick={() => setOffset(Math.max(0, offset - limit))}><Icon name="chevron-left" size={14} />Back</button>
            <button type="button" className="btn sm" disabled={page.rows.length < limit || (page.total >= 0 && offset + limit >= page.total)} onClick={() => setOffset(offset + limit)}>Next<Icon name="chevron-right" size={14} /></button>
          </div>
        )}
      </>}
      {tab === 'structure' && <Structure info={info} />}
      {tab === 'ddl' && <div className="card"><div className="card-body"><div className="row mb"><span className="grow muted small">The statement that creates this {info.kind}{conn.engine === 'postgres' ? ', rebuilt from the catalog' : ''}.</span><button type="button" className="btn sm" onClick={async () => toast(await copyText(info.ddl) ? 'Copied' : 'Could not copy')}><Icon name="copy" size={14} />Copy</button></div><pre className="ddl mono">{info.ddl || '—'}</pre></div></div>}

      {filterAt && <FilterPop anchor={filterAt} columns={info.columns.map(c => c.name)} onClose={() => setFilterAt(null)} onAdd={f => { setFilters([...filters, f]); setFilterAt(null) }} />}
      {sheet && page && (
        <RowSheet columns={sheet.insert ? info.columns.map(c => c.name) : page.columns.filter(c => !(info.rowid && c === '__rowid'))} colInfo={info.columns}
          row={sheet.row !== undefined ? page.rows[sheet.row].filter((_, i) => !(info.rowid && page.columns[i] === '__rowid')) : undefined}
          editable={canEdit} insert={sheet.insert} title={sheet.insert ? `New row in ${info.name}` : `Row ${offset + (sheet.row || 0) + 1}`}
          onClose={() => setSheet(null)}
          onDelete={canEdit && sheet.row !== undefined ? () => { setSelected(new Set([sheet.row!])); setSheet(null); setDel(true) } : undefined}
          onSave={async vals => {
            const changes: Change[] = sheet.insert
              ? [{ op: 'insert', values: Object.fromEntries(Object.entries(vals).filter(([, v]) => v !== null || true)) }]
              : [{ op: 'update', key: rowKey(sheet.row!), values: vals }]
            if (await apply(changes, sheet.insert ? 'Row added' : 'Row saved')) setSheet(null)
          }} />
      )}
      {del && <Confirm title={`Delete ${selected.size} ${selected.size === 1 ? 'row' : 'rows'}?`} confirmLabel="Delete" message={<>They are removed from <b>{info.name}</b> for good{conn.engine === 'sqlite' ? ' (a snapshot of the file is kept first)' : ''}.</>}
        onCancel={() => setDel(false)} onConfirm={() => { setDel(false); apply([...selected].map(r => ({ op: 'delete' as const, key: rowKey(r) })), `Deleted ${selected.size} ${selected.size === 1 ? 'row' : 'rows'}`) }} />}
      {gate && <GateDialog gate={gate} onClose={() => setGate(null)} onPaused={() => { setGate(null); reloadSafety() }} />}
    </div>
  )
}

function FilterPop({ anchor, columns, onClose, onAdd }: { anchor: Anchor; columns: string[]; onClose: () => void; onAdd: (f: Filter) => void }) {
  const [f, setF] = useState<Filter>({ column: columns[0] || '', op: 'contains', value: '' })
  const needs = OPS.find(o => o.value === f.op)?.needs
  return (
    <Popover anchor={anchor} onClose={onClose} prefer="below" width={320} label="Add a filter">
      <form className="filter-pop" onSubmit={e => { e.preventDefault(); onAdd(f) }}>
        <Select ariaLabel="Column" value={f.column} onChange={v => setF({ ...f, column: v })} options={columns.map(c => ({ value: c, label: c }))} />
        <Select ariaLabel="Condition" value={f.op} onChange={v => setF({ ...f, op: v })} options={OPS.map(o => ({ value: o.value, label: o.label }))} />
        {needs && <input autoFocus value={f.value} onChange={e => setF({ ...f, value: e.target.value })} placeholder="Value" aria-label="Value" />}
        <div className="form-actions"><button type="button" className="btn sm" onClick={onClose}>Cancel</button><button className="btn sm primary">Add filter</button></div>
      </form>
    </Popover>
  )
}

function Structure({ info }: { info: TableInfo }) {
  return (
    <div className="stack">
      <div className="card flush"><table className="table compact">
        <thead><tr><th>Column</th><th>Type</th><th>Empty allowed</th><th>Default</th><th /></tr></thead>
        <tbody>{info.columns.map(c => (
          <tr key={c.name}>
            <td><b className="mono">{c.name}</b>{c.comment && <div className="muted small">{c.comment}</div>}</td>
            <td className="mono small">{c.type}</td>
            <td>{c.nullable ? 'yes' : <span className="muted">no</span>}</td>
            <td className="mono small muted ellipsis" style={{ maxWidth: 260 }}>{c.default ?? ''}</td>
            <td className="right nowrap">{c.pk && <span className="badge accent">key</span>}{c.auto && <span className="badge">auto</span>}{c.generated && <span className="badge">computed</span>}</td>
          </tr>
        ))}</tbody>
      </table></div>
      {info.indexes.length > 0 && <div className="card flush"><div className="card-head"><h2>Indexes</h2></div><table className="table compact">
        <tbody>{info.indexes.map(ix => <tr key={ix.name}><td className="mono">{ix.name}</td><td className="mono small">{ix.columns.join(', ')}</td><td className="right">{ix.primary ? <span className="badge accent">primary</span> : ix.unique ? <span className="badge">unique</span> : null}</td></tr>)}</tbody>
      </table></div>}
      {(info.foreign_keys.length > 0 || info.referenced_by.length > 0) && <div className="card flush"><div className="card-head"><h2>Links to other tables</h2></div><table className="table compact">
        <tbody>
          {info.foreign_keys.map(f => <tr key={'o' + f.name}><td className="mono">{f.columns.join(', ')}</td><td>→</td><td className="mono">{f.ref_table}({f.ref_columns.join(', ')})</td></tr>)}
          {info.referenced_by.map(f => <tr key={'i' + f.table + f.name}><td className="mono">{f.table}({f.columns.join(', ')})</td><td>→</td><td className="mono">{f.ref_columns.join(', ')}</td></tr>)}
        </tbody>
      </table></div>}
    </div>
  )
}

/** A change was refused for safety: explain, and offer the pause right here. */
export function GateDialog({ gate, onClose, onPaused }: { gate: { code: string; msg: string }; onClose: () => void; onPaused: () => void }) {
  const { conn, reload } = useWs()
  const nav = useNavigate()
  const { toast } = useToast()
  const [busy, setBusy] = useState(false)
  const pause = async () => {
    setBusy(true)
    try { await api.post(`${V1}/connections/${conn.id}/pause`, { action: 'start' }); toast('Paused: you can change the file now'); reload(); onPaused() } catch (e) { toast((e as Error).message, 'err') } finally { setBusy(false) }
  }
  const turnOn = async () => { try { await api.put(`${V1}/connections/${conn.id}`, { allow_writes: true }); reload(); onClose(); toast('Changes are on: try again') } catch (e) { toast((e as Error).message, 'err') } }
  return (
    <Modal title={gate.code === 'writes_off' ? 'This connection is read-only' : 'Pause first'} onClose={onClose} size="sm" footer={<>
      <button type="button" className="btn" onClick={onClose}>Cancel</button>
      {gate.code === 'writes_off' && <button type="button" className="btn primary" onClick={turnOn}>Allow changes</button>}
      {gate.code === 'needs_pause' && <button type="button" className="btn primary" disabled={busy} onClick={pause}><Icon name="pause" size={15} />{busy ? 'Stopping…' : 'Pause and continue'}</button>}
      {gate.code === 'in_use' && <button type="button" className="btn primary" onClick={() => { onClose(); nav(`/c/${conn.id}/safety`) }}>Open Safety</button>}
    </>}>
      <p>{gate.msg}</p>
      {gate.code === 'needs_pause' && <p className="muted small mt">Your change was not made. After pausing, make it again; press “Done, start again” at the top when you are finished.</p>}
    </Modal>
  )
}


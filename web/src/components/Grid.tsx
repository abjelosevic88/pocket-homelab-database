// The data grid: sticky header, row numbers, NULLs and binary shown as such,
// double-click to edit a cell (when changes are allowed), a row opens its
// own sheet with every field — the way to read and edit on a phone.
import React, { useEffect, useRef, useState } from 'react'
import type { Cell, Column } from '../lib/types'
import { asJSON, cellEdit, cellText, isBin, isLong, isNumeric } from '../lib/db'
import { fmtBytes } from '../lib/format'
import { Icon } from './icons'
import { Modal, Switch, copyText, useIsMobile, useToast } from './ui'

export type Edits = Record<number, Record<string, string | null>>

export function Grid({ columns, types, rows, start = 0, sort, desc, onSort, editable, edits, onEdit, selected, onSelect, onOpenRow, density = 'compact', hidden }: {
  columns: string[]; types?: string[]; rows: Cell[][]; start?: number
  sort?: string; desc?: boolean; onSort?: (col: string) => void
  editable?: boolean; edits?: Edits; onEdit?: (row: number, col: string, value: string | null) => void
  selected?: Set<number>; onSelect?: (rows: Set<number>) => void
  onOpenRow?: (row: number) => void; density?: string; hidden?: Set<string>
}) {
  const mobile = useIsMobile()
  const [editing, setEditing] = useState<{ r: number; c: number } | null>(null)
  const [focus, setFocus] = useState<{ r: number; c: number } | null>(null)
  const { toast } = useToast()
  const numCol = (i: number) => { const r = rows.find(x => x[i] !== null); return !!r && typeof r[i] === 'number' }
  const cols = columns.map((name, i) => ({ name, i, type: types?.[i] || '', num: isNumeric(types?.[i] || '') || numCol(i) })).filter(c => !hidden?.has(c.name))
  const allSel = !!selected && rows.length > 0 && selected.size === rows.length
  const toggle = (r: number) => {
    if (!selected || !onSelect) return
    const n = new Set(selected)
    if (n.has(r)) n.delete(r); else n.add(r)
    onSelect(n)
  }
  const copyCell = async (r: number, c: number) => { if (await copyText(cellText(rows[r][c], ''))) toast('Copied') }
  return (
    <div className={`grid-wrap ${density}`} onKeyDown={e => {
      if (!focus || editing) return
      if ((e.metaKey || e.ctrlKey) && e.key === 'c') { e.preventDefault(); copyCell(focus.r, focus.c) }
      if (e.key === 'Enter' && editable) { e.preventDefault(); setEditing(focus) }
    }}>
      <table className="dgrid">
        <thead>
          <tr>
            <th className="rn">
              {editable && onSelect ? <input type="checkbox" aria-label="Select all rows" checked={allSel} onChange={() => onSelect(allSel ? new Set() : new Set(rows.map((_, i) => i)))} /> : '#'}
            </th>
            {cols.map(c => (
              <th key={c.i} className={c.num ? 'num' : ''} title={c.type ? `${c.name} · ${c.type}` : c.name}>
                {onSort ? <button type="button" className="th-sort" onClick={() => onSort(c.name)}>
                  <span className="ellipsis">{c.name}</span>
                  {sort === c.name && <Icon name={desc ? 'chevron-down' : 'chevron-up'} size={13} />}
                </button> : <span className="ellipsis">{c.name}</span>}
                {c.type && <span className="th-type">{c.type.toLowerCase()}</span>}
              </th>
            ))}
          </tr>
        </thead>
        <tbody>
          {rows.map((row, r) => (
            <tr key={r} className={selected?.has(r) ? 'sel' : ''} onClick={() => { if (mobile && onOpenRow) onOpenRow(r) }}>
              <td className="rn">
                {editable && onSelect && !mobile ? <input type="checkbox" aria-label={`Select row ${start + r + 1}`} checked={!!selected?.has(r)} onChange={() => toggle(r)} onClick={e => e.stopPropagation()} />
                  : null}
                {onOpenRow && !mobile ? <button type="button" className="rn-btn" title="Open the row" onClick={() => onOpenRow(r)}>{start + r + 1}</button> : !editable || mobile ? <span className="rn-n">{start + r + 1}</span> : null}
              </td>
              {cols.map(c => {
                const v = row[c.i]
                const pending = edits?.[r] && c.name in edits[r]
                const shown: Cell = pending ? edits![r][c.name] : v
                const isEd = editing?.r === r && editing.c === c.i
                const isFocus = focus?.r === r && focus.c === c.i
                return (
                  <td key={c.i} className={`${c.num || typeof v === 'number' ? 'num' : ''} ${pending ? 'pending' : ''} ${isFocus ? 'focus' : ''}`}
                    tabIndex={isFocus ? 0 : -1}
                    onClick={() => !mobile && setFocus({ r, c: c.i })}
                    onDoubleClick={() => { if (editable && !isBin(v)) setEditing({ r, c: c.i }) }}>
                    {isEd ? <CellEditor value={pending ? edits![r][c.name] : (isLong(v) ? null : cellEdit(v))} long={isLong(v)} wasNull={v === null}
                      onDone={val => { setEditing(null); if (val !== undefined && onEdit) onEdit(r, c.name, val) }} />
                      : <CellView v={shown} />}
                  </td>
                )
              })}
            </tr>
          ))}
        </tbody>
      </table>
    </div>
  )
}

export function CellView({ v }: { v: Cell }) {
  if (v === null) return <span className="null">NULL</span>
  if (isBin(v)) return <span className="bin">binary · {fmtBytes(v.size)}</span>
  if (isLong(v)) return <span className="cell-text">{v.$trunc.slice(0, 300)}<span className="more">…</span></span>
  if (typeof v === 'boolean') return <span className={`bool ${v}`}>{String(v)}</span>
  const s = String(v)
  return <span className="cell-text">{s.length > 300 ? s.slice(0, 300) + '…' : s}</span>
}

function CellEditor({ value, onDone, long, wasNull }: { value: string | null; onDone: (v: string | null | undefined) => void; long: boolean; wasNull: boolean }) {
  const [v, setV] = useState(value ?? '')
  const [isNull, setNull] = useState(value === null && wasNull)
  const ref = useRef<HTMLInputElement>(null)
  useEffect(() => { ref.current?.focus(); ref.current?.select() }, [])
  if (long) return <span className="muted small" onClick={() => onDone(undefined)}>Too long to edit here: open the row</span>
  return (
    <span className="cell-edit">
      <input ref={ref} value={isNull ? '' : v} placeholder={isNull ? 'NULL' : ''} onChange={e => { setNull(false); setV(e.target.value) }}
        onKeyDown={e => {
          if (e.key === 'Enter') { e.preventDefault(); onDone(isNull ? null : v) }
          if (e.key === 'Escape') { e.preventDefault(); e.stopPropagation(); onDone(undefined) }
        }}
        onBlur={() => onDone(isNull ? null : v)} />
      <button type="button" className={`null-btn ${isNull ? 'on' : ''}`} onMouseDown={e => { e.preventDefault(); setNull(n => !n) }} title="Set to NULL">∅</button>
    </span>
  )
}

/** Every field of one row: read it whole, copy it, change it (or fill in a new row). */
export function RowSheet({ columns, colInfo, row, editable, insert, onClose, onSave, onDelete, title }: {
  columns: string[]; colInfo?: Column[]; row?: Cell[]; editable: boolean; insert?: boolean
  onClose: () => void; onSave?: (values: Record<string, string | null>) => Promise<void>; onDelete?: () => void; title?: string
}) {
  const info = (n: string) => colInfo?.find(c => c.name === n)
  const start = () => {
    const v: Record<string, string | null> = {}
    columns.forEach((n, i) => { v[n] = row ? (row[i] === null ? null : cellEdit(row[i])) : null })
    return v
  }
  const [vals, setVals] = useState<Record<string, string | null>>(start)
  const [touched, setTouched] = useState<Set<string>>(new Set())
  const [busy, setBusy] = useState(false)
  const { toast } = useToast()
  const set = (n: string, v: string | null) => { setVals({ ...vals, [n]: v }); setTouched(new Set(touched).add(n)) }
  const save = async () => {
    if (!onSave) return
    const out: Record<string, string | null> = {}
    touched.forEach(n => { out[n] = vals[n] })
    if (Object.keys(out).length === 0 && !insert) { onClose(); return }
    setBusy(true)
    try { await onSave(out) } finally { setBusy(false) }
  }
  return (
    <Modal title={title || (insert ? 'New row' : 'Row')} onClose={onClose} size="lg" footer={<>
      {onDelete && !insert && <button type="button" className="btn ghost danger-text" onClick={onDelete}><Icon name="trash" size={15} />Delete</button>}
      <span className="spacer" />
      <button type="button" className="btn" onClick={onClose}>{editable ? 'Cancel' : 'Close'}</button>
      {editable && <button type="button" className="btn primary" disabled={busy} onClick={save}>{insert ? 'Add row' : 'Save'}</button>}
    </>}>
      <div className="row-sheet">
        {columns.map((n, i) => {
          const ci = info(n)
          const v = row?.[i] ?? null
          const ro = !editable || ci?.generated || (row && isBin(v)) || (row && isLong(v) && false)
          const json = typeof v === 'string' ? asJSON(v) : null
          const val = vals[n]
          return (
            <div key={n} className="rs-field">
              <div className="rs-label">
                <b>{n}</b>{ci?.pk && <span className="badge accent">key</span>}
                <span className="muted small">{ci?.type || ''}{ci && !ci.nullable ? ' · required' : ''}{insert && ci?.auto ? ' · filled in by the database' : ''}{insert && ci?.default ? ` · default ${ci.default}` : ''}</span>
                <span className="spacer" />
                {!insert && row && <button type="button" className="btn ghost sm icon-only" title="Copy" aria-label={`Copy ${n}`} onClick={async () => toast(await copyText(cellText(v, '')) ? 'Copied' : 'Could not copy')}><Icon name="copy" size={13} /></button>}
              </div>
              {ro ? (
                <div className="rs-value">{json ? <pre className="mono">{json}</pre> : <CellView v={isLong(v) ? v.$trunc : v} />}{isLong(v) && <p className="muted small">Shown in part ({fmtBytes(v.size)}). The full value: export the table or use SQL.</p>}</div>
              ) : (
                <div className="rs-edit">
                  {(json || (val?.length || 0) > 60 || (val || '').includes('\n'))
                    ? <textarea className="mono" rows={Math.min(12, Math.max(3, (val || '').split('\n').length + 1))} value={val ?? ''} placeholder={val === null ? 'NULL' : ''} onChange={e => set(n, e.target.value)} />
                    : <input value={val ?? ''} placeholder={val === null ? (insert ? (ci?.auto || ci?.default ? 'automatic' : 'NULL') : 'NULL') : ''} onChange={e => set(n, e.target.value)} />}
                  {(ci?.nullable ?? true) && <label className="rs-null"><Switch size="sm" checked={val === null} onChange={on => set(n, on ? null : '')} label="NULL" />NULL</label>}
                </div>
              )}
            </div>
          )
        })}
      </div>
    </Modal>
  )
}

/** "1–100 of 2,063" */
export function pageLabel(start: number, count: number, total: number, approx: boolean): React.ReactNode {
  if (count === 0) return total > 0 ? `nothing from ${(start + 1).toLocaleString()}` : 'no rows'
  const t = total < 0 ? '' : ` of ${approx ? '~' : ''}${total.toLocaleString()}`
  return `${(start + 1).toLocaleString()}–${(start + count).toLocaleString()}${t}`
}

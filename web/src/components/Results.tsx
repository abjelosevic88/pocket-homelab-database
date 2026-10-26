// What a run returned: one tab per statement, the rows in the grid, what changed, what failed.
import { useEffect, useState } from 'react'
import type { Result } from '../lib/types'
import { toTSV } from '../lib/db'
import { Grid, RowSheet } from './Grid'
import { Icon } from './icons'
import { Modal, copyText, useToast } from './ui'

export function Results({ results, density, onFix, exportURL }: { results: Result[]; density?: string; onFix?: (r: Result) => void; exportURL?: (r: Result, format: 'csv' | 'json') => string | null }) {
  const firstRows = Math.max(0, results.findIndex(r => r.error) >= 0 ? results.findIndex(r => r.error) : results.map(r => r.has_rows).lastIndexOf(true))
  const [tab, setTab] = useState(firstRows)
  const [row, setRow] = useState<number | null>(null)
  const { toast } = useToast()
  useEffect(() => { setTab(firstRows); setRow(null) }, [results]) // eslint-disable-line react-hooks/exhaustive-deps
  if (results.length === 0) return null
  const r = results[Math.min(tab, results.length - 1)]
  const summary = (x: Result) => x.skipped ? 'not run' : x.error ? 'failed' : x.has_rows ? `${x.rows.length.toLocaleString()}${x.truncated ? '+' : ''} rows` : x.kind === 'read' ? 'done' : `${x.rows_affected.toLocaleString()} changed`
  return (
    <div className="results">
      {results.length > 1 && (
        <div className="res-tabs" role="tablist">
          {results.map((x, i) => (
            <button key={i} type="button" role="tab" aria-selected={i === tab} className={`res-tab ${i === tab ? 'active' : ''} ${x.error ? 'bad' : x.skipped ? 'skip' : ''}`} onClick={() => setTab(i)} title={x.statement}>
              <span className="mono ellipsis">{x.statement.replace(/\s+/g, ' ').slice(0, 40)}</span><span className="res-sum">{summary(x)}</span>
            </button>
          ))}
        </div>
      )}
      <div className="res-head">
        <span className={`res-state ${r.error ? 'bad' : r.skipped ? 'skip' : 'ok'}`}><Icon name={r.error ? 'alert' : r.skipped ? 'minus' : 'check-circle'} size={14} />{summary(r)}</span>
        {!r.skipped && <span className="muted small tnum">{r.duration_ms} ms</span>}
        {r.truncated && <span className="muted small">Only the first {r.rows.length.toLocaleString()} rows are shown (Settings → General). Export gets them all.</span>}
        <span className="spacer" />
        {r.has_rows && r.rows.length > 0 && <button type="button" className="btn ghost sm" onClick={async () => toast(await copyText(toTSV(r.columns, r.rows)) ? 'Copied as a table' : 'Could not copy')}><Icon name="copy" size={14} />Copy</button>}
        {r.has_rows && exportURL && exportURL(r, 'csv') && <>
          <a className="btn ghost sm" href={exportURL(r, 'csv')!}><Icon name="download" size={14} />CSV</a>
          <a className="btn ghost sm" href={exportURL(r, 'json')!}>JSON</a>
        </>}
      </div>
      {r.error ? (
        <div className="res-error">
          <pre className="mono">{r.error}</pre>
          {onFix && <button type="button" className="btn sm" onClick={() => onFix(r)}><Icon name="sparkles" size={14} />Fix it with AI</button>}
        </div>
      ) : r.skipped ? <p className="muted res-note">Not run: an earlier statement failed, so nothing was kept.</p>
        : r.has_rows ? (r.rows.length === 0 ? <p className="muted res-note">No rows.</p> : <Grid columns={r.columns} types={r.types} rows={r.rows} density={density} onOpenRow={setRow} />)
          : <p className="muted res-note">{r.kind === 'read' ? 'Done.' : `Done: ${r.rows_affected.toLocaleString()} ${r.rows_affected === 1 ? 'row' : 'rows'} changed.`}</p>}
      {row !== null && r.rows[row] && <RowSheet columns={r.columns} row={r.rows[row]} editable={false} onClose={() => setRow(null)} title={`Row ${row + 1}`} />}
    </div>
  )
}

/** "This changes data — run it?" with the statements and why each one deserves a look. */
export function ConfirmRun({ reasons, onRun, onCancel, what = 'Run' }: { reasons: { statement: string; reason: string }[]; onRun: () => void; onCancel: () => void; what?: string }) {
  const danger = reasons.some(r => /delete|drop|empt|every|remov|swap|for good/.test(r.reason))
  return (
    <Modal title={danger ? 'Are you sure?' : 'This changes something'} onClose={onCancel} size="sm" footer={<>
      <button type="button" className="btn" onClick={onCancel}>Cancel</button>
      <button type="button" className={`btn ${danger ? 'danger' : 'primary'}`} onClick={onRun} autoFocus>{what}</button>
    </>}>
      <div className="confirm-run">
        {reasons.map((r, i) => (
          <div key={i} className="cr-item">
            <pre className="mono">{r.statement.length > 400 ? r.statement.slice(0, 400) + '…' : r.statement}</pre>
            <p className={danger ? 'bad small' : 'muted small'}>{r.reason.charAt(0).toUpperCase() + r.reason.slice(1)}.</p>
          </div>
        ))}
        <p className="muted small">Everything you run together is one transaction: it is kept only when every statement works.</p>
      </div>
    </Modal>
  )
}

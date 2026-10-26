// Changes: every edit, statement that changed something, pause, snapshot and restore — who, what, when, which copy was kept.
import { useState } from 'react'
import { Link } from 'react-router-dom'
import { api, V1 } from '../lib/api'
import type { Activity } from '../lib/types'
import { KIND_ICON, KIND_LABEL } from '../lib/db'
import { fmtDateTime } from '../lib/format'
import { Empty, Loading, Modal, PageHeader, useAsync } from '../components/ui'
import { Icon } from '../components/icons'
import { Select } from '../components/Select'
import { useConns } from '../components/Layout'

export default function ActivityPage() {
  const { conns } = useConns()
  const [conn, setConn] = useState('')
  const { data } = useAsync(() => api.get<Activity[]>(`${V1}/activity?limit=300${conn ? `&connection=${conn}` : ''}`), [conn])
  const [open, setOpen] = useState<Activity | null>(null)
  let lastDay = ''
  return (
    <>
      <PageHeader title="Changes" sub="Everything changed through Pocket Database, by you or by another app through Pocket Core. Snapshots of SQLite files can be put back under a connection's Safety." />
      <div className="toolbar"><Select ariaLabel="Connection" value={conn} onChange={setConn} options={[{ value: '', label: 'Every connection' }, ...(conns || []).map(c => ({ value: String(c.id), label: c.name }))]} /></div>
      {!data ? <Loading /> : data.length === 0 ? <Empty icon="activity" title="Nothing changed yet">Edits, new and deleted rows, SQL that changed something, pauses and snapshots are listed here.</Empty> : (
        <div className="act-list">
          {data.map(a => {
            const day = new Date(a.created_at).toDateString()
            const head = day !== lastDay ? <h3 className="group-h" key={'d' + a.id}>{fmtDateTime(a.created_at).replace(/,?\s*\d{1,2}:\d{2}.*$/, '')}</h3> : null
            lastDay = day
            return (
              <div key={a.id}>
                {head}
                <button type="button" className={`act ${a.ok ? '' : 'failed'}`} onClick={() => setOpen(a)}>
                  <span className={`act-ico k-${a.kind}`}><Icon name={KIND_ICON[a.kind] || 'dot'} size={15} /></span>
                  <span className="grow min0">
                    <span className="act-line ellipsis"><b>{a.connection_name}</b>{a.target ? <> · <span className="mono">{a.target}</span></> : null} — {a.summary}</span>
                    <span className="small muted">{KIND_LABEL[a.kind] || a.kind} · {new Date(a.created_at).toLocaleTimeString([], { hour: '2-digit', minute: '2-digit' })}{a.source !== 'app' ? ` · by ${a.source}` : ''}{!a.ok ? ' · failed, nothing kept' : ''}{a.snapshot_id ? ' · copy kept' : ''}</span>
                  </span>
                  <Icon name="chevron-right" size={14} className="faint" />
                </button>
              </div>
            )
          })}
        </div>
      )}
      {open && <Modal title={`${KIND_LABEL[open.kind] || open.kind} · ${open.connection_name}`} onClose={() => setOpen(null)} size="lg">
        <dl className="set-dl">
          <div><dt>When</dt><dd>{fmtDateTime(open.created_at)}</dd></div>
          <div><dt>What</dt><dd>{open.summary}</dd></div>
          {open.target && <div><dt>Where</dt><dd className="mono">{open.database ? `${open.database} · ` : ''}{open.target}</dd></div>}
          {open.rows > 0 && <div><dt>Rows</dt><dd>{open.rows.toLocaleString()}</dd></div>}
          <div><dt>By</dt><dd>{open.source === 'app' ? 'you, in Pocket Database' : open.source}</dd></div>
          {!open.ok && <div><dt>Error</dt><dd className="bad">{open.error}</dd></div>}
        </dl>
        {open.statement && <pre className="ddl mono mt">{open.statement}</pre>}
        {open.connection_id && <div className="row mt"><Link className="btn" to={`/c/${open.connection_id}/safety`} onClick={() => setOpen(null)}><Icon name="shield" size={15} />Snapshots and safety</Link></div>}
      </Modal>}
    </>
  )
}

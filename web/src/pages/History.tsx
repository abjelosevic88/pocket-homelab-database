// Every query and console command you ran, newest first: search, run again, clear.
import { useState } from 'react'
import { Link } from 'react-router-dom'
import { api, V1 } from '../lib/api'
import type { HistoryEntry } from '../lib/types'
import { isSQL } from '../lib/db'
import { fmtDateTime } from '../lib/format'
import { Confirm, Empty, Loading, PageHeader, useAsync, useDebounce, copyText, useToast } from '../components/ui'
import { Icon } from '../components/icons'
import { EngineBadge } from '../components/EngineBadge'
import { Select } from '../components/Select'
import { useConns } from '../components/Layout'

export default function History() {
  const { conns } = useConns()
  const { toast } = useToast()
  const [q, setQ] = useState('')
  const [conn, setConn] = useState('')
  const dq = useDebounce(q, 300)
  const { data, reload } = useAsync(() => api.get<HistoryEntry[]>(`${V1}/history?limit=300&q=${encodeURIComponent(dq)}${conn ? `&connection=${conn}` : ''}`), [dq, conn])
  const [clear, setClear] = useState(false)
  return (
    <>
      <PageHeader title="History" sub="The last thousand queries and commands you ran. Only you see yours." actions={<button type="button" className="btn ghost danger-text" onClick={() => setClear(true)} disabled={!data?.length}><Icon name="trash" size={15} />Clear</button>} />
      <div className="toolbar">
        <div className="search-box"><Icon name="search" size={15} /><input value={q} onChange={e => setQ(e.target.value)} placeholder="Search the queries" aria-label="Search history" /></div>
        <Select ariaLabel="Connection" value={conn} onChange={setConn} options={[{ value: '', label: 'Every connection' }, ...(conns || []).map(c => ({ value: String(c.id), label: c.name }))]} />
      </div>
      {!data ? <Loading /> : data.length === 0 ? <Empty icon="history" title={q || conn ? 'Nothing matches' : 'Nothing run yet'}>Queries from the SQL editor and commands from the Redis console show up here.</Empty> : (
        <ul className="hist-list card flush">
          {data.map(h => (
            <li key={h.id} className="hist">
              <EngineBadge engine={h.engine} size={22} />
              <div className="grow min0">
                <pre className="hist-body mono">{h.body.length > 600 ? h.body.slice(0, 600) + '…' : h.body}</pre>
                <div className="small muted">{h.connection}{h.database ? ` · ${h.database}` : ''} · {fmtDateTime(h.created_at)} · {h.ok ? `${h.rows.toLocaleString()} ${h.rows === 1 ? 'row' : 'rows'} · ${h.duration_ms} ms` : <span className="bad">{h.error}</span>}</div>
              </div>
              <div className="hist-acts">
                <button type="button" className="btn ghost sm icon-only" title="Copy" aria-label="Copy" onClick={async () => toast(await copyText(h.body) ? 'Copied' : 'Could not copy')}><Icon name="copy" size={14} /></button>
                <Link className="btn sm" to={isSQL(h.engine) ? `/c/${h.connection_id}/sql?q=${encodeURIComponent(h.body)}${h.database ? `&db=${encodeURIComponent(h.database)}` : ''}` : `/c/${h.connection_id}/console`}>Open</Link>
              </div>
            </li>
          ))}
        </ul>
      )}
      {clear && <Confirm title="Clear the history?" confirmLabel="Clear" message={conn ? 'Every query you ran on this connection is forgotten.' : 'Every query you ran is forgotten. Saved queries stay.'} onCancel={() => setClear(false)}
        onConfirm={async () => { setClear(false); await api.del(`${V1}/history${conn ? `?connection=${conn}` : ''}`); reload() }} />}
    </>
  )
}

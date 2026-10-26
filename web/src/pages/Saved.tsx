// Saved queries: find one, open it in its connection's editor, rename, star, remove.
import { useMemo, useState } from 'react'
import { useNavigate } from 'react-router-dom'
import { api, V1 } from '../lib/api'
import type { SavedQuery } from '../lib/types'
import { isSQL } from '../lib/db'
import { ago } from '../lib/format'
import { Confirm, Empty, Field, Loading, MenuButton, Modal, PageHeader, useAsync, useToast } from '../components/ui'
import { Icon } from '../components/icons'
import { Select } from '../components/Select'
import { EngineBadge } from '../components/EngineBadge'
import { useConns } from '../components/Layout'

export default function Saved() {
  const { data, reload } = useAsync(() => api.get<SavedQuery[]>(`${V1}/saved`), [])
  const { conns } = useConns()
  const navigate = useNavigate()
  const { toast } = useToast()
  const [q, setQ] = useState('')
  const [edit, setEdit] = useState<SavedQuery | null>(null)
  const [del, setDel] = useState<SavedQuery | null>(null)
  const [pick, setPick] = useState<SavedQuery | null>(null)
  const list = useMemo(() => (data || []).filter(s => !q || `${s.name} ${s.description} ${s.body}`.toLowerCase().includes(q.toLowerCase())), [data, q])
  if (!data) return <Loading />
  const conn = (id: number | null) => conns?.find(c => c.id === id)
  const open = (s: SavedQuery) => { if (s.connection_id) navigate(`/c/${s.connection_id}/sql?saved=${s.id}${s.database ? `&db=${encodeURIComponent(s.database)}` : ''}`); else setPick(s) }
  const star = async (s: SavedQuery) => { try { await api.put(`${V1}/saved/${s.id}`, { favorite: !s.favorite }); reload() } catch (e) { toast((e as Error).message, 'err') } }
  return (
    <>
      <PageHeader title="Saved queries" sub="Save one from the SQL editor (⌘S). A query saved for every connection opens wherever you choose." />
      {data.length === 0 ? <Empty icon="bookmark" title="Nothing saved yet">Write a query in a connection's SQL editor and press Save.</Empty> : <>
        <div className="toolbar"><div className="search-box"><Icon name="search" size={15} /><input value={q} onChange={e => setQ(e.target.value)} placeholder="Search saved queries" aria-label="Search saved queries" /></div></div>
        <div className="saved-grid">
          {list.map(s => {
            const c = conn(s.connection_id)
            return (
              <article key={s.id} className="saved-card card">
                <div className="saved-head">
                  <button type="button" className={`btn ghost icon-only sm ${s.favorite ? 'star-on' : ''}`} aria-label={s.favorite ? 'Unstar' : 'Star'} onClick={() => star(s)}><Icon name="star" size={15} /></button>
                  <button type="button" className="saved-name grow ellipsis" onClick={() => open(s)}>{s.name}</button>
                  <MenuButton label="More" items={[{ label: 'Open', icon: 'code', onSelect: () => open(s) }, { label: 'Rename…', icon: 'edit', onSelect: () => setEdit(s) }, 'sep', { label: 'Remove', icon: 'trash', danger: true, onSelect: () => setDel(s) }]}><Icon name="more" size={16} /></MenuButton>
                </div>
                {s.description && <p className="muted small">{s.description}</p>}
                <pre className="saved-body mono" onClick={() => open(s)}>{s.body.length > 400 ? s.body.slice(0, 400) + '…' : s.body}</pre>
                <div className="saved-foot small muted">
                  {c ? <span className="row"><EngineBadge engine={c.engine} size={16} />{c.name}</span> : <span>Every connection</span>}
                  <span className="spacer" />
                  {s.run_count > 0 && <span>run {s.run_count}× · {ago(s.last_run_at)}</span>}
                </div>
              </article>
            )
          })}
        </div>
      </>}
      {edit && <EditSaved s={edit} onClose={() => setEdit(null)} onSaved={() => { setEdit(null); reload() }} />}
      {del && <Confirm title={`Remove “${del.name}”?`} confirmLabel="Remove" message="The query is forgotten; nothing in any database changes." onCancel={() => setDel(null)} onConfirm={async () => { const d = del; setDel(null); await api.del(`${V1}/saved/${d.id}`); reload() }} />}
      {pick && <Modal title={`Open “${pick.name}” in…`} onClose={() => setPick(null)} size="sm">
        <ul className="pick-list">{(conns || []).filter(c => isSQL(c.engine)).map(c => <li key={c.id}><button type="button" onClick={() => navigate(`/c/${c.id}/sql?q=${encodeURIComponent(pick.body)}`)}><EngineBadge engine={c.engine} size={22} />{c.name}</button></li>)}</ul>
      </Modal>}
    </>
  )
}

function EditSaved({ s, onClose, onSaved }: { s: SavedQuery; onClose: () => void; onSaved: () => void }) {
  const { conns } = useConns()
  const { toast } = useToast()
  const [f, setF] = useState({ name: s.name, description: s.description, connection_id: s.connection_id ? String(s.connection_id) : '' })
  const save = async (e: React.FormEvent) => {
    e.preventDefault()
    try { await api.put(`${V1}/saved/${s.id}`, { name: f.name, description: f.description, connection_id: f.connection_id ? Number(f.connection_id) : null }); onSaved() } catch (err) { toast((err as Error).message, 'err') }
  }
  return (
    <Modal title="Edit the saved query" onClose={onClose} size="sm">
      <form onSubmit={save} className="grid" style={{ gap: 14 }}>
        <Field label="Name"><input value={f.name} onChange={e => setF({ ...f, name: e.target.value })} required autoFocus /></Field>
        <Field label="What it shows"><input value={f.description} onChange={e => setF({ ...f, description: e.target.value })} /></Field>
        <Field group label="Connection"><Select ariaLabel="Connection" value={f.connection_id} onChange={v => setF({ ...f, connection_id: v })} options={[{ value: '', label: 'Every connection' }, ...(conns || []).filter(c => isSQL(c.engine)).map(c => ({ value: String(c.id), label: c.name }))]} /></Field>
        <div className="form-actions"><button type="button" className="btn" onClick={onClose}>Cancel</button><button className="btn primary">Save</button></div>
      </form>
    </Modal>
  )
}

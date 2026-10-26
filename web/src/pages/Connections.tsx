// Every connection, grouped, with its health and size; search and quick actions.
import { useMemo, useState } from 'react'
import { Link, useNavigate } from 'react-router-dom'
import { api, V1 } from '../lib/api'
import { useApp } from '../lib/app-context'
import type { Connection } from '../lib/types'
import { connLine, isSQL, statusLine, statusOf, STATUS_LABEL } from '../lib/db'
import { Confirm, Empty, Loading, MenuButton, PageHeader, Segmented, useToast } from '../components/ui'
import { Icon } from '../components/icons'
import { EngineBadge, StatusDot } from '../components/EngineBadge'
import { ConnForm } from '../components/ConnForm'
import { announceChange, openCreate, useConns } from '../components/Layout'

export default function Connections() {
  const { conns, reload } = useConns()
  const { user } = useApp()
  const navigate = useNavigate()
  const { toast } = useToast()
  const [q, setQ] = useState('')
  const [show, setShow] = useState<'all' | 'down' | 'writable'>('all')
  const [edit, setEdit] = useState<Connection | null>(null)
  const [del, setDel] = useState<Connection | null>(null)
  const groups = useMemo(() => {
    const words = q.toLowerCase().split(/\s+/).filter(Boolean)
    const list = (conns || []).filter(c => words.every(w => `${c.name} ${c.engine_name} ${c.group} ${c.address} ${c.database}`.toLowerCase().includes(w)))
      .filter(c => show === 'all' || (show === 'down' ? statusOf(c) === 'down' : c.allow_writes))
    const m = new Map<string, Connection[]>()
    for (const c of list) {
      const g = c.group || 'Other'
      m.set(g, [...(m.get(g) || []), c])
    }
    return [...m.entries()].sort((a, b) => (a[0] === 'Other' ? 1 : b[0] === 'Other' ? -1 : a[0].localeCompare(b[0])))
  }, [conns, q, show])
  if (!conns) return <Loading />
  const act = async (fn: () => Promise<unknown>, msg: string) => { try { await fn(); toast(msg); reload(); announceChange() } catch (e) { toast((e as Error).message, 'err') } }
  const total = conns.length
  return (
    <>
      <PageHeader title="Connections" sub={`${total} ${total === 1 ? 'database' : 'databases'}`} actions={<>
        {user?.role === 'admin' && <Link className="btn" to="/discover"><Icon name="radar" size={15} />On this server</Link>}
        <button type="button" className="btn primary" onClick={() => openCreate('connection')}><Icon name="plus" size={15} />Add</button>
      </>} />
      {total === 0 ? <Empty icon="database" title="No connections yet">Add one with the button above{user?.role === 'admin' ? ', or find the databases running on this server.' : '.'}</Empty> : <>
        <div className="toolbar">
          <div className="search-box"><Icon name="search" size={15} /><input value={q} onChange={e => setQ(e.target.value)} placeholder="Search connections" aria-label="Search connections" /></div>
          <Segmented size="sm" value={show} onChange={setShow} options={[{ value: 'all', label: 'All' }, { value: 'down', label: 'Down' }, { value: 'writable', label: 'Changes on' }]} />
        </div>
        {groups.length === 0 && <p className="muted">Nothing matches.</p>}
        {groups.map(([g, list]) => (
          <section key={g} className="conn-group">
            <h3 className="group-h">{g}<span className="faint">{list.length}</span></h3>
            <div className="card flush"><ul className="conn-list big">
              {list.map(c => {
                const s = statusOf(c)
                return (
                  <li key={c.id} className="conn-li-wrap">
                    <Link to={`/c/${c.id}`} className="conn-li">
                      <EngineBadge engine={c.engine} size={30} color={c.color || undefined} />
                      <span className="grow min0">
                        <span className="cl-name ellipsis">{c.favorite && <Icon name="star" size={12} className="star" />}{c.name}</span>
                        <span className="cl-sub ellipsis">{c.address}{c.database && c.engine !== 'sqlite' ? ` · ${c.database}` : ''}</span>
                      </span>
                      <span className="cl-meta hide-sm"><span className="small">{connLine(c)}</span><span className={`small ${s === 'down' ? 'bad' : 'faint'} ellipsis`}>{statusLine(c)}</span></span>
                      {c.allow_writes ? <span className="badge warn hide-sm" title="Edits and changing statements are allowed">changes on</span> : <Icon name="lock" size={13} className="faint" title="Read-only" />}
                      <StatusDot status={s} label={STATUS_LABEL[s]} />
                    </Link>
                    <MenuButton label="More" items={[
                      { label: 'Open', icon: 'database', onSelect: () => navigate(`/c/${c.id}`) },
                      ...(isSQL(c.engine) ? [{ label: 'SQL editor', icon: 'code', onSelect: () => navigate(`/c/${c.id}/sql`) }] : [{ label: 'Console', icon: 'terminal', onSelect: () => navigate(`/c/${c.id}/console`) }]),
                      { label: 'Check now', icon: 'refresh', onSelect: () => act(() => api.post(`${V1}/connections/${c.id}/check`), 'Checked') },
                      { label: c.favorite ? 'Remove from favourites' : 'Add to favourites', icon: 'star', onSelect: () => act(() => api.put(`${V1}/connections/${c.id}`, { favorite: !c.favorite }), c.favorite ? 'Removed from favourites' : 'Added to favourites') },
                      { label: 'Edit…', icon: 'edit', onSelect: () => setEdit(c) },
                      'sep',
                      { label: 'Remove…', icon: 'trash', danger: true, onSelect: () => setDel(c) },
                    ]}><Icon name="more" size={18} /></MenuButton>
                  </li>
                )
              })}
            </ul></div>
          </section>
        ))}
      </>}
      {edit && <ConnForm conn={edit} onClose={() => setEdit(null)} onSaved={() => { setEdit(null); reload(); announceChange() }} />}
      {del && <Confirm title={`Remove ${del.name}?`} confirmLabel="Remove" message={<>Pocket Database forgets this connection, its history and its snapshots. {del.source === 'upload' ? 'The uploaded copy of the file is deleted too.' : 'The database itself is not touched.'}</>}
        onCancel={() => setDel(null)} onConfirm={() => { const d = del; setDel(null); act(() => api.del(`${V1}/connections/${d.id}`), 'Removed') }} />}
    </>
  )
}

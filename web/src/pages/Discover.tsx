// On this server: the database containers and SQLite files the apps use, ready to add in one go.
import { useMemo, useState } from 'react'
import { Link, Navigate } from 'react-router-dom'
import { api, V1 } from '../lib/api'
import { useApp } from '../lib/app-context'
import type { Connection, Found } from '../lib/types'
import { engineOf } from '../lib/db'
import { ago, fmtBytes } from '../lib/format'
import { Empty, Loading, PageHeader, Switch, useAsync, useToast } from '../components/ui'
import { Icon } from '../components/icons'
import { EngineBadge } from '../components/EngineBadge'
import { announceChange } from '../components/Layout'

interface DiscoverResp { docker: boolean; error?: string; found: Found[]; scan_paths: string[] | null; host_root: string }

export default function Discover() {
  const { user, settings, setSettings } = useApp()
  const { toast } = useToast()
  const { data, error, loading, reload } = useAsync(() => api.get<DiscoverResp>(`${V1}/discover`), [])
  const [pick, setPick] = useState<Set<string>>(new Set())
  const [pw, setPw] = useState<Record<string, string>>({})
  const [busy, setBusy] = useState(false)
  const [q, setQ] = useState('')
  const showBackups = !!settings?.show_backups
  const found = useMemo(() => (data?.found || []).filter(f => showBackups || !f.backup).filter(f => {
    const words = q.toLowerCase().split(/\s+/).filter(Boolean)
    return words.every(w => `${f.name} ${f.group} ${f.engine} ${f.host_path || ''} ${f.image || ''}`.toLowerCase().includes(w))
  }), [data, showBackups, q])
  if (user?.role !== 'admin') return <Navigate to="/" replace />
  const servers = found.filter(f => f.engine !== 'sqlite')
  const files = found.filter(f => f.engine === 'sqlite')
  const backups = (data?.found || []).filter(f => f.backup).length
  const addable = found.filter(f => f.supported && !f.added)
  const toggle = (k: string) => { const n = new Set(pick); if (n.has(k)) n.delete(k); else n.add(k); setPick(n) }
  const add = async (keys: string[]) => {
    setBusy(true)
    try {
      const r = await api.post<{ added: Connection[]; skipped: string[] | null }>(`${V1}/discover/add`, { keys, passwords: pw })
      toast(`Added ${r.added.length} ${r.added.length === 1 ? 'connection' : 'connections'}${r.skipped?.length ? ` · skipped ${r.skipped.join(', ')}` : ''}`)
      setPick(new Set()); announceChange(); reload()
    } catch (e) { toast((e as Error).message, 'err') } finally { setBusy(false) }
  }
  const toggleBackups = async (v: boolean) => { try { setSettings(await api.put(`${V1}/settings`, { show_backups: v })) } catch (e) { toast((e as Error).message, 'err') } }
  const row = (f: Found) => (
    <li key={f.key} className={`found ${f.added ? 'added' : ''} ${!f.supported ? 'unsupported' : ''}`}>
      <label className="found-pick">
        <input type="checkbox" disabled={f.added || !f.supported} checked={pick.has(f.key)} onChange={() => toggle(f.key)} aria-label={`Add ${f.name}`} />
      </label>
      <EngineBadge engine={f.engine} size={28} color={f.supported ? undefined : '#8a8a96'} />
      <div className="grow min0">
        <div className="found-name"><span className="ellipsis">{f.name}</span>{f.group && f.group !== f.name && <span className="faint small">{f.group}</span>}{f.backup && <span className="badge">backup copy</span>}</div>
        <div className="found-sub small muted ellipsis">
          {f.engine === 'sqlite' ? <><span className="mono">{f.host_path}</span> · {fmtBytes(f.size_bytes)}{f.modified ? ` · changed ${ago(f.modified)}` : ''}</>
            : <>{engineOf(f.engine).name === f.engine ? f.engine : engineOf(f.engine).name} · {f.image}{f.host ? ` · ${f.host}:${f.port}` : ''}{f.user ? ` · user ${f.user}` : ''}{f.database && f.engine !== 'redis' ? ` · ${f.database}` : ''}</>}
        </div>
        {f.owners && f.owners.length > 0 && <div className="small faint">Used by {f.owners.join(', ')}: changes need a short pause of {f.owners.length === 1 ? 'that app' : 'those apps'}.</div>}
        {f.note && <div className="small faint">{f.note}</div>}
        {f.needs_password && !f.added && f.supported && <input className="found-pw" type="password" placeholder="Password" value={pw[f.key] || ''} onChange={e => setPw({ ...pw, [f.key]: e.target.value })} autoComplete="new-password" aria-label={`Password for ${f.name}`} />}
      </div>
      {f.added ? <Link className="btn sm" to={`/c/${f.connection_id}`}>Open</Link>
        : f.supported ? <button type="button" className="btn sm" disabled={busy} onClick={() => add([f.key])}>Add</button>
          : <span className="badge">not yet</span>}
    </li>
  )
  return (
    <>
      <PageHeader title="On this server" sub="The databases your apps use: containers running PostgreSQL, MySQL / MariaDB or Redis (their passwords come from the container's settings), and SQLite files in the folders containers keep their data in."
        actions={<>
          <button type="button" className="btn" onClick={() => reload()} disabled={loading}><Icon name="refresh" size={15} />Look again</button>
          <button type="button" className="btn primary" disabled={busy || (pick.size === 0 && addable.length === 0)} onClick={() => add(pick.size ? [...pick] : addable.map(f => f.key))}>
            <Icon name="plus" size={15} />{pick.size ? `Add ${pick.size}` : `Add all ${addable.length}`}
          </button>
        </>} />
      {error && <div className="callout danger mb"><Icon name="alert" size={15} /><span>{error}</span></div>}
      {!data ? <Loading label="Looking at the containers and their folders…" /> : <>
        {!data.docker && <div className="callout info mb"><Icon name="info" size={15} /><div>No container engine here. Mount <code>/var/run/docker.sock</code> (and the folders your apps keep their data in) to find databases automatically; <code>SCAN_PATHS</code> adds more folders to look for SQLite files in.</div></div>}
        {data.error && <div className="callout danger mb"><Icon name="alert" size={15} /><span>{data.error}</span></div>}
        <div className="callout info mb"><Icon name="shield" size={15} /><div>Everything is added <b>read-only</b>: you can browse and query, nothing changes. Switch on “Allow changes” per connection when you need to edit. A SQLite file an app uses is only ever changed with that app paused and a copy kept.</div></div>
        <div className="toolbar">
          <div className="search-box"><Icon name="search" size={15} /><input value={q} onChange={e => setQ(e.target.value)} placeholder="Search" aria-label="Search" /></div>
          {backups > 0 && <label className="row small muted"><Switch size="sm" checked={showBackups} onChange={toggleBackups} label="Show backup copies" />Backup copies ({backups})</label>}
        </div>
        {found.length === 0 ? <Empty icon="radar" title="Nothing found">{data.docker ? 'No database containers or SQLite files in the container folders.' : ''}</Empty> : <>
          {servers.length > 0 && <section className="found-sec"><h3 className="group-h">Database servers<span className="faint">{servers.length}</span></h3><ul className="found-list card flush">{servers.map(row)}</ul></section>}
          {files.length > 0 && <section className="found-sec"><h3 className="group-h">SQLite files<span className="faint">{files.length}</span></h3><ul className="found-list card flush">{files.map(row)}</ul></section>}
        </>}
      </>}
    </>
  )
}

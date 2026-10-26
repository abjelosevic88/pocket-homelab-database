// One connection: its tables (or keys) on the left, and on the right the
// table, the SQL editor, the console, safety and info. On a phone the
// list is the connection's front page and every view has the whole width.
import React, { createContext, useContext, useEffect, useMemo, useState } from 'react'
import { Link, NavLink, useLocation, useNavigate, useParams, useSearchParams } from 'react-router-dom'
import { api, V1 } from '../lib/api'
import { useApp } from '../lib/app-context'
import type { Connection, DBInfo, DBObject, Safety, Schema, ServerInfo, SizePoint } from '../lib/types'
import { connLine, isSQL, statusLine, statusOf, STATUS_LABEL } from '../lib/db'
import { ago, fmtBytes, fmtDateTime } from '../lib/format'
import { Card, Confirm, Empty, Loading, Switch, useAsync, useIsMobile, useToast } from '../components/ui'
import { Icon } from '../components/icons'
import { Select } from '../components/Select'
import { EngineBadge, StatusDot } from '../components/EngineBadge'
import { ConnForm } from '../components/ConnForm'
import { LineChart } from '../components/Chart'
import { announceChange, useConns, useCrumb, useOnChange } from '../components/Layout'
import TableView from './TableView'
import SqlPage from './SqlPage'
import { RedisConsole, RedisKeys } from './RedisPages'

/** The open connection, the chosen database and a way to reload both. */
interface Ws { conn: Connection; db: string; setDb: (d: string) => void; reload: () => void; safety: Safety | null; reloadSafety: () => void; schemas: Schema[] | null; reloadSchemas: () => void }
const WsCtx = createContext<Ws>(null!)
export const useWs = () => useContext(WsCtx)

export default function ConnectionPage() {
  const { id } = useParams()
  const rest = (useParams()['*'] || '').replace(/\/$/, '')
  const [sp, setSp] = useSearchParams()
  const mobile = useIsMobile()
  const { data: conn, error, reload, setData } = useAsync(() => api.get<Connection>(`${V1}/connections/${id}`), [id])
  const { data: dbs } = useAsync(() => conn ? api.get<DBInfo[]>(`${V1}/connections/${id}/databases`).catch(() => [] as DBInfo[]) : Promise.resolve(null), [id, conn?.updated_at, !!conn])
  const { data: safety, reload: reloadSafety } = useAsync(() => conn ? api.get<Safety>(`${V1}/connections/${id}/safety`) : Promise.resolve(null), [id, conn?.updated_at, !!conn])
  const db = sp.get('db') || conn?.database || (conn?.engine === 'redis' ? '0' : dbs?.find(d => d.current)?.name || '')
  const setDb = (d: string) => { const n = new URLSearchParams(sp); if (d) n.set('db', d); else n.delete('db'); setSp(n, { replace: true }) }
  const sql = conn ? isSQL(conn.engine) : false
  const { data: schemas, error: schemaErr, reload: reloadSchemas } = useAsync(() => conn && sql ? api.get<Schema[]>(`${V1}/connections/${id}/schemas?db=${encodeURIComponent(db)}`) : Promise.resolve(null), [id, db, sql, conn?.updated_at])
  useOnChange(() => { reload(); reloadSafety() })
  const page = rest.split('/')[0]
  const tableName = page === 't' ? decodeURIComponent(rest.slice(2)) : ''
  useCrumb(conn?.name, tableName || { sql: 'SQL', console: 'Console', keys: 'Keys', safety: 'Safety', info: 'Info' }[page])
  if (error) return <div className="callout danger"><Icon name="alert" size={15} /><span>{error}</span></div>
  if (!conn) return <Loading />
  const ws: Ws = { conn, db, setDb, reload: () => { reload(); reloadSafety() }, safety, reloadSafety, schemas, reloadSchemas }
  const treeHere = !mobile || page === ''
  const base = `/c/${conn.id}`
  const q = db && db !== conn.database ? `?db=${encodeURIComponent(db)}` : ''
  const tabs = sql
    ? [{ to: '', label: 'Tables', icon: 'table' }, { to: 'sql', label: 'SQL', icon: 'code' }, { to: 'safety', label: 'Safety', icon: 'shield' }, { to: 'info', label: 'Info', icon: 'info' }]
    : [{ to: '', label: 'Keys', icon: 'key' }, { to: 'console', label: 'Console', icon: 'terminal' }, { to: 'safety', label: 'Safety', icon: 'shield' }, { to: 'info', label: 'Info', icon: 'info' }]
  const s = statusOf(conn)
  return (
    <WsCtx.Provider value={ws}>
      <div className="ws-head">
        <EngineBadge engine={conn.engine} size={34} color={conn.color || undefined} />
        <div className="grow min0">
          <h1 className="ws-title ellipsis">{conn.name}</h1>
          <div className="ws-sub small"><StatusDot status={s} label={STATUS_LABEL[s]} /><span className={`ellipsis ${s === 'down' ? 'bad' : 'muted'}`}>{s === 'up' ? connLine(conn) : statusLine(conn)}</span></div>
        </div>
        {(dbs?.length || 0) > 1 && (
          <div className="ws-db"><Select ariaLabel="Database" value={db} onChange={setDb} options={dbs!.map(d => ({ value: d.name, label: d.name, hint: d.size_bytes > 0 ? fmtBytes(d.size_bytes) : d.tables > 0 ? `${d.tables} ${conn.engine === 'redis' ? 'keys' : 'tables'}` : undefined }))} /></div>
        )}
        <WriteBadge conn={conn} safety={safety} />
      </div>
      <nav className="ws-tabs" aria-label="Views">
        {tabs.map(t => (
          <NavLink key={t.to} end={t.to === ''} to={`${base}${t.to ? '/' + t.to : ''}${q}`} className={({ isActive }) => `ws-tab ${isActive || (t.to === '' && page === 't') ? 'active' : ''}`}>
            <Icon name={t.icon} size={15} />{t.label}
          </NavLink>
        ))}
      </nav>
      <SafetyBanner />
      {sql ? (
        <div className={`ws ${page === '' && mobile ? 'tree-only' : ''} ${page === 'sql' || page === 'safety' || page === 'info' ? 'no-tree' : ''}`}>
          {treeHere && page !== 'sql' && page !== 'safety' && page !== 'info' && <Tree schemas={schemas} error={schemaErr} active={tableName} />}
          <section className="ws-main">
            {page === '' && !mobile && <ConnHome />}
            {page === 't' && <TableView key={`${db}|${tableName}`} name={tableName} schema={sp.get('schema') || ''} />}
            {page === 'sql' && <SqlPage />}
            {page === 'safety' && <SafetyPage />}
            {page === 'info' && <InfoPage dbs={dbs} onEdited={c => setData(c)} />}
          </section>
        </div>
      ) : (
        <>
          {page === '' && <RedisKeys />}
          {page === 'console' && <RedisConsole />}
          {page === 'safety' && <SafetyPage />}
          {page === 'info' && <InfoPage dbs={dbs} onEdited={c => setData(c)} />}
        </>
      )}
    </WsCtx.Provider>
  )
}

function WriteBadge({ conn, safety }: { conn: Connection; safety: Safety | null }) {
  const nav = useNavigate()
  if (!conn.allow_writes) return <button type="button" className="ws-mode ro" onClick={() => nav(`/c/${conn.id}/safety`)} title="Read-only: nothing can be changed"><Icon name="lock" size={13} />Read-only</button>
  if (safety?.pause) return <button type="button" className="ws-mode paused" onClick={() => nav(`/c/${conn.id}/safety`)}><Icon name="pause" size={13} />Paused for changes</button>
  if (safety?.code === 'needs_pause' || safety?.code === 'in_use') return <button type="button" className="ws-mode live" onClick={() => nav(`/c/${conn.id}/safety`)} title={safety.reason}><Icon name="shield" size={13} />In use · pause to change</button>
  return <button type="button" className="ws-mode rw" onClick={() => nav(`/c/${conn.id}/safety`)} title="Changes are allowed"><Icon name="unlock" size={13} />Changes on</button>
}

/** A one-line reminder while an app is paused for changes. */
function SafetyBanner() {
  const { conn, safety, reloadSafety, reload } = useWs()
  const { toast } = useToast()
  const [busy, setBusy] = useState(false)
  const [, tick] = useState(0)
  useEffect(() => { if (!safety?.pause) return; const t = setInterval(() => tick(x => x + 1), 30000); return () => clearInterval(t) }, [safety?.pause])
  if (!safety?.pause) return null
  const finish = async () => {
    setBusy(true)
    try { await api.post(`${V1}/connections/${conn.id}/pause`, { action: 'finish' }); toast('Started again'); reloadSafety(); reload(); announceChange() } catch (e) { toast((e as Error).message, 'err') } finally { setBusy(false) }
  }
  const left = Math.max(0, Math.round((new Date(safety.pause.expires_at).getTime() - Date.now()) / 60000))
  return (
    <div className="pause-bar">
      <Icon name="pause" size={15} />
      <span className="grow">{safety.pause.containers.length ? <><b>{safety.pause.containers.join(', ')}</b> {safety.pause.containers.length === 1 ? 'is' : 'are'} stopped while you change this file.</> : 'Opened for changes.'} {left > 0 ? `Starts again by itself in ${left} min.` : 'Starts again in a moment.'}</span>
      <button type="button" className="btn sm" onClick={async () => { await api.post(`${V1}/connections/${conn.id}/pause`, { action: 'extend' }); reloadSafety() }}>More time</button>
      <button type="button" className="btn sm primary" disabled={busy} onClick={finish}>{busy ? 'Starting…' : 'Done, start again'}</button>
    </div>
  )
}

// ---- the tree ----

function Tree({ schemas, error, active }: { schemas: Schema[] | null; error: string; active: string }) {
  const { conn, db, reloadSchemas } = useWs()
  const { settings } = useApp()
  const [q, setQ] = useState('')
  const [closed, setClosed] = useState<Set<string>>(new Set())
  const loc = useLocation()
  const sys = (n: string) => !settings?.show_system && /^(pg_|information_schema$|_timescaledb|timescaledb_|cron$|sys$|mysql$|performance_schema$)/.test(n)
  const shown = useMemo(() => (schemas || []).filter(s => !sys(s.name)).map(s => ({ ...s, objects: s.objects.filter(o => !q || o.name.toLowerCase().includes(q.toLowerCase())) })).filter(s => s.objects.length > 0 || !q), [schemas, q, settings?.show_system]) // eslint-disable-line react-hooks/exhaustive-deps
  const many = shown.length > 1
  const link = (o: DBObject) => `/c/${conn.id}/t/${encodeURIComponent(o.name)}?${new URLSearchParams({ ...(db ? { db } : {}), ...(o.schema && conn.engine === 'postgres' ? { schema: o.schema } : {}) }).toString()}`
  const total = (schemas || []).reduce((n, s) => n + s.objects.length, 0)
  return (
    <aside className="ws-tree" aria-label="Tables">
      <div className="tree-search"><Icon name="search" size={14} /><input value={q} onChange={e => setQ(e.target.value)} placeholder={`Filter ${total} tables`} aria-label="Filter tables" />
        <button type="button" className="btn ghost icon-only sm" title="Reload" aria-label="Reload the tables" onClick={reloadSchemas}><Icon name="refresh" size={13} /></button></div>
      {error ? <p className="bad small pad">{error}</p> : !schemas ? <Loading /> : shown.length === 0 ? <p className="muted small pad">{q ? 'Nothing matches.' : 'No tables.'}</p> : (
        <div className="tree">
          {shown.map(s => (
            <div key={s.name} className="tree-sec">
              {many && <button type="button" className="tree-schema" onClick={() => { const n = new Set(closed); if (n.has(s.name)) n.delete(s.name); else n.add(s.name); setClosed(n) }}>
                <Icon name={closed.has(s.name) ? 'chevron-right' : 'chevron-down'} size={13} />{s.name}<span className="faint">{s.objects.length}</span>
              </button>}
              {!closed.has(s.name) && s.objects.map(o => {
                const on = active === o.name && (new URLSearchParams(loc.search).get('schema') || '') === (conn.engine === 'postgres' ? o.schema : '')
                return (
                  <Link key={o.schema + '.' + o.name} to={link(o)} className={`tree-item ${on ? 'active' : ''}`} title={o.comment || o.name}>
                    <Icon name={o.kind === 'table' ? 'table' : 'eye'} size={14} />
                    <span className="ellipsis grow">{o.name}</span>
                    {o.rows >= 0 && <span className="tree-n tnum">{o.rows >= 10000 ? `${Math.round(o.rows / 1000)}k` : o.rows.toLocaleString()}</span>}
                  </Link>
                )
              })}
            </div>
          ))}
        </div>
      )}
    </aside>
  )
}

/** The connection's front page on a wide screen: what is in it. */
function ConnHome() {
  const { conn, schemas, db } = useWs()
  const nav = useNavigate()
  const objects = (schemas || []).flatMap(s => s.objects)
  const [sort, setSort] = useState<'name' | 'rows' | 'size'>('size')
  const list = [...objects].sort((a, b) => sort === 'name' ? a.name.localeCompare(b.name) : sort === 'rows' ? b.rows - a.rows : b.size_bytes - a.size_bytes)
  const go = (o: DBObject) => nav(`/c/${conn.id}/t/${encodeURIComponent(o.name)}?${new URLSearchParams({ ...(db ? { db } : {}), ...(o.schema && conn.engine === 'postgres' ? { schema: o.schema } : {}) }).toString()}`)
  return (
    <div className="stack">
      {conn.notes && <div className="callout info"><Icon name="note" size={15} /><span>{conn.notes}</span></div>}
      {!schemas ? <Loading /> : objects.length === 0 ? <Empty icon="table" title="No tables here">{conn.engine === 'mysql' && !db ? 'Pick a database at the top.' : 'This database is empty.'}</Empty> : (
        <Card title={`${objects.length} tables and views`} flush actions={
          <div className="segmented sm">{(['size', 'rows', 'name'] as const).map(k => <button key={k} type="button" className={sort === k ? 'active' : ''} onClick={() => setSort(k)}>{{ size: 'Size', rows: 'Rows', name: 'Name' }[k]}</button>)}</div>}>
          <table className="table compact hover">
            <thead><tr><th>Name</th>{conn.engine === 'postgres' && <th>Schema</th>}<th className="right">Rows</th><th className="right">Size</th></tr></thead>
            <tbody>{list.slice(0, 300).map(o => (
              <tr key={o.schema + o.name} onClick={() => go(o)} className="click">
                <td><span className="row nowrap"><Icon name={o.kind === 'table' ? 'table' : 'eye'} size={14} className="muted" />{o.name}{o.kind !== 'table' && <span className="badge">{o.kind}</span>}</span></td>
                {conn.engine === 'postgres' && <td className="muted">{o.schema}</td>}
                <td className="right tnum">{o.rows >= 0 ? o.rows.toLocaleString() : '—'}</td>
                <td className="right tnum muted">{o.size_bytes > 0 ? fmtBytes(o.size_bytes) : '—'}</td>
              </tr>
            ))}</tbody>
          </table>
        </Card>
      )}
    </div>
  )
}

// ---- safety ----

function SafetyPage() {
  const { conn, safety, reloadSafety, reload } = useWs()
  const { toast } = useToast()
  const [busy, setBusy] = useState('')
  const [restore, setRestore] = useState<number | null>(null)
  const [force, setForce] = useState(false)
  if (!safety) return <Loading />
  const run = async (what: string, fn: () => Promise<unknown>, msg: string) => {
    setBusy(what)
    try { await fn(); toast(msg); reloadSafety(); reload(); announceChange() } catch (e) { toast((e as Error).message, 'err') } finally { setBusy('') }
  }
  const setWrites = (v: boolean) => run('writes', () => api.put(`${V1}/connections/${conn.id}`, { allow_writes: v }), v ? 'Changes are on' : 'Read-only now')
  const file = conn.engine === 'sqlite'
  return (
    <div className="stack loose safety">
      <Card title="Changes">
        <div className="switch-row">
          <Switch checked={conn.allow_writes} onChange={setWrites} label="Allow changes" disabled={busy === 'writes'} />
          <span><b>{conn.allow_writes ? 'Changes are allowed' : 'Read-only'}</b><small>{conn.allow_writes ? 'Edits, new rows, deletions and SQL that changes something work — each one confirmed first when it deletes or empties something (or always: Settings → Safety). Every change is logged under Changes.' : 'You can browse and run queries that read; nothing can be changed, by you or by mistake.'}</small></span>
        </div>
        {!file && <p className="muted small mt">{conn.engine === 'redis' ? 'Redis takes changes while its app runs; risky commands (FLUSHDB, CONFIG SET…) ask first.' : 'The server takes changes while its app runs: everything you run together is one transaction, kept only when every statement works.'}</p>}
      </Card>
      {file && (
        <Card title="This file and the apps using it">
          {safety.live ? (
            <p><b>{safety.owners.join(', ')}</b> {safety.owners.length === 1 ? 'has' : 'have'} this file open. Writing under a running app can damage the file, so changes need a short pause: Pocket Database stops {safety.owners.length === 1 ? 'the app' : 'those apps'}, keeps a copy of the file, lets you change it, and starts {safety.owners.length === 1 ? 'it' : 'them'} again when you press Done — or by itself after the pause time (Settings → Safety).</p>
          ) : safety.in_use ? (
            <p>Another program has this file open (there is a <code>-wal</code> or <code>-shm</code> file next to it){safety.docker ? ', and it is not a container Pocket Database can pause' : ''}. Stop that program first, then confirm below.</p>
          ) : <p>Nothing uses this file right now. A copy is kept automatically before the first change (at most one every ten minutes).</p>}
          <div className="row wrap mt">
            {safety.pause ? <>
              <span className="badge warn"><Icon name="pause" size={12} />paused since {fmtDateTime(safety.pause.started_at)}</span>
              <button type="button" className="btn primary" disabled={!!busy} onClick={() => run('finish', () => api.post(`${V1}/connections/${conn.id}/pause`, { action: 'finish' }), 'Started again')}>Done, start again</button>
              <button type="button" className="btn" disabled={!!busy} onClick={() => run('extend', () => api.post(`${V1}/connections/${conn.id}/pause`, { action: 'extend' }), 'More time')}>More time</button>
            </> : (safety.live || safety.in_use) && <>
              {safety.in_use && !safety.live && <label className="row small"><input type="checkbox" checked={force} onChange={e => setForce(e.target.checked)} />I stopped the program that uses it</label>}
              <button type="button" className="btn primary" disabled={!conn.allow_writes || !!busy || (!safety.live && !force)} onClick={() => run('pause', () => api.post(`${V1}/connections/${conn.id}/pause`, { action: 'start', force }), safety.live ? `Paused ${safety.owners.join(', ')}` : 'Open for changes')}>
                <Icon name="pause" size={15} />{busy === 'pause' ? 'Stopping…' : safety.live ? `Pause ${safety.owners.join(', ')} and change` : 'Open for changes'}
              </button>
              {!conn.allow_writes && <span className="muted small">Switch on “Allow changes” first.</span>}
            </>}
          </div>
        </Card>
      )}
      {file && (
        <Card title="Snapshots" sub="Copies of the file taken before changes. Putting one back keeps a copy of the current file first, so it can be undone too." actions={
          <button type="button" className="btn sm" disabled={!!busy} onClick={() => run('snap', () => api.post(`${V1}/connections/${conn.id}/snapshots`), 'Snapshot taken')}><Icon name="archive" size={14} />Take one now</button>} flush>
          {safety.snapshots.length === 0 ? <p className="muted small pad">No snapshots yet.</p> : (
            <ul className="mini-list">{safety.snapshots.map(sn => (
              <li key={sn.id}>
                <Icon name="archive" size={14} className="muted" />
                <span className="grow min0"><span className="ellipsis">{sn.reason}</span><span className="faint small">{fmtDateTime(sn.created_at)} · {fmtBytes(sn.size_bytes)}{sn.restored_at ? ` · put back ${ago(sn.restored_at)}` : ''}</span></span>
                <a className="btn ghost sm" href={`${V1}/snapshots/${sn.id}/download`}><Icon name="download" size={13} /></a>
                <button type="button" className="btn ghost sm" disabled={!!busy} onClick={() => setRestore(sn.id)}>Put back…</button>
              </li>
            ))}</ul>
          )}
        </Card>
      )}
      {restore !== null && <Confirm title="Put this snapshot back?" confirmLabel="Put back" danger={false}
        message={<>The file goes back to how it was then; everything changed since is replaced. A copy of the file as it is now is kept first. {safety.live && !safety.pause ? <b>Pause the app first.</b> : null}</>}
        onCancel={() => setRestore(null)} onConfirm={() => { const r = restore; setRestore(null); run('restore', () => api.post(`${V1}/snapshots/${r}/restore`), 'Snapshot put back') }} />}
    </div>
  )
}

// ---- info ----

function InfoPage({ dbs, onEdited }: { dbs: DBInfo[] | null; onEdited: (c: Connection) => void }) {
  const { conn, db } = useWs()
  const { conns } = useConns()
  const nav = useNavigate()
  const { toast } = useToast()
  const { data: info, error } = useAsync(() => api.get<ServerInfo>(`${V1}/connections/${conn.id}/server?db=${encodeURIComponent(db)}`), [conn.id, db, conn.updated_at])
  const { data: sizes } = useAsync(() => api.get<SizePoint[]>(`${V1}/connections/${conn.id}/sizes?days=90`), [conn.id])
  const [edit, setEdit] = useState(false)
  const [del, setDel] = useState(false)
  const [checking, setChecking] = useState(false)
  const check = async () => { setChecking(true); try { onEdited(await api.post<Connection>(`${V1}/connections/${conn.id}/check`)); announceChange() } catch (e) { toast((e as Error).message, 'err') } finally { setChecking(false) } }
  const rows: [string, React.ReactNode][] = [
    ['Engine', conn.engine_name], ['Where', <span key="a" className="mono">{conn.address}</span>],
    ...(conn.username ? [['User', conn.username] as [string, React.ReactNode]] : []),
    ...(conn.engine !== 'sqlite' ? [['Password', conn.has_password ? 'saved (encrypted)' : 'none'] as [string, React.ReactNode], ['Encryption', { '': 'when offered', disable: 'off', require: 'required', 'verify-full': 'required, certificate checked' }[conn.ssl_mode] || conn.ssl_mode] as [string, React.ReactNode]] : []),
    ['Watched', conn.monitor ? `yes · ${statusLine(conn)}` : 'no'],
    ['Added', `${fmtDateTime(conn.created_at)} · ${{ manual: 'by hand', discovered: 'found on this server', upload: 'uploaded', import: 'imported', demo: 'demo' }[conn.source] || conn.source}`],
  ]
  return (
    <div className="stack loose info-page">
      <div className="row wrap">
        <button type="button" className="btn" onClick={() => setEdit(true)}><Icon name="edit" size={15} />Edit</button>
        <button type="button" className="btn" onClick={check} disabled={checking}><Icon name="refresh" size={15} />{checking ? 'Checking…' : 'Check now'}</button>
        {conn.engine === 'sqlite' && <a className="btn" href={`${V1}/connections/${conn.id}/file`}><Icon name="download" size={15} />Download the file</a>}
        <span className="spacer" />
        <button type="button" className="btn ghost danger-text" onClick={() => setDel(true)}><Icon name="trash" size={15} />Remove</button>
      </div>
      <div className="two-col">
        <Card title="Connection"><dl className="set-dl">{rows.map(([k, v]) => <div key={k}><dt>{k}</dt><dd>{v}</dd></div>)}</dl></Card>
        <Card title={conn.engine === 'sqlite' ? 'File' : 'Server'}>
          {error ? <p className="bad small">{error}</p> : !info ? <Loading /> : (
            <dl className="set-dl">
              <div><dt>Version</dt><dd>{info.extra?.flavor && conn.engine !== 'sqlite' ? `${info.extra.flavor} ` : ''}{info.version}</dd></div>
              <div><dt>{conn.engine === 'redis' ? 'Memory' : 'Size'}</dt><dd>{fmtBytes(info.size_bytes)}</dd></div>
              {info.uptime && <div><dt>Running for</dt><dd>{info.uptime}</dd></div>}
              {Object.entries(info.extra || {}).filter(([k]) => !['flavor', 'file'].includes(k)).map(([k, v]) => <div key={k}><dt>{k.replace(/_/g, ' ')}</dt><dd className="ellipsis" title={v}>{v}</dd></div>)}
            </dl>
          )}
        </Card>
      </div>
      {(dbs?.length || 0) > 1 && conn.engine !== 'redis' && (
        <Card title="Databases on this server" flush>
          <table className="table compact hover"><thead><tr><th>Database</th><th className="right">Size</th></tr></thead>
            <tbody>{dbs!.map(d => <tr key={d.name} className="click" onClick={() => nav(`/c/${conn.id}?db=${encodeURIComponent(d.name)}`)}><td>{d.name}{d.current && <span className="badge">default</span>}</td><td className="right tnum muted">{d.size_bytes >= 0 ? fmtBytes(d.size_bytes) : '—'}</td></tr>)}</tbody>
          </table>
        </Card>
      )}
      <Card title="Size, last 90 days">
        {sizes && sizes.length > 1 ? <LineChart kind="area" height={160} points={sizes.map(p => ({ x: p.date, y: p.size_bytes }))} format={v => fmtBytes(v)} /> : <p className="muted small">{conn.monitor ? 'Fills in day by day.' : 'Switch on watching to record the size every day.'}</p>}
      </Card>
      {edit && <ConnForm conn={conn} onClose={() => setEdit(false)} onSaved={c => { setEdit(false); onEdited(c); announceChange() }} />}
      {del && <Confirm title={`Remove ${conn.name}?`} confirmLabel="Remove" message={<>Pocket Database forgets this connection, its history and snapshots. {conn.source === 'upload' ? 'The uploaded copy is deleted.' : 'The database itself is not touched.'}</>}
        onCancel={() => setDel(false)} onConfirm={async () => { setDel(false); try { await api.del(`${V1}/connections/${conn.id}`); announceChange(); nav((conns || []).length > 1 ? '/connections' : '/') } catch (e) { toast((e as Error).message, 'err') } }} />}
    </div>
  )
}

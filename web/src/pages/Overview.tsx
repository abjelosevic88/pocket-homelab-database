// Home: is everything up, how big is it, what is growing, what changed lately.
import { Link, useNavigate } from 'react-router-dom'
import { api, V1 } from '../lib/api'
import { useApp } from '../lib/app-context'
import type { Overview as OverviewT } from '../lib/types'
import { connLine, KIND_ICON, statusLine, statusOf, STATUS_LABEL } from '../lib/db'
import { ago, fmtBytes } from '../lib/format'
import { Card, Empty, Loading, PageHeader, useAsync } from '../components/ui'
import { Icon } from '../components/icons'
import { EngineBadge, StatusDot } from '../components/EngineBadge'
import { HBars, LineChart } from '../components/Chart'
import { openCreate, useOnChange } from '../components/Layout'

export default function Overview() {
  const { user } = useApp()
  const navigate = useNavigate()
  const admin = user?.role === 'admin'
  const { data, error, reload } = useAsync(() => api.get<OverviewT>(`${V1}/overview`), [])
  useOnChange(reload)
  if (error) return <div className="callout danger"><Icon name="alert" size={15} /><span>{error}</span></div>
  if (!data) return <Loading />
  const c = data.counts
  if (c.connections === 0) {
    return (
      <>
        <PageHeader title="Overview" />
        <Empty icon="database" title="No databases yet">
          <p>Pocket Database opens PostgreSQL, MySQL / MariaDB, Redis and SQLite. {admin ? 'Start by letting it look at what runs on this server: it finds the database containers your apps use, with their passwords, and the SQLite files in their folders.' : 'Add a connection to a server, or open a SQLite file.'}</p>
          <div className="row wrap center mt">
            {admin && <button type="button" className="btn primary" onClick={() => navigate('/discover')}><Icon name="radar" size={15} />Find databases on this server</button>}
            <button type="button" className="btn" onClick={() => openCreate('connection')}><Icon name="plus" size={15} />Add a connection</button>
            <button type="button" className="btn" onClick={() => openCreate('upload')}><Icon name="upload" size={15} />Open a SQLite file</button>
          </div>
        </Empty>
      </>
    )
  }
  const down = data.connections.filter(x => statusOf(x) === 'down')
  const fmtB = (v: number) => fmtBytes(v)
  return (
    <>
      <PageHeader title="Overview" sub={`${c.connections} ${c.connections === 1 ? 'connection' : 'connections'} · ${fmtBytes(data.total_bytes)} in all`}
        actions={<button type="button" className="btn" onClick={() => openCreate('connection')}><Icon name="plus" size={15} />Add</button>} />
      <div className="tiles">
        <Tile icon="check-circle" tone="ok" n={c.up} label="Up" />
        <Tile icon="alert" tone={c.down ? 'bad' : ''} n={c.down} label="Down" />
        <Tile icon="pause" tone={c.paused ? 'warn' : ''} n={c.paused} label="Paused for changes" />
        <Tile icon="database" n={fmtBytes(data.total_bytes)} label={`${c.watched} watched`} />
      </div>
      {down.length > 0 && (
        <div className="callout danger mb">
          <Icon name="alert" size={15} />
          <div className="grow">
            <b>{down.length === 1 ? `${down[0].name} is down` : `${down.length} databases are down`}</b>
            {down.map(d => <div key={d.id} className="small"><Link to={`/c/${d.id}`}>{d.name}</Link>: {d.status_detail || 'does not answer'}{d.down_since ? ` (since ${ago(d.down_since)})` : ''}</div>)}
          </div>
        </div>
      )}
      {(data.pauses?.length || 0) > 0 && (
        <div className="callout warn mb">
          <Icon name="pause" size={15} />
          <div className="grow">{data.pauses!.map(p => {
            const conn = data.connections.find(x => x.id === p.connection_id)
            return <div key={p.id}><b>{p.containers.join(', ') || 'An app'}</b> {p.containers.length === 1 ? 'is' : 'are'} paused so <Link to={`/c/${p.connection_id}/safety`}>{conn?.name || 'a file'}</Link> can be changed; {p.containers.length === 1 ? 'it starts' : 'they start'} again {ago(p.expires_at).replace(' ago', '')} at the latest.</div>
          })}</div>
        </div>
      )}
      <div className="ov-grid">
        <Card title="Connections" flush actions={<Link className="btn ghost sm" to="/connections">All</Link>}>
          <ul className="conn-list">
            {data.connections.slice(0, 12).map(x => {
              const s = statusOf(x)
              return (
                <li key={x.id}>
                  <Link to={`/c/${x.id}`} className="conn-li">
                    <EngineBadge engine={x.engine} color={x.color || undefined} />
                    <span className="grow min0"><span className="cl-name ellipsis">{x.name}</span><span className="cl-sub ellipsis">{s === 'up' ? connLine(x) : statusLine(x)}</span></span>
                    {!x.allow_writes && <Icon name="lock" size={13} className="faint" title="Read-only" />}
                    <StatusDot status={s} label={STATUS_LABEL[s]} />
                  </Link>
                </li>
              )
            })}
          </ul>
          {data.connections.length > 12 && <div className="card-more"><Link to="/connections">{data.connections.length - 12} more</Link></div>}
        </Card>
        <div className="stack">
          <Card title="Growth" sub="All watched databases together, the last 30 days">
            {data.growth.length > 1 ? <LineChart kind="area" height={150} points={data.growth.map(g => ({ x: g.date, y: g.size_bytes }))} format={fmtB} /> : <p className="muted small">The chart fills in as the databases are checked, once a day each.</p>}
            {data.movers.length > 0 && <div className="movers">{data.movers.map(m => <Link key={m.id} to={`/c/${m.id}`} className="mover"><span className="ellipsis">{m.name}</span><span className={`tnum ${m.delta_bytes > 0 ? 'up' : 'down'}`}>{m.delta_bytes > 0 ? '+' : '−'}{fmtBytes(Math.abs(m.delta_bytes))}</span></Link>)}</div>}
          </Card>
          <Card title="Biggest">
            <HBars items={data.biggest.filter(b => b.size_bytes > 0).map(b => ({ label: b.name, value: b.size_bytes }))} format={fmtB} />
          </Card>
        </div>
      </div>
      <div className="ov-grid three">
        <Card title="Recent changes" flush actions={<Link className="btn ghost sm" to="/activity">All</Link>}>
          {(data.activity || []).length === 0 ? <p className="muted small pad">Nothing changed through Pocket Database yet.</p> : (
            <ul className="mini-list">{data.activity!.map(a => (
              <li key={a.id}><Icon name={KIND_ICON[a.kind] || 'dot'} size={14} className={a.ok ? 'muted' : 'bad'} /><span className="grow min0"><span className="ellipsis">{a.connection_name}{a.target ? ` · ${a.target}` : ''}: {a.summary}</span></span><span className="faint small nowrap">{ago(a.created_at)}</span></li>
            ))}</ul>
          )}
        </Card>
        <Card title="Recent queries" flush actions={<Link className="btn ghost sm" to="/history">All</Link>}>
          {(data.history || []).length === 0 ? <p className="muted small pad">Queries you run show up here.</p> : (
            <ul className="mini-list">{data.history!.map(h => (
              <li key={h.id}><Icon name={h.ok ? 'code' : 'alert'} size={14} className={h.ok ? 'muted' : 'bad'} /><Link className="grow min0 mono ellipsis small" to={`/c/${h.connection_id}/sql?q=${encodeURIComponent(h.body)}`}>{h.body.replace(/\s+/g, ' ')}</Link><span className="faint small nowrap">{ago(h.created_at)}</span></li>
            ))}</ul>
          )}
        </Card>
        <Card title="Saved queries" flush actions={<Link className="btn ghost sm" to="/saved">All</Link>}>
          {(data.saved || []).length === 0 ? <p className="muted small pad">Save a query from the editor to keep it here.</p> : (
            <ul className="mini-list">{data.saved!.map(q => (
              <li key={q.id}><Icon name={q.favorite ? 'star' : 'bookmark'} size={14} className="muted" /><Link className="grow min0 ellipsis" to={q.connection_id ? `/c/${q.connection_id}/sql?saved=${q.id}` : '/saved'}>{q.name}</Link>{q.run_count > 0 && <span className="faint small nowrap">{q.run_count}×</span>}</li>
            ))}</ul>
          )}
        </Card>
      </div>
    </>
  )
}

function Tile({ icon, n, label, tone = '' }: { icon: string; n: number | string; label: string; tone?: string }) {
  return <div className={`tile ${tone}`}><Icon name={icon} size={16} /><span className="tile-n tnum">{n}</span><span className="tile-l">{label}</span></div>
}

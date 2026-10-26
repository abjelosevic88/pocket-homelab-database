// Left column: the new-connection button, sections, favourites and recent connections — the Pocket Calendar sidebar.
import { useState } from 'react'
import { Link, NavLink } from 'react-router-dom'
import { useApp } from '../lib/app-context'
import type { Connection } from '../lib/types'
import { statusOf, STATUS_LABEL } from '../lib/db'
import { Icon } from './icons'
import { createItems, type CreateKind } from './Layout'
import { EngineBadge } from './EngineBadge'
import { type Anchor, Menu, rectOf } from './ui'

export function Sidebar({ onCreate, onNavigate, conns }: { onCreate: (k: CreateKind) => void; onNavigate: () => void; conns: Connection[] | null }) {
  const { status, settings, user } = useApp()
  const [menu, setMenu] = useState<Anchor | null>(null)
  const title = settings?.title || status?.title || 'Pocket Database'
  const list = conns || []
  const down = list.filter(c => statusOf(c) === 'down').length
  const nav: { to: string; label: string; icon: string; end?: boolean; count?: string; warn?: boolean }[] = [
    { to: '/', label: 'Overview', icon: 'home', end: true, count: down ? `${down} down` : undefined, warn: down > 0 },
    { to: '/connections', label: 'Connections', icon: 'database', count: list.length ? String(list.length) : undefined },
    ...(user?.role === 'admin' ? [{ to: '/discover', label: 'On this server', icon: 'radar' }] : []),
    { to: '/saved', label: 'Saved queries', icon: 'bookmark' },
    { to: '/history', label: 'History', icon: 'history' },
    { to: '/activity', label: 'Changes', icon: 'activity' },
  ]
  const favs = list.filter(c => c.favorite)
  const recent = list.filter(c => !c.favorite && c.opened_at).sort((a, b) => b.opened_at.localeCompare(a.opened_at)).slice(0, 5)
  const row = (c: Connection) => {
    const s = statusOf(c)
    return (
      <li key={c.id} className="cal-row sb-row">
        <NavLink to={`/c/${c.id}`} onClick={onNavigate} className={({ isActive }) => `cal-check ${isActive ? 'is-active' : ''}`} title={`${c.name} — ${STATUS_LABEL[s]}`}>
          <EngineBadge engine={c.engine} size={20} />
          <span className="cal-name ellipsis">{c.name}</span>
          <span className={`sdot ${s}`} aria-label={STATUS_LABEL[s]} />
        </NavLink>
      </li>
    )
  }
  return (
    <aside className="sidebar" aria-label="Pocket Database">
      <div className="sb-brand">
        <img src="/favicon.svg" alt="" className="sb-logo" />
        <span className="sb-title">{title !== 'Pocket Database' ? title : <>Pocket <b>Database</b></>}</span>
        {status?.version && <span className="sb-version" title={`Version ${status.version}`}>{/^\d/.test(status.version) ? 'v' + status.version : status.version}</span>}
      </div>
      <button type="button" className="btn primary create-btn" aria-haspopup="menu" aria-expanded={!!menu} onClick={e => setMenu(menu ? null : rectOf(e.currentTarget))}>
        <Icon name="plus" size={16} strokeWidth={2.2} />New<Icon name="chevron-down" size={15} className="create-caret" />
      </button>
      {menu && <Menu anchor={menu} items={createItems(onCreate, user?.role === 'admin')} onClose={() => setMenu(null)} width={260} />}

      <nav className="sb-nav" aria-label="Sections">
        {nav.map(n => (
          <NavLink key={n.to} to={n.to} end={n.end} onClick={onNavigate} className={({ isActive }) => `sb-nav-item ${isActive ? 'active' : ''}`}>
            <Icon name={n.icon} size={16} />{n.label}
            {!!n.count && <span className={`sb-count ${n.warn ? 'warn' : ''}`}>{n.count}</span>}
          </NavLink>
        ))}
      </nav>

      {favs.length > 0 && (
        <section className="sb-sec">
          <h4 className="sb-h"><span className="sb-h-btn">Favourites</span></h4>
          <ul className="cal-list">{favs.map(row)}</ul>
        </section>
      )}
      {recent.length > 0 && (
        <section className="sb-sec">
          <h4 className="sb-h"><span className="sb-h-btn">Recently opened</span></h4>
          <ul className="cal-list">{recent.map(row)}</ul>
        </section>
      )}
      {conns && list.length === 0 && (
        <section className="sb-sec"><p className="sb-empty">No connections yet. {user?.role === 'admin' ? <Link to="/discover" onClick={onNavigate}>Find the databases on this server</Link> : 'Add one with New.'}</p></section>
      )}

      <div className="sb-foot">
        <Link to="/settings" className="sb-foot-link" onClick={onNavigate}><Icon name="settings" size={15} />Settings</Link>
        <button type="button" className="sb-foot-link" onClick={() => { onNavigate(); window.dispatchEvent(new CustomEvent('pdb:shortcuts')) }}><Icon name="keyboard" size={15} />Shortcuts</button>
      </div>
    </aside>
  )
}

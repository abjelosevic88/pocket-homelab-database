// The app shell: sidebar, top bar, search palette, shortcuts — the Pocket Calendar layout.
import { createContext, useCallback, useContext, useEffect, useRef, useState } from 'react'
import { Outlet, useLocation, useNavigate } from 'react-router-dom'
import { api, V1 } from '../lib/api'
import { useApp } from '../lib/app-context'
import type { Connection } from '../lib/types'
import { isSQL } from '../lib/db'
import { Icon } from './icons'
import { Sidebar } from './Sidebar'
import { CommandPalette, type PaletteCommand } from './CommandPalette'
import { ShortcutsSheet } from './ShortcutsSheet'
import { ConnForm } from './ConnForm'
import { Avatar, type Anchor, type MenuItem, Menu, MenuButton, modalOpen, rectOf, useIsMobile, useMediaQuery } from './ui'
import { AskButton } from './AskButton'

const SIDEBAR_KEY = 'pdb-sidebar'

/** Something was added or changed from the shell (or a page): the lists reload. */
export const announceChange = () => window.dispatchEvent(new CustomEvent('pdb:changed'))
export function useOnChange(cb: () => void) {
  const ref = useRef(cb)
  ref.current = cb
  useEffect(() => {
    const h = () => ref.current()
    window.addEventListener('pdb:changed', h)
    return () => window.removeEventListener('pdb:changed', h)
  }, [])
}

/** The page's own names for the top bar breadcrumb ("Shop", "orders"). */
const CrumbCtx = createContext<(t: string[]) => void>(() => {})
export function useCrumb(...titles: (string | undefined)[]) {
  const set = useContext(CrumbCtx)
  const key = titles.filter(Boolean).join('\u0000')
  useEffect(() => { set(key ? key.split('\u0000') : []); return () => set([]) }, [key, set])
}

/** Every connection of the person, for the sidebar, the palette and pickers; reload() after a change. */
const ConnsCtx = createContext<{ conns: Connection[] | null; reload: () => void }>({ conns: null, reload: () => {} })
export const useConns = () => useContext(ConnsCtx)

export type CreateKind = 'connection' | 'discover' | 'upload' | 'query'

/** Open a create action from anywhere (a page's button). */
export const openCreate = (k: CreateKind) => window.dispatchEvent(new CustomEvent('pdb:create', { detail: { kind: k } }))

/** The new-thing menu: the sidebar's button and the phone's + button. */
export function createItems(onCreate: (k: CreateKind) => void, admin: boolean): MenuItem[] {
  return [
    { label: 'Add a connection…', icon: 'plus', hint: 'N', onSelect: () => onCreate('connection') },
    { label: 'Find databases on this server', icon: 'radar', hint: 'D', onSelect: () => onCreate('discover'), disabled: !admin },
    { label: 'Open a SQLite file…', icon: 'upload', onSelect: () => onCreate('upload') },
    'sep',
    { label: 'New SQL query', icon: 'code', hint: 'Q', onSelect: () => onCreate('query') },
  ]
}

export default function Layout() {
  const navigate = useNavigate()
  const loc = useLocation()
  const { user } = useApp()
  const admin = user?.role === 'admin'
  const mobile = useIsMobile()
  const narrow = useMediaQuery('(max-width: 1099px)')
  const drawer = mobile || narrow
  const [sidebarOpen, setSidebarOpen] = useState(() => !drawer && localStorage.getItem(SIDEBAR_KEY) !== '0')
  useEffect(() => { setSidebarOpen(!drawer && localStorage.getItem(SIDEBAR_KEY) !== '0') }, [drawer])
  const drawerOpenedAt = useRef(0)
  const toggleSidebar = () => setSidebarOpen(o => { drawerOpenedAt.current = performance.now(); const n = !o; if (!drawer) localStorage.setItem(SIDEBAR_KEY, n ? '1' : '0'); return n })
  const closeDrawer = () => { if (drawer) setSidebarOpen(false) }
  useEffect(() => { document.querySelector('.content')?.scrollTo({ top: 0 }) }, [loc.pathname])

  const [crumb, setCrumbRaw] = useState<string[]>([])
  const setCrumb = useCallback((t: string[]) => setCrumbRaw(t), [])

  const [conns, setConns] = useState<Connection[] | null>(null)
  const load = useCallback(() => { api.get<Connection[]>(`${V1}/connections`).then(setConns).catch(() => {}) }, [])
  useEffect(() => { load(); const t = setInterval(load, 60000); return () => clearInterval(t) }, [load])
  useOnChange(load)

  const [palette, setPalette] = useState(false)
  const [shortcuts, setShortcuts] = useState(false)
  const [modal, setModal] = useState<{ kind: 'connection' | 'upload' } | null>(null)
  const [fabMenu, setFabMenu] = useState<Anchor | null>(null)

  const lastSQL = () => [...(conns || [])].filter(c => isSQL(c.engine)).sort((a, b) => (b.opened_at || '').localeCompare(a.opened_at || ''))[0]
  const create = (k: CreateKind) => {
    closeDrawer()
    switch (k) {
      case 'connection': setModal({ kind: 'connection' }); break
      case 'upload': setModal({ kind: 'upload' }); break
      case 'discover': navigate('/discover'); break
      case 'query': { const c = lastSQL(); if (c) navigate(`/c/${c.id}/sql`); else setPalette(true); break }
    }
  }
  const createRef = useRef(create)
  createRef.current = create

  useEffect(() => {
    const open = () => setShortcuts(true)
    const make = (e: Event) => { const d = (e as CustomEvent<{ kind: CreateKind }>).detail; createRef.current(d.kind) }
    window.addEventListener('pdb:shortcuts', open)
    window.addEventListener('pdb:create', make)
    return () => { window.removeEventListener('pdb:shortcuts', open); window.removeEventListener('pdb:create', make) }
  }, [])
  useEffect(() => {
    const h = (e: KeyboardEvent) => {
      const t = e.target as HTMLElement
      if ((e.metaKey || e.ctrlKey) && e.key.toLowerCase() === 'k') { e.preventDefault(); setPalette(p => !p); return }
      if (e.metaKey || e.ctrlKey || e.altKey) return
      if (t.closest('input, textarea, select, [contenteditable="true"]') || modalOpen() || document.querySelector('.menu, .popover, .sel-pop')) return
      const go = (to: string) => { e.preventDefault(); navigate(to) }
      const mk = (k: CreateKind) => { e.preventDefault(); createRef.current(k) }
      switch (e.key) {
        case '/': e.preventDefault(); setPalette(true); break
        case '?': e.preventDefault(); setShortcuts(true); break
        case 'n': mk('connection'); break
        case 'q': mk('query'); break
        case 'd': if (admin) mk('discover'); break
        case 'h': go('/'); break
        case 'c': go('/connections'); break
        case 's': go('/saved'); break
        case 'y': go('/history'); break
        case 'a': go('/activity'); break
        case ',': go('/settings'); break
        case '[': e.preventDefault(); toggleSidebar(); break
      }
    }
    window.addEventListener('keydown', h)
    return () => window.removeEventListener('keydown', h)
  }) // eslint-disable-line react-hooks/exhaustive-deps

  const commands: PaletteCommand[] = [
    { id: 'add', label: 'Add a connection', icon: 'plus', hint: 'N', keywords: 'new server postgres mysql redis sqlite', run: () => create('connection') },
    ...(admin ? [{ id: 'discover', label: 'Find databases on this server', icon: 'radar', hint: 'D', keywords: 'discover docker containers scan sqlite files', run: () => create('discover') }] : []),
    { id: 'upload', label: 'Open a SQLite file', icon: 'upload', keywords: 'upload file db', run: () => create('upload') },
    { id: 'query', label: 'New SQL query', icon: 'code', hint: 'Q', keywords: 'sql editor run', run: () => create('query') },
    { id: 'home', label: 'Overview', icon: 'home', hint: 'H', run: () => navigate('/') },
    { id: 'conns', label: 'Connections', icon: 'database', hint: 'C', run: () => navigate('/connections') },
    { id: 'saved', label: 'Saved queries', icon: 'bookmark', hint: 'S', run: () => navigate('/saved') },
    { id: 'history', label: 'History', icon: 'history', hint: 'Y', keywords: 'queries run before', run: () => navigate('/history') },
    { id: 'activity', label: 'Changes', icon: 'activity', hint: 'A', keywords: 'activity log audit edits snapshots', run: () => navigate('/activity') },
    { id: 'settings', label: 'Settings', icon: 'settings', hint: ',', run: () => navigate('/settings') },
    { id: 'safety', label: 'Settings: safety', icon: 'shield', keywords: 'pause snapshots confirm', run: () => navigate('/settings/safety') },
    { id: 'watch', label: 'Settings: watching and alerts', icon: 'bell', keywords: 'notify down push', run: () => navigate('/settings/watching') },
    { id: 'shortcuts', label: 'Keyboard shortcuts', icon: 'keyboard', hint: '?', run: () => setShortcuts(true) },
  ]

  const closeModal = () => setModal(null)
  return (
    <CrumbCtx.Provider value={setCrumb}>
      <ConnsCtx.Provider value={{ conns, reload: load }}>
        <div className={`app ${sidebarOpen ? 'with-sidebar' : 'no-sidebar'} ${mobile ? 'is-mobile' : ''} ${drawer ? 'drawer-mode' : ''}`}>
          {drawer && sidebarOpen && <div className="drawer-backdrop" onClick={e => { if (e.timeStamp - drawerOpenedAt.current > 400) setSidebarOpen(false) }} />}
          <div className={`sidebar-wrap ${sidebarOpen ? 'open' : ''}`}>
            <Sidebar onCreate={create} onNavigate={closeDrawer} conns={conns} />
          </div>
          <div className="main">
            <TopBar onMenu={toggleSidebar} onPalette={() => setPalette(true)} mobile={mobile} compact={narrow} sidebarOpen={sidebarOpen} crumb={crumb} />
            <main className="content"><div className="content-inner"><Outlet /></div></main>
          </div>
          {mobile && <button type="button" className="fab" aria-label="New" title="New" aria-haspopup="menu" aria-expanded={!!fabMenu} onClick={e => setFabMenu(fabMenu ? null : rectOf(e.currentTarget))}><Icon name="plus" size={26} strokeWidth={2.2} /></button>}
          {fabMenu && <Menu anchor={fabMenu} align="right" items={createItems(create, admin)} onClose={() => setFabMenu(null)} width={260} />}
          {palette && <CommandPalette onClose={() => setPalette(false)} commands={commands} conns={conns} />}
          {shortcuts && <ShortcutsSheet onClose={() => setShortcuts(false)} />}
          {modal && <ConnForm upload={modal.kind === 'upload'} onClose={closeModal} onSaved={c => { closeModal(); announceChange(); navigate(`/c/${c.id}`) }} />}
        </div>
      </ConnsCtx.Provider>
    </CrumbCtx.Provider>
  )
}

const SECTION: Record<string, [string, string]> = { '': ['Overview', '/'], connections: ['Connections', '/connections'], c: ['Connections', '/connections'], discover: ['On this server', '/discover'], saved: ['Saved queries', '/saved'], history: ['History', '/history'], activity: ['Changes', '/activity'] }

function TopBar({ onMenu, onPalette, mobile, compact, sidebarOpen, crumb }: { onMenu: () => void; onPalette: () => void; mobile: boolean; compact: boolean; sidebarOpen: boolean; crumb: string[] }) {
  const { user, themeMode, setThemeMode, status, setUser } = useApp()
  const navigate = useNavigate()
  const loc = useLocation()
  const [section, sectionTo] = SECTION[loc.pathname.split('/')[1] || ''] || ['', '/']
  const signOut = async () => { await api.post(`${V1}/auth/logout`).catch(() => {}); setUser(null); navigate('/login') }
  const avatarMenu = (): MenuItem[] => [
    { heading: user?.name || user?.email || '' },
    { label: 'Profile', icon: 'user', onSelect: () => navigate('/settings/profile') },
    { label: 'Settings', icon: 'settings', onSelect: () => navigate('/settings'), hint: ',' },
    { label: 'Keyboard shortcuts', icon: 'keyboard', onSelect: () => window.dispatchEvent(new CustomEvent('pdb:shortcuts')), hint: '?' },
    'sep',
    { heading: 'Appearance' },
    { label: 'System', checked: themeMode === 'system', onSelect: () => setThemeMode('system') },
    { label: 'Light', checked: themeMode === 'light', onSelect: () => setThemeMode('light') },
    { label: 'Dark', checked: themeMode === 'dark', onSelect: () => setThemeMode('dark') },
    ...(status?.auth_mode === 'local' ? ['sep' as const, { label: 'Sign out', icon: 'log-out', onSelect: signOut }] : []),
  ]
  const connPath = loc.pathname.match(/^\/c\/(\d+)/)
  const shown = mobile ? crumb.slice(-1) : crumb
  return (
    <header className="topbar">
      <button type="button" className="btn ghost icon-only" onClick={onMenu} aria-label={sidebarOpen ? 'Hide the sidebar' : 'Show the sidebar'} title="Menu ([)"><Icon name={mobile || compact ? 'menu' : 'sidebar'} size={18} /></button>
      <h1 className="range-title">
        {crumb.length === 0 ? section : <>
          {!mobile && <><button type="button" className="crumb" onClick={() => navigate(sectionTo)}>{section}</button><Icon name="chevron-right" size={16} className="crumb-sep" /></>}
          {shown.map((t, i) => {
            const last = i === shown.length - 1
            const toConn = !last && connPath && i === 0
            return <span key={i} className="crumb-part">{toConn ? <button type="button" className="crumb" onClick={() => navigate(`/c/${connPath![1]}`)}>{t}</button> : <span className="ellipsis">{t}</span>}{!last && <Icon name="chevron-right" size={16} className="crumb-sep" />}</span>
          })}
        </>}
      </h1>
      <span className="spacer" />
      <button type="button" className="btn ghost icon-only" onClick={onPalette} aria-label="Search" title="Search (⌘K or /)"><Icon name="search" size={18} /></button>
      <AskButton />
      <MenuButton label="Account" className="avatar-btn" align="right" width={220} items={avatarMenu}>
        <Avatar name={user?.name} email={user?.email} size={30} />
      </MenuButton>
    </header>
  )
}

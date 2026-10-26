// Add or edit a connection: the engine, where it is, how to sign in, and what the app may do with it.
import React, { useState } from 'react'
import { api, uploadForm, V1 } from '../lib/api'
import { useApp } from '../lib/app-context'
import type { Connection, Engine } from '../lib/types'
import { ENGINES, engineOf } from '../lib/db'
import { fmtBytes } from '../lib/format'
import { Field, Modal, Segmented, Switch, useToast } from './ui'
import { Icon } from './icons'
import { Select } from './Select'
import { EngineBadge } from './EngineBadge'

const COLORS = ['', '#3d5a99', '#0f766e', '#15803d', '#ca8a04', '#c2410c', '#dc2626', '#c026d3', '#7c3aed', '#475569']

export function ConnForm({ conn, upload, onClose, onSaved }: { conn?: Connection; upload?: boolean; onClose: () => void; onSaved: (c: Connection) => void }) {
  const { user, settings } = useApp()
  const { toast } = useToast()
  const admin = user?.role === 'admin'
  const [f, setF] = useState({
    name: conn?.name || '', engine: (conn?.engine || (upload ? 'sqlite' : 'postgres')) as Engine, host: conn?.host || '', port: conn?.port ? String(conn.port) : '',
    username: conn?.username || '', password: conn?.has_password ? '••••••••' : '', database: conn?.database || '', ssl_mode: conn?.ssl_mode || '',
    path: conn?.host_path || '', container: conn?.container || '', group: conn?.group || '', color: conn?.color || '', notes: conn?.notes || '',
    allow_writes: conn ? conn.allow_writes : false, favorite: conn?.favorite || false, monitor: conn ? conn.monitor : true,
  })
  const [sqliteHow, setSqliteHow] = useState<'upload' | 'path'>(upload || !admin ? 'upload' : conn?.source === 'upload' ? 'upload' : 'path')
  const [file, setFile] = useState<File | null>(null)
  const [test, setTest] = useState<{ ok: boolean; text: string } | null>(null)
  const [busy, setBusy] = useState(false)
  const set = (patch: Partial<typeof f>) => { setF({ ...f, ...patch }); setTest(null) }
  const e = engineOf(f.engine)
  const isFile = f.engine === 'sqlite'
  const uploading = isFile && sqliteHow === 'upload' && !conn
  const body = () => ({ ...f, port: Number(f.port) || 0, id: conn?.id })

  const runTest = async () => {
    setTest({ ok: true, text: 'Connecting…' })
    try {
      const r = await api.post<{ ok: boolean; message?: string; error?: string }>(`${V1}/connections/test`, body())
      setTest({ ok: r.ok, text: r.ok ? r.message || 'Works' : r.error || 'Failed' })
    } catch (err) { setTest({ ok: false, text: (err as Error).message }) }
  }
  const save = async (ev: React.FormEvent) => {
    ev.preventDefault(); setBusy(true)
    try {
      let c: Connection
      if (uploading) {
        if (!file) { toast('Choose the file first', 'err'); setBusy(false); return }
        c = await uploadForm<Connection>(`${V1}/connections/upload`, file, { name: f.name })
      } else if (conn) c = await api.put<Connection>(`${V1}/connections/${conn.id}`, body())
      else c = await api.post<Connection>(`${V1}/connections`, body())
      toast(conn ? 'Saved' : 'Connection added')
      onSaved(c)
    } catch (err) { toast((err as Error).message, 'err') } finally { setBusy(false) }
  }
  return (
    <Modal title={conn ? `Edit ${conn.name}` : uploading ? 'Open a SQLite file' : 'Add a connection'} onClose={onClose} size="lg">
      <form onSubmit={save} className="conn-form">
        {!conn && !upload && (
          <div className="engine-pick" role="radiogroup" aria-label="Database">
            {ENGINES.map(x => (
              <button key={x.id} type="button" role="radio" aria-checked={f.engine === x.id} className={`engine-opt ${f.engine === x.id ? 'on' : ''}`} onClick={() => set({ engine: x.id, port: '' })}>
                <EngineBadge engine={x.id} size={28} /><span><b>{x.name}</b><small>{x.hint}</small></span>
              </button>
            ))}
          </div>
        )}
        {isFile && !conn && admin && !upload && (
          <Field group label="The file"><Segmented value={sqliteHow} onChange={setSqliteHow} options={[{ value: 'path', label: 'A file on this server' }, { value: 'upload', label: 'Upload a copy' }]} /></Field>
        )}
        {uploading ? (
          <Field label="SQLite file" help={`Your own copy, kept by Pocket Database (up to ${fmtBytes(settings?.upload_max || 1 << 30)}). Changes to it change nothing else.`}>
            <label className="file-drop">
              <Icon name="upload" size={18} /><span className="grow ellipsis">{file ? `${file.name} · ${fmtBytes(file.size)}` : 'Choose a .db, .sqlite or .sqlite3 file…'}</span>
              <input type="file" hidden accept=".db,.sqlite,.sqlite3,.db3,.s3db,application/vnd.sqlite3,application/x-sqlite3" onChange={ev => { const x = ev.target.files?.[0] || null; setFile(x); if (x && !f.name) set({ name: x.name.replace(/\.[^.]+$/, '') }) }} />
            </label>
          </Field>
        ) : isFile ? (
          conn?.source === 'upload' || conn?.source === 'demo'
            ? <p className="muted small">An uploaded copy kept by Pocket Database.</p>
            : <Field label="Path on this server" help={admin ? 'The full path, e.g. /home/me/app/data/app.db. Find files under On this server.' : 'Only an admin can open files on the server.'}><input value={f.path} onChange={ev => set({ path: ev.target.value })} placeholder="/srv/app/data/app.db" disabled={!admin} required className="mono" /></Field>
        ) : (
          <>
            <div className="conn-grid">
              <Field label={f.container ? 'Address (used when the container is not found)' : 'Server address'} className="span2"><input value={f.host} onChange={ev => set({ host: ev.target.value })} placeholder={f.container ? 'found from the container' : 'db.example.com or 10.0.0.5'} required={!f.container} autoComplete="off" /></Field>
              <Field label="Port"><input value={f.port} onChange={ev => set({ port: ev.target.value.replace(/\D/g, '') })} placeholder={String(e.port)} inputMode="numeric" /></Field>
              {f.engine !== 'redis' && <Field label="Database" help={f.engine === 'postgres' ? 'Empty = postgres' : 'Empty = pick one later'}><input value={f.database} onChange={ev => set({ database: ev.target.value })} autoComplete="off" /></Field>}
              {f.engine === 'redis' && <Field label="Database number"><input value={f.database} onChange={ev => set({ database: ev.target.value.replace(/\D/g, '') })} placeholder="0" inputMode="numeric" /></Field>}
              <Field label={f.engine === 'redis' ? 'User (Redis 6+, optional)' : 'User'}><input value={f.username} onChange={ev => set({ username: ev.target.value })} autoComplete="off" /></Field>
              <Field label="Password" help={conn?.has_password ? 'Saved; type to replace it.' : 'Kept encrypted on the server.'}><input type="password" value={f.password} onChange={ev => set({ password: ev.target.value })} onFocus={ev => { if (ev.target.value.includes('••')) set({ password: '' }) }} autoComplete="new-password" /></Field>
              <Field group label="Encryption" className="span2">
                <Select ariaLabel="Encryption" value={f.ssl_mode} onChange={v => set({ ssl_mode: v })} options={[{ value: '', label: 'Use it when the server offers it' }, { value: 'disable', label: 'Off' }, { value: 'require', label: 'Required' }, { value: 'verify-full', label: 'Required, with a checked certificate' }]} />
              </Field>
            </div>
            {admin && (f.container || conn?.container) && <Field label="Docker container" help="The address is looked up again on every connect, so a recreated container keeps working."><input value={f.container} onChange={ev => set({ container: ev.target.value })} className="mono" /></Field>}
          </>
        )}
        <div className="conn-grid">
          <Field label="Name" className="span2"><input value={f.name} onChange={ev => set({ name: ev.target.value })} placeholder={isFile ? 'Named after the file' : 'Named after the database'} maxLength={80} /></Field>
          <Field label="Group" help="Connections with the same group sit together."><input value={f.group} onChange={ev => set({ group: ev.target.value })} placeholder="e.g. photos" /></Field>
        </div>
        <Field group label="Colour">
          <div className="swatches">{COLORS.map(c => <button key={c || 'none'} type="button" className={`swatch ${f.color === c ? 'on' : ''}`} style={{ background: c || undefined }} aria-label={c || 'Engine colour'} onClick={() => set({ color: c })}>{!c && <Icon name="x" size={12} />}</button>)}</div>
        </Field>
        <div className="conn-switches">
          <label className="switch-row"><Switch checked={f.allow_writes} onChange={v => set({ allow_writes: v })} label="Allow changes" /><span><b>Allow changes</b><small>Off: read-only — browsing and queries only. On: edits and statements that change something, each confirmed{isFile ? '; a file an app uses is changed with the app paused' : ''}.</small></span></label>
          <label className="switch-row"><Switch checked={f.monitor} onChange={v => set({ monitor: v })} label="Watch" /><span><b>Watch it</b><small>Checked every five minutes; you hear when it goes down. Its size goes into the growth chart.</small></span></label>
          <label className="switch-row"><Switch checked={f.favorite} onChange={v => set({ favorite: v })} label="Favourite" /><span><b>Favourite</b><small>Kept at the top of the sidebar.</small></span></label>
        </div>
        <Field label="Notes"><textarea rows={2} value={f.notes} onChange={ev => set({ notes: ev.target.value })} placeholder="What it is for, who to ask…" /></Field>
        {test && <div className={`conn-test ${test.ok ? 'ok' : 'bad'}`}><Icon name={test.ok ? 'check-circle' : 'alert'} size={15} />{test.text}</div>}
        <div className="form-actions">
          {!uploading && <button type="button" className="btn" onClick={runTest}><Icon name="zap" size={15} />Test</button>}
          <span className="spacer" />
          <button type="button" className="btn" onClick={onClose}>Cancel</button>
          <button className="btn primary" disabled={busy}>{busy ? 'Saving…' : conn ? 'Save' : uploading ? 'Open' : 'Add connection'}</button>
        </div>
      </form>
    </Modal>
  )
}

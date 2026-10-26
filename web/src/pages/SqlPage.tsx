// The SQL editor for one connection: write (or ask AI to write), run, see the results, save.
import { useEffect, useRef, useState } from 'react'
import { Link, useSearchParams } from 'react-router-dom'
import { api, ApiError, V1 } from '../lib/api'
import { useApp } from '../lib/app-context'
import type { QueryResponse, Result, SavedQuery } from '../lib/types'
import { tableSQL } from '../lib/db'
import { Field, Loading, MenuButton, Modal, Spinner, useAsync, useToast } from '../components/ui'
import { Icon } from '../components/icons'
import { SqlEditor, type SqlEditorHandle } from '../components/SqlEditor'
import { ConfirmRun, Results } from '../components/Results'
import { announceChange } from '../components/Layout'
import { GateDialog } from './TableView'
import { useWs } from './Connection'

const draftKey = (id: number) => `pdb-sql-${id}`

export default function SqlPage() {
  const { conn, db, schemas, reloadSafety, reloadSchemas } = useWs()
  const { settings } = useApp()
  const { toast } = useToast()
  const [sp, setSp] = useSearchParams()
  const editor = useRef<SqlEditorHandle>(null)
  const [sql, setSql] = useState(() => localStorage.getItem(draftKey(conn.id)) || '')
  const [savedId, setSavedId] = useState<number | null>(null)
  const [results, setResults] = useState<Result[] | null>(null)
  const [running, setRunning] = useState(false)
  const [took, setTook] = useState<number | null>(null)
  const [confirm, setConfirm] = useState<{ sql: string; reasons: { statement: string; reason: string }[] } | null>(null)
  const [gate, setGate] = useState<{ code: string; msg: string } | null>(null)
  const [save, setSave] = useState(false)
  const [ask, setAsk] = useState('')
  const [asking, setAsking] = useState('')
  const [note, setNote] = useState('')
  const [explain, setExplain] = useState<string | null>(null)
  const { data: saved, reload: reloadSaved } = useAsync(() => api.get<SavedQuery[]>(`${V1}/saved?connection=${conn.id}`), [conn.id])

  // ?q= opens a query, ?saved= a saved one
  useEffect(() => {
    const q = sp.get('q'), sid = sp.get('saved')
    if (q) { setSql(q); setSavedId(null) }
    if (sid && saved) { const s = saved.find(x => String(x.id) === sid); if (s) { setSql(s.body); setSavedId(s.id) } }
    if (q || (sid && saved)) { const n = new URLSearchParams(sp); n.delete('q'); n.delete('saved'); setSp(n, { replace: true }) }
  }, [sp, saved]) // eslint-disable-line react-hooks/exhaustive-deps
  useEffect(() => { localStorage.setItem(draftKey(conn.id), sql) }, [sql, conn.id])

  const run = async (text: string, confirmed = false) => {
    const body = text.trim()
    if (!body) { toast('Type a query first', 'err'); return }
    setRunning(true)
    try {
      const r = await api.post<QueryResponse>(`${V1}/connections/${conn.id}/query`, { db, sql: body, confirm: confirmed, saved_id: savedId || 0 })
      if (r.needs_confirm) { setConfirm({ sql: body, reasons: r.reasons || [] }); return }
      setResults(r.results || []); setTook(r.duration_ms ?? null)
      if (!r.read_only) { reloadSafety(); announceChange(); if ((r.results || []).some(x => x.kind === 'ddl' && !x.error)) reloadSchemas() }
    } catch (e) {
      const ae = e as ApiError
      if (ae.data?.code) setGate({ code: String(ae.data.code), msg: ae.message })
      else toast(ae.message, 'err')
    } finally { setRunning(false) }
  }
  const ai = async (mode: 'write' | 'explain' | 'fix', extra: { prompt?: string; error?: string; sql?: string } = {}) => {
    setAsking(mode); setNote('')
    try {
      const r = await api.post<{ ok: boolean; error?: string; sql?: string; note?: string; text?: string }>(`${V1}/ai/sql`, { connection_id: conn.id, db, mode, sql: extra.sql ?? sql, prompt: extra.prompt, error: extra.error })
      if (!r.ok) { toast(r.error || 'The AI did not answer', 'err'); return }
      if (mode === 'explain') { setExplain(r.text || r.note || ''); return }
      if (r.sql) { setSql(r.sql); setSavedId(null); setNote(r.note || ''); editor.current?.focus() } else if (r.text) setExplain(r.text)
    } catch (e) { toast((e as Error).message, 'err') } finally { setAsking('') }
  }
  const tables = (schemas || []).flatMap(s => s.objects)
  const current = saved?.find(s => s.id === savedId)
  const exportURL = (r: Result, format: 'csv' | 'json') => r.kind === 'read' && /^\s*(select|with|values|table|show|pragma|explain)\b/i.test(r.statement)
    ? `${V1}/connections/${conn.id}/export?${new URLSearchParams({ db, format, sql: r.statement })}` : null
  return (
    <div className="sql-page">
      <div className="ask-bar">
        <Icon name="sparkles" size={16} />
        <input value={ask} onChange={e => setAsk(e.target.value)} placeholder={`Ask for a query: “orders per month this year”, “customers in Sarajevo without orders”…`} aria-label="Describe the query"
          onKeyDown={e => { if (e.key === 'Enter' && ask.trim()) { e.preventDefault(); ai('write', { prompt: ask }) } }} />
        <button type="button" className="btn sm" disabled={!ask.trim() || !!asking} onClick={() => ai('write', { prompt: ask })}>{asking === 'write' ? <Spinner /> : 'Write it'}</button>
      </div>
      {note && <div className="ai-note"><Icon name="sparkles" size={13} />{note}<button type="button" className="toast-x" aria-label="Dismiss" onClick={() => setNote('')}><Icon name="x" size={12} /></button></div>}
      <div className="editor-card">
        <div className="editor-tools">
          <button type="button" className="btn primary sm" disabled={running} onClick={() => run(editor.current?.selection().trim() || sql)} title="Run (⌘↵) — the selection, or everything"><Icon name="play" size={14} />{running ? 'Running…' : 'Run'}</button>
          <button type="button" className="btn sm hide-sm" disabled={running} onClick={() => run(editor.current?.current() || sql)} title="Run the statement under the cursor (⇧↵)">Run this one</button>
          <MenuButton label="Insert a table" className="btn sm ghost" items={tables.length ? tables.slice(0, 60).map(t => ({ label: t.name, icon: t.kind === 'table' ? 'table' : 'eye', onSelect: () => editor.current?.insert(tableSQL(conn.engine, t.schema, t.name)) })) : [{ heading: 'No tables' }]}><Icon name="table" size={14} /><span className="hide-sm">Tables</span></MenuButton>
          <button type="button" className="btn sm ghost" disabled={!sql.trim() || !!asking} onClick={() => ai('explain')} title="Explain this query in plain words">{asking === 'explain' ? <Spinner /> : <Icon name="help" size={14} />}<span className="hide-sm">Explain</span></button>
          <span className="spacer" />
          {current && <span className="muted small ellipsis hide-sm">{current.name}</span>}
          <MenuButton label="Saved queries" className="btn sm ghost" align="right" width={280} items={[
            { label: current ? 'Save changes' : 'Save…', icon: 'bookmark', onSelect: () => setSave(true) },
            ...(current ? [{ label: 'Save as new…', icon: 'plus', onSelect: () => { setSavedId(null); setSave(true) } }] : []),
            'sep', { heading: (saved || []).length ? 'Open' : 'Nothing saved yet' },
            ...(saved || []).slice(0, 15).map(s => ({ label: s.name, icon: s.favorite ? 'star' : 'bookmark', onSelect: () => { setSql(s.body); setSavedId(s.id) } })),
          ]}><Icon name="bookmark" size={14} /><span className="hide-sm">Saved</span></MenuButton>
          <button type="button" className="btn sm ghost icon-only" title="Clear" aria-label="Clear the editor" onClick={() => { setSql(''); setSavedId(null); setResults(null); editor.current?.focus() }}><Icon name="x" size={14} /></button>
        </div>
        <SqlEditor ref={editor} value={sql} onChange={v => setSql(v)} onRun={t => run(t)} onSave={() => setSave(true)} fontSize={settings?.editor_font || 13}
          placeholder={conn.engine === 'sqlite' ? 'SELECT * FROM sqlite_schema;' : conn.engine === 'mysql' ? 'SHOW TABLES;' : 'SELECT * FROM pg_stat_activity;'} />
        <div className="editor-foot muted small">
          <span>{conn.allow_writes ? 'Changes are on: statements that change something ask first, and run as one transaction.' : 'Read-only: only queries that read run here.'}</span>
          <span className="spacer" />
          {took !== null && <span className="tnum">{took} ms</span>}
          <span className="hide-sm"><kbd>⌘</kbd><kbd>↵</kbd> run · <kbd>⇧</kbd><kbd>↵</kbd> this one</span>
        </div>
      </div>
      {running && !results && <Loading label="Running…" />}
      {results && <Results results={results} density={settings?.density} exportURL={exportURL} onFix={r => ai('fix', { sql: r.statement, error: r.error })} />}
      {confirm && <ConfirmRun reasons={confirm.reasons} onCancel={() => setConfirm(null)} onRun={() => { const c = confirm; setConfirm(null); run(c.sql, true) }} />}
      {gate && <GateDialog gate={gate} onClose={() => setGate(null)} onPaused={() => setGate(null)} />}
      {save && <SaveModal sql={sql} db={db} existing={current} onClose={() => setSave(false)} onSaved={s => { setSave(false); setSavedId(s.id); reloadSaved() }} />}
      {explain !== null && <Modal title="What this query does" onClose={() => setExplain(null)} size="lg"><div className="ai-text">{explain.split('\n').map((l, i) => <p key={i}>{l}</p>)}</div></Modal>}
      <p className="muted small mt">All queries you ran are in <Link to="/history">History</Link>; changes are logged under <Link to="/activity">Changes</Link>.</p>
    </div>
  )
}

function SaveModal({ sql, db, existing, onClose, onSaved }: { sql: string; db: string; existing?: SavedQuery; onClose: () => void; onSaved: (s: SavedQuery) => void }) {
  const { conn } = useWs()
  const { toast } = useToast()
  const [f, setF] = useState({ name: existing?.name || '', description: existing?.description || '', favorite: existing?.favorite || false, anywhere: existing ? existing.connection_id === null : false })
  const submit = async (e: React.FormEvent) => {
    e.preventDefault()
    const body = { name: f.name, description: f.description, favorite: f.favorite, body: sql, database: db, connection_id: f.anywhere ? null : conn.id }
    try {
      const s = existing ? await api.put<SavedQuery>(`${V1}/saved/${existing.id}`, body) : await api.post<SavedQuery>(`${V1}/saved`, body)
      toast('Saved'); onSaved(s)
    } catch (err) { toast((err as Error).message, 'err') }
  }
  return (
    <Modal title={existing ? 'Save changes' : 'Save the query'} onClose={onClose} size="sm">
      <form onSubmit={submit} className="grid" style={{ gap: 14 }}>
        <Field label="Name"><input value={f.name} onChange={e => setF({ ...f, name: e.target.value })} required autoFocus maxLength={120} /></Field>
        <Field label="What it shows (optional)"><input value={f.description} onChange={e => setF({ ...f, description: e.target.value })} /></Field>
        <label className="row small"><input type="checkbox" checked={f.favorite} onChange={e => setF({ ...f, favorite: e.target.checked })} />Favourite</label>
        <label className="row small"><input type="checkbox" checked={f.anywhere} onChange={e => setF({ ...f, anywhere: e.target.checked })} />For every connection, not only {conn.name}</label>
        <div className="form-actions"><button type="button" className="btn" onClick={onClose}>Cancel</button><button className="btn primary">Save</button></div>
      </form>
    </Modal>
  )
}

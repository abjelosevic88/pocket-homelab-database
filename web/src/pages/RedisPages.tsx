// Redis: browse keys by pattern and read each one by its type; a console for everything else.
import { useEffect, useRef, useState } from 'react'
import { api, ApiError, V1 } from '../lib/api'
import type { KeyInfo, KeyValue } from '../lib/types'
import { asJSON, fmtTTL } from '../lib/db'
import { fmtBytes } from '../lib/format'
import { Confirm, Empty, Field, Loading, Modal, useDebounce, useIsMobile, useToast, copyText } from '../components/ui'
import { Icon } from '../components/icons'
import { ConfirmRun } from '../components/Results'
import { announceChange } from '../components/Layout'
import { GateDialog } from './TableView'
import { useWs } from './Connection'

const TYPE_COLOR: Record<string, string> = { string: '#3d5a99', hash: '#0f766e', list: '#c2410c', set: '#7c3aed', zset: '#c026d3', stream: '#15803d' }

export function RedisKeys() {
  const { conn, db } = useWs()
  const mobile = useIsMobile()
  const [pattern, setPattern] = useState('')
  const dp = useDebounce(pattern, 350)
  const [keys, setKeys] = useState<KeyInfo[] | null>(null)
  const [cursor, setCursor] = useState('0')
  const [err, setErr] = useState('')
  const [busy, setBusy] = useState(false)
  const [open, setOpen] = useState<string | null>(null)
  const [seq, setSeq] = useState(0)
  const load = async (more: boolean) => {
    setBusy(true)
    try {
      const p = new URLSearchParams({ db, pattern: dp ? (dp.includes('*') || dp.includes('?') ? dp : `*${dp}*`) : '*', cursor: more ? cursor : '0', count: '200' })
      const r = await api.get<{ keys: KeyInfo[]; cursor: string }>(`${V1}/connections/${conn.id}/keys?${p}`)
      setKeys(k => more && k ? [...k, ...r.keys.filter(x => !k.some(y => y.key === x.key))] : r.keys); setCursor(r.cursor); setErr('')
    } catch (e) { setErr((e as Error).message) } finally { setBusy(false) }
  }
  useEffect(() => { load(false) }, [conn.id, db, dp, seq]) // eslint-disable-line react-hooks/exhaustive-deps
  return (
    <div className={`kv ${open && mobile ? 'kv-open' : ''}`}>
      <aside className="kv-list">
        <div className="tree-search"><Icon name="search" size={14} /><input value={pattern} onChange={e => setPattern(e.target.value)} placeholder="Find keys (user:*)" aria-label="Find keys" />
          <button type="button" className="btn ghost icon-only sm" title="Reload" aria-label="Reload" onClick={() => setSeq(s => s + 1)}><Icon name="refresh" size={13} /></button></div>
        {err ? <p className="bad small pad">{err}</p> : !keys ? <Loading /> : keys.length === 0 ? <p className="muted small pad">No keys{pattern ? ' match' : ' in this database'}.</p> : (
          <div className="tree">
            {keys.map(k => (
              <button key={k.key} type="button" className={`tree-item kv-item ${open === k.key ? 'active' : ''}`} onClick={() => setOpen(k.key)} title={k.key}>
                <span className="kv-type" style={{ background: TYPE_COLOR[k.type] || '#7a7a85' }}>{k.type.slice(0, 3)}</span>
                <span className="ellipsis grow mono">{k.key}</span>
                {k.ttl_ms >= 0 && <span className="tree-n" title="Expires in">{fmtTTL(k.ttl_ms)}</span>}
              </button>
            ))}
            {cursor !== '0' && <button type="button" className="btn sm ghost kv-more" disabled={busy} onClick={() => load(true)}>{busy ? 'Loading…' : 'More keys'}</button>}
          </div>
        )}
      </aside>
      <section className="kv-main">
        {open ? <KeyView key={open + seq} name={open} onBack={() => setOpen(null)} onGone={() => { setOpen(null); setSeq(s => s + 1) }} />
          : <Empty icon="key" title="Pick a key">Its value is shown here by type: text, fields of a hash, items of a list or set, a sorted set with scores, the latest entries of a stream.</Empty>}
      </section>
    </div>
  )
}

function KeyView({ name, onBack, onGone }: { name: string; onBack: () => void; onGone: () => void }) {
  const { conn, db } = useWs()
  const { toast } = useToast()
  const mobile = useIsMobile()
  const [kv, setKv] = useState<KeyValue | null>(null)
  const [err, setErr] = useState('')
  const [edit, setEdit] = useState<{ field?: string; value: string; ttl?: string } | null>(null)
  const [del, setDel] = useState<{ field?: string } | null>(null)
  const [gate, setGate] = useState<{ code: string; msg: string } | null>(null)
  const [seq, setSeq] = useState(0)
  useEffect(() => {
    api.get<KeyValue>(`${V1}/connections/${conn.id}/key?${new URLSearchParams({ db, key: name })}`).then(v => { setKv(v); setErr('') }).catch(e => setErr((e as Error).message))
  }, [conn.id, db, name, seq])
  const call = async (fn: () => Promise<unknown>, msg: string, after?: () => void) => {
    try { await fn(); toast(msg); announceChange(); (after || (() => setSeq(s => s + 1)))() } catch (e) {
      const ae = e as ApiError
      if (ae.data?.code) setGate({ code: String(ae.data.code), msg: ae.message }); else toast(ae.message, 'err')
    }
  }
  if (err) return <div className="callout danger"><Icon name="alert" size={15} /><span>{err}</span></div>
  if (!kv) return <Loading />
  const w = conn.allow_writes
  const text = typeof kv.value === 'string' ? kv.value : null
  const json = text ? asJSON(text) : null
  return (
    <div className="key-view">
      <div className="kv-head">
        {mobile && <button type="button" className="btn ghost icon-only" aria-label="Back" onClick={onBack}><Icon name="chevron-left" size={18} /></button>}
        <span className="kv-type big" style={{ background: TYPE_COLOR[kv.type] || '#7a7a85' }}>{kv.type}</span>
        <h2 className="mono ellipsis grow" title={kv.key}>{kv.key}</h2>
        <button type="button" className="btn ghost sm icon-only" title="Copy the name" aria-label="Copy the name" onClick={async () => toast(await copyText(kv.key) ? 'Copied' : 'Could not copy')}><Icon name="copy" size={14} /></button>
      </div>
      <div className="kv-meta muted small">
        <span>{kv.type === 'string' ? fmtBytes(kv.length) : `${kv.length.toLocaleString()} ${{ hash: 'fields', list: 'items', set: 'members', zset: 'members', stream: 'entries' }[kv.type] || 'items'}`}</span>
        <span>· expires {kv.ttl_ms < 0 ? 'never' : `in ${fmtTTL(kv.ttl_ms)}`}</span>
        <span className="spacer" />
        {w && kv.type === 'string' && <button type="button" className="btn sm" onClick={() => setEdit({ value: text || '' })}><Icon name="edit" size={13} />Edit</button>}
        {w && kv.type === 'hash' && <button type="button" className="btn sm" onClick={() => setEdit({ field: '', value: '' })}><Icon name="plus" size={13} />Field</button>}
        {w && <button type="button" className="btn sm" onClick={() => setEdit({ value: '', ttl: kv.ttl_ms < 0 ? '' : String(Math.round(kv.ttl_ms / 1000)) })}><Icon name="clock" size={13} />Expiry</button>}
        {w && <button type="button" className="btn sm danger-text" onClick={() => setDel({})}><Icon name="trash" size={13} />Delete</button>}
      </div>
      {kv.truncated && <p className="muted small">Shown in part — the first {kv.type === 'string' ? '256 KB' : '500 items'}.</p>}
      {kv.type === 'string' && <pre className="kv-value mono">{json || (text ?? JSON.stringify(kv.value))}</pre>}
      {kv.type === 'hash' && <table className="table compact"><thead><tr><th>Field</th><th>Value</th><th /></tr></thead><tbody>{(kv.value as [string, string][]).map(([f, v]) => (
        <tr key={f}><td className="mono">{f}</td><td className="mono small kv-cell">{String(v)}</td><td className="right nowrap">{w && <><button type="button" className="btn ghost sm" onClick={() => setEdit({ field: f, value: String(v) })}>Edit</button><button type="button" className="btn ghost sm danger-text" onClick={() => setDel({ field: f })}>Delete</button></>}</td></tr>
      ))}</tbody></table>}
      {(kv.type === 'list' || kv.type === 'set') && <table className="table compact"><tbody>{(kv.value as unknown[]).map((v, i) => <tr key={i}><td className="muted tnum" style={{ width: 50 }}>{kv.type === 'list' ? i : ''}</td><td className="mono small kv-cell">{String(v)}</td></tr>)}</tbody></table>}
      {kv.type === 'zset' && <table className="table compact"><thead><tr><th>Member</th><th className="right">Score</th></tr></thead><tbody>{(kv.value as [string, number][]).map(([m, s], i) => <tr key={i}><td className="mono small kv-cell">{m}</td><td className="right tnum">{s}</td></tr>)}</tbody></table>}
      {kv.type === 'stream' && <table className="table compact"><thead><tr><th>Entry</th><th>Fields</th></tr></thead><tbody>{(kv.value as { id: string; fields: Record<string, unknown> }[]).map(m => <tr key={m.id}><td className="mono small nowrap">{m.id}</td><td className="mono small kv-cell">{JSON.stringify(m.fields)}</td></tr>)}</tbody></table>}
      {!['string', 'hash', 'list', 'set', 'zset', 'stream'].includes(kv.type) && <p className="muted">This type ({kv.type}) is shown in the console: try <code>TYPE</code> and the module's own commands.</p>}
      {edit && (
        <Modal title={edit.ttl !== undefined ? 'When it expires' : edit.field !== undefined ? (edit.field ? `Edit ${edit.field}` : 'New field') : 'Edit the value'} onClose={() => setEdit(null)} size={edit.ttl !== undefined ? 'sm' : 'lg'}>
          <form className="grid" style={{ gap: 14 }} onSubmit={e => {
            e.preventDefault()
            const done = () => { setEdit(null); setSeq(s => s + 1) }
            if (edit.ttl !== undefined) call(() => api.post(`${V1}/connections/${conn.id}/key/meta`, { db, key: name, ttl_s: Number(edit.ttl) || 0 }), 'Expiry saved', done)
            else call(() => api.put(`${V1}/connections/${conn.id}/key`, { db, key: name, field: edit.field, value: edit.value, ttl_s: -1 }), 'Saved', done)
          }}>
            {edit.ttl !== undefined ? <Field label="Seconds from now" help="Empty or 0: never expires."><input value={edit.ttl} onChange={e => setEdit({ ...edit, ttl: e.target.value.replace(/\D/g, '') })} inputMode="numeric" autoFocus /></Field> : <>
              {edit.field !== undefined && <Field label="Field"><input value={edit.field} onChange={e => setEdit({ ...edit, field: e.target.value })} required className="mono" /></Field>}
              <Field label="Value"><textarea className="mono" rows={10} value={edit.value} onChange={e => setEdit({ ...edit, value: e.target.value })} autoFocus /></Field>
            </>}
            <div className="form-actions"><button type="button" className="btn" onClick={() => setEdit(null)}>Cancel</button><button className="btn primary">Save</button></div>
          </form>
        </Modal>
      )}
      {del && <Confirm title={del.field ? `Delete the field ${del.field}?` : `Delete ${name}?`} confirmLabel="Delete" message="This cannot be undone."
        onCancel={() => setDel(null)} onConfirm={() => { const d = del; setDel(null); call(() => api.del(`${V1}/connections/${conn.id}/key?${new URLSearchParams({ db, key: name, ...(d.field ? { field: d.field } : {}) })}`), 'Deleted', d.field ? undefined : onGone) }} />}
      {gate && <GateDialog gate={gate} onClose={() => setGate(null)} onPaused={() => setGate(null)} />}
    </div>
  )
}

type Line = { cmd: string; ok: boolean; out: string; ms?: number }

function fmtReply(v: unknown, indent = ''): string {
  if (v === null || v === undefined) return '(nil)'
  if (typeof v === 'string') return `"${v}"`
  if (typeof v === 'number') return `(integer) ${v}`
  if (Array.isArray(v)) return v.length === 0 ? '(empty list)' : v.map((x, i) => `${indent}${i + 1}) ${Array.isArray(x) ? '\n' + fmtReply(x, indent + '   ') : fmtReply(x, indent)}`).join('\n')
  if (typeof v === 'object') return JSON.stringify(v, null, 2)
  return String(v)
}

export function RedisConsole() {
  const { conn, db } = useWs()
  const [lines, setLines] = useState<Line[]>([])
  const [cmd, setCmd] = useState('')
  const [hist, setHist] = useState<string[]>([])
  const [hi, setHi] = useState(-1)
  const [busy, setBusy] = useState(false)
  const [confirm, setConfirm] = useState<{ line: string; reasons: { statement: string; reason: string }[] } | null>(null)
  const [gate, setGate] = useState<{ code: string; msg: string } | null>(null)
  const end = useRef<HTMLDivElement>(null)
  useEffect(() => { end.current?.scrollIntoView({ block: 'end' }) }, [lines])
  const run = async (line: string, confirmed = false) => {
    if (!line.trim()) return
    setBusy(true)
    try {
      const r = await api.post<{ ok?: boolean; result?: unknown; error?: string; duration_ms?: number; needs_confirm?: boolean; reasons?: { statement: string; reason: string }[] }>(`${V1}/connections/${conn.id}/command`, { db, line, confirm: confirmed })
      if (r.needs_confirm) { setConfirm({ line, reasons: r.reasons || [] }); return }
      setLines(l => [...l.slice(-200), { cmd: line, ok: !!r.ok, out: r.ok ? fmtReply(r.result) : `(error) ${r.error}`, ms: r.duration_ms }])
      setHist(h => [line, ...h.filter(x => x !== line)].slice(0, 50)); setHi(-1); setCmd('')
    } catch (e) {
      const ae = e as ApiError
      if (ae.data?.code) setGate({ code: String(ae.data.code), msg: ae.message })
      else setLines(l => [...l, { cmd: line, ok: false, out: `(error) ${ae.message}` }])
    } finally { setBusy(false) }
  }
  return (
    <div className="console">
      <div className="console-out" role="log" aria-live="polite">
        {lines.length === 0 && <div className="muted small">Type a Redis command, e.g. <code>INFO memory</code>, <code>SCAN 0 MATCH user:* COUNT 50</code> or <code>TTL session:1</code>. {conn.allow_writes ? 'Changes are on: risky commands ask first.' : 'Read-only: only commands that read run.'}</div>}
        {lines.map((l, i) => <div key={i} className="console-entry"><div className="console-cmd"><span className="prompt">db{db || 0}&gt;</span> {l.cmd}{l.ms !== undefined && <span className="faint small"> · {l.ms} ms</span>}</div><pre className={l.ok ? '' : 'bad'}>{l.out}</pre></div>)}
        <div ref={end} />
      </div>
      <form className="console-in" onSubmit={e => { e.preventDefault(); run(cmd) }}>
        <span className="prompt">db{db || 0}&gt;</span>
        <input value={cmd} onChange={e => setCmd(e.target.value)} autoFocus spellCheck={false} autoCapitalize="off" autoCorrect="off" aria-label="Command" disabled={busy}
          onKeyDown={e => {
            if (e.key === 'ArrowUp' && hist.length) { e.preventDefault(); const n = Math.min(hist.length - 1, hi + 1); setHi(n); setCmd(hist[n]) }
            if (e.key === 'ArrowDown') { e.preventDefault(); const n = hi - 1; setHi(n); setCmd(n >= 0 ? hist[n] : '') }
          }} />
        <button className="btn sm primary" disabled={busy || !cmd.trim()}>Run</button>
      </form>
      {confirm && <ConfirmRun reasons={confirm.reasons} onCancel={() => setConfirm(null)} onRun={() => { const c = confirm; setConfirm(null); run(c.line, true) }} />}
      {gate && <GateDialog gate={gate} onClose={() => setGate(null)} onPaused={() => setGate(null)} />}
    </div>
  )
}

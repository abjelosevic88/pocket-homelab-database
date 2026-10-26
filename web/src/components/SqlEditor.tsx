// The SQL editor: a plain textarea over a highlighted copy of its text, with
// line numbers. ⌘↵ runs the selection (or everything), ⇧↵ the statement
// under the cursor, ⌘S saves, Tab indents. No dependencies.
import { forwardRef, useImperativeHandle, useMemo, useRef } from 'react'
import { highlight, statementAt } from '../lib/db'

export interface SqlEditorHandle { focus: () => void; insert: (text: string) => void; selection: () => string; current: () => string }

export const SqlEditor = forwardRef<SqlEditorHandle, {
  value: string; onChange: (v: string) => void; onRun?: (sql: string) => void; onSave?: () => void
  fontSize?: number; placeholder?: string; minRows?: number; plain?: boolean
}>(function SqlEditor({ value, onChange, onRun, onSave, fontSize = 13, placeholder, minRows = 8, plain }, ref) {
  const ta = useRef<HTMLTextAreaElement>(null)
  const hl = useRef<HTMLPreElement>(null)
  const gutter = useRef<HTMLDivElement>(null)
  const tokens = useMemo(() => plain ? [{ t: value, c: '' }] : highlight(value), [value, plain])
  const lines = Math.max(minRows, value.split('\n').length)
  const sync = () => {
    if (!ta.current) return
    if (hl.current) { hl.current.scrollTop = ta.current.scrollTop; hl.current.scrollLeft = ta.current.scrollLeft }
    if (gutter.current) gutter.current.scrollTop = ta.current.scrollTop
  }
  const selection = () => {
    const el = ta.current
    if (!el) return ''
    return el.value.slice(el.selectionStart, el.selectionEnd)
  }
  const current = () => {
    const el = ta.current
    if (!el) return value
    return statementAt(el.value, el.selectionStart)
  }
  useImperativeHandle(ref, () => ({
    focus: () => ta.current?.focus(),
    selection, current,
    insert: (text: string) => {
      const el = ta.current
      if (!el) { onChange(value + text); return }
      const s = el.selectionStart, e = el.selectionEnd
      const next = value.slice(0, s) + text + value.slice(e)
      onChange(next)
      requestAnimationFrame(() => { el.focus(); el.selectionStart = el.selectionEnd = s + text.length })
    },
  }))
  return (
    <div className="sql-editor" style={{ fontSize, ['--rows' as string]: lines }}>
      <div className="sql-gutter" ref={gutter} aria-hidden="true">{Array.from({ length: lines }, (_, i) => <div key={i}>{i + 1}</div>)}</div>
      <div className="sql-area">
        <pre className="sql-hl" ref={hl} aria-hidden="true">{tokens.map((t, i) => t.c ? <span key={i} className={t.c}>{t.t}</span> : t.t)}{'\n'}</pre>
        <textarea ref={ta} value={value} spellCheck={false} autoCapitalize="off" autoCorrect="off" autoComplete="off" placeholder={placeholder} aria-label="SQL"
          onChange={e => onChange(e.target.value)} onScroll={sync}
          onKeyDown={e => {
            const el = e.currentTarget
            if ((e.metaKey || e.ctrlKey) && e.key === 'Enter') { e.preventDefault(); onRun?.(selection().trim() || value); return }
            if (e.shiftKey && e.key === 'Enter') { e.preventDefault(); onRun?.(current()); return }
            if ((e.metaKey || e.ctrlKey) && e.key.toLowerCase() === 's') { e.preventDefault(); onSave?.(); return }
            if (e.key === 'Tab' && !e.metaKey && !e.ctrlKey) {
              e.preventDefault()
              const s = el.selectionStart, en = el.selectionEnd
              if (e.shiftKey) {
                const lineStart = value.lastIndexOf('\n', s - 1) + 1
                if (value.slice(lineStart, lineStart + 2) === '  ') { onChange(value.slice(0, lineStart) + value.slice(lineStart + 2)); requestAnimationFrame(() => { el.selectionStart = el.selectionEnd = Math.max(lineStart, s - 2) }) }
                return
              }
              onChange(value.slice(0, s) + '  ' + value.slice(en))
              requestAnimationFrame(() => { el.selectionStart = el.selectionEnd = s + 2 })
            }
          }} />
      </div>
    </div>
  )
})

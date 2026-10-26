// Small SVG charts, no dependencies: a line/area chart with a hover readout,
// a bar chart, a sparkline, a progress ring, a stacked bar, a year heatmap,
// horizontal bars and a scatter.
import { useMemo, useRef, useState } from 'react'
import { fmtDate } from '../lib/format'

export type Pt = { x: string; y: number | null; y2?: number | null }

function extent(vals: number[], pad = 0.1, zero = false): [number, number] {
  if (!vals.length) return [0, 1]
  let lo = Math.min(...vals), hi = Math.max(...vals)
  if (zero) lo = Math.min(0, lo)
  if (hi === lo) { hi = lo + (Math.abs(lo) || 1) * 0.1; lo = lo - (Math.abs(lo) || 1) * 0.1 }
  const span = hi - lo
  return [zero && lo === 0 ? 0 : lo - span * pad, hi + span * pad]
}

function nice(v: number): string {
  if (Math.abs(v) >= 1000) return `${(v / 1000).toFixed(Math.abs(v) >= 10000 ? 0 : 1)}k`
  if (Math.abs(v) >= 100) return String(Math.round(v))
  if (Math.abs(v) >= 10) return v.toFixed(1).replace(/\.0$/, '')
  return v.toFixed(2).replace(/\.?0+$/, '')
}

/** Line (or area) chart over dates. `kind` bar draws bars (summed metrics). */
export function LineChart({ points, height = 180, color = 'var(--accent)', kind = 'line', goal, goalLabel, format = nice, zero = false, avg, band, className = '' }: {
  points: Pt[]; height?: number; color?: string; kind?: 'line' | 'bar' | 'area'; goal?: number; goalLabel?: string; format?: (v: number) => string; zero?: boolean; avg?: number
  band?: [number, number]; className?: string
}) {
  const ref = useRef<HTMLDivElement>(null)
  const [hover, setHover] = useState<number | null>(null)
  const W = 600, H = height, L = 40, R = 10, T = 12, B = 24
  const vals = points.map(p => p.y).filter((v): v is number => v !== null && !Number.isNaN(v))
  const extra = [goal ?? NaN, band?.[0] ?? NaN, band?.[1] ?? NaN].filter(v => !Number.isNaN(v))
  const [lo, hi] = extent([...vals, ...extra, ...(points.map(p => p.y2).filter((v): v is number => v !== null && v !== undefined))], 0.12, zero || kind === 'bar')
  const n = points.length
  const x = (i: number) => L + (n > 1 ? (i * (W - L - R)) / (n - 1) : (W - L - R) / 2)
  const y = (v: number) => T + (1 - (v - lo) / (hi - lo)) * (H - T - B)
  const path = useMemo(() => {
    let d = '', open = false
    points.forEach((p, i) => {
      if (p.y === null || Number.isNaN(p.y)) { open = false; return }
      d += `${open ? 'L' : 'M'}${x(i).toFixed(1)},${y(p.y).toFixed(1)} `
      open = true
    })
    return d
  }, [points, lo, hi]) // eslint-disable-line react-hooks/exhaustive-deps
  const path2 = useMemo(() => {
    let d = '', open = false
    points.forEach((p, i) => {
      if (p.y2 === null || p.y2 === undefined) { open = false; return }
      d += `${open ? 'L' : 'M'}${x(i).toFixed(1)},${y(p.y2).toFixed(1)} `
      open = true
    })
    return d
  }, [points, lo, hi]) // eslint-disable-line react-hooks/exhaustive-deps
  const ticks = useMemo(() => {
    const raw = (hi - lo) / 4
    const mag = Math.pow(10, Math.floor(Math.log10(raw || 1)))
    const norm = raw / mag
    const step = (norm <= 1 ? 1 : norm <= 2 ? 2 : norm <= 2.5 ? 2.5 : norm <= 5 ? 5 : 10) * mag
    const out: number[] = []
    for (let t = Math.ceil(lo / step) * step; t <= hi + 1e-9 && out.length < 8; t += step) out.push(+t.toFixed(10))
    return out
  }, [lo, hi])
  const labelEvery = Math.max(1, Math.ceil(n / 6))
  const onMove = (e: React.MouseEvent | React.TouchEvent) => {
    const el = ref.current
    if (!el || !n) return
    const rect = el.getBoundingClientRect()
    const cx = 'touches' in e ? e.touches[0].clientX : e.clientX
    const px = ((cx - rect.left) / rect.width) * W
    const i = Math.round(((px - L) / (W - L - R)) * (n - 1))
    setHover(Math.max(0, Math.min(n - 1, i)))
  }
  const hp = hover !== null ? points[hover] : null
  const barW = n > 0 ? Math.max(2, ((W - L - R) / n) * 0.7) : 4
  return (
    <div ref={ref} className={`chart ${className}`} onMouseMove={onMove} onMouseLeave={() => setHover(null)} onTouchStart={onMove} onTouchMove={onMove} onTouchEnd={() => setHover(null)}>
      <svg viewBox={`0 0 ${W} ${H}`} preserveAspectRatio="none" style={{ height }} role="img" aria-label="Chart">
        {ticks.map((t, i) => <g key={i}><line className="grid" x1={L} x2={W - R} y1={y(t)} y2={y(t)} /><text className="tick" x={L - 6} y={y(t) + 4} textAnchor="end">{format(t)}</text></g>)}
        {band && <rect className="band" x={L} width={W - L - R} y={y(band[1])} height={Math.max(0, y(band[0]) - y(band[1]))} />}
        {goal !== undefined && <g><line className="goal" x1={L} x2={W - R} y1={y(goal)} y2={y(goal)} /><text className="goal-label" x={L + 4} y={y(goal) - 5} textAnchor="start">{goalLabel || `goal ${format(goal)}`}</text></g>}
        {avg !== undefined && <line className="avg" x1={L} x2={W - R} y1={y(avg)} y2={y(avg)} />}
        {kind === 'bar' && points.map((p, i) => p.y === null || Number.isNaN(p.y) ? null : <rect key={i} className="bar" x={x(i) - barW / 2} width={barW} y={y(Math.max(0, p.y))} height={Math.max(1, Math.abs(y(p.y) - y(0)))} fill={goal !== undefined && p.y >= goal ? color : color} opacity={goal !== undefined && p.y < goal ? 0.55 : 1} rx={2} />)}
        {kind === 'area' && path && <path className="area" d={`${path} L${x(n - 1)},${y(lo)} L${x(0)},${y(lo)} Z`} fill={color} />}
        {kind !== 'bar' && path && <path className="line" d={path} stroke={color} />}
        {kind !== 'bar' && path2 && <path className="line line2" d={path2} stroke={color} />}
        {kind !== 'bar' && n <= 60 && points.map((p, i) => p.y === null || Number.isNaN(p.y) ? null : <circle key={i} className="dot" cx={x(i)} cy={y(p.y)} r={2.6} fill={color} />)}
        {points.map((p, i) => i % labelEvery === 0 || i === n - 1 ? <text key={i} className="tick" x={x(i)} y={H - 6} textAnchor={i === 0 ? 'start' : i === n - 1 ? 'end' : 'middle'}>{fmtDate(p.x, false)}</text> : null)}
        {hp && hover !== null && <g><line className="cursor" x1={x(hover)} x2={x(hover)} y1={T} y2={H - B} />{hp.y !== null && !Number.isNaN(hp.y) && <circle cx={x(hover)} cy={y(hp.y)} r={4.5} fill={color} className="dot-hi" />}</g>}
      </svg>
      {hp && <div className="chart-tip" style={{ left: `${((x(hover!) / W) * 100).toFixed(1)}%` }}><b>{hp.y === null || Number.isNaN(hp.y) ? '—' : format(hp.y)}{hp.y2 !== null && hp.y2 !== undefined ? ` / ${format(hp.y2)}` : ''}</b><span>{fmtDate(hp.x)}</span></div>}
    </div>
  )
}

/** Tiny inline line for tiles and lists. */
export function Sparkline({ values, color = 'currentColor', width = 80, height = 24, goal }: { values: (number | null)[]; color?: string; width?: number; height?: number; goal?: number }) {
  const vals = values.filter((v): v is number => v !== null && !Number.isNaN(v))
  if (vals.length < 2) return <svg width={width} height={height} className="spark" />
  const [lo, hi] = extent([...vals, ...(goal !== undefined ? [goal] : [])], 0.1)
  const n = values.length
  const x = (i: number) => (i * (width - 2)) / (n - 1) + 1
  const y = (v: number) => 1 + (1 - (v - lo) / (hi - lo)) * (height - 2)
  let d = '', open = false
  values.forEach((v, i) => { if (v === null || Number.isNaN(v)) { open = false; return } d += `${open ? 'L' : 'M'}${x(i).toFixed(1)},${y(v).toFixed(1)} `; open = true })
  return (
    <svg width={width} height={height} className="spark" aria-hidden="true">
      {goal !== undefined && <line x1={0} x2={width} y1={y(goal)} y2={y(goal)} className="spark-goal" />}
      <path d={d} fill="none" stroke={color} strokeWidth={1.6} strokeLinecap="round" strokeLinejoin="round" />
    </svg>
  )
}

/** A progress ring (0..100). */
export function Ring({ pct, size = 56, stroke = 6, color = 'var(--accent)', children }: { pct: number; size?: number; stroke?: number; color?: string; children?: React.ReactNode }) {
  const r = (size - stroke) / 2
  const c = 2 * Math.PI * r
  const p = Math.max(0, Math.min(100, pct))
  return (
    <span className="ring" style={{ width: size, height: size }}>
      <svg width={size} height={size} viewBox={`0 0 ${size} ${size}`} aria-hidden="true">
        <circle cx={size / 2} cy={size / 2} r={r} className="ring-bg" strokeWidth={stroke} />
        <circle cx={size / 2} cy={size / 2} r={r} className="ring-fg" stroke={color} strokeWidth={stroke} strokeDasharray={`${(c * p) / 100} ${c}`} strokeLinecap="round" transform={`rotate(-90 ${size / 2} ${size / 2})`} />
      </svg>
      <span className="ring-label">{children}</span>
    </span>
  )
}

/** Horizontal stacked bar (sleep stages). */
export function Stacked({ parts, height = 10 }: { parts: { value: number; color: string; label: string }[]; height?: number }) {
  const total = parts.reduce((a, p) => a + p.value, 0) || 1
  return (
    <div className="stacked" style={{ height }} role="img" aria-label={parts.map(p => `${p.label} ${Math.round(p.value)}`).join(', ')}>
      {parts.map(p => p.value > 0 ? <span key={p.label} style={{ width: `${(p.value / total) * 100}%`, background: p.color }} title={`${p.label}: ${Math.round((p.value / total) * 100)}%`} /> : null)}
    </div>
  )
}

/** Vertical bars by category (weekday pattern, workouts by kind). */
export function Bars({ items, color = 'var(--accent)', format = nice, height = 120 }: { items: { label: string; value: number; hint?: string }[]; color?: string; format?: (v: number) => string; height?: number }) {
  const max = Math.max(...items.map(i => i.value), 0) || 1
  return (
    <div className="bars" style={{ height }}>
      {items.map(it => (
        <div key={it.label} className="bars-col" title={`${it.label}: ${format(it.value)}${it.hint ? ' · ' + it.hint : ''}`}>
          <span className="bars-val">{it.value ? format(it.value) : ''}</span>
          <span className="bars-bar" style={{ height: `${(it.value / max) * 100}%`, background: color }} />
          <span className="bars-lbl">{it.label}</span>
        </div>
      ))}
    </div>
  )
}

/** A year of days as a GitHub-style grid: one column per week, rows by weekday (week_start aware). */
export function Heatmap({ days, weekStart = 1, goal = 0, fmt = (m: number) => `${Math.round(m)} min` }: { days: { date: string; minutes: number }[]; weekStart?: number; goal?: number; fmt?: (m: number) => string }) {
  const [tip, setTip] = useState<{ d: string; m: number; x: number; y: number } | null>(null)
  if (!days.length) return null
  const wd = (iso: string) => { const [y, m, d] = iso.split('-').map(Number); const w = new Date(y, m - 1, d).getDay(); return w === 0 ? 7 : w }
  const lead = (wd(days[0].date) - weekStart + 7) % 7
  const cells: ({ date: string; minutes: number } | null)[] = [...Array(lead).fill(null), ...days]
  const cols = Math.ceil(cells.length / 7)
  const max = Math.max(goal * 2, ...days.map(d => d.minutes), 1)
  const level = (m: number) => m <= 0 ? 0 : m < max * 0.15 ? 1 : m < max * 0.35 ? 2 : m < max * 0.6 ? 3 : 4
  const S = 12, G = 3
  const months: { col: number; label: string }[] = []
  let last = ''
  for (let c = 0; c < cols; c++) {
    const cell = cells.slice(c * 7, c * 7 + 7).find(Boolean)
    if (!cell) continue
    const mo = cell.date.slice(0, 7)
    if (mo !== last && cell.date.slice(8) <= '07') { months.push({ col: c, label: new Date(cell.date + 'T12:00').toLocaleDateString(undefined, { month: 'short' }) }); last = mo }
  }
  const W = cols * (S + G), H = 7 * (S + G) + 16
  return (
    <div className="heatmap" onMouseLeave={() => setTip(null)}>
      <svg viewBox={`0 0 ${W} ${H}`} width={W} height={H} role="img" aria-label="The last year">
        {months.map(m => <text key={m.col} className="tick" x={m.col * (S + G)} y={10}>{m.label}</text>)}
        {cells.map((c, i) => c && <rect key={c.date} x={Math.floor(i / 7) * (S + G)} y={16 + (i % 7) * (S + G)} width={S} height={S} rx={3} className={`hm hm-${level(c.minutes)}`}
          onMouseEnter={() => setTip({ d: c.date, m: c.minutes, x: Math.floor(i / 7) * (S + G), y: (i % 7) * (S + G) })} />)}
      </svg>
      {tip && <div className="chart-tip" style={{ left: tip.x + 6, top: tip.y + 36 }}><b>{tip.m ? fmt(tip.m) : 'nothing'}</b><span>{fmtDate(tip.d)}</span></div>}
      <div className="hm-legend small muted">Less {[0, 1, 2, 3, 4].map(l => <i key={l} className={`hm-${l}`} />)} More</div>
    </div>
  )
}

/** Horizontal bars with a label and a value (techniques, kinds). */
export function HBars({ items, format = nice }: { items: { label: string; value: number; color?: string; hint?: string }[]; format?: (v: number) => string }) {
  const max = Math.max(...items.map(i => i.value), 0) || 1
  return (
    <div className="hbars">
      {items.map(it => (
        <div key={it.label} className="hbar" title={it.hint}>
          <span className="hbar-l ellipsis">{it.label}</span>
          <span className="hbar-track"><span className="hbar-fill" style={{ width: `${(it.value / max) * 100}%`, background: it.color || 'var(--accent)' }} /></span>
          <span className="hbar-v tnum">{format(it.value)}</span>
        </div>
      ))}
    </div>
  )
}

/** Dots of y against x (minutes practised vs a health number). */
export function Scatter({ points, height = 160, color = 'var(--accent)', xLabel, yLabel }: { points: { x: number; y: number; label?: string }[]; height?: number; color?: string; xLabel?: string; yLabel?: string }) {
  const W = 400, H = height, L = 36, R = 8, T = 8, B = 22
  if (!points.length) return null
  const [x0, x1] = extent(points.map(p => p.x), 0.05, true)
  const [y0, y1] = extent(points.map(p => p.y), 0.1)
  const x = (v: number) => L + ((v - x0) / (x1 - x0)) * (W - L - R)
  const y = (v: number) => T + (1 - (v - y0) / (y1 - y0)) * (H - T - B)
  return (
    <div className="chart">
      <svg viewBox={`0 0 ${W} ${H}`} preserveAspectRatio="none" style={{ height }} role="img" aria-label="Scatter">
        <line className="grid" x1={L} x2={W - R} y1={H - B} y2={H - B} />
        <text className="tick" x={L - 4} y={y(y1) + 8} textAnchor="end">{nice(y1)}</text>
        <text className="tick" x={L - 4} y={y(y0)} textAnchor="end">{nice(y0)}</text>
        {xLabel && <text className="tick" x={W - R} y={H - 6} textAnchor="end">{xLabel}</text>}
        {yLabel && <text className="tick" x={L + 4} y={T + 8}>{yLabel}</text>}
        {points.map((p, i) => <circle key={i} cx={x(p.x)} cy={y(p.y)} r={3.2} fill={color} opacity={0.6}><title>{p.label}</title></circle>)}
      </svg>
    </div>
  )
}

export { nice as fmtTick }

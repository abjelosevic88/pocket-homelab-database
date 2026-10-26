// A small coloured square naming the engine (PG, My, SQ, R) — enough to tell connections apart at a glance.
import { engineOf } from '../lib/db'

export function EngineBadge({ engine, size = 22, color }: { engine: string; size?: number; color?: string }) {
  const e = engineOf(engine)
  return (
    <span className="engine-badge" style={{ width: size, height: size, fontSize: size * 0.42, background: color || e.color }} title={e.name} aria-label={e.name}>
      {e.short}
    </span>
  )
}

/** A status dot with its words for screen readers. */
export function StatusDot({ status, label }: { status: string; label?: string }) {
  return <span className={`sdot ${status}`} role="img" aria-label={label || status} title={label} />
}

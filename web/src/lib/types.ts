export interface User { id: number; email: string; name: string; role: 'admin' | 'member'; created_at: string }
export interface APIToken { id: number; user_id: number; name: string; prefix: string; last_used_at: string | null; created_at: string }

export type ConnMode = 'core' | 'direct' | 'off'
export type ConnVia = '' | 'core' | 'direct'

export interface Settings {
  core_configured: boolean
  core_registered?: boolean
  title: string; date_format: string; time_format: string
  page_size: number; row_limit: number; query_timeout_s: number; confirm_writes: boolean
  editor_font: number; density: 'compact' | 'comfortable'; show_system: boolean
  pause_minutes: number; snapshot_keep: number; show_backups: boolean
  notify_down: boolean; notify_recovered: boolean
  notify_url: string; notify_format: string; notify_configured: boolean
  ai_provider: 'auto' | 'pocket' | 'openai'; ai_pocket_model: string; openai_api_key: string; openai_model: string; openai_base_url: string
  setup_complete: boolean; base_url: string
  docker: boolean; upload_max: number
}

export type Engine = 'postgres' | 'mysql' | 'sqlite' | 'redis'

export interface Connection {
  id: number; name: string; engine: Engine; engine_name: string
  host: string; port: number; username: string; has_password: boolean; database: string; ssl_mode: string
  path: string; host_path: string; container: string; group: string; color: string; notes: string
  allow_writes: boolean; favorite: boolean; monitor: boolean; source: string; discovery_key: string
  status: '' | 'up' | 'down' | 'paused'; status_detail: string; version: string; size_bytes: number; latency_ms: number
  checked_at: string; down_since: string; opened_at: string; created_at: string; updated_at: string
  paused: boolean; address: string
}

export interface ServerInfo { engine: string; version: string; size_bytes: number; databases: number; uptime: string; extra: Record<string, string> }
export interface DBInfo { name: string; size_bytes: number; tables: number; current: boolean }
export interface DBObject { schema: string; name: string; kind: 'table' | 'view' | 'matview' | 'foreign'; rows: number; size_bytes: number; comment?: string }
export interface Schema { name: string; objects: DBObject[] }
export interface Column { name: string; type: string; nullable: boolean; default: string | null; pk: boolean; position: number; comment?: string; auto: boolean; generated: boolean }
export interface Index { name: string; columns: string[]; unique: boolean; primary: boolean; definition?: string }
export interface ForeignKey { name: string; schema?: string; table?: string; columns: string[]; ref_schema: string; ref_table: string; ref_columns: string[] }
export interface TableInfo {
  schema: string; name: string; kind: string; columns: Column[]; primary_key: string[]; indexes: Index[]
  foreign_keys: ForeignKey[]; referenced_by: ForeignKey[]; ddl: string; rows: number; size_bytes: number; rowid: boolean; comment?: string
}

/** A cell as the API sends it: plain JSON, or binary / a long text cut short. */
export type Cell = null | string | number | boolean | { $bin: string; size: number } | { $trunc: string; size: number }

export interface Result {
  statement: string; kind: 'read' | 'write' | 'ddl' | 'other'; columns: string[]; types: string[]; rows: Cell[][]
  has_rows: boolean; rows_affected: number; truncated: boolean; duration_ms: number; error?: string; skipped?: boolean
}
export interface Page extends Result { total: number; total_approx: boolean; offset: number; limit: number }
export interface Filter { column: string; op: string; value: string }

export interface Snapshot { id: number; connection_id: number; file: string; size_bytes: number; reason: string; restored_at: string; created_at: string }
export interface Pause { id: number; connection_id: number; containers: string[]; snapshot_id: number | null; started_at: string; expires_at: string }
export interface Safety {
  live: boolean; owners: string[]; in_use: boolean; pause: Pause | null; can_write: boolean; reason: string; code: string
  snapshots: Snapshot[]; docker: boolean
}

export interface Found {
  key: string; engine: string; supported: boolean; name: string; group: string; container?: string; image?: string; running: boolean
  host?: string; port?: number; user?: string; has_password: boolean; database?: string; needs_password: boolean
  path?: string; host_path?: string; size_bytes: number; modified?: string; owners?: string[]; backup: boolean; note?: string
  added: boolean; connection_id?: number
}

export interface HistoryEntry { id: number; connection_id: number; connection: string; engine: Engine; database: string; body: string; ok: boolean; error: string; rows: number; duration_ms: number; created_at: string }
export interface SavedQuery { id: number; connection_id: number | null; name: string; database: string; body: string; description: string; favorite: boolean; run_count: number; last_run_at: string; created_at: string; updated_at: string }
export interface Activity {
  id: number; connection_id: number | null; connection_name: string; kind: string; database: string; target: string; summary: string
  statement: string; rows: number; ok: boolean; error: string; snapshot_id: number | null; source: string; created_at: string
}

export interface KeyInfo { key: string; type: string; ttl_ms: number }
export interface KeyValue { key: string; type: string; ttl_ms: number; length: number; value: unknown; truncated: boolean }

export interface Overview {
  counts: { connections: number; up: number; down: number; paused: number; unknown: number; watched: number; writable: number }
  engines: Record<string, number>; total_bytes: number; connections: Connection[]; biggest: Connection[]
  growth: { date: string; size_bytes: number }[]; movers: { id: number; name: string; delta_bytes: number; size_bytes: number }[]
  docker: boolean; activity?: Activity[]; history?: HistoryEntry[]; saved?: SavedQuery[]; pauses?: Pause[]
}

export interface QueryResponse { results?: Result[]; duration_ms?: number; read_only?: boolean; needs_confirm?: boolean; reasons?: { statement: string; reason: string }[] }
export interface SizePoint { connection_id: number; date: string; size_bytes: number }

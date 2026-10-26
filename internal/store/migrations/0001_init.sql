CREATE TABLE users (
  id            INTEGER PRIMARY KEY AUTOINCREMENT,
  email         TEXT NOT NULL UNIQUE,
  name          TEXT NOT NULL DEFAULT '',
  password_hash TEXT NOT NULL DEFAULT '',
  role          TEXT NOT NULL DEFAULT 'member',
  created_at    TEXT NOT NULL DEFAULT (strftime('%Y-%m-%dT%H:%M:%fZ','now')),
  updated_at    TEXT
);

CREATE TABLE sessions (
  id         TEXT PRIMARY KEY,
  user_id    INTEGER NOT NULL REFERENCES users(id) ON DELETE CASCADE,
  expires_at TEXT NOT NULL,
  created_at TEXT NOT NULL DEFAULT (strftime('%Y-%m-%dT%H:%M:%fZ','now'))
);

CREATE TABLE api_tokens (
  id           INTEGER PRIMARY KEY AUTOINCREMENT,
  user_id      INTEGER NOT NULL REFERENCES users(id) ON DELETE CASCADE,
  name         TEXT NOT NULL DEFAULT '',
  token_hash   TEXT NOT NULL UNIQUE,
  prefix       TEXT NOT NULL DEFAULT '',
  last_used_at TEXT,
  created_at   TEXT NOT NULL DEFAULT (strftime('%Y-%m-%dT%H:%M:%fZ','now'))
);

-- Box-wide settings (title, setup flag); everything else is personal.
CREATE TABLE settings (
  key   TEXT PRIMARY KEY,
  value TEXT NOT NULL DEFAULT ''
);

CREATE TABLE user_settings (
  user_id INTEGER NOT NULL REFERENCES users(id) ON DELETE CASCADE,
  key     TEXT NOT NULL,
  value   TEXT NOT NULL DEFAULT '',
  PRIMARY KEY (user_id, key)
);

CREATE TABLE cache (
  key        TEXT PRIMARY KEY,
  value      TEXT NOT NULL DEFAULT '',
  fetched_at TEXT NOT NULL
);

-- A database a person can open: a server (PostgreSQL, MySQL / MariaDB,
-- Redis) or a SQLite file. Everything is per person; the password is sealed
-- with DATA_DIR/secret.key. A container connection finds its address again
-- on every connect, so a recreated container keeps working.
CREATE TABLE connections (
  id             INTEGER PRIMARY KEY AUTOINCREMENT,
  user_id        INTEGER NOT NULL REFERENCES users(id) ON DELETE CASCADE,
  name           TEXT NOT NULL,
  engine         TEXT NOT NULL,                 -- postgres | mysql | sqlite | redis
  host           TEXT NOT NULL DEFAULT '',
  port           INTEGER NOT NULL DEFAULT 0,
  username       TEXT NOT NULL DEFAULT '',
  password       TEXT NOT NULL DEFAULT '',      -- sealed
  database       TEXT NOT NULL DEFAULT '',      -- the database it opens first
  ssl_mode       TEXT NOT NULL DEFAULT '',
  path           TEXT NOT NULL DEFAULT '',      -- SQLite file, as this app sees it
  host_path      TEXT NOT NULL DEFAULT '',      -- … and as the server sees it
  container      TEXT NOT NULL DEFAULT '',      -- Docker container it runs in
  group_name     TEXT NOT NULL DEFAULT '',
  color          TEXT NOT NULL DEFAULT '',
  notes          TEXT NOT NULL DEFAULT '',
  allow_writes   INTEGER NOT NULL DEFAULT 0,
  favorite       INTEGER NOT NULL DEFAULT 0,
  monitor        INTEGER NOT NULL DEFAULT 1,
  source         TEXT NOT NULL DEFAULT 'manual', -- manual | discovered | upload | import | demo
  discovery_key  TEXT NOT NULL DEFAULT '',
  status         TEXT NOT NULL DEFAULT '',      -- up | down | paused | ''
  status_detail  TEXT NOT NULL DEFAULT '',
  version        TEXT NOT NULL DEFAULT '',
  size_bytes     INTEGER NOT NULL DEFAULT -1,
  latency_ms     INTEGER NOT NULL DEFAULT 0,
  checked_at     TEXT NOT NULL DEFAULT '',
  down_since     TEXT NOT NULL DEFAULT '',
  fails          INTEGER NOT NULL DEFAULT 0,
  alerted        INTEGER NOT NULL DEFAULT 0,    -- a "down" push went out
  opened_at      TEXT NOT NULL DEFAULT '',
  created_at     TEXT NOT NULL DEFAULT (strftime('%Y-%m-%dT%H:%M:%fZ','now')),
  updated_at     TEXT NOT NULL DEFAULT (strftime('%Y-%m-%dT%H:%M:%fZ','now'))
);
CREATE INDEX connections_user ON connections(user_id, name);
CREATE UNIQUE INDEX connections_discovery ON connections(user_id, discovery_key) WHERE discovery_key <> '';

-- One size a day per connection, for the growth chart.
CREATE TABLE size_history (
  user_id       INTEGER NOT NULL REFERENCES users(id) ON DELETE CASCADE,
  connection_id INTEGER NOT NULL REFERENCES connections(id) ON DELETE CASCADE,
  date          TEXT NOT NULL,
  size_bytes    INTEGER NOT NULL DEFAULT 0,
  PRIMARY KEY (connection_id, date)
);
CREATE INDEX size_history_user ON size_history(user_id, date);

-- Queries a person keeps.
CREATE TABLE saved_queries (
  id            INTEGER PRIMARY KEY AUTOINCREMENT,
  user_id       INTEGER NOT NULL REFERENCES users(id) ON DELETE CASCADE,
  connection_id INTEGER REFERENCES connections(id) ON DELETE SET NULL,
  name          TEXT NOT NULL,
  database      TEXT NOT NULL DEFAULT '',
  body          TEXT NOT NULL,
  description   TEXT NOT NULL DEFAULT '',
  favorite      INTEGER NOT NULL DEFAULT 0,
  run_count     INTEGER NOT NULL DEFAULT 0,
  last_run_at   TEXT NOT NULL DEFAULT '',
  created_at    TEXT NOT NULL DEFAULT (strftime('%Y-%m-%dT%H:%M:%fZ','now')),
  updated_at    TEXT NOT NULL DEFAULT (strftime('%Y-%m-%dT%H:%M:%fZ','now'))
);
CREATE INDEX saved_queries_user ON saved_queries(user_id, name);

-- Every run of the SQL editor and the Redis console (the last 1000 per person).
CREATE TABLE query_history (
  id            INTEGER PRIMARY KEY AUTOINCREMENT,
  user_id       INTEGER NOT NULL REFERENCES users(id) ON DELETE CASCADE,
  connection_id INTEGER NOT NULL REFERENCES connections(id) ON DELETE CASCADE,
  database      TEXT NOT NULL DEFAULT '',
  body          TEXT NOT NULL,
  ok            INTEGER NOT NULL DEFAULT 1,
  error         TEXT NOT NULL DEFAULT '',
  rows          INTEGER NOT NULL DEFAULT 0,
  duration_ms   INTEGER NOT NULL DEFAULT 0,
  created_at    TEXT NOT NULL DEFAULT (strftime('%Y-%m-%dT%H:%M:%fZ','now'))
);
CREATE INDEX query_history_user ON query_history(user_id, created_at);
CREATE INDEX query_history_conn ON query_history(user_id, connection_id, created_at);

-- Every change made through the app: edits, SQL that changed something,
-- pauses, snapshots and restores. Kept when the connection is removed.
CREATE TABLE activity (
  id              INTEGER PRIMARY KEY AUTOINCREMENT,
  user_id         INTEGER NOT NULL REFERENCES users(id) ON DELETE CASCADE,
  connection_id   INTEGER REFERENCES connections(id) ON DELETE SET NULL,
  connection_name TEXT NOT NULL DEFAULT '',
  kind            TEXT NOT NULL,                 -- sql | edit | insert | delete | key | pause | resume | snapshot | restore
  database        TEXT NOT NULL DEFAULT '',
  target          TEXT NOT NULL DEFAULT '',      -- a table or a key
  summary         TEXT NOT NULL DEFAULT '',
  statement       TEXT NOT NULL DEFAULT '',
  rows            INTEGER NOT NULL DEFAULT 0,
  ok              INTEGER NOT NULL DEFAULT 1,
  error           TEXT NOT NULL DEFAULT '',
  snapshot_id     INTEGER,
  source          TEXT NOT NULL DEFAULT 'app',   -- app | api | <pocket app slug>
  created_at      TEXT NOT NULL DEFAULT (strftime('%Y-%m-%dT%H:%M:%fZ','now'))
);
CREATE INDEX activity_user ON activity(user_id, created_at);
CREATE INDEX activity_conn ON activity(user_id, connection_id, created_at);

-- Copies of a SQLite file taken before it was changed (DATA_DIR/snapshots).
CREATE TABLE snapshots (
  id            INTEGER PRIMARY KEY AUTOINCREMENT,
  user_id       INTEGER NOT NULL REFERENCES users(id) ON DELETE CASCADE,
  connection_id INTEGER NOT NULL REFERENCES connections(id) ON DELETE CASCADE,
  dir           TEXT NOT NULL,                   -- relative to DATA_DIR/snapshots
  file          TEXT NOT NULL,                   -- the original file
  size_bytes    INTEGER NOT NULL DEFAULT 0,
  reason        TEXT NOT NULL DEFAULT '',
  restored_at   TEXT NOT NULL DEFAULT '',
  created_at    TEXT NOT NULL DEFAULT (strftime('%Y-%m-%dT%H:%M:%fZ','now'))
);
CREATE INDEX snapshots_conn ON snapshots(user_id, connection_id, created_at);

-- A live SQLite file opened for changes: the containers using it are
-- stopped, a snapshot is taken, and everything starts again at the end
-- (or by itself when the time is up).
CREATE TABLE maintenance (
  id            INTEGER PRIMARY KEY AUTOINCREMENT,
  user_id       INTEGER NOT NULL REFERENCES users(id) ON DELETE CASCADE,
  connection_id INTEGER NOT NULL REFERENCES connections(id) ON DELETE CASCADE,
  containers    TEXT NOT NULL DEFAULT '[]',       -- JSON: the containers stopped
  snapshot_id   INTEGER,
  started_at    TEXT NOT NULL,
  expires_at    TEXT NOT NULL,
  ended_at      TEXT NOT NULL DEFAULT '',
  end_reason    TEXT NOT NULL DEFAULT '',
  error         TEXT NOT NULL DEFAULT ''
);
CREATE INDEX maintenance_open ON maintenance(ended_at, expires_at);

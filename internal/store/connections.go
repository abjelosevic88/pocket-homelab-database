package store

import (
	"context"
	"database/sql"
	"strings"
)

const connCols = `id, user_id, name, engine, host, port, username, password, database, ssl_mode, path, host_path, container, group_name, color, notes,
	allow_writes, favorite, monitor, source, discovery_key, status, status_detail, version, size_bytes, latency_ms, checked_at, down_since, fails, alerted,
	opened_at, created_at, updated_at`

func scanConn(row interface{ Scan(...any) error }) (*Connection, error) {
	var c Connection
	var aw, fav, mon, al int
	err := row.Scan(&c.ID, &c.UserID, &c.Name, &c.Engine, &c.Host, &c.Port, &c.Username, &c.Password, &c.Database, &c.SSLMode, &c.Path, &c.HostPath,
		&c.Container, &c.Group, &c.Color, &c.Notes, &aw, &fav, &mon, &c.Source, &c.DiscoveryKey, &c.Status, &c.StatusDetail, &c.Version,
		&c.SizeBytes, &c.LatencyMs, &c.CheckedAt, &c.DownSince, &c.Fails, &al, &c.OpenedAt, &c.CreatedAt, &c.UpdatedAt)
	if err != nil {
		return nil, notFound(err)
	}
	c.AllowWrites, c.Favorite, c.Monitor, c.Alerted = aw == 1, fav == 1, mon == 1, al == 1
	c.HasPassword = c.Password != ""
	return &c, nil
}

// ListConnections lists one person's connections.
func (s *Store) ListConnections(ctx context.Context, uid int64) ([]Connection, error) {
	rows, err := s.DB.QueryContext(ctx, `SELECT `+connCols+` FROM connections WHERE user_id = ? ORDER BY favorite DESC, lower(group_name), lower(name)`, uid)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []Connection{}
	for rows.Next() {
		c, err := scanConn(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, *c)
	}
	return out, rows.Err()
}

// MonitoredConnections lists every connection with watching on (all people; background work).
func (s *Store) MonitoredConnections(ctx context.Context) ([]Connection, error) {
	rows, err := s.DB.QueryContext(ctx, `SELECT `+connCols+` FROM connections WHERE monitor = 1 ORDER BY id`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []Connection{}
	for rows.Next() {
		c, err := scanConn(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, *c)
	}
	return out, rows.Err()
}

// GetConnection returns one of the person's connections (someone else's: ErrNotFound).
func (s *Store) GetConnection(ctx context.Context, uid, id int64) (*Connection, error) {
	return scanConn(s.DB.QueryRowContext(ctx, `SELECT `+connCols+` FROM connections WHERE id = ? AND user_id = ?`, id, uid))
}

// ConnectionByKey finds a connection made by discovery.
func (s *Store) ConnectionByKey(ctx context.Context, uid int64, key string) (*Connection, error) {
	return scanConn(s.DB.QueryRowContext(ctx, `SELECT `+connCols+` FROM connections WHERE user_id = ? AND discovery_key = ?`, uid, key))
}

// CreateConnection adds a connection (Password must already be sealed).
func (s *Store) CreateConnection(ctx context.Context, uid int64, c *Connection) error {
	if c.Source == "" {
		c.Source = "manual"
	}
	now := Now()
	res, err := s.DB.ExecContext(ctx, `INSERT INTO connections (user_id, name, engine, host, port, username, password, database, ssl_mode, path, host_path,
		container, group_name, color, notes, allow_writes, favorite, monitor, source, discovery_key, status, status_detail, version, size_bytes, latency_ms,
		checked_at, created_at, updated_at) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`,
		uid, c.Name, c.Engine, c.Host, c.Port, c.Username, c.Password, c.Database, c.SSLMode, c.Path, c.HostPath, c.Container, c.Group, c.Color, c.Notes,
		boolInt(c.AllowWrites), boolInt(c.Favorite), boolInt(c.Monitor), c.Source, c.DiscoveryKey, c.Status, c.StatusDetail, c.Version, c.SizeBytes,
		c.LatencyMs, c.CheckedAt, now, now)
	if err != nil {
		return conflict(err)
	}
	c.ID, _ = res.LastInsertId()
	c.UserID, c.CreatedAt, c.UpdatedAt = uid, now, now
	c.HasPassword = c.Password != ""
	return nil
}

// UpdateConnection saves what a person can edit (Password already sealed).
func (s *Store) UpdateConnection(ctx context.Context, uid int64, c *Connection) error {
	c.UpdatedAt = Now()
	res, err := s.DB.ExecContext(ctx, `UPDATE connections SET name = ?, engine = ?, host = ?, port = ?, username = ?, password = ?, database = ?, ssl_mode = ?,
		path = ?, host_path = ?, container = ?, group_name = ?, color = ?, notes = ?, allow_writes = ?, favorite = ?, monitor = ?, updated_at = ?
		WHERE id = ? AND user_id = ?`,
		c.Name, c.Engine, c.Host, c.Port, c.Username, c.Password, c.Database, c.SSLMode, c.Path, c.HostPath, c.Container, c.Group, c.Color, c.Notes,
		boolInt(c.AllowWrites), boolInt(c.Favorite), boolInt(c.Monitor), c.UpdatedAt, c.ID, uid)
	if err != nil {
		return conflict(err)
	}
	if n, _ := res.RowsAffected(); n == 0 {
		return ErrNotFound
	}
	c.HasPassword = c.Password != ""
	return nil
}

// SetConnectionStatus stores the outcome of a check.
func (s *Store) SetConnectionStatus(ctx context.Context, c *Connection) error {
	_, err := s.DB.ExecContext(ctx, `UPDATE connections SET status = ?, status_detail = ?, version = CASE WHEN ? = '' THEN version ELSE ? END,
		size_bytes = CASE WHEN ? < 0 THEN size_bytes ELSE ? END, latency_ms = ?, checked_at = ?, down_since = ?, fails = ?, alerted = ? WHERE id = ?`,
		c.Status, c.StatusDetail, c.Version, c.Version, c.SizeBytes, c.SizeBytes, c.LatencyMs, c.CheckedAt, c.DownSince, c.Fails, boolInt(c.Alerted), c.ID)
	return err
}

// TouchConnection remembers when a connection was last opened.
func (s *Store) TouchConnection(ctx context.Context, uid, id int64) {
	_, _ = s.DB.ExecContext(ctx, `UPDATE connections SET opened_at = ? WHERE id = ? AND user_id = ?`, Now(), id, uid)
}

// DeleteConnection removes a connection.
func (s *Store) DeleteConnection(ctx context.Context, uid, id int64) error {
	res, err := s.DB.ExecContext(ctx, `DELETE FROM connections WHERE id = ? AND user_id = ?`, id, uid)
	if err != nil {
		return err
	}
	if n, _ := res.RowsAffected(); n == 0 {
		return ErrNotFound
	}
	return nil
}

// AddSize records a connection's size for today (the last check of the day wins).
func (s *Store) AddSize(ctx context.Context, uid, connID int64, date string, size int64) error {
	_, err := s.DB.ExecContext(ctx, `INSERT INTO size_history (user_id, connection_id, date, size_bytes) VALUES (?, ?, ?, ?)
		ON CONFLICT(connection_id, date) DO UPDATE SET size_bytes = excluded.size_bytes`, uid, connID, date, size)
	return err
}

// SizeHistory returns one person's daily sizes since a date (connID 0 = all).
func (s *Store) SizeHistory(ctx context.Context, uid, connID int64, since string) ([]SizePoint, error) {
	q := `SELECT connection_id, date, size_bytes FROM size_history WHERE user_id = ? AND date >= ?`
	args := []any{uid, since}
	if connID > 0 {
		q += ` AND connection_id = ?`
		args = append(args, connID)
	}
	rows, err := s.DB.QueryContext(ctx, q+` ORDER BY date`, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []SizePoint{}
	for rows.Next() {
		var p SizePoint
		if err := rows.Scan(&p.ConnectionID, &p.Date, &p.SizeBytes); err != nil {
			return nil, err
		}
		out = append(out, p)
	}
	return out, rows.Err()
}

// ---- saved queries ----

const savedCols = `id, user_id, connection_id, name, database, body, description, favorite, run_count, last_run_at, created_at, updated_at`

func scanSaved(row interface{ Scan(...any) error }) (*SavedQuery, error) {
	var q SavedQuery
	var conn sql.NullInt64
	var fav int
	if err := row.Scan(&q.ID, &q.UserID, &conn, &q.Name, &q.Database, &q.Body, &q.Description, &fav, &q.RunCount, &q.LastRunAt, &q.CreatedAt, &q.UpdatedAt); err != nil {
		return nil, notFound(err)
	}
	if conn.Valid {
		q.ConnectionID = &conn.Int64
	}
	q.Favorite = fav == 1
	return &q, nil
}

// ListSaved lists one person's saved queries (connID 0 = all).
func (s *Store) ListSaved(ctx context.Context, uid, connID int64) ([]SavedQuery, error) {
	q := `SELECT ` + savedCols + ` FROM saved_queries WHERE user_id = ?`
	args := []any{uid}
	if connID > 0 {
		q += ` AND (connection_id = ? OR connection_id IS NULL)`
		args = append(args, connID)
	}
	rows, err := s.DB.QueryContext(ctx, q+` ORDER BY favorite DESC, lower(name)`, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []SavedQuery{}
	for rows.Next() {
		sq, err := scanSaved(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, *sq)
	}
	return out, rows.Err()
}

// GetSaved returns one saved query of the person.
func (s *Store) GetSaved(ctx context.Context, uid, id int64) (*SavedQuery, error) {
	return scanSaved(s.DB.QueryRowContext(ctx, `SELECT `+savedCols+` FROM saved_queries WHERE id = ? AND user_id = ?`, id, uid))
}

// CreateSaved keeps a query.
func (s *Store) CreateSaved(ctx context.Context, uid int64, q *SavedQuery) error {
	now := Now()
	res, err := s.DB.ExecContext(ctx, `INSERT INTO saved_queries (user_id, connection_id, name, database, body, description, favorite, created_at, updated_at)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?)`, uid, nullI(q.ConnectionID), q.Name, q.Database, q.Body, q.Description, boolInt(q.Favorite), now, now)
	if err != nil {
		return err
	}
	q.ID, _ = res.LastInsertId()
	q.CreatedAt, q.UpdatedAt = now, now
	return nil
}

// UpdateSaved changes a saved query.
func (s *Store) UpdateSaved(ctx context.Context, uid int64, q *SavedQuery) error {
	q.UpdatedAt = Now()
	res, err := s.DB.ExecContext(ctx, `UPDATE saved_queries SET connection_id = ?, name = ?, database = ?, body = ?, description = ?, favorite = ?, updated_at = ?
		WHERE id = ? AND user_id = ?`, nullI(q.ConnectionID), q.Name, q.Database, q.Body, q.Description, boolInt(q.Favorite), q.UpdatedAt, q.ID, uid)
	if err != nil {
		return err
	}
	if n, _ := res.RowsAffected(); n == 0 {
		return ErrNotFound
	}
	return nil
}

// RanSaved counts a run of a saved query.
func (s *Store) RanSaved(ctx context.Context, uid, id int64) {
	_, _ = s.DB.ExecContext(ctx, `UPDATE saved_queries SET run_count = run_count + 1, last_run_at = ? WHERE id = ? AND user_id = ?`, Now(), id, uid)
}

// DeleteSaved removes a saved query.
func (s *Store) DeleteSaved(ctx context.Context, uid, id int64) error {
	res, err := s.DB.ExecContext(ctx, `DELETE FROM saved_queries WHERE id = ? AND user_id = ?`, id, uid)
	if err != nil {
		return err
	}
	if n, _ := res.RowsAffected(); n == 0 {
		return ErrNotFound
	}
	return nil
}

// ---- history ----

// AddHistory records a run and keeps the last 1000 per person.
func (s *Store) AddHistory(ctx context.Context, uid int64, h *HistoryEntry) error {
	if len(h.Body) > 20000 {
		h.Body = h.Body[:20000]
	}
	res, err := s.DB.ExecContext(ctx, `INSERT INTO query_history (user_id, connection_id, database, body, ok, error, rows, duration_ms, created_at) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?)`,
		uid, h.ConnectionID, h.Database, h.Body, boolInt(h.OK), h.Error, h.Rows, h.DurationMs, firstNonEmpty(h.CreatedAt, Now()))
	if err != nil {
		return err
	}
	h.ID, _ = res.LastInsertId()
	_, _ = s.DB.ExecContext(ctx, `DELETE FROM query_history WHERE user_id = ? AND id <= (SELECT id FROM query_history WHERE user_id = ? ORDER BY id DESC LIMIT 1 OFFSET 1000)`, uid, uid)
	return nil
}

// ListHistory lists a person's runs, newest first (connID 0 = all; q filters the text).
func (s *Store) ListHistory(ctx context.Context, uid, connID int64, q string, limit int) ([]HistoryEntry, error) {
	if limit <= 0 || limit > 500 {
		limit = 100
	}
	query := `SELECT h.id, h.connection_id, c.name, c.engine, h.database, h.body, h.ok, h.error, h.rows, h.duration_ms, h.created_at
		FROM query_history h JOIN connections c ON c.id = h.connection_id WHERE h.user_id = ?`
	args := []any{uid}
	if connID > 0 {
		query += ` AND h.connection_id = ?`
		args = append(args, connID)
	}
	if q = strings.TrimSpace(q); q != "" {
		query += ` AND h.body LIKE ?`
		args = append(args, "%"+q+"%")
	}
	rows, err := s.DB.QueryContext(ctx, query+` ORDER BY h.id DESC LIMIT ?`, append(args, limit)...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []HistoryEntry{}
	for rows.Next() {
		var h HistoryEntry
		var ok int
		if err := rows.Scan(&h.ID, &h.ConnectionID, &h.Connection, &h.Engine, &h.Database, &h.Body, &ok, &h.Error, &h.Rows, &h.DurationMs, &h.CreatedAt); err != nil {
			return nil, err
		}
		h.OK = ok == 1
		out = append(out, h)
	}
	return out, rows.Err()
}

// ClearHistory forgets a person's runs (connID 0 = all).
func (s *Store) ClearHistory(ctx context.Context, uid, connID int64) error {
	if connID > 0 {
		_, err := s.DB.ExecContext(ctx, `DELETE FROM query_history WHERE user_id = ? AND connection_id = ?`, uid, connID)
		return err
	}
	_, err := s.DB.ExecContext(ctx, `DELETE FROM query_history WHERE user_id = ?`, uid)
	return err
}

// ---- activity ----

// AddActivity records a change.
func (s *Store) AddActivity(ctx context.Context, uid int64, a *Activity) error {
	if len(a.Statement) > 8000 {
		a.Statement = a.Statement[:8000] + "…"
	}
	res, err := s.DB.ExecContext(ctx, `INSERT INTO activity (user_id, connection_id, connection_name, kind, database, target, summary, statement, rows, ok, error, snapshot_id, source, created_at)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`, uid, nullI(a.ConnectionID), a.ConnectionName, a.Kind, a.Database, a.Target, a.Summary, a.Statement,
		a.Rows, boolInt(a.OK), a.Error, nullI(a.SnapshotID), firstNonEmpty(a.Source, "app"), firstNonEmpty(a.CreatedAt, Now()))
	if err != nil {
		return err
	}
	a.ID, _ = res.LastInsertId()
	return nil
}

// ListActivity lists a person's changes, newest first (connID 0 = all).
func (s *Store) ListActivity(ctx context.Context, uid, connID int64, limit int) ([]Activity, error) {
	if limit <= 0 || limit > 500 {
		limit = 100
	}
	q := `SELECT id, connection_id, connection_name, kind, database, target, summary, statement, rows, ok, error, snapshot_id, source, created_at FROM activity WHERE user_id = ?`
	args := []any{uid}
	if connID > 0 {
		q += ` AND connection_id = ?`
		args = append(args, connID)
	}
	rows, err := s.DB.QueryContext(ctx, q+` ORDER BY id DESC LIMIT ?`, append(args, limit)...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []Activity{}
	for rows.Next() {
		var a Activity
		var conn, snap sql.NullInt64
		var ok int
		if err := rows.Scan(&a.ID, &conn, &a.ConnectionName, &a.Kind, &a.Database, &a.Target, &a.Summary, &a.Statement, &a.Rows, &ok, &a.Error, &snap, &a.Source, &a.CreatedAt); err != nil {
			return nil, err
		}
		if conn.Valid {
			a.ConnectionID = &conn.Int64
		}
		if snap.Valid {
			a.SnapshotID = &snap.Int64
		}
		a.OK = ok == 1
		out = append(out, a)
	}
	return out, rows.Err()
}

// ---- snapshots ----

// AddSnapshot records a snapshot.
func (s *Store) AddSnapshot(ctx context.Context, uid int64, sn *Snapshot) error {
	now := Now()
	res, err := s.DB.ExecContext(ctx, `INSERT INTO snapshots (user_id, connection_id, dir, file, size_bytes, reason, created_at) VALUES (?, ?, ?, ?, ?, ?, ?)`,
		uid, sn.ConnectionID, sn.Dir, sn.File, sn.SizeBytes, sn.Reason, now)
	if err != nil {
		return err
	}
	sn.ID, _ = res.LastInsertId()
	sn.CreatedAt = now
	return nil
}

// ListSnapshots lists a connection's snapshots, newest first.
func (s *Store) ListSnapshots(ctx context.Context, uid, connID int64) ([]Snapshot, error) {
	rows, err := s.DB.QueryContext(ctx, `SELECT id, connection_id, dir, file, size_bytes, reason, restored_at, created_at FROM snapshots
		WHERE user_id = ? AND connection_id = ? ORDER BY id DESC`, uid, connID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []Snapshot{}
	for rows.Next() {
		var sn Snapshot
		if err := rows.Scan(&sn.ID, &sn.ConnectionID, &sn.Dir, &sn.File, &sn.SizeBytes, &sn.Reason, &sn.RestoredAt, &sn.CreatedAt); err != nil {
			return nil, err
		}
		out = append(out, sn)
	}
	return out, rows.Err()
}

// GetSnapshot returns one of the person's snapshots.
func (s *Store) GetSnapshot(ctx context.Context, uid, id int64) (*Snapshot, error) {
	var sn Snapshot
	err := s.DB.QueryRowContext(ctx, `SELECT id, connection_id, dir, file, size_bytes, reason, restored_at, created_at FROM snapshots WHERE id = ? AND user_id = ?`, id, uid).
		Scan(&sn.ID, &sn.ConnectionID, &sn.Dir, &sn.File, &sn.SizeBytes, &sn.Reason, &sn.RestoredAt, &sn.CreatedAt)
	if err != nil {
		return nil, notFound(err)
	}
	return &sn, nil
}

// SnapshotRestored marks a snapshot as put back.
func (s *Store) SnapshotRestored(ctx context.Context, uid, id int64) error {
	_, err := s.DB.ExecContext(ctx, `UPDATE snapshots SET restored_at = ? WHERE id = ? AND user_id = ?`, Now(), id, uid)
	return err
}

// DeleteSnapshot forgets a snapshot (the caller removes the files).
func (s *Store) DeleteSnapshot(ctx context.Context, uid, id int64) error {
	_, err := s.DB.ExecContext(ctx, `DELETE FROM snapshots WHERE id = ? AND user_id = ?`, id, uid)
	return err
}

// AllSnapshotDirs lists every snapshot folder still referenced (cleanup).
func (s *Store) AllSnapshotDirs(ctx context.Context) (map[string]bool, error) {
	rows, err := s.DB.QueryContext(ctx, `SELECT dir FROM snapshots`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := map[string]bool{}
	for rows.Next() {
		var d string
		if rows.Scan(&d) == nil {
			out[d] = true
		}
	}
	return out, rows.Err()
}

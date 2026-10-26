package store

import (
	"context"
	"database/sql"
	"encoding/json"
)

const maintCols = `id, user_id, connection_id, containers, snapshot_id, started_at, expires_at, ended_at, end_reason, error`

func scanMaint(row interface{ Scan(...any) error }) (*Maintenance, error) {
	var m Maintenance
	var containers string
	var snap sql.NullInt64
	if err := row.Scan(&m.ID, &m.UserID, &m.ConnectionID, &containers, &snap, &m.StartedAt, &m.ExpiresAt, &m.EndedAt, &m.EndReason, &m.Error); err != nil {
		return nil, notFound(err)
	}
	_ = json.Unmarshal([]byte(containers), &m.Containers)
	if m.Containers == nil {
		m.Containers = []string{}
	}
	if snap.Valid {
		m.SnapshotID = &snap.Int64
	}
	return &m, nil
}

// StartMaintenance records a pause.
func (s *Store) StartMaintenance(ctx context.Context, m *Maintenance) error {
	b, _ := json.Marshal(m.Containers)
	res, err := s.DB.ExecContext(ctx, `INSERT INTO maintenance (user_id, connection_id, containers, snapshot_id, started_at, expires_at) VALUES (?, ?, ?, ?, ?, ?)`,
		m.UserID, m.ConnectionID, string(b), nullI(m.SnapshotID), m.StartedAt, m.ExpiresAt)
	if err != nil {
		return err
	}
	m.ID, _ = res.LastInsertId()
	return nil
}

// OpenMaintenance is the running pause of a connection, if any.
func (s *Store) OpenMaintenance(ctx context.Context, connID int64) (*Maintenance, error) {
	return scanMaint(s.DB.QueryRowContext(ctx, `SELECT `+maintCols+` FROM maintenance WHERE connection_id = ? AND ended_at = '' ORDER BY id DESC LIMIT 1`, connID))
}

// AllOpenMaintenance lists every running pause (background work, start-up).
func (s *Store) AllOpenMaintenance(ctx context.Context) ([]Maintenance, error) {
	rows, err := s.DB.QueryContext(ctx, `SELECT `+maintCols+` FROM maintenance WHERE ended_at = '' ORDER BY id`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []Maintenance
	for rows.Next() {
		m, err := scanMaint(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, *m)
	}
	return out, rows.Err()
}

// ExtendMaintenance moves a pause's end.
func (s *Store) ExtendMaintenance(ctx context.Context, id int64, expires string) error {
	_, err := s.DB.ExecContext(ctx, `UPDATE maintenance SET expires_at = ? WHERE id = ? AND ended_at = ''`, expires, id)
	return err
}

// SetMaintenanceSnapshot links the snapshot taken for a pause.
func (s *Store) SetMaintenanceSnapshot(ctx context.Context, id, snap int64) error {
	_, err := s.DB.ExecContext(ctx, `UPDATE maintenance SET snapshot_id = ? WHERE id = ?`, snap, id)
	return err
}

// EndMaintenance closes a pause.
func (s *Store) EndMaintenance(ctx context.Context, id int64, reason, errText string) error {
	_, err := s.DB.ExecContext(ctx, `UPDATE maintenance SET ended_at = ?, end_reason = ?, error = ? WHERE id = ? AND ended_at = ''`, Now(), reason, errText, id)
	return err
}

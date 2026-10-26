package server

import (
	"context"
	"database/sql"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/abjelosevic88/pocket-homelab-database/internal/discover"
	"github.com/abjelosevic88/pocket-homelab-database/internal/engine"
	"github.com/abjelosevic88/pocket-homelab-database/internal/notify"
	"github.com/abjelosevic88/pocket-homelab-database/internal/store"
)

// ---- changing data safely ----
//
// A server database (PostgreSQL, MySQL, Redis) takes changes while the app
// using it runs; every change through Pocket Database is one transaction.
// A SQLite file is different: an app that has it open keeps its own view
// and writing under it can corrupt it. So a SQLite file a running container
// uses can only be changed while that app is paused: Pocket Database stops
// the containers, copies the file (a snapshot), lets the person change it,
// and starts the containers again when they are done — or by itself when the
// pause time is up, so an app is never left stopped. A file nobody uses gets
// a snapshot before the first change instead.

// gateError says why a change cannot be made right now, with a code the UI acts on.
type gateError struct{ code, msg string }

func (e *gateError) Error() string { return e.msg }

// safetyState is what the UI shows about changing a connection.
type safetyState struct {
	Live      bool               `json:"live"`   // an app uses the file right now
	Owners    []string           `json:"owners"` // the running containers using it
	InUse     bool               `json:"in_use"` // a program has it open (-wal / -shm next to it)
	Pause     *store.Maintenance `json:"pause"`
	CanWrite  bool               `json:"can_write"` // changes can be made now
	Reason    string             `json:"reason"`    // why not
	Code      string             `json:"code"`
	Snapshots []store.Snapshot   `json:"snapshots"`
	Docker    bool               `json:"docker"`
}

// owners lists the running containers that use a SQLite file.
func (s *Server) owners(ctx context.Context, c *store.Connection) []string {
	if c.Engine != engine.SQLite || !s.docker.Available() || c.HostPath == "" {
		return nil
	}
	list, err := s.docker.Containers(ctx)
	if err != nil {
		return nil
	}
	var out []string
	for _, o := range discover.Owners(list, c.HostPath) {
		if o != s.cfg.SelfContainer {
			out = append(out, o)
		}
	}
	return out
}

func (s *Server) safety(ctx context.Context, c *store.Connection, withSnapshots bool) *safetyState {
	st := &safetyState{Owners: []string{}, Snapshots: []store.Snapshot{}, Docker: s.docker.Available()}
	if c.Engine == engine.SQLite {
		st.Owners = append(st.Owners, s.owners(ctx, c)...)
		st.InUse = discover.InUse(c.Path)
		st.Live = len(st.Owners) > 0
		if m, err := s.store.OpenMaintenance(ctx, c.ID); err == nil {
			st.Pause = m
		}
		if withSnapshots {
			if list, err := s.store.ListSnapshots(ctx, c.UserID, c.ID); err == nil {
				st.Snapshots = list
			}
		}
	}
	if err := s.gateCheck(c, st); err != nil {
		st.Reason, st.Code = err.msg, err.code
	} else {
		st.CanWrite = true
	}
	return st
}

func (s *Server) gateCheck(c *store.Connection, st *safetyState) *gateError {
	if !c.AllowWrites {
		return &gateError{"writes_off", "Changes are off for this connection: switch on “Allow changes” in its settings first."}
	}
	if c.Engine != engine.SQLite || st.Pause != nil {
		return nil
	}
	if len(st.Owners) > 0 {
		return &gateError{"needs_pause", strings.Join(st.Owners, ", ") + " use" + map[bool]string{true: "s", false: ""}[len(st.Owners) == 1] +
			" this file right now. Pause first: Pocket Database stops it, keeps a copy of the file and starts it again when you are done."}
	}
	if st.InUse {
		return &gateError{"in_use", "Another program has this file open right now (there is a -wal or -shm file next to it). Stop that program, or pause here to confirm you did."}
	}
	return nil
}

// writeGate is called before every change; for a SQLite file nobody uses it
// takes a snapshot first (at most one every ten minutes).
func (s *Server) writeGate(ctx context.Context, c *store.Connection) (*int64, error) {
	if linkApp(ctx) != "" {
		return nil, &gateError{"link_read_only", "Other apps can only read through Pocket Core."}
	}
	st := s.safety(ctx, c, false)
	if err := s.gateCheck(c, st); err != nil {
		return nil, err
	}
	if c.Engine != engine.SQLite {
		return nil, nil
	}
	if st.Pause != nil {
		_ = s.store.ExtendMaintenance(ctx, st.Pause.ID, s.pauseUntil(ctx, c.UserID))
		return st.Pause.SnapshotID, nil
	}
	if list, err := s.store.ListSnapshots(ctx, c.UserID, c.ID); err == nil && len(list) > 0 {
		if t, err := time.Parse(time.RFC3339Nano, list[0].CreatedAt); err == nil && time.Since(t) < 10*time.Minute {
			return &list[0].ID, nil
		}
	}
	sn, err := s.takeSnapshot(ctx, c, "Before a change")
	if err != nil {
		return nil, fmt.Errorf("could not keep a copy of the file before changing it, so nothing was changed: %w", err)
	}
	return &sn.ID, nil
}

func (s *Server) pauseUntil(ctx context.Context, uid int64) string {
	st, _ := s.store.GetSettings(ctx, uid)
	m := st.PauseMinutes
	if m <= 0 {
		m = 15
	}
	return time.Now().UTC().Add(time.Duration(m) * time.Minute).Format(time.RFC3339Nano)
}

// ---- snapshots ----

func (s *Server) snapshotRoot() string { return filepath.Join(s.cfg.DataDir, "snapshots") }

// takeSnapshot copies a SQLite file into DATA_DIR/snapshots: a consistent
// copy through SQLite itself (VACUUM INTO), else the file and its log.
func (s *Server) takeSnapshot(ctx context.Context, c *store.Connection, reason string) (*store.Snapshot, error) {
	if c.Engine != engine.SQLite {
		return nil, fmt.Errorf("only SQLite files have snapshots")
	}
	fi, err := os.Stat(c.Path)
	if err != nil {
		return nil, err
	}
	if fi.Size() > 4<<30 {
		return nil, fmt.Errorf("the file is larger than 4 GB")
	}
	rel := filepath.Join(fmt.Sprint(c.UserID), fmt.Sprint(c.ID), time.Now().Format("20060102-150405.000"))
	dir := filepath.Join(s.snapshotRoot(), rel)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return nil, err
	}
	dst := filepath.Join(dir, filepath.Base(c.Path))
	if err := vacuumInto(ctx, c.Path, dst); err != nil {
		s.log.Debug("snapshot: VACUUM INTO failed, copying the file", "err", err)
		_ = os.Remove(dst)
		if err := copyFile(c.Path, dst); err != nil {
			_ = os.RemoveAll(dir)
			return nil, err
		}
		for _, suffix := range []string{"-wal"} {
			if _, err := os.Stat(c.Path + suffix); err == nil {
				_ = copyFile(c.Path+suffix, dst+suffix)
			}
		}
	}
	size := int64(0)
	if fi, err := os.Stat(dst); err == nil {
		size = fi.Size()
	}
	sn := &store.Snapshot{ConnectionID: c.ID, Dir: rel, File: c.Path, SizeBytes: size, Reason: reason}
	if err := s.store.AddSnapshot(ctx, c.UserID, sn); err != nil {
		_ = os.RemoveAll(dir)
		return nil, err
	}
	cid := c.ID
	_ = s.store.AddActivity(ctx, c.UserID, &store.Activity{ConnectionID: &cid, ConnectionName: c.Name, Kind: "snapshot", Target: filepath.Base(c.Path),
		Summary: reason, OK: true, SnapshotID: &sn.ID, Source: sourceOf(ctx)})
	s.pruneConnSnapshots(ctx, c)
	return sn, nil
}

func vacuumInto(ctx context.Context, src, dst string) error {
	db, err := sql.Open("sqlite", "file:"+src+"?mode=ro&_pragma=busy_timeout(5000)")
	if err != nil {
		return err
	}
	defer db.Close()
	db.SetMaxOpenConns(1)
	cctx, cancel := context.WithTimeout(ctx, 5*time.Minute)
	defer cancel()
	_, err = db.ExecContext(cctx, `VACUUM INTO ?`, dst)
	return err
}

func copyFile(src, dst string) error {
	in, err := os.Open(src)
	if err != nil {
		return err
	}
	defer in.Close()
	out, err := os.OpenFile(dst, os.O_CREATE|os.O_WRONLY|os.O_TRUNC, 0o644)
	if err != nil {
		return err
	}
	if _, err := io.Copy(out, in); err != nil {
		out.Close()
		return err
	}
	return out.Close()
}

// pruneConnSnapshots keeps the newest snapshots of a connection.
func (s *Server) pruneConnSnapshots(ctx context.Context, c *store.Connection) {
	st, _ := s.store.GetSettings(ctx, c.UserID)
	keep := st.SnapshotKeep
	if keep <= 0 {
		keep = 10
	}
	list, err := s.store.ListSnapshots(ctx, c.UserID, c.ID)
	if err != nil {
		return
	}
	for i, sn := range list {
		if i < keep {
			continue
		}
		_ = os.RemoveAll(filepath.Join(s.snapshotRoot(), sn.Dir))
		_ = s.store.DeleteSnapshot(ctx, c.UserID, sn.ID)
	}
}

// pruneSnapshots removes snapshot folders nothing points at any more.
func (s *Server) pruneSnapshots(ctx context.Context) {
	known, err := s.store.AllSnapshotDirs(ctx)
	if err != nil {
		return
	}
	root := s.snapshotRoot()
	matches, _ := filepath.Glob(filepath.Join(root, "*", "*", "*"))
	for _, m := range matches {
		rel, err := filepath.Rel(root, m)
		if err == nil && !known[rel] {
			_ = os.RemoveAll(m)
		}
	}
}

// restoreSnapshot puts a snapshot back (the app must be paused, or unused).
func (s *Server) restoreSnapshot(ctx context.Context, c *store.Connection, sn *store.Snapshot) error {
	st := s.safety(ctx, c, false)
	if st.Pause == nil && (st.Live || st.InUse) {
		return &gateError{"needs_pause", "Pause the app first: a file in use cannot be replaced."}
	}
	src := filepath.Join(s.snapshotRoot(), sn.Dir, filepath.Base(sn.File))
	if !engine.IsSQLiteFile(src) {
		return fmt.Errorf("the snapshot file is missing or damaged")
	}
	before, err := s.takeSnapshot(ctx, c, fmt.Sprintf("Before putting back the snapshot of %s", humanTime(sn.CreatedAt)))
	if err != nil {
		return fmt.Errorf("could not keep a copy of the current file first: %w", err)
	}
	s.pools.drop(c.ID)
	tmp := c.Path + ".pocket-restore"
	if err := copyFile(src, tmp); err != nil {
		return err
	}
	for _, suffix := range []string{"-wal", "-shm", "-journal"} {
		_ = os.Remove(c.Path + suffix)
	}
	if err := os.Rename(tmp, c.Path); err != nil {
		_ = os.Remove(tmp)
		return err
	}
	if _, err := os.Stat(src + "-wal"); err == nil {
		_ = copyFile(src+"-wal", c.Path+"-wal")
	}
	_ = s.store.SnapshotRestored(ctx, c.UserID, sn.ID)
	cid := c.ID
	_ = s.store.AddActivity(ctx, c.UserID, &store.Activity{ConnectionID: &cid, ConnectionName: c.Name, Kind: "restore", Target: filepath.Base(c.Path),
		Summary: "Put back the snapshot of " + humanTime(sn.CreatedAt), OK: true, SnapshotID: &before.ID, Source: sourceOf(ctx)})
	s.publish(ctx, c.UserID, "database.restored", c.Name, map[string]any{"connection_id": c.ID, "connection": c.Name, "snapshot_at": sn.CreatedAt})
	return nil
}

func humanTime(rfc string) string {
	t, err := time.Parse(time.RFC3339Nano, rfc)
	if err != nil {
		return rfc
	}
	return t.Local().Format("2 Jan 15:04")
}

// ---- pauses ----

// startPause stops the containers using a SQLite file, snapshots it and
// opens it for changes. force pauses a file no container uses (the person
// stopped the program that has it open themselves).
func (s *Server) startPause(ctx context.Context, c *store.Connection, force bool) (*store.Maintenance, error) {
	if c.Engine != engine.SQLite {
		return nil, &gateError{"not_sqlite", "Only SQLite files are paused: server databases take changes while their app runs."}
	}
	if !c.AllowWrites {
		return nil, &gateError{"writes_off", "Switch on “Allow changes” for this connection first."}
	}
	s.maintMu.Lock()
	defer s.maintMu.Unlock()
	if m, err := s.store.OpenMaintenance(ctx, c.ID); err == nil {
		return m, nil
	}
	owners := s.owners(ctx, c)
	if len(owners) == 0 && !discover.InUse(c.Path) && !force {
		return nil, &gateError{"no_pause_needed", "Nothing uses this file right now: changes need no pause."}
	}
	var stopped []string
	for _, name := range owners {
		sctx, cancel := context.WithTimeout(ctx, 60*time.Second)
		err := s.docker.Stop(sctx, name, 30*time.Second)
		cancel()
		if err != nil {
			for _, n := range stopped {
				_ = s.docker.Start(context.Background(), n)
			}
			return nil, fmt.Errorf("could not stop %s: %w", name, err)
		}
		stopped = append(stopped, name)
	}
	// a cleanly stopped app folds its log back into the file
	for i := 0; i < 20 && len(stopped) > 0 && discover.InUse(c.Path); i++ {
		time.Sleep(250 * time.Millisecond)
	}
	s.pools.drop(c.ID)
	m := &store.Maintenance{UserID: c.UserID, ConnectionID: c.ID, Containers: stopped, StartedAt: store.Now(), ExpiresAt: s.pauseUntil(ctx, c.UserID)}
	if m.Containers == nil {
		m.Containers = []string{}
	}
	sn, err := s.takeSnapshot(ctx, c, "Before changes (paused "+firstNonEmpty(strings.Join(stopped, ", "), "by hand")+")")
	if err != nil {
		for _, n := range stopped {
			_ = s.docker.Start(context.Background(), n)
		}
		return nil, fmt.Errorf("could not keep a copy of the file, so the app was started again: %w", err)
	}
	m.SnapshotID = &sn.ID
	if err := s.store.StartMaintenance(ctx, m); err != nil {
		for _, n := range stopped {
			_ = s.docker.Start(context.Background(), n)
		}
		return nil, err
	}
	cid := c.ID
	_ = s.store.AddActivity(ctx, c.UserID, &store.Activity{ConnectionID: &cid, ConnectionName: c.Name, Kind: "pause", Target: filepath.Base(c.Path),
		Summary: "Paused " + firstNonEmpty(strings.Join(stopped, ", "), "(nothing to stop)") + " to change the file", OK: true, SnapshotID: &sn.ID, Source: sourceOf(ctx)})
	c.Status, c.StatusDetail = "paused", "Paused for changes"
	c.CheckedAt = store.Now()
	_ = s.store.SetConnectionStatus(ctx, c)
	s.log.Info("paused for changes", "connection", c.Name, "containers", stopped)
	return m, nil
}

// endPause starts the stopped containers again.
func (s *Server) endPause(ctx context.Context, m *store.Maintenance, reason string) error {
	s.maintMu.Lock()
	defer s.maintMu.Unlock()
	s.pools.drop(m.ConnectionID)
	var failed []string
	for _, name := range m.Containers {
		sctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
		err := s.docker.Start(sctx, name)
		cancel()
		if err != nil {
			failed = append(failed, name+": "+err.Error())
		}
	}
	errText := strings.Join(failed, "; ")
	_ = s.store.EndMaintenance(ctx, m.ID, reason, errText)
	c, err := s.store.GetConnection(ctx, m.UserID, m.ConnectionID)
	if err != nil {
		return nil
	}
	cid := c.ID
	summary := "Started " + firstNonEmpty(strings.Join(m.Containers, ", "), "nothing") + " again (" + reason + ")"
	_ = s.store.AddActivity(ctx, c.UserID, &store.Activity{ConnectionID: &cid, ConnectionName: c.Name, Kind: "resume", Target: filepath.Base(c.Path),
		Summary: summary, OK: errText == "", Error: errText, Source: sourceOf(ctx)})
	c.Status, c.StatusDetail, c.CheckedAt = "", "", store.Now()
	_ = s.store.SetConnectionStatus(ctx, c)
	if errText != "" || reason != "done" {
		st, _ := s.store.GetSettings(ctx, c.UserID)
		body := summary + "."
		prio := "info"
		if errText != "" {
			body, prio = "Could not start again: "+errText, "warning"
		}
		_ = notify.New(st.NotifyURL, st.NotifyFormat).Send(ctx, notify.Message{Title: "Pocket Database: " + c.Name, Body: body, Priority: prio, Tags: []string{"floppy_disk"}, Link: s.cfg.BaseURL + fmt.Sprintf("/c/%d", c.ID)})
	}
	if errText != "" {
		return fmt.Errorf("could not start everything again: %s", errText)
	}
	return nil
}

// expirePauses ends the pauses whose time is up.
func (s *Server) expirePauses(ctx context.Context) {
	open, err := s.store.AllOpenMaintenance(ctx)
	if err != nil {
		return
	}
	now := time.Now().UTC()
	for _, m := range open {
		t, err := time.Parse(time.RFC3339Nano, m.ExpiresAt)
		if err == nil && now.After(t) {
			s.log.Info("pause time is up, starting the app again", "connection", m.ConnectionID)
			_ = s.endPause(ctx, &m, "the pause time was up")
		}
	}
}

// resumeAll ends every open pause (on start-up).
func (s *Server) resumeAll(ctx context.Context, reason string) {
	open, err := s.store.AllOpenMaintenance(ctx)
	if err != nil {
		return
	}
	for _, m := range open {
		_ = s.endPause(ctx, &m, reason)
	}
}

// ---- handlers ----

func (s *Server) handleSafety(w http.ResponseWriter, r *http.Request) {
	c, ok := s.conn(w, r)
	if !ok {
		return
	}
	writeJSON(w, http.StatusOK, s.safety(r.Context(), c, true))
}

func (s *Server) handlePause(w http.ResponseWriter, r *http.Request) {
	c, ok := s.conn(w, r)
	if !ok {
		return
	}
	if linkApp(r.Context()) != "" {
		writeErr(w, http.StatusForbidden, "other apps cannot pause apps")
		return
	}
	var in struct {
		Action string `json:"action"` // start | finish | extend
		Force  bool   `json:"force"`
	}
	if err := decode(r, &in); err != nil {
		writeErr(w, http.StatusBadRequest, "invalid json")
		return
	}
	ctx := r.Context()
	switch in.Action {
	case "start":
		if _, err := s.startPause(ctx, c, in.Force); err != nil {
			s.dbErr(w, err)
			return
		}
	case "finish", "extend":
		m, err := s.store.OpenMaintenance(ctx, c.ID)
		if err != nil {
			writeErr(w, http.StatusConflict, "nothing is paused")
			return
		}
		if in.Action == "extend" {
			_ = s.store.ExtendMaintenance(ctx, m.ID, s.pauseUntil(ctx, c.UserID))
		} else if err := s.endPause(ctx, m, "done"); err != nil {
			writeErr(w, http.StatusBadGateway, err.Error())
			return
		}
	default:
		writeErr(w, http.StatusBadRequest, "action is start, finish or extend")
		return
	}
	c, _ = s.store.GetConnection(ctx, c.UserID, c.ID)
	writeJSON(w, http.StatusOK, s.safety(ctx, c, true))
}

func (s *Server) handleTakeSnapshot(w http.ResponseWriter, r *http.Request) {
	c, ok := s.conn(w, r)
	if !ok {
		return
	}
	if linkApp(r.Context()) != "" {
		writeErr(w, http.StatusForbidden, "other apps can only read")
		return
	}
	sn, err := s.takeSnapshot(r.Context(), c, "Taken by hand")
	if err != nil {
		s.dbErr(w, err)
		return
	}
	writeJSON(w, http.StatusOK, sn)
}

func (s *Server) snapshotFor(w http.ResponseWriter, r *http.Request) (*store.Connection, *store.Snapshot, bool) {
	uid := actorID(r.Context())
	sn, err := s.store.GetSnapshot(r.Context(), uid, idParam(r, "id"))
	if err != nil {
		s.fail(w, err, "snapshot")
		return nil, nil, false
	}
	c, err := s.store.GetConnection(r.Context(), uid, sn.ConnectionID)
	if err != nil {
		s.fail(w, err, "connection")
		return nil, nil, false
	}
	return c, sn, true
}

func (s *Server) handleRestoreSnapshot(w http.ResponseWriter, r *http.Request) {
	if linkApp(r.Context()) != "" {
		writeErr(w, http.StatusForbidden, "other apps can only read")
		return
	}
	c, sn, ok := s.snapshotFor(w, r)
	if !ok {
		return
	}
	if err := s.restoreSnapshot(r.Context(), c, sn); err != nil {
		s.dbErr(w, err)
		return
	}
	writeJSON(w, http.StatusOK, s.safety(r.Context(), c, true))
}

func (s *Server) handleDeleteSnapshot(w http.ResponseWriter, r *http.Request) {
	if linkApp(r.Context()) != "" {
		writeErr(w, http.StatusForbidden, "other apps can only read")
		return
	}
	c, sn, ok := s.snapshotFor(w, r)
	if !ok {
		return
	}
	_ = os.RemoveAll(filepath.Join(s.snapshotRoot(), sn.Dir))
	if err := s.store.DeleteSnapshot(r.Context(), c.UserID, sn.ID); err != nil {
		s.fail(w, err, "delete snapshot")
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

func (s *Server) handleDownloadSnapshot(w http.ResponseWriter, r *http.Request) {
	_, sn, ok := s.snapshotFor(w, r)
	if !ok {
		return
	}
	p := filepath.Join(s.snapshotRoot(), sn.Dir, filepath.Base(sn.File))
	name := strings.TrimSuffix(filepath.Base(sn.File), filepath.Ext(sn.File)) + "-" + strings.ReplaceAll(filepath.Base(sn.Dir), ".", "-") + filepath.Ext(sn.File)
	w.Header().Set("Content-Type", "application/vnd.sqlite3")
	w.Header().Set("Content-Disposition", `attachment; filename="`+name+`"`)
	http.ServeFile(w, r, p)
}

// sourceOf names who made a change: the app, a token, or a Pocket app through Core.
func sourceOf(ctx context.Context) string {
	if a := linkApp(ctx); a != "" {
		return a
	}
	return "app"
}

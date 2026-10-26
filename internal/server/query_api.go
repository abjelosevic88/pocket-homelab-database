package server

import (
	"context"
	"fmt"
	"net/http"
	"strings"
	"time"

	"github.com/abjelosevic88/pocket-homelab-database/internal/engine"
	"github.com/abjelosevic88/pocket-homelab-database/internal/store"
)

// ---- the SQL editor ----

type confirmReason struct {
	Statement string `json:"statement"`
	Reason    string `json:"reason"`
}

// handleQuery runs a script. Anything that changes data needs "Allow
// changes" on the connection, passes the safety gate, and — when it is
// destructive, or the person asked to be asked — a second look first.
func (s *Server) handleQuery(w http.ResponseWriter, r *http.Request) {
	c, ok := s.conn(w, r)
	if !ok {
		return
	}
	var in struct {
		DB      string `json:"db"`
		SQL     string `json:"sql"`
		Confirm bool   `json:"confirm"`
		SavedID int64  `json:"saved_id"`
	}
	if err := decode(r, &in); err != nil {
		writeErr(w, http.StatusBadRequest, "invalid json")
		return
	}
	if !engine.IsSQL(c.Engine) {
		writeErr(w, http.StatusBadRequest, "use the console for Redis")
		return
	}
	stmts := engine.Split(in.SQL, c.Engine)
	if len(stmts) == 0 {
		writeErr(w, http.StatusBadRequest, "type a query first")
		return
	}
	if len(stmts) > 200 {
		writeErr(w, http.StatusBadRequest, "up to 200 statements at once")
		return
	}
	ctx := r.Context()
	st, _ := s.store.GetSettings(ctx, c.UserID)
	writes := false
	var reasons []confirmReason
	for _, stmt := range stmts {
		kind := engine.Classify(stmt)
		if kind == "read" {
			continue
		}
		writes = true
		if why := engine.Destructive(stmt); why != "" {
			reasons = append(reasons, confirmReason{stmt, why})
		} else if st.ConfirmWrites && kind != "other" {
			reasons = append(reasons, confirmReason{stmt, map[string]string{"ddl": "it changes the structure", "write": "it changes data"}[kind]})
		}
	}
	var snap *int64
	if writes {
		if linkApp(ctx) != "" {
			writeErr(w, http.StatusForbidden, "other apps can only read through Pocket Core")
			return
		}
		var err error
		if !c.AllowWrites {
			s.dbErr(w, &gateError{"writes_off", "This connection is read-only: switch on “Allow changes” for it to run statements that change something."})
			return
		}
		if len(reasons) > 0 && !in.Confirm {
			writeJSON(w, http.StatusOK, map[string]any{"needs_confirm": true, "reasons": reasons})
			return
		}
		if snap, err = s.writeGate(ctx, c); err != nil {
			s.dbErr(w, err)
			return
		}
	}
	timeout := time.Duration(st.QueryTimeoutS) * time.Second
	qctx, cancel := context.WithTimeout(ctx, timeout+10*time.Second)
	defer cancel()
	q, err := s.openSQL(qctx, c, in.DB, !writes)
	if err != nil {
		s.dbErr(w, err)
		return
	}
	start := time.Now()
	results := q.Run(qctx, in.SQL, engine.RunOptions{ReadOnly: !writes, MaxRows: st.RowLimit, MaxCell: 4000, Timeout: timeout})
	took := time.Since(start).Milliseconds()
	s.queries.Add(1)
	h := &store.HistoryEntry{ConnectionID: c.ID, Database: in.DB, Body: in.SQL, OK: true, DurationMs: took}
	var changed int64
	var verbs []string
	for _, res := range results {
		if res.Error != "" && h.Error == "" {
			h.OK, h.Error = false, res.Error
		}
		if res.HasRows && res.Kind == "read" {
			h.Rows += int64(len(res.Rows))
		} else {
			h.Rows += res.RowsAffected
		}
		if res.Kind != "read" && res.Error == "" && !res.Skipped {
			changed += res.RowsAffected
			verbs = append(verbs, engine.Verb(res.Statement))
		}
	}
	_ = s.store.AddHistory(ctx, c.UserID, h)
	if in.SavedID > 0 {
		s.store.RanSaved(ctx, c.UserID, in.SavedID)
	}
	if writes {
		cid := c.ID
		summary := fmt.Sprintf("Ran %s", strings.Join(uniq(verbs), ", "))
		if len(verbs) == 0 {
			summary = "Tried to run SQL that changes data"
		} else if changed > 0 {
			summary += fmt.Sprintf(" (%d %s)", changed, plural(int(changed), "row", "rows"))
		}
		_ = s.store.AddActivity(ctx, c.UserID, &store.Activity{ConnectionID: &cid, ConnectionName: c.Name, Kind: "sql", Database: in.DB, Summary: summary,
			Statement: in.SQL, Rows: changed, OK: h.OK, Error: h.Error, SnapshotID: snap, Source: sourceOf(ctx)})
		if h.OK {
			s.changes.Add(1)
			s.publish(ctx, c.UserID, "database.changed", c.Name, map[string]any{"connection_id": c.ID, "connection": c.Name, "rows": changed, "summary": summary})
		}
	}
	writeJSON(w, http.StatusOK, map[string]any{"results": results, "duration_ms": took, "read_only": !writes})
}

func uniq(list []string) []string {
	seen := map[string]bool{}
	var out []string
	for _, v := range list {
		if v != "" && !seen[v] {
			seen[v] = true
			out = append(out, v)
		}
	}
	return out
}

// ---- history ----

func (s *Server) handleHistory(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	list, err := s.store.ListHistory(ctx, actorID(ctx), int64(queryInt(r, "connection")), r.URL.Query().Get("q"), queryInt(r, "limit"))
	if err != nil {
		s.fail(w, err, "history")
		return
	}
	writeJSON(w, http.StatusOK, list)
}

func (s *Server) handleClearHistory(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	if err := s.store.ClearHistory(ctx, actorID(ctx), int64(queryInt(r, "connection"))); err != nil {
		s.fail(w, err, "history")
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

// ---- saved queries ----

type savedInput struct {
	ConnectionID *int64 `json:"connection_id"`
	Name         string `json:"name"`
	Database     string `json:"database"`
	Body         string `json:"body"`
	Description  string `json:"description"`
	Favorite     bool   `json:"favorite"`
}

func (s *Server) checkSaved(ctx context.Context, uid int64, in *savedInput) string {
	in.Name, in.Body = strings.TrimSpace(in.Name), strings.TrimSpace(in.Body)
	if in.Name == "" || in.Body == "" {
		return "a name and the query are needed"
	}
	if len(in.Name) > 120 || len(in.Body) > 100000 {
		return "too long"
	}
	if in.ConnectionID != nil && *in.ConnectionID > 0 {
		if _, err := s.store.GetConnection(ctx, uid, *in.ConnectionID); err != nil {
			return "unknown connection"
		}
	} else {
		in.ConnectionID = nil
	}
	return ""
}

func (s *Server) handleListSaved(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	list, err := s.store.ListSaved(ctx, actorID(ctx), int64(queryInt(r, "connection")))
	if err != nil {
		s.fail(w, err, "saved queries")
		return
	}
	writeJSON(w, http.StatusOK, list)
}

func (s *Server) handleCreateSaved(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	var in savedInput
	if err := decode(r, &in); err != nil {
		writeErr(w, http.StatusBadRequest, "invalid json")
		return
	}
	if msg := s.checkSaved(ctx, actorID(ctx), &in); msg != "" {
		writeErr(w, http.StatusBadRequest, msg)
		return
	}
	q := &store.SavedQuery{ConnectionID: in.ConnectionID, Name: in.Name, Database: in.Database, Body: in.Body, Description: in.Description, Favorite: in.Favorite}
	if err := s.store.CreateSaved(ctx, actorID(ctx), q); err != nil {
		s.fail(w, err, "save query")
		return
	}
	writeJSON(w, http.StatusCreated, q)
}

func (s *Server) handleUpdateSaved(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	uid := actorID(ctx)
	q, err := s.store.GetSaved(ctx, uid, idParam(r, "id"))
	if err != nil {
		s.fail(w, err, "saved query")
		return
	}
	in := savedInput{ConnectionID: q.ConnectionID, Name: q.Name, Database: q.Database, Body: q.Body, Description: q.Description, Favorite: q.Favorite}
	if _, err := decodeKeys(r, &in); err != nil {
		writeErr(w, http.StatusBadRequest, "invalid json")
		return
	}
	if msg := s.checkSaved(ctx, uid, &in); msg != "" {
		writeErr(w, http.StatusBadRequest, msg)
		return
	}
	q.ConnectionID, q.Name, q.Database, q.Body, q.Description, q.Favorite = in.ConnectionID, in.Name, in.Database, in.Body, in.Description, in.Favorite
	if err := s.store.UpdateSaved(ctx, uid, q); err != nil {
		s.fail(w, err, "save query")
		return
	}
	writeJSON(w, http.StatusOK, q)
}

func (s *Server) handleDeleteSaved(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	if err := s.store.DeleteSaved(ctx, actorID(ctx), idParam(r, "id")); err != nil {
		s.fail(w, err, "delete query")
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

// ---- activity ----

func (s *Server) handleActivity(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	list, err := s.store.ListActivity(ctx, actorID(ctx), int64(queryInt(r, "connection")), queryInt(r, "limit"))
	if err != nil {
		s.fail(w, err, "activity")
		return
	}
	writeJSON(w, http.StatusOK, list)
}

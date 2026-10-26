package server

import (
	"context"
	"encoding/csv"
	"encoding/json"
	"fmt"
	"net/http"
	"regexp"
	"strings"
	"time"

	"github.com/abjelosevic88/pocket-homelab-database/internal/engine"
	"github.com/abjelosevic88/pocket-homelab-database/internal/store"
)

// ---- browsing ----

func (s *Server) handleServerInfo(w http.ResponseWriter, r *http.Request) {
	c, ok := s.conn(w, r)
	if !ok {
		return
	}
	ctx, cancel := context.WithTimeout(r.Context(), 20*time.Second)
	defer cancel()
	var info engine.ServerInfo
	var err error
	if c.Engine == engine.Redis {
		var k *engine.KV
		if k, err = s.openKV(ctx, c, "", true); err == nil {
			info, err = k.Server(ctx)
		}
	} else {
		var q *engine.SQL
		if q, err = s.openSQL(ctx, c, r.URL.Query().Get("db"), true); err == nil {
			info, err = q.Server(ctx)
		}
	}
	if err != nil {
		s.dbErr(w, err)
		return
	}
	if linkApp(ctx) != "" {
		delete(info.Extra, "file")
	}
	writeJSON(w, http.StatusOK, info)
}

func (s *Server) handleDatabases(w http.ResponseWriter, r *http.Request) {
	c, ok := s.conn(w, r)
	if !ok {
		return
	}
	ctx, cancel := context.WithTimeout(r.Context(), 20*time.Second)
	defer cancel()
	var list []engine.DBInfo
	var err error
	if c.Engine == engine.Redis {
		var k *engine.KV
		if k, err = s.openKV(ctx, c, "", true); err == nil {
			list, err = k.Databases(ctx)
		}
	} else {
		var q *engine.SQL
		if q, err = s.openSQL(ctx, c, "", true); err == nil {
			list, err = q.Databases(ctx)
		}
	}
	if err != nil {
		s.dbErr(w, err)
		return
	}
	writeJSON(w, http.StatusOK, list)
}

func (s *Server) handleSchemas(w http.ResponseWriter, r *http.Request) {
	c, ok := s.conn(w, r)
	if !ok {
		return
	}
	ctx, cancel := context.WithTimeout(r.Context(), 30*time.Second)
	defer cancel()
	q, err := s.openSQL(ctx, c, r.URL.Query().Get("db"), true)
	if err != nil {
		s.dbErr(w, err)
		return
	}
	list, err := q.Schemas(ctx)
	if err != nil {
		s.dbErr(w, err)
		return
	}
	writeJSON(w, http.StatusOK, list)
}

// tableRef reads ?db&schema&name (or the JSON body's).
type tableRef struct {
	DB     string `json:"db"`
	Schema string `json:"schema"`
	Name   string `json:"name"`
}

func refFrom(r *http.Request) tableRef {
	q := r.URL.Query()
	return tableRef{DB: q.Get("db"), Schema: q.Get("schema"), Name: q.Get("name")}
}

func (s *Server) tableFor(ctx context.Context, c *store.Connection, ref tableRef, readOnly bool) (*engine.SQL, *engine.TableInfo, error) {
	if ref.Name == "" {
		return nil, nil, &engine.UserError{Msg: "which table?"}
	}
	q, err := s.openSQL(ctx, c, ref.DB, readOnly)
	if err != nil {
		return nil, nil, err
	}
	t, err := q.Table(ctx, ref.Schema, ref.Name)
	return q, t, err
}

func (s *Server) handleTable(w http.ResponseWriter, r *http.Request) {
	c, ok := s.conn(w, r)
	if !ok {
		return
	}
	ctx, cancel := context.WithTimeout(r.Context(), 30*time.Second)
	defer cancel()
	_, t, err := s.tableFor(ctx, c, refFrom(r), true)
	if err != nil {
		s.dbErr(w, err)
		return
	}
	writeJSON(w, http.StatusOK, t)
}

func (s *Server) handleRows(w http.ResponseWriter, r *http.Request) {
	c, ok := s.conn(w, r)
	if !ok {
		return
	}
	ctx, cancel := context.WithTimeout(r.Context(), 45*time.Second)
	defer cancel()
	q, t, err := s.tableFor(ctx, c, refFrom(r), true)
	if err != nil {
		s.dbErr(w, err)
		return
	}
	st, _ := s.store.GetSettings(ctx, c.UserID)
	rq := engine.RowsQuery{Search: r.URL.Query().Get("search"), Sort: r.URL.Query().Get("sort"), Desc: queryBool(r, "desc"),
		Limit: queryInt(r, "limit"), Offset: queryInt(r, "offset"), MaxCell: 8000}
	if rq.Limit <= 0 {
		rq.Limit = st.PageSize
	}
	if f := r.URL.Query().Get("filters"); f != "" {
		if err := json.Unmarshal([]byte(f), &rq.Filters); err != nil {
			writeErr(w, http.StatusBadRequest, "filters are a JSON list")
			return
		}
	}
	page, err := q.Rows(ctx, t, rq)
	if err != nil {
		s.dbErr(w, err)
		return
	}
	writeJSON(w, http.StatusOK, page)
}

// handleChangeRows saves edits, new rows and deletions in one transaction.
func (s *Server) handleChangeRows(w http.ResponseWriter, r *http.Request) {
	c, ok := s.conn(w, r)
	if !ok {
		return
	}
	var in struct {
		tableRef
		Changes []engine.Change `json:"changes"`
	}
	if err := decode(r, &in); err != nil {
		writeErr(w, http.StatusBadRequest, "invalid json")
		return
	}
	if len(in.Changes) == 0 {
		writeErr(w, http.StatusBadRequest, "nothing to change")
		return
	}
	if len(in.Changes) > 1000 {
		writeErr(w, http.StatusBadRequest, "up to 1000 changes at once")
		return
	}
	ctx, cancel := context.WithTimeout(r.Context(), 60*time.Second)
	defer cancel()
	snap, err := s.writeGate(ctx, c)
	if err != nil {
		s.dbErr(w, err)
		return
	}
	q, t, err := s.tableFor(ctx, c, in.tableRef, false)
	if err != nil {
		s.dbErr(w, err)
		return
	}
	n, stmts, err := q.Apply(ctx, t, in.Changes)
	kinds := map[string]int{}
	for _, ch := range in.Changes {
		kinds[ch.Op]++
	}
	kind := "edit"
	if len(kinds) == 1 {
		for k := range kinds {
			kind = map[string]string{"insert": "insert", "update": "edit", "delete": "delete"}[k]
		}
	}
	var parts []string
	for _, op := range []string{"update", "insert", "delete"} {
		if k := kinds[op]; k > 0 {
			parts = append(parts, fmt.Sprintf("%s %d %s", map[string]string{"update": "changed", "insert": "added", "delete": "deleted"}[op], k, plural(k, "row", "rows")))
		}
	}
	summary := strings.Join(parts, ", ")
	cid := c.ID
	act := &store.Activity{ConnectionID: &cid, ConnectionName: c.Name, Kind: kind, Database: in.DB, Target: tableName(in.tableRef), Summary: summary,
		Statement: strings.Join(stmts, ";\n"), Rows: n, OK: err == nil, SnapshotID: snap, Source: sourceOf(ctx)}
	if err != nil {
		act.Error = engine.CleanErr(err)
		act.Summary = "Tried to " + strings.Replace(strings.Replace(strings.Replace(summary, "changed", "change", 1), "added", "add", 1), "deleted", "delete", 1)
		_ = s.store.AddActivity(ctx, c.UserID, act)
		s.dbErr(w, err)
		return
	}
	_ = s.store.AddActivity(ctx, c.UserID, act)
	s.changes.Add(1)
	s.publish(ctx, c.UserID, "database.changed", c.Name, map[string]any{"connection_id": c.ID, "connection": c.Name, "table": tableName(in.tableRef), "rows": n, "summary": summary})
	writeJSON(w, http.StatusOK, map[string]any{"ok": true, "rows": n, "summary": summary})
}

func tableName(t tableRef) string {
	if t.Schema != "" && t.Schema != "main" && t.Schema != "public" && t.Schema != t.DB {
		return t.Schema + "." + t.Name
	}
	return t.Name
}

func plural(n int, one, many string) string {
	if n == 1 {
		return one
	}
	return many
}

var safeName = regexp.MustCompile(`[^A-Za-z0-9._-]+`)

// handleExport streams a table, or a query (?sql=), as CSV or JSON.
func (s *Server) handleExport(w http.ResponseWriter, r *http.Request) {
	c, ok := s.conn(w, r)
	if !ok {
		return
	}
	ctx, cancel := context.WithTimeout(r.Context(), 10*time.Minute)
	defer cancel()
	ref := refFrom(r)
	stmt := strings.TrimSpace(r.URL.Query().Get("sql"))
	name := "query"
	q, err := s.openSQL(ctx, c, ref.DB, true)
	if err != nil {
		s.dbErr(w, err)
		return
	}
	if stmt == "" {
		t, err := q.Table(ctx, ref.Schema, ref.Name)
		if err != nil {
			s.dbErr(w, err)
			return
		}
		stmt, name = q.SelectAll(t), t.Name
	} else if parts := engine.Split(stmt, c.Engine); len(parts) != 1 {
		writeErr(w, http.StatusBadRequest, "export one query at a time")
		return
	} else {
		stmt = parts[0]
	}
	format := r.URL.Query().Get("format")
	file := safeName.ReplaceAllString(c.Name+"-"+name, "-")
	maxRows := 1_000_000
	if format == "json" {
		w.Header().Set("Content-Type", "application/json; charset=utf-8")
		w.Header().Set("Content-Disposition", `attachment; filename="`+file+`.json"`)
		enc := json.NewEncoder(w)
		first := true
		_, _ = w.Write([]byte("[\n"))
		err = q.Stream(ctx, stmt, maxRows, func(cols []string, row []any) error {
			obj := make(map[string]any, len(cols))
			for i, col := range cols {
				obj[col] = row[i]
			}
			if !first {
				_, _ = w.Write([]byte(","))
			}
			first = false
			return enc.Encode(obj)
		})
		_, _ = w.Write([]byte("]\n"))
	} else {
		w.Header().Set("Content-Type", "text/csv; charset=utf-8")
		w.Header().Set("Content-Disposition", `attachment; filename="`+file+`.csv"`)
		cw := csv.NewWriter(w)
		header := false
		err = q.Stream(ctx, stmt, maxRows, func(cols []string, row []any) error {
			if !header {
				header = true
				if err := cw.Write(cols); err != nil {
					return err
				}
			}
			rec := make([]string, len(row))
			for i, v := range row {
				rec[i] = csvCell(v)
			}
			return cw.Write(rec)
		})
		cw.Flush()
	}
	if err != nil {
		s.log.Warn("export stopped", "connection", c.Name, "err", err)
	}
}

func csvCell(v any) string {
	switch x := v.(type) {
	case nil:
		return ""
	case string:
		return x
	case engine.Binary:
		return "\\x" + x.Bin
	}
	return fmt.Sprint(v)
}

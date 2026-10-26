package server

import (
	"context"
	"fmt"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/abjelosevic88/pocket-homelab-database/internal/engine"
	"github.com/abjelosevic88/pocket-homelab-database/internal/store"
)

// ---- Redis: keys and the console ----

func (s *Server) kvFor(w http.ResponseWriter, r *http.Request, db string, write bool) (*store.Connection, *engine.KV, context.Context, context.CancelFunc, bool) {
	c, ok := s.conn(w, r)
	if !ok {
		return nil, nil, nil, nil, false
	}
	ctx, cancel := context.WithTimeout(r.Context(), 30*time.Second)
	if write {
		if _, err := s.writeGate(ctx, c); err != nil {
			cancel()
			s.dbErr(w, err)
			return nil, nil, nil, nil, false
		}
	}
	k, err := s.openKV(ctx, c, db, !write)
	if err != nil {
		cancel()
		s.dbErr(w, err)
		return nil, nil, nil, nil, false
	}
	return c, k, ctx, cancel, true
}

func (s *Server) handleKeys(w http.ResponseWriter, r *http.Request) {
	q := r.URL.Query()
	_, k, ctx, cancel, ok := s.kvFor(w, r, q.Get("db"), false)
	if !ok {
		return
	}
	defer cancel()
	cursor, _ := strconv.ParseUint(q.Get("cursor"), 10, 64)
	keys, next, err := k.Scan(ctx, q.Get("pattern"), cursor, queryInt(r, "count"))
	if err != nil {
		s.dbErr(w, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"keys": keys, "cursor": strconv.FormatUint(next, 10)})
}

func (s *Server) handleKey(w http.ResponseWriter, r *http.Request) {
	q := r.URL.Query()
	_, k, ctx, cancel, ok := s.kvFor(w, r, q.Get("db"), false)
	if !ok {
		return
	}
	defer cancel()
	kv, err := k.Get(ctx, q.Get("key"), queryInt(r, "limit"))
	if err != nil {
		s.dbErr(w, err)
		return
	}
	writeJSON(w, http.StatusOK, kv)
}

func (s *Server) keyActivity(ctx context.Context, c *store.Connection, db, key, summary string, err error) {
	cid := c.ID
	a := &store.Activity{ConnectionID: &cid, ConnectionName: c.Name, Kind: "key", Database: db, Target: key, Summary: summary, OK: err == nil, Source: sourceOf(ctx)}
	if err != nil {
		a.Error = err.Error()
	}
	_ = s.store.AddActivity(ctx, c.UserID, a)
	if err == nil {
		s.changes.Add(1)
		s.publish(ctx, c.UserID, "database.changed", c.Name, map[string]any{"connection_id": c.ID, "connection": c.Name, "key": key, "summary": summary})
	}
}

// handleSetKey writes a string, or one field of a hash; ttl_s: -1 keeps the expiry, 0 none.
func (s *Server) handleSetKey(w http.ResponseWriter, r *http.Request) {
	var in struct {
		DB    string `json:"db"`
		Key   string `json:"key"`
		Field string `json:"field"`
		Value string `json:"value"`
		TTL   int64  `json:"ttl_s"`
	}
	if err := decode(r, &in); err != nil || strings.TrimSpace(in.Key) == "" {
		writeErr(w, http.StatusBadRequest, "a key is needed")
		return
	}
	c, k, ctx, cancel, ok := s.kvFor(w, r, in.DB, true)
	if !ok {
		return
	}
	defer cancel()
	var err error
	summary := "Set " + in.Key
	if in.Field != "" {
		err = k.HashSet(ctx, in.Key, in.Field, in.Value)
		summary = fmt.Sprintf("Set field %s of %s", in.Field, in.Key)
	} else {
		ttl := time.Duration(in.TTL) * time.Second
		if in.TTL < 0 {
			ttl = -1
		}
		err = k.SetString(ctx, in.Key, in.Value, ttl)
	}
	s.keyActivity(ctx, c, in.DB, in.Key, summary, err)
	if err != nil {
		s.dbErr(w, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"ok": true})
}

func (s *Server) handleDeleteKey(w http.ResponseWriter, r *http.Request) {
	q := r.URL.Query()
	key, field := q.Get("key"), q.Get("field")
	c, k, ctx, cancel, ok := s.kvFor(w, r, q.Get("db"), true)
	if !ok {
		return
	}
	defer cancel()
	var err error
	summary := "Deleted " + key
	if field != "" {
		err = k.HashDel(ctx, key, field)
		summary = fmt.Sprintf("Deleted field %s of %s", field, key)
	} else {
		_, err = k.Delete(ctx, key)
	}
	s.keyActivity(ctx, c, q.Get("db"), key, summary, err)
	if err != nil {
		s.dbErr(w, err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

// handleKeyMeta renames a key or changes its time to live.
func (s *Server) handleKeyMeta(w http.ResponseWriter, r *http.Request) {
	var in struct {
		DB     string `json:"db"`
		Key    string `json:"key"`
		Rename string `json:"rename"`
		TTL    *int64 `json:"ttl_s"`
	}
	if err := decode(r, &in); err != nil || in.Key == "" {
		writeErr(w, http.StatusBadRequest, "a key is needed")
		return
	}
	c, k, ctx, cancel, ok := s.kvFor(w, r, in.DB, true)
	if !ok {
		return
	}
	defer cancel()
	var err error
	var summary string
	switch {
	case in.Rename != "":
		err = k.Rename(ctx, in.Key, in.Rename)
		summary = fmt.Sprintf("Renamed %s to %s", in.Key, in.Rename)
	case in.TTL != nil:
		err = k.Expire(ctx, in.Key, time.Duration(*in.TTL)*time.Second)
		summary = "Made " + in.Key + " never expire"
		if *in.TTL > 0 {
			summary = fmt.Sprintf("Set %s to expire in %s", in.Key, time.Duration(*in.TTL)*time.Second)
		}
	default:
		writeErr(w, http.StatusBadRequest, "rename or ttl_s")
		return
	}
	s.keyActivity(ctx, c, in.DB, in.Key, summary, err)
	if err != nil {
		s.dbErr(w, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"ok": true})
}

// handleCommand runs one console line.
func (s *Server) handleCommand(w http.ResponseWriter, r *http.Request) {
	var in struct {
		DB      string `json:"db"`
		Line    string `json:"line"`
		Confirm bool   `json:"confirm"`
	}
	if err := decode(r, &in); err != nil {
		writeErr(w, http.StatusBadRequest, "invalid json")
		return
	}
	args, err := engine.ParseArgs(in.Line)
	if err != nil {
		writeErr(w, http.StatusBadRequest, err.Error())
		return
	}
	read, danger, refused := engine.RedisCommandKind(args)
	if refused != nil {
		writeErr(w, http.StatusBadRequest, refused.Error())
		return
	}
	c, ok := s.conn(w, r)
	if !ok {
		return
	}
	if !read {
		if !c.AllowWrites {
			s.dbErr(w, &gateError{"writes_off", "This connection is read-only: switch on “Allow changes” for it to run commands that change something."})
			return
		}
		if danger != "" && !in.Confirm {
			writeJSON(w, http.StatusOK, map[string]any{"needs_confirm": true, "reasons": []confirmReason{{in.Line, danger}}})
			return
		}
	}
	_, k, ctx, cancel, ok := s.kvFor(w, r, in.DB, !read)
	if !ok {
		return
	}
	defer cancel()
	start := time.Now()
	out, err := k.Command(ctx, args)
	took := time.Since(start).Milliseconds()
	h := &store.HistoryEntry{ConnectionID: c.ID, Database: in.DB, Body: in.Line, OK: err == nil, DurationMs: took}
	if err != nil {
		h.Error = engine.CleanErr(err)
	}
	_ = s.store.AddHistory(ctx, c.UserID, h)
	s.queries.Add(1)
	if !read {
		s.keyActivity(ctx, c, in.DB, firstArg(args), "Ran "+strings.ToUpper(args[0]), err)
	}
	if err != nil {
		writeJSON(w, http.StatusOK, map[string]any{"ok": false, "error": h.Error, "duration_ms": took})
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"ok": true, "result": out, "duration_ms": took})
}

func firstArg(args []string) string {
	if len(args) > 1 {
		return args[1]
	}
	return ""
}

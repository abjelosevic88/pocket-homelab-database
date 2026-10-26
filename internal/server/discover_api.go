package server

import (
	"context"
	"net/http"
	"strings"
	"time"

	"github.com/abjelosevic88/pocket-homelab-database/internal/discover"
	"github.com/abjelosevic88/pocket-homelab-database/internal/engine"
	"github.com/abjelosevic88/pocket-homelab-database/internal/store"
)

// ---- finding the databases on this server ----

func (s *Server) scanOptions() discover.Options {
	skip := []string{s.cfg.DataDir}
	if s.cfg.HostRoot != "" {
		skip = append(skip, s.cfg.HostRoot+"/proc", s.cfg.HostRoot+"/sys")
	}
	return discover.Options{HostRoot: s.cfg.HostRoot, ExtraPaths: s.cfg.ScanPaths, Skip: []string{s.cfg.SelfContainer}, SkipPaths: skip}
}

type foundView struct {
	discover.Found
	Added        bool  `json:"added"`
	ConnectionID int64 `json:"connection_id,omitempty"`
}

// handleDiscover lists what runs on this server (admins only: it reads
// container credentials and the file system).
func (s *Server) handleDiscover(w http.ResponseWriter, r *http.Request) {
	if !s.requireAdmin(w, r) {
		return
	}
	ctx, cancel := context.WithTimeout(r.Context(), 45*time.Second)
	defer cancel()
	uid := actorID(ctx)
	found, err := discover.Scan(ctx, s.docker, s.scanOptions())
	resp := map[string]any{"docker": s.docker.Available(), "found": []foundView{}, "scan_paths": s.cfg.ScanPaths, "host_root": s.cfg.HostRoot}
	if err != nil {
		resp["error"] = err.Error()
		writeJSON(w, http.StatusOK, resp)
		return
	}
	mine, _ := s.store.ListConnections(ctx, uid)
	byKey := map[string]int64{}
	for _, c := range mine {
		if c.DiscoveryKey != "" {
			byKey[c.DiscoveryKey] = c.ID
		}
		if c.Engine == engine.SQLite && c.HostPath != "" {
			byKey["file:"+c.HostPath] = c.ID
		}
		if c.Container != "" {
			byKey["docker:"+c.Container] = c.ID
		}
	}
	out := make([]foundView, 0, len(found))
	for _, f := range found {
		v := foundView{Found: f}
		if id, ok := byKey[f.Key]; ok {
			v.Added, v.ConnectionID = true, id
		}
		out = append(out, v)
	}
	resp["found"] = out
	writeJSON(w, http.StatusOK, resp)
}

// handleDiscoverAdd turns found databases into connections (read-only to start with).
func (s *Server) handleDiscoverAdd(w http.ResponseWriter, r *http.Request) {
	if !s.requireAdmin(w, r) {
		return
	}
	var in struct {
		Keys      []string          `json:"keys"`
		Passwords map[string]string `json:"passwords"` // for databases whose password discovery could not read
	}
	if err := decode(r, &in); err != nil || len(in.Keys) == 0 {
		writeErr(w, http.StatusBadRequest, "choose what to add")
		return
	}
	ctx, cancel := context.WithTimeout(r.Context(), 60*time.Second)
	defer cancel()
	uid := actorID(ctx)
	found, err := discover.Scan(ctx, s.docker, s.scanOptions())
	if err != nil {
		writeErr(w, http.StatusBadGateway, err.Error())
		return
	}
	want := map[string]bool{}
	for _, k := range in.Keys {
		want[k] = true
	}
	added := []connView{}
	var skipped []string
	for _, f := range found {
		if !want[f.Key] {
			continue
		}
		if !f.Supported {
			skipped = append(skipped, f.Name+" (not supported yet)")
			continue
		}
		if _, err := s.store.ConnectionByKey(ctx, uid, f.Key); err == nil {
			skipped = append(skipped, f.Name+" (already added)")
			continue
		}
		c := &store.Connection{Name: f.Name, Engine: f.Engine, Group: f.Group, Source: "discovered", DiscoveryKey: f.Key, Monitor: true, SizeBytes: -1}
		pass := f.Password
		if p := in.Passwords[f.Key]; p != "" {
			pass = p
		}
		if f.Engine == engine.SQLite {
			c.Path, c.HostPath, c.SizeBytes = f.Path, f.HostPath, f.SizeBytes
			c.Name = niceFileName(f)
			c.Monitor = !f.Backup
		} else {
			c.Container, c.Host, c.Port, c.Username, c.Database = f.Container, f.Host, f.Port, f.User, f.Database
		}
		if pass != "" {
			if c.Password, err = s.box.Seal(pass); err != nil {
				s.fail(w, err, "seal")
				return
			}
		}
		if err := s.store.CreateConnection(ctx, uid, c); err != nil {
			skipped = append(skipped, f.Name+" ("+err.Error()+")")
			continue
		}
		added = append(added, s.view(ctx, *c, nil))
		go s.checkSoon(uid, c.ID)
	}
	writeJSON(w, http.StatusOK, map[string]any{"added": added, "skipped": skipped})
}

// niceFileName names a found SQLite file after its app: "pocket-day.db" in
// group pocket-day stays "pocket-day", "db.sqlite3" of jellyseerr becomes
// "jellyseerr · db.sqlite3".
func niceFileName(f discover.Found) string {
	base := strings.TrimSuffix(f.Name, ".db")
	if strings.EqualFold(base, f.Group) || strings.Contains(strings.ToLower(base), strings.ToLower(f.Group)) {
		return base
	}
	return f.Group + " · " + f.Name
}

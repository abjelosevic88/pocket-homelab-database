package server

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"time"

	"github.com/abjelosevic88/pocket-homelab-database/internal/engine"
	"github.com/abjelosevic88/pocket-homelab-database/internal/store"
)

// conn loads the caller's connection {id} (someone else's: 404).
func (s *Server) conn(w http.ResponseWriter, r *http.Request) (*store.Connection, bool) {
	c, err := s.store.GetConnection(r.Context(), actorID(r.Context()), idParam(r, "id"))
	if err != nil {
		s.fail(w, err, "connection")
		return nil, false
	}
	return c, true
}

// connView is a connection as the browser sees it.
type connView struct {
	store.Connection
	EngineName string `json:"engine_name"`
	Paused     bool   `json:"paused"`
	Address    string `json:"address"`
}

func (s *Server) view(ctx context.Context, c store.Connection, paused map[int64]bool) connView {
	v := connView{Connection: c, EngineName: engine.EngineName(c.Engine), Paused: paused[c.ID]}
	switch {
	case c.Engine == engine.SQLite:
		v.Address = firstNonEmpty(c.HostPath, c.Path)
		if c.Source == "upload" || c.Source == "demo" {
			v.Address = filepath.Base(c.Path)
		}
	case c.Container != "":
		v.Address = "container " + c.Container
	default:
		v.Address = engine.Spec{Engine: c.Engine, Host: c.Host, Port: c.Port}.Addr()
	}
	if linkApp(ctx) != "" { // other apps see no file system or network details
		v.Path, v.HostPath, v.Host, v.Username, v.Notes = "", "", "", "", ""
	}
	return v
}

func (s *Server) pausedSet(ctx context.Context) map[int64]bool {
	out := map[int64]bool{}
	if list, err := s.store.AllOpenMaintenance(ctx); err == nil {
		for _, m := range list {
			out[m.ConnectionID] = true
		}
	}
	return out
}

func (s *Server) handleListConnections(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	list, err := s.store.ListConnections(ctx, actorID(ctx))
	if err != nil {
		s.fail(w, err, "connections")
		return
	}
	paused := s.pausedSet(ctx)
	out := make([]connView, 0, len(list))
	for _, c := range list {
		out = append(out, s.view(ctx, c, paused))
	}
	writeJSON(w, http.StatusOK, out)
}

func (s *Server) handleGetConnection(w http.ResponseWriter, r *http.Request) {
	c, ok := s.conn(w, r)
	if !ok {
		return
	}
	s.store.TouchConnection(r.Context(), c.UserID, c.ID)
	writeJSON(w, http.StatusOK, s.view(r.Context(), *c, s.pausedSet(r.Context())))
}

// connInput is what the connection form sends.
type connInput struct {
	Name        string `json:"name"`
	Engine      string `json:"engine"`
	Host        string `json:"host"`
	Port        int    `json:"port"`
	Username    string `json:"username"`
	Password    string `json:"password"`
	Database    string `json:"database"`
	SSLMode     string `json:"ssl_mode"`
	Path        string `json:"path"`
	Container   string `json:"container"`
	Group       string `json:"group"`
	Color       string `json:"color"`
	Notes       string `json:"notes"`
	AllowWrites bool   `json:"allow_writes"`
	Favorite    bool   `json:"favorite"`
	Monitor     bool   `json:"monitor"`
}

var colorRe = regexp.MustCompile(`^(#[0-9a-fA-F]{6}|[a-z]{3,12})?$`)

// apply validates the form into c. keys says which fields were sent (nil = all).
func (s *Server) apply(ctx context.Context, u *store.User, c *store.Connection, in connInput, keys map[string]bool) error {
	has := func(k string) bool { return keys == nil || keys[k] }
	bad := func(f string, a ...any) error { return &engine.UserError{Msg: fmt.Sprintf(f, a...)} }
	if has("name") {
		c.Name = strings.TrimSpace(in.Name)
	}
	if has("engine") {
		c.Engine = strings.ToLower(strings.TrimSpace(in.Engine))
	}
	if !engine.Supported(c.Engine) {
		return bad("pick PostgreSQL, MySQL / MariaDB, SQLite or Redis")
	}
	if has("host") {
		c.Host = strings.TrimSpace(in.Host)
	}
	if has("port") {
		if in.Port < 0 || in.Port > 65535 {
			return bad("a port is 1 to 65535")
		}
		c.Port = in.Port
	}
	if has("username") {
		c.Username = strings.TrimSpace(in.Username)
	}
	if has("password") && !strings.Contains(in.Password, "••") {
		sealed, err := s.box.Seal(in.Password)
		if err != nil {
			return err
		}
		c.Password = sealed
	}
	if has("database") {
		c.Database = strings.TrimSpace(in.Database)
	}
	if has("ssl_mode") {
		switch in.SSLMode {
		case "", "disable", "prefer", "require", "verify-full":
			c.SSLMode = in.SSLMode
		default:
			return bad("encryption is off, preferred, required or verified")
		}
	}
	if has("container") {
		c.Container = strings.TrimSpace(in.Container)
		if c.Container != "" && u.Role != "admin" {
			return bad("only an admin can connect to a container on this server")
		}
	}
	if has("group") {
		c.Group = strings.TrimSpace(in.Group)
	}
	if has("color") {
		if !colorRe.MatchString(in.Color) {
			return bad("unknown colour")
		}
		c.Color = in.Color
	}
	if has("notes") {
		c.Notes = strings.TrimSpace(in.Notes)
	}
	if has("allow_writes") {
		c.AllowWrites = in.AllowWrites
	}
	if has("favorite") {
		c.Favorite = in.Favorite
	}
	if has("monitor") {
		c.Monitor = in.Monitor
	}
	if has("path") && c.Engine == engine.SQLite && strings.TrimSpace(in.Path) != c.HostPath {
		p := strings.TrimSpace(in.Path)
		local, host, err := s.sqlitePath(u, p)
		if err != nil {
			return err
		}
		c.Path, c.HostPath = local, host
	}
	if c.Name == "" {
		switch {
		case c.Engine == engine.SQLite && c.Path != "":
			c.Name = filepath.Base(c.Path)
		case c.Container != "":
			c.Name = c.Container
		default:
			c.Name = firstNonEmpty(c.Database, c.Host, engine.EngineName(c.Engine))
		}
	}
	if len(c.Name) > 80 {
		return bad("the name is too long")
	}
	switch c.Engine {
	case engine.SQLite:
		if c.Path == "" {
			return bad("choose the SQLite file")
		}
		c.Host, c.Port, c.Username, c.Password, c.SSLMode, c.Container = "", 0, "", "", "", ""
	default:
		if c.Host == "" && c.Container == "" {
			return bad("the server address is needed")
		}
		if c.Engine != engine.Redis {
			c.Path, c.HostPath = "", ""
		}
		if c.Engine == engine.Redis {
			c.Path, c.HostPath = "", ""
		}
	}
	return nil
}

// sqlitePath checks a SQLite path someone typed: only an admin may open a
// file on the server, and never this app's own data.
func (s *Server) sqlitePath(u *store.User, p string) (local, host string, err error) {
	if p == "" {
		return "", "", &engine.UserError{Msg: "choose the SQLite file"}
	}
	if u.Role != "admin" {
		return "", "", &engine.UserError{Msg: "only an admin can open a file on this server; upload a copy instead"}
	}
	if !filepath.IsAbs(p) {
		return "", "", &engine.UserError{Msg: "give the full path, starting with /"}
	}
	host = filepath.Clean(p)
	local = host
	if s.cfg.HostRoot != "" {
		local = filepath.Join(s.cfg.HostRoot, host)
	}
	data, _ := filepath.Abs(s.cfg.DataDir)
	if abs, _ := filepath.Abs(local); abs == data || strings.HasPrefix(abs, data+string(os.PathSeparator)) {
		return "", "", &engine.UserError{Msg: "that is Pocket Database's own data"}
	}
	fi, err := os.Stat(local)
	if err != nil {
		return "", "", &engine.UserError{Msg: "cannot see " + host + " from here: " + err.Error()}
	}
	if fi.IsDir() {
		return "", "", &engine.UserError{Msg: host + " is a folder"}
	}
	if !engine.IsSQLiteFile(local) {
		return "", "", &engine.UserError{Msg: host + " is not a SQLite database"}
	}
	return local, host, nil
}

func (s *Server) handleCreateConnection(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	u := userFrom(ctx)
	if linkApp(ctx) != "" {
		writeErr(w, http.StatusForbidden, "other apps can only read")
		return
	}
	var in connInput
	if err := decode(r, &in); err != nil {
		writeErr(w, http.StatusBadRequest, "invalid json")
		return
	}
	c := &store.Connection{Source: "manual", SizeBytes: -1}
	if err := s.apply(ctx, u, c, in, nil); err != nil {
		s.dbErr(w, err)
		return
	}
	if err := s.store.CreateConnection(ctx, u.ID, c); err != nil {
		s.fail(w, err, "create connection")
		return
	}
	go s.checkSoon(u.ID, c.ID)
	writeJSON(w, http.StatusCreated, s.view(ctx, *c, nil))
}

func (s *Server) handleUpdateConnection(w http.ResponseWriter, r *http.Request) {
	c, ok := s.conn(w, r)
	if !ok {
		return
	}
	ctx := r.Context()
	if linkApp(ctx) != "" {
		writeErr(w, http.StatusForbidden, "other apps can only read")
		return
	}
	var in connInput
	keys, err := decodeKeys(r, &in)
	if err != nil {
		writeErr(w, http.StatusBadRequest, "invalid json")
		return
	}
	if keys["path"] && c.Source == "upload" {
		delete(keys, "path") // an uploaded copy stays where it is
	}
	if err := s.apply(ctx, userFrom(ctx), c, in, keys); err != nil {
		s.dbErr(w, err)
		return
	}
	if err := s.store.UpdateConnection(ctx, c.UserID, c); err != nil {
		s.fail(w, err, "update connection")
		return
	}
	s.pools.drop(c.ID)
	writeJSON(w, http.StatusOK, s.view(ctx, *c, s.pausedSet(ctx)))
}

func (s *Server) handleDeleteConnection(w http.ResponseWriter, r *http.Request) {
	c, ok := s.conn(w, r)
	if !ok {
		return
	}
	ctx := r.Context()
	if linkApp(ctx) != "" {
		writeErr(w, http.StatusForbidden, "other apps can only read")
		return
	}
	if m, err := s.store.OpenMaintenance(ctx, c.ID); err == nil {
		_ = s.endPause(ctx, m, "the connection was removed")
	}
	s.pools.drop(c.ID)
	if err := s.store.DeleteConnection(ctx, c.UserID, c.ID); err != nil {
		s.fail(w, err, "delete connection")
		return
	}
	_ = os.RemoveAll(filepath.Join(s.snapshotRoot(), fmt.Sprint(c.UserID), fmt.Sprint(c.ID)))
	if c.Source == "upload" || c.Source == "demo" {
		if s.ownUpload(c.UserID, c.Path) {
			for _, suffix := range []string{"", "-wal", "-shm"} {
				_ = os.Remove(c.Path + suffix)
			}
		}
	}
	w.WriteHeader(http.StatusNoContent)
}

// ownUpload reports whether path is in the person's own upload folder.
func (s *Server) ownUpload(uid int64, path string) bool {
	dir, _ := filepath.Abs(s.uploadDir(uid))
	abs, _ := filepath.Abs(path)
	return strings.HasPrefix(abs, dir+string(os.PathSeparator))
}

func (s *Server) uploadDir(uid int64) string { return filepath.Join(s.cfg.FilesDir, fmt.Sprint(uid)) }

// handleTestConnection tries a connection from the form (or a saved one with changes).
func (s *Server) handleTestConnection(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	u := userFrom(ctx)
	var in struct {
		connInput
		ID int64 `json:"id"`
	}
	keys, err := decodeKeys(r, &in)
	if err != nil {
		writeErr(w, http.StatusBadRequest, "invalid json")
		return
	}
	c := &store.Connection{SizeBytes: -1}
	if in.ID > 0 {
		if c, err = s.store.GetConnection(ctx, u.ID, in.ID); err != nil {
			s.fail(w, err, "connection")
			return
		}
		delete(keys, "id")
	} else {
		keys = nil
	}
	if err := s.apply(ctx, u, c, in.connInput, keys); err != nil {
		writeJSON(w, http.StatusOK, map[string]any{"ok": false, "error": err.Error()})
		return
	}
	c.ID = -time.Now().UnixNano() // never shares a pool
	tctx, cancel := context.WithTimeout(ctx, 20*time.Second)
	defer cancel()
	info, err := s.probe(tctx, c)
	if err != nil {
		writeJSON(w, http.StatusOK, map[string]any{"ok": false, "error": err.Error()})
		return
	}
	msg := fmt.Sprintf("%s %s", engine.EngineName(c.Engine), info.Version)
	if f := info.Extra["flavor"]; f != "" && c.Engine != engine.SQLite {
		msg = fmt.Sprintf("%s %s", f, info.Version)
	}
	if info.SizeBytes > 0 {
		msg += ", " + fmtBytes(info.SizeBytes)
	}
	writeJSON(w, http.StatusOK, map[string]any{"ok": true, "message": msg, "server": info})
}

// probe opens c once (not pooled), reads the server and closes it.
func (s *Server) probe(ctx context.Context, c *store.Connection) (engine.ServerInfo, error) {
	sp, err := s.spec(ctx, c, "", true)
	if err != nil {
		return engine.ServerInfo{}, err
	}
	if c.Engine == engine.Redis {
		k, err := engine.OpenRedis(sp)
		if err != nil {
			return engine.ServerInfo{}, err
		}
		defer k.Close()
		if err := k.Ping(ctx); err != nil {
			return engine.ServerInfo{}, errors.New(engine.CleanErr(err))
		}
		return k.Server(ctx)
	}
	q, err := engine.OpenSQL(sp)
	if err != nil {
		return engine.ServerInfo{}, err
	}
	defer q.Close()
	if err := q.Ping(ctx); err != nil {
		return engine.ServerInfo{}, errors.New(engine.CleanErr(err))
	}
	return q.Server(ctx)
}

// handleUpload opens an uploaded SQLite file as the caller's own copy.
func (s *Server) handleUpload(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	u := userFrom(ctx)
	if linkApp(ctx) != "" {
		writeErr(w, http.StatusForbidden, "other apps can only read")
		return
	}
	r.Body = http.MaxBytesReader(w, r.Body, s.cfg.UploadMax+1<<20)
	f, hdr, err := r.FormFile("file")
	if err != nil {
		writeErr(w, http.StatusBadRequest, "choose a SQLite file (up to "+fmtBytes(s.cfg.UploadMax)+")")
		return
	}
	defer f.Close()
	dir := s.uploadDir(u.ID)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		s.fail(w, err, "upload")
		return
	}
	base := regexp.MustCompile(`[^A-Za-z0-9._-]+`).ReplaceAllString(filepath.Base(hdr.Filename), "-")
	if base == "" || base == "." {
		base = "database.db"
	}
	dst := filepath.Join(dir, store.RandomToken(4)+"-"+base)
	out, err := os.Create(dst)
	if err != nil {
		s.fail(w, err, "upload")
		return
	}
	if _, err := io.Copy(out, f); err != nil {
		out.Close()
		_ = os.Remove(dst)
		writeErr(w, http.StatusBadRequest, "the upload did not finish: "+err.Error())
		return
	}
	out.Close()
	if !engine.IsSQLiteFile(dst) {
		_ = os.Remove(dst)
		writeErr(w, http.StatusBadRequest, "that is not a SQLite database")
		return
	}
	name := strings.TrimSpace(r.FormValue("name"))
	if name == "" {
		name = strings.TrimSuffix(hdr.Filename, filepath.Ext(hdr.Filename))
	}
	c := &store.Connection{Name: name, Engine: engine.SQLite, Path: dst, Source: "upload", AllowWrites: true, Monitor: false, SizeBytes: -1, Group: "Uploaded"}
	if err := s.store.CreateConnection(ctx, u.ID, c); err != nil {
		_ = os.Remove(dst)
		s.fail(w, err, "create connection")
		return
	}
	go s.checkSoon(u.ID, c.ID)
	writeJSON(w, http.StatusCreated, s.view(ctx, *c, nil))
}

// handleDownloadFile downloads the caller's SQLite file (a consistent copy).
func (s *Server) handleDownloadFile(w http.ResponseWriter, r *http.Request) {
	c, ok := s.conn(w, r)
	if !ok {
		return
	}
	if c.Engine != engine.SQLite {
		writeErr(w, http.StatusBadRequest, "only a SQLite file can be downloaded")
		return
	}
	tmp, err := os.MkdirTemp(s.cfg.DataDir, "dl-")
	if err != nil {
		s.fail(w, err, "download")
		return
	}
	defer os.RemoveAll(tmp)
	dst := filepath.Join(tmp, filepath.Base(c.Path))
	if err := vacuumInto(r.Context(), c.Path, dst); err != nil {
		if err := copyFile(c.Path, dst); err != nil {
			s.fail(w, err, "download")
			return
		}
	}
	w.Header().Set("Content-Type", "application/vnd.sqlite3")
	w.Header().Set("Content-Disposition", `attachment; filename="`+filepath.Base(c.Path)+`"`)
	http.ServeFile(w, r, dst)
}

func (s *Server) handleCheckConnection(w http.ResponseWriter, r *http.Request) {
	c, ok := s.conn(w, r)
	if !ok {
		return
	}
	s.checkOne(r.Context(), c)
	c, _ = s.store.GetConnection(r.Context(), c.UserID, c.ID)
	writeJSON(w, http.StatusOK, s.view(r.Context(), *c, s.pausedSet(r.Context())))
}

// checkSoon checks a new connection in the background.
func (s *Server) checkSoon(uid, id int64) {
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	if c, err := s.store.GetConnection(ctx, uid, id); err == nil {
		s.checkOne(ctx, c)
	}
}

func fmtBytes(n int64) string {
	switch {
	case n >= 1<<30:
		return fmt.Sprintf("%.1f GB", float64(n)/(1<<30))
	case n >= 1<<20:
		return fmt.Sprintf("%.1f MB", float64(n)/(1<<20))
	case n >= 1<<10:
		return fmt.Sprintf("%.0f KB", float64(n)/(1<<10))
	}
	return fmt.Sprintf("%d bytes", n)
}

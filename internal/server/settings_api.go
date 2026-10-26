package server

import (
	"context"
	"encoding/csv"
	"fmt"
	"net/http"
	"os"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
	"time"

	"github.com/abjelosevic88/pocket-homelab-database/internal/ai"
	"github.com/abjelosevic88/pocket-homelab-database/internal/notify"
	"github.com/abjelosevic88/pocket-homelab-database/internal/store"
)

// ---- settings ----

func mask(s string) string {
	if s == "" {
		return ""
	}
	if len(s) <= 8 {
		return "••••"
	}
	return s[:4] + "••••" + s[len(s)-4:]
}

// publicSettings is what one person sees: their settings with the secrets
// masked and, per Pocket app, the mode in effect (<app>_mode), whether it
// works and how (<app>_via: core | direct | "") and why not (<app>_detail).
func (s *Server) publicSettings(ctx context.Context, u *store.User, st store.Settings) map[string]any {
	out := map[string]any{
		"core_configured": s.core.Configured(), "core_registered": s.coreStatus()["registered"],
		"title": st.Title, "date_format": st.DateFormat, "time_format": st.TimeFormat,
		"page_size": st.PageSize, "row_limit": st.RowLimit, "query_timeout_s": st.QueryTimeoutS, "confirm_writes": st.ConfirmWrites,
		"editor_font": st.EditorFont, "density": st.Density, "show_system": st.ShowSystem,
		"pause_minutes": st.PauseMinutes, "snapshot_keep": st.SnapshotKeep, "show_backups": st.ShowBackups,
		"notify_down": st.NotifyDown, "notify_recovered": st.NotifyRecovered,
		"notify_url": st.NotifyURL, "notify_format": st.NotifyFormat, "notify_configured": st.NotifyURL != "",
		"ai_provider": st.AIProvider, "ai_pocket_model": st.AIPocketModel, "openai_api_key": mask(st.OpenAIKey), "openai_model": st.OpenAIModel, "openai_base_url": st.OpenAIBaseURL,
		"setup_complete": st.SetupComplete, "base_url": s.cfg.BaseURL,
		"docker": s.docker.Available(), "upload_max": s.cfg.UploadMax,
	}
	for _, a := range pocketApps {
		mode, via, detail := s.connStatus(ctx, u, st, a)
		out[a.slug+"_mode"], out[a.slug+"_via"], out[a.slug+"_detail"] = mode, via, detail
		out[a.slug+"_configured"] = via != ""
	}
	return out
}

func (s *Server) handleGetSettings(w http.ResponseWriter, r *http.Request) {
	st, err := s.store.GetSettings(r.Context(), actorID(r.Context()))
	if err != nil {
		s.fail(w, err, "settings")
		return
	}
	writeJSON(w, http.StatusOK, s.publicSettings(r.Context(), userFrom(r.Context()), st))
}

// handlePutSettings saves the caller's personal settings. The box-wide title
// is only taken from an admin.
func (s *Server) handlePutSettings(w http.ResponseWriter, r *http.Request) {
	me := userFrom(r.Context())
	isAdmin := me.Role == "admin"
	cur, err := s.store.GetSettings(r.Context(), me.ID)
	if err != nil {
		s.fail(w, err, "settings")
		return
	}
	var in store.Settings
	keys, err := decodeKeys(r, &in)
	if err != nil {
		writeErr(w, http.StatusBadRequest, "invalid json")
		return
	}
	bad := func(msg string) { writeErr(w, http.StatusBadRequest, msg) }
	if keys["title"] && isAdmin {
		cur.Title = strings.TrimSpace(in.Title)
		if cur.Title == "" {
			cur.Title = store.DefaultTitle
		}
	}
	if keys["date_format"] {
		cur.DateFormat = in.DateFormat
	}
	if keys["time_format"] {
		cur.TimeFormat = in.TimeFormat
	}
	intIn := func(k string, dst *int, v, lo, hi int, what string) bool {
		if !keys[k] {
			return true
		}
		if v < lo || v > hi {
			bad(fmt.Sprintf("%s is %d to %d", what, lo, hi))
			return false
		}
		*dst = v
		return true
	}
	if !intIn("page_size", &cur.PageSize, in.PageSize, 10, 1000, "rows per page") || !intIn("row_limit", &cur.RowLimit, in.RowLimit, 10, 50000, "the row limit") ||
		!intIn("query_timeout_s", &cur.QueryTimeoutS, in.QueryTimeoutS, 2, 600, "the time limit") || !intIn("editor_font", &cur.EditorFont, in.EditorFont, 10, 22, "the font size") ||
		!intIn("pause_minutes", &cur.PauseMinutes, in.PauseMinutes, 2, 240, "a pause") || !intIn("snapshot_keep", &cur.SnapshotKeep, in.SnapshotKeep, 1, 100, "snapshots kept") {
		return
	}
	if keys["density"] {
		if in.Density != "compact" && in.Density != "comfortable" {
			bad("density is compact or comfortable")
			return
		}
		cur.Density = in.Density
	}
	secret := func(k string, dst *string, v string) {
		if keys[k] && !strings.Contains(v, "••") {
			*dst = strings.TrimSpace(v)
		}
	}
	url := func(k string, dst *string, v string) {
		if keys[k] {
			*dst = strings.TrimRight(strings.TrimSpace(v), "/")
		}
	}
	text := func(k string, dst *string, v string) {
		if keys[k] {
			*dst = strings.TrimSpace(v)
		}
	}
	boolIn := func(k string, dst *bool, v bool) {
		if keys[k] {
			*dst = v
		}
	}
	boolIn("confirm_writes", &cur.ConfirmWrites, in.ConfirmWrites)
	boolIn("show_system", &cur.ShowSystem, in.ShowSystem)
	boolIn("show_backups", &cur.ShowBackups, in.ShowBackups)
	boolIn("notify_down", &cur.NotifyDown, in.NotifyDown)
	boolIn("notify_recovered", &cur.NotifyRecovered, in.NotifyRecovered)
	text("notify_url", &cur.NotifyURL, in.NotifyURL)
	if keys["notify_format"] && in.NotifyFormat != "" {
		cur.NotifyFormat = in.NotifyFormat
	}
	if keys["ai_provider"] {
		if !validAIProvider(in.AIProvider) {
			bad("AI is automatic, Pocket AI or OpenAI")
			return
		}
		cur.AIProvider = in.AIProvider
	}
	if keys["ai_pocket_model"] {
		if !validPocketModel(in.AIPocketModel) {
			bad("unknown model")
			return
		}
		cur.AIPocketModel = in.AIPocketModel
	}
	secret("openai_api_key", &cur.OpenAIKey, in.OpenAIKey)
	text("openai_model", &cur.OpenAIModel, in.OpenAIModel)
	url("openai_base_url", &cur.OpenAIBaseURL, in.OpenAIBaseURL)
	if err := s.store.SaveUserSettings(r.Context(), me.ID, cur); err != nil {
		s.fail(w, err, "save settings")
		return
	}
	if isAdmin && keys["title"] {
		if err := s.store.SaveBoxSettings(r.Context(), cur); err != nil {
			s.fail(w, err, "save settings")
			return
		}
	}
	for _, k := range modeKeys {
		if keys[k] {
			s.reportModesSoon()
			break
		}
	}
	writeJSON(w, http.StatusOK, s.publicSettings(r.Context(), me, cur))
}

// handleIntegrationTest checks a connection with the caller's own saved
// settings (or the URL/key typed into the form).
func (s *Server) handleIntegrationTest(w http.ResponseWriter, r *http.Request) {
	var in struct {
		Which  string `json:"which"`
		URL    string `json:"url"`
		Key    string `json:"key"`
		Format string `json:"format"`
	}
	if err := decode(r, &in); err != nil {
		writeErr(w, http.StatusBadRequest, "invalid json")
		return
	}
	uid := actorID(r.Context())
	st, _ := s.store.GetSettings(r.Context(), uid)
	ctx, cancel := context.WithTimeout(r.Context(), 20*time.Second)
	defer cancel()
	keyOr := func(saved string) string {
		if strings.Contains(in.Key, "••") || in.Key == "" {
			return saved
		}
		return in.Key
	}
	urlOr := func(saved string) string {
		if strings.TrimSpace(in.URL) == "" {
			return saved
		}
		return strings.TrimRight(strings.TrimSpace(in.URL), "/")
	}
	var msg string
	var err error
	switch in.Which {
	case "openai":
		c := ai.New(urlOr(st.OpenAIBaseURL), keyOr(st.OpenAIKey), st.OpenAIModel, 20*time.Second)
		var ids []string
		if ids, err = c.Models(ctx); err == nil {
			msg = fmt.Sprintf("The key works (%d models)", len(ids))
		}
	case "notify":
		f := in.Format
		if f == "" {
			f = st.NotifyFormat
		}
		n := notify.New(urlOr(st.NotifyURL), f)
		err = n.Send(ctx, notify.Message{Title: "Pocket Database", Body: "Notifications work. You will hear here when a database you watch goes down or comes back.", Priority: "info", Tags: []string{"floppy_disk"}})
		msg = "Test message sent via " + n.Format
	default:
		writeErr(w, http.StatusBadRequest, "unknown integration")
		return
	}
	if err != nil {
		writeJSON(w, http.StatusOK, map[string]any{"ok": false, "error": err.Error()})
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"ok": true, "message": msg})
}

func (s *Server) handleNotifyTest(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	st, _ := s.store.GetSettings(ctx, actorID(ctx))
	n := notify.New(st.NotifyURL, st.NotifyFormat)
	if err := n.Send(ctx, notify.Message{Title: "Pocket Database", Body: "Notifications work.", Priority: "info", Tags: []string{"floppy_disk"}}); err != nil {
		writeJSON(w, http.StatusOK, map[string]any{"ok": false, "error": err.Error()})
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"ok": true})
}

// ---- system / backup / export ----

func (s *Server) handleSystemInfo(w http.ResponseWriter, r *http.Request) {
	var size int64
	if fi, err := os.Stat(s.cfg.DBPath); err == nil {
		size = fi.Size()
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"version": Version, "go": runtime.Version(), "os": runtime.GOOS, "arch": runtime.GOARCH,
		"core":      s.coreStatus(),
		"auth_mode": s.cfg.AuthMode, "data_dir": s.cfg.DataDir, "db_path": s.cfg.DBPath, "db_bytes": size,
		"uptime": time.Since(s.started).Round(time.Second).String(), "timezone": time.Now().Location().String(),
		"counts": s.store.CountsFor(r.Context(), actorID(r.Context())), "queries": s.queries.Load(), "changes": s.changes.Load(),
		"docker": s.docker.Available(), "host_root": s.cfg.HostRoot, "scan_paths": s.cfg.ScanPaths,
	})
}

// handleBackup downloads the whole database, an admin-only box operation.
func (s *Server) handleBackup(w http.ResponseWriter, r *http.Request) {
	if !s.requireAdmin(w, r) {
		return
	}
	dir := filepath.Join(s.cfg.DataDir, "backups")
	name := fmt.Sprintf("pocket-database-%s.db", time.Now().Format("20060102-150405"))
	dst := filepath.Join(dir, name)
	if err := s.store.Backup(r.Context(), dst); err != nil {
		s.fail(w, err, "backup")
		return
	}
	w.Header().Set("Content-Type", "application/vnd.sqlite3")
	w.Header().Set("Content-Disposition", `attachment; filename="`+name+`"`)
	http.ServeFile(w, r, dst)
}

// exportDoc is the JSON export of one person's data: connections without
// their passwords (they are sealed with this box's key) and saved queries.
type exportDoc struct {
	App         string             `json:"app"`
	Version     string             `json:"version"`
	Exported    string             `json:"exported_at"`
	Connections []exportConn       `json:"connections"`
	Saved       []store.SavedQuery `json:"saved_queries"`
}

type exportConn struct {
	Ref         int64  `json:"ref"`
	Name        string `json:"name"`
	Engine      string `json:"engine"`
	Host        string `json:"host"`
	Port        int    `json:"port"`
	Username    string `json:"username"`
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

func (s *Server) handleExportJSON(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	uid := actorID(ctx)
	doc := exportDoc{App: "pocket-database", Version: Version, Exported: store.Now(), Connections: []exportConn{}}
	conns, _ := s.store.ListConnections(ctx, uid)
	for _, c := range conns {
		if c.Source == "upload" || c.Source == "demo" {
			continue // the file lives in this app's data
		}
		doc.Connections = append(doc.Connections, exportConn{Ref: c.ID, Name: c.Name, Engine: c.Engine, Host: c.Host, Port: c.Port, Username: c.Username,
			Database: c.Database, SSLMode: c.SSLMode, Path: c.HostPath, Container: c.Container, Group: c.Group, Color: c.Color, Notes: c.Notes,
			AllowWrites: c.AllowWrites, Favorite: c.Favorite, Monitor: c.Monitor})
	}
	doc.Saved, _ = s.store.ListSaved(ctx, uid, 0)
	w.Header().Set("Content-Disposition", `attachment; filename="pocket-database-export.json"`)
	writeJSON(w, http.StatusOK, doc)
}

// handleExportCSV writes the saved queries as CSV.
func (s *Server) handleExportCSV(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	uid := actorID(ctx)
	list, err := s.store.ListSaved(ctx, uid, 0)
	if err != nil {
		s.fail(w, err, "export")
		return
	}
	names := map[int64]string{}
	if conns, err := s.store.ListConnections(ctx, uid); err == nil {
		for _, c := range conns {
			names[c.ID] = c.Name
		}
	}
	w.Header().Set("Content-Type", "text/csv; charset=utf-8")
	w.Header().Set("Content-Disposition", `attachment; filename="pocket-database-saved-queries.csv"`)
	cw := csv.NewWriter(w)
	_ = cw.Write([]string{"name", "connection", "database", "description", "favorite", "run_count", "last_run_at", "query"})
	for _, q := range list {
		conn := ""
		if q.ConnectionID != nil {
			conn = names[*q.ConnectionID]
		}
		_ = cw.Write([]string{q.Name, conn, q.Database, q.Description, strconv.FormatBool(q.Favorite), strconv.Itoa(q.RunCount), q.LastRunAt, q.Body})
	}
	cw.Flush()
}

// handleImportJSON merges an export into the caller's own data: a
// connection with the same name and engine is skipped, so importing twice
// adds nothing. Passwords are not in the file and have to be entered again.
func (s *Server) handleImportJSON(w http.ResponseWriter, r *http.Request) {
	var doc exportDoc
	if err := decode(r, &doc); err != nil {
		writeErr(w, http.StatusBadRequest, "invalid json")
		return
	}
	if doc.App != "" && doc.App != "pocket-database" {
		writeErr(w, http.StatusBadRequest, "this is not a Pocket Database export")
		return
	}
	ctx := r.Context()
	u := userFrom(ctx)
	counts := map[string]int{}
	existing := map[string]int64{}
	if conns, err := s.store.ListConnections(ctx, u.ID); err == nil {
		for _, c := range conns {
			existing[c.Engine+"|"+strings.ToLower(c.Name)] = c.ID
		}
	}
	ids := map[int64]int64{}
	for _, ec := range doc.Connections {
		key := ec.Engine + "|" + strings.ToLower(ec.Name)
		if id, ok := existing[key]; ok {
			ids[ec.Ref] = id
			counts["skipped"]++
			continue
		}
		c := &store.Connection{Source: "import", SizeBytes: -1}
		in := connInput{Name: ec.Name, Engine: ec.Engine, Host: ec.Host, Port: ec.Port, Username: ec.Username, Database: ec.Database, SSLMode: ec.SSLMode,
			Path: ec.Path, Container: ec.Container, Group: ec.Group, Color: ec.Color, Notes: ec.Notes, AllowWrites: ec.AllowWrites, Favorite: ec.Favorite, Monitor: ec.Monitor}
		if err := s.apply(ctx, u, c, in, nil); err != nil {
			counts["skipped"]++
			continue
		}
		if err := s.store.CreateConnection(ctx, u.ID, c); err != nil {
			counts["skipped"]++
			continue
		}
		existing[key] = c.ID
		ids[ec.Ref] = c.ID
		counts["connections"]++
	}
	have := map[string]bool{}
	if list, err := s.store.ListSaved(ctx, u.ID, 0); err == nil {
		for _, q := range list {
			have[strings.ToLower(q.Name)+"|"+q.Body] = true
		}
	}
	for _, q := range doc.Saved {
		if have[strings.ToLower(q.Name)+"|"+q.Body] || strings.TrimSpace(q.Body) == "" {
			continue
		}
		sq := &store.SavedQuery{Name: q.Name, Database: q.Database, Body: q.Body, Description: q.Description, Favorite: q.Favorite}
		if q.ConnectionID != nil {
			if id, ok := ids[*q.ConnectionID]; ok {
				sq.ConnectionID = &id
			}
		}
		if err := s.store.CreateSaved(ctx, u.ID, sq); err == nil {
			counts["saved queries"]++
		}
	}
	writeJSON(w, http.StatusOK, counts)
}

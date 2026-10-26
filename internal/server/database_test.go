package server

import (
	"bytes"
	"context"
	"database/sql"
	"encoding/json"
	"io"
	"log/slog"
	"mime/multipart"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"

	"github.com/abjelosevic88/pocket-homelab-database/internal/store"
)

// testApp is a server with Alice (the admin) and Bob (a member), each with a token.
type testApp struct {
	t     *testing.T
	s     *Server
	st    *store.Store
	ts    *httptest.Server
	alice string
	bob   string
	dir   string
}

func newTestApp(t *testing.T) *testApp {
	t.Helper()
	dir := t.TempDir()
	cfg := testConfig(dir)
	st := openTestStore(t, cfg)
	s := New(cfg, st, slog.New(slog.NewTextHandler(io.Discard, nil)), nil)
	ctx := context.Background()
	if err := s.Bootstrap(ctx); err != nil {
		t.Fatal(err)
	}
	a := &store.User{Email: "alice@example.com", Name: "Alice", Role: "admin", PasswordHash: "x"}
	b := &store.User{Email: "bob@example.com", Name: "Bob", Role: "member", PasswordHash: "x"}
	for _, u := range []*store.User{a, b} {
		if err := st.CreateUser(ctx, u); err != nil {
			t.Fatal(err)
		}
	}
	at, _, _ := st.CreateAPIToken(ctx, a.ID, "t")
	bt, _, _ := st.CreateAPIToken(ctx, b.ID, "t")
	ts := httptest.NewServer(s.Router())
	t.Cleanup(ts.Close)
	return &testApp{t: t, s: s, st: st, ts: ts, alice: at, bob: bt, dir: dir}
}

func (a *testApp) do(tok, method, path string, body any) (int, map[string]any, []byte) {
	a.t.Helper()
	var r io.Reader
	if body != nil {
		b, _ := json.Marshal(body)
		r = bytes.NewReader(b)
	}
	req, _ := http.NewRequest(method, a.ts.URL+"/api/v1"+path, r)
	req.Header.Set("Authorization", "Bearer "+tok)
	res, err := http.DefaultClient.Do(req)
	if err != nil {
		a.t.Fatal(err)
	}
	defer res.Body.Close()
	raw, _ := io.ReadAll(res.Body)
	var m map[string]any
	_ = json.Unmarshal(raw, &m)
	return res.StatusCode, m, raw
}

// sqliteFile makes a small SQLite database with a table.
func sqliteFile(t *testing.T, path string) {
	t.Helper()
	db, err := sql.Open("sqlite", "file:"+path)
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	if _, err := db.Exec(`CREATE TABLE items (id INTEGER PRIMARY KEY, name TEXT NOT NULL, qty INTEGER NOT NULL DEFAULT 0);
		INSERT INTO items (name, qty) VALUES ('apple', 3), ('pear', 0), ('plum', 7);
		CREATE TABLE log (msg TEXT);
		INSERT INTO log VALUES ('a'), ('b');`); err != nil {
		t.Fatal(err)
	}
}

// upload sends a SQLite file as the given person and returns the new connection id.
func (a *testApp) upload(tok, name string) int64 {
	a.t.Helper()
	src := filepath.Join(a.t.TempDir(), name)
	sqliteFile(a.t, src)
	var buf bytes.Buffer
	mw := multipart.NewWriter(&buf)
	fw, _ := mw.CreateFormFile("file", name)
	b, _ := os.ReadFile(src)
	_, _ = fw.Write(b)
	mw.Close()
	req, _ := http.NewRequest("POST", a.ts.URL+"/api/v1/connections/upload", &buf)
	req.Header.Set("Authorization", "Bearer "+tok)
	req.Header.Set("Content-Type", mw.FormDataContentType())
	res, err := http.DefaultClient.Do(req)
	if err != nil {
		a.t.Fatal(err)
	}
	defer res.Body.Close()
	var c struct {
		ID int64 `json:"id"`
	}
	_ = json.NewDecoder(res.Body).Decode(&c)
	if res.StatusCode != 201 || c.ID == 0 {
		a.t.Fatalf("upload: %d", res.StatusCode)
	}
	return c.ID
}

func TestPerPersonIsolation(t *testing.T) {
	a := newTestApp(t)
	id := a.upload(a.alice, "alice.db")
	p := "/connections/" + itoa(id)
	// Bob sees none of it: every route is a 404
	for _, c := range []struct{ method, path string }{
		{"GET", p}, {"GET", p + "/schemas"}, {"GET", p + "/table?name=items"}, {"GET", p + "/rows?name=items"},
		{"GET", p + "/export?name=items"}, {"GET", p + "/safety"}, {"GET", p + "/file"}, {"PUT", p}, {"DELETE", p},
	} {
		if code, _, _ := a.do(a.bob, c.method, c.path, map[string]any{}); code != 404 {
			t.Errorf("Bob %s %s = %d, want 404", c.method, c.path, code)
		}
	}
	if code, _, _ := a.do(a.bob, "POST", p+"/query", map[string]any{"sql": "SELECT 1"}); code != 404 {
		t.Errorf("Bob query = %d, want 404", code)
	}
	if code, _, _ := a.do(a.bob, "POST", p+"/rows", map[string]any{"name": "items", "changes": []any{map[string]any{"op": "delete", "key": map[string]any{"id": 1}}}}); code != 404 {
		t.Errorf("Bob change = %d, want 404", code)
	}
	// Bob's list is empty, Alice's has hers
	_, _, raw := a.do(a.bob, "GET", "/connections", nil)
	if strings.TrimSpace(string(raw)) != "[]" {
		t.Errorf("Bob's connections = %s", raw)
	}
	// history and activity stay with their owner
	a.do(a.alice, "POST", p+"/query", map[string]any{"sql": "SELECT * FROM items"})
	_, _, raw = a.do(a.bob, "GET", "/history", nil)
	if strings.TrimSpace(string(raw)) != "[]" {
		t.Errorf("Bob's history = %s", raw)
	}
	// a saved query cannot point at someone else's connection
	if code, _, _ := a.do(a.bob, "POST", "/saved", map[string]any{"name": "x", "body": "SELECT 1", "connection_id": id}); code != 400 {
		t.Errorf("Bob saving against Alice's connection = %d, want 400", code)
	}
}

func TestOnlyAdminsTouchTheServer(t *testing.T) {
	a := newTestApp(t)
	file := filepath.Join(t.TempDir(), "outside.db")
	sqliteFile(t, file)
	// a member may not open a file on the server, nor a container, nor discover
	if code, m, _ := a.do(a.bob, "POST", "/connections", map[string]any{"engine": "sqlite", "path": file}); code != 400 || !strings.Contains(m["error"].(string), "admin") {
		t.Errorf("member sqlite path = %d %v", code, m)
	}
	if code, _, _ := a.do(a.bob, "POST", "/connections", map[string]any{"engine": "postgres", "container": "db"}); code != 400 {
		t.Errorf("member container = %d", code)
	}
	if code, _, _ := a.do(a.bob, "GET", "/discover", nil); code != 403 {
		t.Errorf("member discover = %d", code)
	}
	// the admin may, but never this app's own data
	if code, _, _ := a.do(a.alice, "POST", "/connections", map[string]any{"engine": "sqlite", "path": file}); code != 201 {
		t.Errorf("admin sqlite path = %d", code)
	}
	if code, _, _ := a.do(a.alice, "POST", "/connections", map[string]any{"engine": "sqlite", "path": a.s.cfg.DBPath}); code != 400 {
		t.Errorf("admin opening the app's own database = %d, want 400", code)
	}
}

func TestReadOnlyAndConfirm(t *testing.T) {
	a := newTestApp(t)
	id := a.upload(a.alice, "shop.db")
	p := "/connections/" + itoa(id)
	// uploads allow changes; switch them off: writes are refused, reads work
	a.do(a.alice, "PUT", p, map[string]any{"allow_writes": false})
	if code, m, _ := a.do(a.alice, "POST", p+"/query", map[string]any{"sql": "DELETE FROM items WHERE id = 1", "confirm": true}); code != 409 || m["code"] != "writes_off" {
		t.Errorf("write on read-only = %d %v", code, m)
	}
	if code, m, _ := a.do(a.alice, "POST", p+"/query", map[string]any{"sql": "SELECT name FROM items ORDER BY id"}); code != 200 || m["read_only"] != true {
		t.Errorf("read = %d %v", code, m)
	}
	a.do(a.alice, "PUT", p, map[string]any{"allow_writes": true})
	// a destructive statement waits for a second look
	_, m, _ := a.do(a.alice, "POST", p+"/query", map[string]any{"sql": "DELETE FROM log"})
	if m["needs_confirm"] != true {
		t.Fatalf("DELETE without WHERE ran without asking: %v", m)
	}
	_, m, _ = a.do(a.alice, "POST", p+"/query", map[string]any{"sql": "DELETE FROM log", "confirm": true})
	res := m["results"].([]any)[0].(map[string]any)
	if res["rows_affected"].(float64) != 2 {
		t.Errorf("deleted %v rows, want 2", res["rows_affected"])
	}
	// a snapshot was kept first, and the change is in the activity log
	_, m, _ = a.do(a.alice, "GET", p+"/safety", nil)
	if len(m["snapshots"].([]any)) == 0 {
		t.Error("no snapshot before the change")
	}
	_, _, raw := a.do(a.alice, "GET", "/activity", nil)
	if !strings.Contains(string(raw), `"kind":"sql"`) {
		t.Errorf("activity = %s", raw)
	}
	// a failing statement keeps nothing before it
	_, m, _ = a.do(a.alice, "POST", p+"/query", map[string]any{"sql": "UPDATE items SET qty = 99 WHERE id = 1; INSERT INTO nope VALUES (1)", "confirm": true})
	results := m["results"].([]any)
	if results[1].(map[string]any)["error"] == "" {
		t.Fatal("the bad insert did not fail")
	}
	_, m, _ = a.do(a.alice, "POST", p+"/query", map[string]any{"sql": "SELECT qty FROM items WHERE id = 1"})
	if q := m["results"].([]any)[0].(map[string]any)["rows"].([]any)[0].([]any)[0]; q.(float64) != 3 {
		t.Errorf("qty = %v after a rolled back run, want 3", q)
	}
}

func TestRowChanges(t *testing.T) {
	a := newTestApp(t)
	id := a.upload(a.alice, "rows.db")
	p := "/connections/" + itoa(id)
	code, m, _ := a.do(a.alice, "POST", p+"/rows", map[string]any{"name": "items", "changes": []any{
		map[string]any{"op": "update", "key": map[string]any{"id": 1}, "values": map[string]any{"qty": "10"}},
		map[string]any{"op": "insert", "values": map[string]any{"name": "fig", "qty": "2"}},
		map[string]any{"op": "delete", "key": map[string]any{"id": 2}},
	}})
	if code != 200 || m["rows"].(float64) != 3 {
		t.Fatalf("changes = %d %v", code, m)
	}
	_, m, _ = a.do(a.alice, "GET", p+"/rows?name=items&sort=id", nil)
	if got := len(m["rows"].([]any)); got != 3 {
		t.Errorf("%d rows, want 3", got)
	}
	// a table without a primary key is keyed by rowid in SQLite
	_, m, _ = a.do(a.alice, "GET", p+"/table?name=log", nil)
	if m["rowid"] != true {
		t.Errorf("log rowid = %v", m["rowid"])
	}
	if code, _, _ := a.do(a.alice, "POST", p+"/rows", map[string]any{"name": "log", "changes": []any{map[string]any{"op": "delete", "key": map[string]any{"__rowid": 1}}}}); code != 200 {
		t.Errorf("delete by rowid = %d", code)
	}
	// unknown columns are refused
	if code, _, _ := a.do(a.alice, "POST", p+"/rows", map[string]any{"name": "items", "changes": []any{map[string]any{"op": "update", "key": map[string]any{"id": 1}, "values": map[string]any{"nope": 1}}}}); code != 400 {
		t.Errorf("unknown column = %d", code)
	}
}

func TestExportImportIdempotent(t *testing.T) {
	a := newTestApp(t)
	file := filepath.Join(t.TempDir(), "keep.db")
	sqliteFile(t, file)
	a.do(a.alice, "POST", "/connections", map[string]any{"name": "Keep", "engine": "sqlite", "path": file})
	a.do(a.alice, "POST", "/connections", map[string]any{"name": "Remote", "engine": "postgres", "host": "db.example.invalid", "port": 5432, "username": "u", "password": "secret"})
	a.do(a.alice, "POST", "/saved", map[string]any{"name": "All", "body": "SELECT * FROM items"})
	_, _, raw := a.do(a.alice, "GET", "/export.json", nil)
	if strings.Contains(string(raw), "secret") {
		t.Fatal("the export holds a password")
	}
	var doc map[string]any
	_ = json.Unmarshal(raw, &doc)
	// into a fresh person's account (Alice again, after deleting): twice adds once
	b := newTestApp(t)
	_ = os.MkdirAll(filepath.Dir(file), 0o755)
	_, first, _ := b.do(b.alice, "POST", "/import", doc)
	_, second, _ := b.do(b.alice, "POST", "/import", doc)
	if first["connections"].(float64) != 2 || first["saved queries"].(float64) != 1 {
		t.Errorf("first import = %v", first)
	}
	if _, ok := second["connections"]; ok || second["saved queries"] != nil {
		t.Errorf("second import added again: %v", second)
	}
}

func TestSettingsArePersonal(t *testing.T) {
	a := newTestApp(t)
	a.do(a.alice, "PUT", "/settings", map[string]any{"page_size": 250, "title": "Our databases"})
	a.do(a.bob, "PUT", "/settings", map[string]any{"page_size": 50, "title": "Bob's title"})
	_, am, _ := a.do(a.alice, "GET", "/settings", nil)
	_, bm, _ := a.do(a.bob, "GET", "/settings", nil)
	if am["page_size"].(float64) != 250 || bm["page_size"].(float64) != 50 {
		t.Errorf("page sizes %v / %v", am["page_size"], bm["page_size"])
	}
	if am["title"] != "Our databases" || bm["title"] != "Our databases" {
		t.Errorf("title %v / %v (only the admin sets it, for everyone)", am["title"], bm["title"])
	}
	if code, _, _ := a.do(a.alice, "PUT", "/settings", map[string]any{"page_size": 5}); code != 400 {
		t.Errorf("page_size 5 = %d", code)
	}
}

func TestPasswordsNeverLeave(t *testing.T) {
	a := newTestApp(t)
	_, m, raw := a.do(a.alice, "POST", "/connections", map[string]any{"engine": "redis", "host": "cache.example.invalid", "password": "hunter2"})
	if strings.Contains(string(raw), "hunter2") || m["has_password"] != true {
		t.Fatalf("create returned %s", raw)
	}
	var stored string
	_ = a.st.DB.QueryRow(`SELECT password FROM connections`).Scan(&stored)
	if stored == "" || strings.Contains(stored, "hunter2") {
		t.Errorf("stored password %q is not sealed", stored)
	}
	// saving the form again with the mask keeps it
	id := int64(m["id"].(float64))
	a.do(a.alice, "PUT", "/connections/"+itoa(id), map[string]any{"password": "••••", "name": "Cache"})
	c, _ := a.st.GetConnection(context.Background(), 1, id)
	if p, _ := a.s.box.Open(c.Password); p != "hunter2" {
		t.Errorf("password after a masked save = %q", p)
	}
}

func TestLinkScopes(t *testing.T) {
	req := func(method, path string) *http.Request { return httptest.NewRequest(method, path, nil) }
	read := []string{"databases:read"}
	query := []string{"databases:read", "databases:query"}
	cases := []struct {
		scopes []string
		r      *http.Request
		want   bool
	}{
		{read, req("GET", "/api/v1/overview"), true},
		{read, req("GET", "/api/v1/connections"), true},
		{read, req("GET", "/api/v1/connections/3/schemas"), true},
		{read, req("GET", "/api/v1/connections/3/rows"), false},
		{read, req("POST", "/api/v1/connections/3/query"), false},
		{query, req("POST", "/api/v1/connections/3/query"), true},
		{query, req("POST", "/api/v1/connections/3/rows"), false},
		{query, req("PUT", "/api/v1/connections/3"), false},
		{query, req("POST", "/api/v1/connections/3/pause"), false},
		{query, req("GET", "/api/v1/discover"), false},
		{nil, req("GET", "/api/v1/auth/me"), true},
	}
	for _, c := range cases {
		if got := linkAllows(c.scopes, c.r); got != c.want {
			t.Errorf("%v %s %s = %v, want %v", c.scopes, c.r.Method, c.r.URL.Path, got, c.want)
		}
	}
	for _, o := range linkOffers {
		if !regexp.MustCompile(`^[a-z0-9][a-z0-9_.:-]{0,60}$`).MatchString(o.Scope) {
			t.Errorf("scope %q is not valid for Core", o.Scope)
		}
	}
}

func TestLinkedAppsCannotWrite(t *testing.T) {
	ctx := context.WithValue(context.Background(), linkAppKey, "ai")
	a := newTestApp(t)
	c := &store.Connection{ID: 1, Engine: "postgres", AllowWrites: true}
	if _, err := a.s.writeGate(ctx, c); err == nil {
		t.Fatal("a linked app passed the write gate")
	}
}

func itoa(n int64) string { b, _ := json.Marshal(n); return string(b) }

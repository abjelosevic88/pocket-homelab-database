package server

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	core "github.com/abjelosevic88/pocket-homelab-core/sdk"
	"github.com/abjelosevic88/pocket-homelab-database/internal/store"
)

// newMoveApp is newTestApp behind a fake Core whose pcx_<who>_<export|import|read>
// tokens are Pocket Move links acting for alice@ or bob@ (both members to Core).
func newMoveApp(t *testing.T) *testApp {
	t.Helper()
	coreSrv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		if r.URL.Path != "/api/v1/auth/introspect" {
			io.WriteString(w, `{}`)
			return
		}
		var in struct {
			Token string `json:"token"`
		}
		_ = json.NewDecoder(r.Body).Decode(&in)
		parts := strings.SplitN(strings.TrimPrefix(in.Token, "pcx_"), "_", 2)
		if len(parts) != 2 {
			io.WriteString(w, `{"active":false}`)
			return
		}
		scope := map[string]string{"export": core.ScopeMoveExport, "import": core.ScopeMoveImport, "read": "databases:read"}[parts[1]]
		email := parts[0] + "@example.com"
		fmt.Fprintf(w, `{"active":true,"kind":"delegated","scope":%q,"audience":"database","user":{"id":1,"sub":%q,"email":%q,"name":%q,"role":"member"},"app":{"id":7,"slug":"move","name":"Pocket Move"}}`, scope, email, email, parts[0])
	}))
	t.Cleanup(coreSrv.Close)
	dir := t.TempDir()
	cfg := testConfig(dir)
	cfg.CoreURL = coreSrv.URL
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

func (a *testApp) raw(tok, method, path string, body []byte) (int, []byte) {
	a.t.Helper()
	req, _ := http.NewRequest(method, a.ts.URL+path, bytes.NewReader(body))
	req.Header.Set("Authorization", "Bearer "+tok)
	req.Header.Set("Content-Type", "application/json")
	res, err := http.DefaultClient.Do(req)
	if err != nil {
		a.t.Fatal(err)
	}
	defer res.Body.Close()
	out, _ := io.ReadAll(res.Body)
	return res.StatusCode, out
}

func TestMoveLinkScopes(t *testing.T) {
	a := newMoveApp(t)
	exp, imp, read := "pcx_bob_export", "pcx_bob_import", "pcx_bob_read"
	for _, c := range []struct {
		tok, method, path string
		want              int
	}{
		{exp, "GET", core.PortablePath, 200},
		{exp, "GET", "/api/v1/export.json", 200},
		{exp, "GET", "/api/v1/export.csv", 200},
		{exp, "POST", "/api/v1/import", 403},
		{exp, "GET", "/api/v1/saved", 403},
		{exp, "GET", "/api/v1/history", 403},
		{exp, "GET", "/api/v1/settings", 403},
		{exp, "GET", "/api/v1/backup", 403},
		{exp, "GET", "/api/v1/tokens", 403},
		{imp, "GET", core.PortablePath, 200},
		{imp, "POST", "/api/v1/import", 200},
		{imp, "GET", "/api/v1/export.json", 403},
		{imp, "POST", "/api/v1/connections", 403},
		{imp, "POST", "/api/v1/saved", 403},
		{imp, "PUT", "/api/v1/settings", 403},
		{read, "GET", core.PortablePath, 403},
		{read, "GET", "/api/v1/export.json", 403},
		{read, "POST", "/api/v1/import", 403},
	} {
		if code, body := a.raw(c.tok, c.method, c.path, []byte(`{}`)); code != c.want {
			t.Errorf("%s %s %s → %d, want %d (%s)", c.tok, c.method, c.path, code, c.want, body)
		}
	}
	_, body := a.raw(exp, "GET", core.PortablePath, nil)
	var p core.Portable
	if err := json.Unmarshal(body, &p); err != nil || len(p.Exports) != 2 || len(p.Imports) != 1 || p.Exports[0].Kind != core.KindPocket {
		t.Fatalf("portable: %s", body)
	}
}

// Move brings Alice's connections and saved queries to Bob: added once,
// read-only, no passwords, and nothing of one shows up for the other.
func TestMoveImportAddsOnce(t *testing.T) {
	a := newMoveApp(t)
	a.do(a.alice, "POST", "/connections", map[string]any{"name": "Shop", "engine": "postgres", "host": "db.example.invalid", "port": 5432, "username": "u", "password": "zebra-secret", "allow_writes": true})
	a.do(a.alice, "POST", "/saved", map[string]any{"name": "Zebra orders", "body": "SELECT * FROM orders"})
	a.do(a.bob, "POST", "/saved", map[string]any{"name": "Bob's own", "body": "SELECT 1"})

	code, export := a.raw("pcx_alice_export", "GET", "/api/v1/export.json", nil)
	if code != 200 || !strings.Contains(string(export), "Zebra orders") || strings.Contains(string(export), "zebra-secret") || strings.Contains(string(export), "Bob's own") {
		t.Fatalf("alice's export: %d %s", code, export)
	}
	if _, bob := a.raw("pcx_bob_export", "GET", "/api/v1/export.json", nil); strings.Contains(string(bob), "Zebra") || strings.Contains(string(bob), "Shop") {
		t.Fatalf("bob's export holds alice's data: %s", bob)
	}

	imp := func() map[string]float64 {
		code, out := a.raw("pcx_bob_import", "POST", "/api/v1/import", export)
		if code != 200 {
			t.Fatalf("import: %d %s", code, out)
		}
		var m map[string]float64
		_ = json.Unmarshal(out, &m)
		return m
	}
	if first := imp(); first["connections"] != 1 || first["saved queries"] != 1 {
		t.Fatalf("first import: %v", first)
	}
	if second := imp(); second["connections"]+second["saved queries"] != 0 {
		t.Fatalf("second import added: %v", second)
	}

	ctx := context.Background()
	bob, _ := a.st.GetUserByEmail(ctx, "bob@example.com")
	alice, _ := a.st.GetUserByEmail(ctx, "alice@example.com")
	conns, _ := a.st.ListConnections(ctx, bob.ID)
	if len(conns) != 1 || conns[0].Name != "Shop" || conns[0].AllowWrites || conns[0].Password != "" {
		t.Fatalf("bob's connections: %+v", conns)
	}
	bs, _ := a.st.ListSaved(ctx, bob.ID, 0)
	as, _ := a.st.ListSaved(ctx, alice.ID, 0)
	if len(bs) != 2 || len(as) != 1 {
		t.Fatalf("saved queries: bob %d, alice %d", len(bs), len(as))
	}
	if ac, _ := a.st.ListConnections(ctx, alice.ID); len(ac) != 1 || !ac[0].AllowWrites {
		t.Fatalf("alice's connections changed: %+v", ac)
	}
}

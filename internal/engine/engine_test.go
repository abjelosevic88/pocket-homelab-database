package engine

import (
	"context"
	"database/sql"
	"path/filepath"
	"testing"
)

func TestSplit(t *testing.T) {
	cases := []struct {
		engine, in string
		want       int
	}{
		{Postgres, "SELECT 1; SELECT 2;", 2},
		{Postgres, "SELECT ';' AS semi; -- a comment; here\nSELECT 2", 2},
		{Postgres, "CREATE FUNCTION f() RETURNS int AS $$ BEGIN RETURN 1; END; $$ LANGUAGE plpgsql; SELECT f()", 2},
		{Postgres, "SELECT $1; SELECT 2", 2},
		{MySQL, "SELECT 'it''s'; SELECT \"a;b\"; SELECT `x;y` FROM t # c;\n", 3},
		{SQLite, "CREATE TRIGGER t AFTER INSERT ON a BEGIN UPDATE b SET n = n + 1; DELETE FROM c; END; SELECT 1", 2},
		{SQLite, "/* only; a comment */ ;;", 0},
	}
	for _, c := range cases {
		if got := Split(c.in, c.engine); len(got) != c.want {
			t.Errorf("Split(%q) = %d statements %q, want %d", c.in, len(got), got, c.want)
		}
	}
}

func TestClassify(t *testing.T) {
	cases := map[string]string{
		"select * from t":                      "read",
		"  (SELECT 1)":                         "read",
		"WITH x AS (SELECT 1) SELECT * FROM x": "read",
		"WITH d AS (DELETE FROM t RETURNING *) SELECT * FROM d": "write",
		"SELECT * INTO new_t FROM t":                            "write",
		"select 'into' as word":                                 "read",
		"EXPLAIN SELECT 1":                                      "read",
		"EXPLAIN ANALYZE DELETE FROM t":                         "write",
		"PRAGMA table_info(t)":                                  "read",
		"PRAGMA journal_mode = WAL":                             "write",
		"update t set a = 1":                                    "write",
		"-- note\nDELETE FROM t WHERE id = 1":                   "write",
		"DROP TABLE t":                                          "ddl",
		"VACUUM":                                                "ddl",
		"SET search_path TO app":                                "read",
		"SET GLOBAL max_connections = 10":                       "ddl",
		"BEGIN":                                                 "other",
	}
	for in, want := range cases {
		if got := Classify(in); got != want {
			t.Errorf("Classify(%q) = %s, want %s", in, got, want)
		}
	}
}

func TestDestructive(t *testing.T) {
	for in, risky := range map[string]bool{
		"DELETE FROM t":                  true,
		"DELETE FROM t WHERE id = 1":     false,
		"UPDATE t SET a = 1":             true,
		"UPDATE t SET a = 'where'":       true,
		"UPDATE t SET a = 1 WHERE b = 2": false,
		"TRUNCATE t":                     true,
		"DROP TABLE t":                   true,
		"ALTER TABLE t DROP COLUMN a":    true,
		"ALTER TABLE t ADD COLUMN a int": false,
		"INSERT INTO t VALUES (1)":       false,
	} {
		if got := Destructive(in) != ""; got != risky {
			t.Errorf("Destructive(%q) = %v, want %v", in, got, risky)
		}
	}
}

func testDB(t *testing.T) string {
	t.Helper()
	p := filepath.Join(t.TempDir(), "t.db")
	db, err := sql.Open("sqlite", "file:"+p)
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	if _, err := db.Exec(`CREATE TABLE a (id INTEGER PRIMARY KEY, name TEXT, blob BLOB); INSERT INTO a (name, blob) VALUES ('x', X'00FF'), ('y_%', NULL), ('z', NULL);`); err != nil {
		t.Fatal(err)
	}
	return p
}

func TestSQLiteReadOnly(t *testing.T) {
	p := testDB(t)
	ro, err := OpenSQL(Spec{Engine: SQLite, Path: p, ReadOnly: true})
	if err != nil {
		t.Fatal(err)
	}
	defer ro.Close()
	ctx := context.Background()
	res := ro.Run(ctx, "SELECT * FROM a ORDER BY id", RunOptions{ReadOnly: true})
	if res[0].Error != "" || len(res[0].Rows) != 3 {
		t.Fatalf("read = %+v", res[0])
	}
	if b, ok := res[0].Rows[0][2].(Binary); !ok || b.Bin != "00ff" {
		t.Errorf("blob = %#v", res[0].Rows[0][2])
	}
	if res := ro.Run(ctx, "DELETE FROM a", RunOptions{ReadOnly: true}); res[0].Error == "" {
		t.Error("a read-only run deleted")
	}
	// even past the check, the file is opened read-only
	if res := ro.Run(ctx, "DELETE FROM a", RunOptions{}); res[0].Error == "" {
		t.Error("the read-only file took a delete")
	}
	tbl, err := ro.Table(ctx, "main", "a")
	if err != nil {
		t.Fatal(err)
	}
	if _, _, err := ro.Apply(ctx, tbl, []Change{{Op: "delete", Key: map[string]any{"id": float64(1)}}}); err != ErrReadOnly {
		t.Errorf("Apply on read-only = %v", err)
	}
}

func TestRowsFiltersAndSearch(t *testing.T) {
	p := testDB(t)
	c, err := OpenSQL(Spec{Engine: SQLite, Path: p, ReadOnly: true})
	if err != nil {
		t.Fatal(err)
	}
	defer c.Close()
	ctx := context.Background()
	tbl, _ := c.Table(ctx, "main", "a")
	page, err := c.Rows(ctx, tbl, RowsQuery{Search: "%", Limit: 10})
	if err != nil {
		t.Fatal(err)
	}
	if len(page.Rows) != 1 || page.Rows[0][1] != "y_%" {
		t.Errorf("search for a literal %% = %v", page.Rows)
	}
	page, _ = c.Rows(ctx, tbl, RowsQuery{Filters: []Filter{{Column: "blob", Op: "null"}}, Sort: "name", Desc: true, Limit: 10})
	if len(page.Rows) != 2 || page.Rows[0][1] != "z" {
		t.Errorf("null filter, sorted = %v", page.Rows)
	}
	if _, err := c.Rows(ctx, tbl, RowsQuery{Filters: []Filter{{Column: "x; DROP", Op: "eq"}}}); !IsUserError(err) {
		t.Errorf("unknown column = %v", err)
	}
}

func TestParseArgs(t *testing.T) {
	got, err := ParseArgs(`SET "my key" 'a b' plain "with \"quote\""`)
	if err != nil || len(got) != 5 || got[1] != "my key" || got[2] != "a b" || got[4] != `with "quote"` {
		t.Errorf("ParseArgs = %q, %v", got, err)
	}
	if read, _, _ := RedisCommandKind([]string{"get", "k"}); !read {
		t.Error("GET is not read")
	}
	if read, danger, _ := RedisCommandKind([]string{"FLUSHALL"}); read || danger == "" {
		t.Error("FLUSHALL is not dangerous")
	}
	if _, _, err := RedisCommandKind([]string{"MONITOR"}); err == nil {
		t.Error("MONITOR is allowed")
	}
}

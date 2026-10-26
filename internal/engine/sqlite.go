package engine

import (
	"context"
	"database/sql"
	"fmt"
	"net/url"
	"os"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"time"

	_ "modernc.org/sqlite" // the "sqlite" driver
)

// IsSQLiteFile reports whether path starts with the SQLite header.
func IsSQLiteFile(path string) bool {
	f, err := os.Open(path)
	if err != nil {
		return false
	}
	defer f.Close()
	b := make([]byte, 16)
	n, _ := f.Read(b)
	return n == 16 && string(b) == "SQLite format 3\x00"
}

func openSQLite(s Spec) (*sql.DB, error) {
	if s.Path == "" {
		return nil, userErr("no file")
	}
	fi, err := os.Stat(s.Path)
	if err != nil {
		return nil, userErr("cannot open %s: %v", s.Path, err)
	}
	if fi.IsDir() {
		return nil, userErr("%s is a folder, not a SQLite file", s.Path)
	}
	if fi.Size() > 0 && !IsSQLiteFile(s.Path) {
		return nil, userErr("%s is not a SQLite database", s.Path)
	}
	q := "_pragma=busy_timeout(5000)"
	if s.ReadOnly {
		// never write, never create: an app may be using this file right now
		q = "mode=ro&_pragma=busy_timeout(3000)&_pragma=query_only(1)"
	} else {
		q = "mode=rw&" + q + "&_pragma=foreign_keys(1)"
	}
	dsn := "file:" + (&url.URL{Path: s.Path}).EscapedPath() + "?" + q
	return sql.Open("sqlite", dsn)
}

type liteDialect struct{ path string }

func (liteDialect) quote(n string) string { return `"` + strings.ReplaceAll(n, `"`, `""`) + `"` }
func (d liteDialect) table(schema, n string) string {
	if schema == "" || schema == "main" {
		return d.quote(n)
	}
	return d.quote(schema) + "." + d.quote(n)
}
func (liteDialect) ph(int) string { return "?" }
func (liteDialect) likeText(col, ph string) string {
	return "CAST(" + col + " AS TEXT) LIKE " + ph + ` ESCAPE '\'`
}
func (d liteDialect) search(_ string, cols []Column, like func() string) string {
	parts := make([]string, len(cols))
	for i, c := range cols {
		parts[i] = d.likeText(d.quote(c.Name), like())
	}
	return "(" + strings.Join(parts, " OR ") + ")"
}
func (liteDialect) prepare(context.Context, *sql.Tx, bool, time.Duration) error { return nil }

func fileSize(path string) int64 {
	var n int64
	for _, p := range []string{path, path + "-wal"} {
		if fi, err := os.Stat(p); err == nil {
			n += fi.Size()
		}
	}
	return n
}

func (d liteDialect) server(ctx context.Context, db *sql.DB) (ServerInfo, error) {
	si := ServerInfo{Databases: 1, SizeBytes: fileSize(d.path), Extra: map[string]string{"file": d.path}}
	if err := db.QueryRowContext(ctx, `SELECT sqlite_version()`).Scan(&si.Version); err != nil {
		return si, err
	}
	for _, p := range []string{"journal_mode", "page_size", "page_count", "encoding", "user_version", "freelist_count"} {
		var v string
		if db.QueryRowContext(ctx, "PRAGMA "+p).Scan(&v) == nil {
			si.Extra[p] = v
		}
	}
	if fi, err := os.Stat(d.path); err == nil {
		si.Extra["modified"] = fi.ModTime().UTC().Format(time.RFC3339)
	}
	return si, nil
}

func (d liteDialect) databases(ctx context.Context, db *sql.DB) ([]DBInfo, error) {
	n := 0
	_ = db.QueryRowContext(ctx, `SELECT COUNT(*) FROM sqlite_schema WHERE type = 'table' AND name NOT LIKE 'sqlite_%'`).Scan(&n)
	return []DBInfo{{Name: "main", SizeBytes: fileSize(d.path), Tables: n, Current: true}}, nil
}

func (d liteDialect) schemas(ctx context.Context, db *sql.DB) ([]Schema, error) {
	rows, err := db.QueryContext(ctx, `SELECT name, type FROM sqlite_schema WHERE type IN ('table', 'view') AND name NOT LIKE 'sqlite_%' ORDER BY name`)
	if err != nil {
		return nil, err
	}
	s := Schema{Name: "main", Objects: []Object{}}
	for rows.Next() {
		o := Object{Schema: "main", Rows: -1}
		if err := rows.Scan(&o.Name, &o.Kind); err != nil {
			rows.Close()
			return nil, err
		}
		s.Objects = append(s.Objects, o)
	}
	rows.Close()
	// sizes per table when the build has the dbstat table
	sizes := map[string]int64{}
	if r, err := db.QueryContext(ctx, `SELECT name, SUM(pgsize) FROM dbstat GROUP BY name`); err == nil {
		for r.Next() {
			var n string
			var b int64
			if r.Scan(&n, &b) == nil {
				sizes[n] = b
			}
		}
		r.Close()
	}
	// rows: exact for a small file (within a time budget), else sqlite_stat1
	small := fileSize(d.path) < 256<<20
	deadline := time.Now().Add(3 * time.Second)
	stat := map[string]int64{}
	if r, err := db.QueryContext(ctx, `SELECT tbl, stat FROM sqlite_stat1`); err == nil {
		for r.Next() {
			var tbl, st string
			if r.Scan(&tbl, &st) == nil {
				if n, err := strconv.ParseInt(strings.Fields(st + " 0")[0], 10, 64); err == nil && n > stat[tbl] {
					stat[tbl] = n
				}
			}
		}
		r.Close()
	}
	for i := range s.Objects {
		o := &s.Objects[i]
		o.SizeBytes = sizes[o.Name]
		if o.Kind != "table" {
			continue
		}
		if n, ok := stat[o.Name]; ok {
			o.Rows = n
		}
		if small && time.Now().Before(deadline) {
			var n int64
			if db.QueryRowContext(ctx, "SELECT COUNT(*) FROM "+d.quote(o.Name)).Scan(&n) == nil {
				o.Rows = n
			}
		}
	}
	return []Schema{s}, nil
}

var withoutRowid = regexp.MustCompile(`(?i)\bwithout\s+rowid\b`)
var autoinc = regexp.MustCompile(`(?i)\bautoincrement\b`)

func (d liteDialect) tableInfo(ctx context.Context, db *sql.DB, _ string, name string) (*TableInfo, error) {
	t := &TableInfo{Schema: "main", Name: name, Rows: -1}
	var ddl sql.NullString
	err := db.QueryRowContext(ctx, `SELECT type, sql FROM sqlite_schema WHERE name = ? AND type IN ('table', 'view')`, name).Scan(&t.Kind, &ddl)
	if err == sql.ErrNoRows {
		return nil, userErr("there is no table %q here", name)
	}
	if err != nil {
		return nil, err
	}
	rows, err := db.QueryContext(ctx, `SELECT cid, name, type, "notnull", dflt_value, pk, hidden FROM pragma_table_xinfo(?) ORDER BY cid`, name)
	if err != nil {
		return nil, err
	}
	type pkCol struct {
		pos  int
		name string
	}
	var pks []pkCol
	for rows.Next() {
		var c Column
		var notnull, pk, hidden int
		var def sql.NullString
		if err := rows.Scan(&c.Position, &c.Name, &c.Type, &notnull, &def, &pk, &hidden); err != nil {
			rows.Close()
			return nil, err
		}
		if hidden == 1 {
			continue // a virtual table's hidden column
		}
		c.Position++
		c.Nullable = notnull == 0
		c.Generated = hidden == 2 || hidden == 3
		if def.Valid {
			c.Default = &def.String
		}
		if pk > 0 {
			c.PK = true
			pks = append(pks, pkCol{pk, c.Name})
			if strings.EqualFold(c.Type, "INTEGER") {
				c.Auto = true // an INTEGER PRIMARY KEY is the rowid
			}
		}
		t.Columns = append(t.Columns, c)
	}
	rows.Close()
	for i := 1; i <= len(pks); i++ {
		for _, p := range pks {
			if p.pos == i {
				t.PrimaryKey = append(t.PrimaryKey, p.name)
			}
		}
	}
	if len(pks) > 1 {
		for i := range t.Columns {
			t.Columns[i].Auto = false
		}
	} else if len(pks) == 1 && autoinc.MatchString(ddl.String) {
		for i := range t.Columns {
			t.Columns[i].Auto = t.Columns[i].PK
		}
	}
	if t.Kind == "table" && len(t.PrimaryKey) == 0 && !withoutRowid.MatchString(ddl.String) {
		t.RowID = true
	}
	// indexes
	if rows, err := db.QueryContext(ctx, `SELECT name, "unique", origin FROM pragma_index_list(?)`, name); err == nil {
		for rows.Next() {
			var ix Index
			var uniq int
			var origin string
			if rows.Scan(&ix.Name, &uniq, &origin) == nil {
				ix.Unique, ix.Primary = uniq == 1, origin == "pk"
				t.Indexes = append(t.Indexes, ix)
			}
		}
		rows.Close()
	}
	for i := range t.Indexes {
		ix := &t.Indexes[i]
		if rows, err := db.QueryContext(ctx, `SELECT name FROM pragma_index_info(?) ORDER BY seqno`, ix.Name); err == nil {
			for rows.Next() {
				var n sql.NullString
				if rows.Scan(&n) == nil {
					ix.Columns = append(ix.Columns, firstNonEmpty(n.String, "(expression)"))
				}
			}
			rows.Close()
		}
		var def sql.NullString
		if db.QueryRowContext(ctx, `SELECT sql FROM sqlite_schema WHERE type = 'index' AND name = ?`, ix.Name).Scan(&def) == nil {
			ix.Definition = def.String
		}
	}
	// foreign keys out of this table
	readFKs := func(table string) []ForeignKey {
		var out []ForeignKey
		rows, err := db.QueryContext(ctx, `SELECT id, "table", "from", "to" FROM pragma_foreign_key_list(?) ORDER BY id, seq`, table)
		if err != nil {
			return nil
		}
		defer rows.Close()
		byID := map[int]int{}
		for rows.Next() {
			var id int
			var ref, from string
			var to sql.NullString
			if rows.Scan(&id, &ref, &from, &to) != nil {
				continue
			}
			i, ok := byID[id]
			if !ok {
				out = append(out, ForeignKey{Name: fmt.Sprintf("fk_%s_%d", table, id), RefSchema: "main", RefTable: ref})
				i = len(out) - 1
				byID[id] = i
			}
			out[i].Columns = append(out[i].Columns, from)
			out[i].RefColumns = append(out[i].RefColumns, to.String)
		}
		return out
	}
	t.ForeignKeys = readFKs(name)
	// … and into it, from every other table
	var tables []string
	if rows, err := db.QueryContext(ctx, `SELECT name FROM sqlite_schema WHERE type = 'table' AND name NOT LIKE 'sqlite_%'`); err == nil {
		for rows.Next() {
			var n string
			if rows.Scan(&n) == nil {
				tables = append(tables, n)
			}
		}
		rows.Close()
	}
	for _, other := range tables {
		for _, f := range readFKs(other) {
			if strings.EqualFold(f.RefTable, name) {
				f.Schema, f.Table = "main", other
				t.ReferencedBy = append(t.ReferencedBy, f)
			}
		}
	}
	// the CREATE statements: table, its indexes and triggers
	var b strings.Builder
	b.WriteString(strings.TrimSpace(ddl.String) + ";")
	if rows, err := db.QueryContext(ctx, `SELECT sql FROM sqlite_schema WHERE tbl_name = ? AND type IN ('index', 'trigger') AND sql IS NOT NULL ORDER BY type, name`, name); err == nil {
		for rows.Next() {
			var s string
			if rows.Scan(&s) == nil {
				b.WriteString("\n" + strings.TrimSpace(s) + ";")
			}
		}
		rows.Close()
	}
	t.DDL = b.String()
	if t.Kind == "table" {
		var n int64
		cctx, cancel := context.WithTimeout(ctx, 2*time.Second)
		if db.QueryRowContext(cctx, "SELECT COUNT(*) FROM "+d.quote(name)).Scan(&n) == nil {
			t.Rows = n
		}
		cancel()
		var sz int64
		if db.QueryRowContext(ctx, `SELECT SUM(pgsize) FROM dbstat WHERE name = ?`, name).Scan(&sz) == nil {
			t.SizeBytes = sz
		}
	}
	return t, nil
}

// sortSchemas puts the usual schema first, the rest by name.
func sortSchemas(s []Schema, first string) {
	sort.SliceStable(s, func(i, j int) bool {
		if (s[i].Name == first) != (s[j].Name == first) {
			return s[i].Name == first
		}
		return s[i].Name < s[j].Name
	})
}

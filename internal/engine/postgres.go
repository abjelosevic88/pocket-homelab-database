package engine

import (
	"context"
	"database/sql"
	"fmt"
	"net/url"
	"strconv"
	"strings"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/stdlib"
)

func openPostgres(s Spec) (*sql.DB, error) {
	u := url.URL{Scheme: "postgres", Host: s.Addr(), Path: "/" + firstNonEmpty(s.Database, "postgres")}
	if s.User != "" {
		if s.Password != "" {
			u.User = url.UserPassword(s.User, s.Password)
		} else {
			u.User = url.User(s.User)
		}
	}
	q := url.Values{}
	q.Set("sslmode", firstNonEmpty(s.SSLMode, "prefer"))
	q.Set("connect_timeout", strconv.Itoa(int(s.timeout().Seconds())))
	q.Set("application_name", "pocket-database")
	u.RawQuery = q.Encode()
	cfg, err := pgx.ParseConfig(u.String())
	if err != nil {
		return nil, fmt.Errorf("connection settings: %w", err)
	}
	// Plain text over the wire: values are sent as literals and PostgreSQL
	// turns them into each column's type, so editing works for every type.
	cfg.DefaultQueryExecMode = pgx.QueryExecModeSimpleProtocol
	if s.ReadOnly {
		cfg.RuntimeParams["default_transaction_read_only"] = "on"
	}
	return stdlib.OpenDB(*cfg), nil
}

type pgDialect struct{}

func (pgDialect) quote(n string) string { return `"` + strings.ReplaceAll(n, `"`, `""`) + `"` }
func (d pgDialect) table(schema, n string) string {
	if schema == "" {
		return d.quote(n)
	}
	return d.quote(schema) + "." + d.quote(n)
}
func (pgDialect) ph(i int) string { return "$" + strconv.Itoa(i) }
func (pgDialect) likeText(col, ph string) string {
	return col + "::text ILIKE " + ph
}

// search matches the whole row as text.
func (d pgDialect) search(name string, _ []Column, like func() string) string {
	return d.quote(name) + "::text ILIKE " + like()
}

func (pgDialect) prepare(ctx context.Context, tx *sql.Tx, readOnly bool, timeout time.Duration) error {
	if timeout > 0 {
		if _, err := tx.ExecContext(ctx, fmt.Sprintf("SET LOCAL statement_timeout = %d", timeout.Milliseconds())); err != nil {
			return err
		}
	}
	return nil
}

func (pgDialect) server(ctx context.Context, db *sql.DB) (ServerInfo, error) {
	var si ServerInfo
	var size sql.NullInt64
	var up sql.NullString
	err := db.QueryRowContext(ctx, `SELECT current_setting('server_version'), pg_database_size(current_database()), (SELECT count(*) FROM pg_database WHERE NOT datistemplate), date_trunc('second', now() - pg_postmaster_start_time())::text`).Scan(&si.Version, &size, &si.Databases, &up)
	si.SizeBytes, si.Uptime = size.Int64, up.String
	if v, _, ok := strings.Cut(si.Version, " "); ok {
		si.Version = v // "18.6 (Debian 18.6-1.pgdg13+2)"
	}
	si.Extra = map[string]string{}
	var full, user, db2 string
	if db.QueryRowContext(ctx, `SELECT version(), current_user, current_database()`).Scan(&full, &user, &db2) == nil {
		si.Extra["build"], si.Extra["user"], si.Extra["database"] = full, user, db2
	}
	var conns, max int
	if db.QueryRowContext(ctx, `SELECT (SELECT count(*) FROM pg_stat_activity), current_setting('max_connections')::int`).Scan(&conns, &max) == nil {
		si.Extra["connections"] = fmt.Sprintf("%d of %d", conns, max)
	}
	return si, err
}

func (pgDialect) databases(ctx context.Context, db *sql.DB) ([]DBInfo, error) {
	rows, err := db.QueryContext(ctx, `SELECT datname, CASE WHEN has_database_privilege(datname, 'CONNECT') THEN pg_database_size(datname) ELSE -1 END, datname = current_database()
		FROM pg_database WHERE NOT datistemplate AND datallowconn ORDER BY datname`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []DBInfo{}
	for rows.Next() {
		d := DBInfo{Tables: -1}
		if err := rows.Scan(&d.Name, &d.SizeBytes, &d.Current); err != nil {
			return nil, err
		}
		out = append(out, d)
	}
	return out, rows.Err()
}

func pgKind(k string) string {
	switch k {
	case "v":
		return "view"
	case "m":
		return "matview"
	case "f":
		return "foreign"
	}
	return "table"
}

func (pgDialect) schemas(ctx context.Context, db *sql.DB) ([]Schema, error) {
	rows, err := db.QueryContext(ctx, `SELECT n.nspname, c.relname, c.relkind::text, c.reltuples::bigint,
			CASE WHEN c.relkind IN ('r','m','p') THEN pg_total_relation_size(c.oid) ELSE 0 END, coalesce(obj_description(c.oid, 'pg_class'), '')
		FROM pg_class c JOIN pg_namespace n ON n.oid = c.relnamespace
		WHERE c.relkind IN ('r','v','m','p','f') AND NOT c.relispartition
		  AND n.nspname NOT IN ('pg_catalog', 'information_schema') AND n.nspname NOT LIKE 'pg_toast%' AND n.nspname NOT LIKE 'pg_temp%'
		ORDER BY 1, 2`)
	if err != nil {
		return nil, err
	}
	byName := map[string]*Schema{}
	var order []string
	for rows.Next() {
		var o Object
		var kind string
		if err := rows.Scan(&o.Schema, &o.Name, &kind, &o.Rows, &o.SizeBytes, &o.Comment); err != nil {
			rows.Close()
			return nil, err
		}
		o.Kind = pgKind(kind)
		if o.Kind == "view" {
			o.Rows = -1
		}
		if byName[o.Schema] == nil {
			byName[o.Schema] = &Schema{Name: o.Schema, Objects: []Object{}}
			order = append(order, o.Schema)
		}
		byName[o.Schema].Objects = append(byName[o.Schema].Objects, o)
	}
	rows.Close()
	// empty schemas too, so a new one shows up
	if r2, err := db.QueryContext(ctx, `SELECT nspname FROM pg_namespace WHERE nspname NOT IN ('pg_catalog','information_schema') AND nspname NOT LIKE 'pg_toast%' AND nspname NOT LIKE 'pg_temp%' ORDER BY 1`); err == nil {
		for r2.Next() {
			var n string
			if r2.Scan(&n) == nil && byName[n] == nil {
				byName[n] = &Schema{Name: n, Objects: []Object{}}
				order = append(order, n)
			}
		}
		r2.Close()
	}
	out := make([]Schema, 0, len(order))
	for _, n := range order {
		out = append(out, *byName[n])
	}
	sortSchemas(out, "public")
	return out, nil
}

func (d pgDialect) tableInfo(ctx context.Context, db *sql.DB, schema, name string) (*TableInfo, error) {
	if schema == "" {
		schema = "public"
	}
	reg := d.table(schema, name)
	t := &TableInfo{Schema: schema, Name: name}
	var kind string
	err := db.QueryRowContext(ctx, `SELECT c.relkind::text, c.reltuples::bigint, CASE WHEN c.relkind IN ('r','m','p') THEN pg_total_relation_size(c.oid) ELSE 0 END, coalesce(obj_description(c.oid, 'pg_class'), '')
		FROM pg_class c WHERE c.oid = $1::regclass`, reg).Scan(&kind, &t.Rows, &t.SizeBytes, &t.Comment)
	if err != nil {
		if strings.Contains(err.Error(), "does not exist") {
			return nil, userErr("there is no table %s.%s", schema, name)
		}
		return nil, err
	}
	t.Kind = pgKind(kind)
	rows, err := db.QueryContext(ctx, `SELECT a.attname, format_type(a.atttypid, a.atttypmod), NOT a.attnotnull, pg_get_expr(ad.adbin, ad.adrelid), a.attnum,
			coalesce(col_description(a.attrelid, a.attnum), ''), a.attidentity <> '' OR coalesce(pg_get_expr(ad.adbin, ad.adrelid), '') LIKE 'nextval(%', a.attgenerated <> ''
		FROM pg_attribute a LEFT JOIN pg_attrdef ad ON ad.adrelid = a.attrelid AND ad.adnum = a.attnum
		WHERE a.attrelid = $1::regclass AND a.attnum > 0 AND NOT a.attisdropped ORDER BY a.attnum`, reg)
	if err != nil {
		return nil, err
	}
	for rows.Next() {
		var c Column
		var def sql.NullString
		if err := rows.Scan(&c.Name, &c.Type, &c.Nullable, &def, &c.Position, &c.Comment, &c.Auto, &c.Generated); err != nil {
			rows.Close()
			return nil, err
		}
		if def.Valid {
			c.Default = &def.String
		}
		t.Columns = append(t.Columns, c)
	}
	rows.Close()
	// primary key
	if rows, err := db.QueryContext(ctx, `SELECT a.attname FROM pg_index i JOIN pg_attribute a ON a.attrelid = i.indrelid AND a.attnum = ANY(i.indkey)
		WHERE i.indrelid = $1::regclass AND i.indisprimary ORDER BY array_position(i.indkey::int2[], a.attnum)`, reg); err == nil {
		for rows.Next() {
			var n string
			if rows.Scan(&n) == nil {
				t.PrimaryKey = append(t.PrimaryKey, n)
			}
		}
		rows.Close()
	}
	pk := map[string]bool{}
	for _, n := range t.PrimaryKey {
		pk[n] = true
	}
	for i := range t.Columns {
		t.Columns[i].PK = pk[t.Columns[i].Name]
	}
	// indexes
	if rows, err := db.QueryContext(ctx, `SELECT ic.relname, i.indisunique, i.indisprimary, pg_get_indexdef(i.indexrelid),
			array_to_string(ARRAY(SELECT pg_get_indexdef(i.indexrelid, k, true) FROM generate_series(1, i.indnkeyatts) k ORDER BY k), chr(31))
		FROM pg_index i JOIN pg_class ic ON ic.oid = i.indexrelid WHERE i.indrelid = $1::regclass ORDER BY i.indisprimary DESC, 1`, reg); err == nil {
		for rows.Next() {
			var ix Index
			var cols string
			if rows.Scan(&ix.Name, &ix.Unique, &ix.Primary, &ix.Definition, &cols) == nil {
				ix.Columns = strings.Split(cols, "\x1f")
				t.Indexes = append(t.Indexes, ix)
			}
		}
		rows.Close()
	}
	// foreign keys, both ways
	fk := func(q string, incoming bool) {
		rows, err := db.QueryContext(ctx, q, reg)
		if err != nil {
			return
		}
		defer rows.Close()
		for rows.Next() {
			var f ForeignKey
			var cols, refs string
			if rows.Scan(&f.Name, &f.Schema, &f.Table, &cols, &f.RefSchema, &f.RefTable, &refs) == nil {
				f.Columns, f.RefColumns = strings.Split(cols, "\x1f"), strings.Split(refs, "\x1f")
				if incoming {
					t.ReferencedBy = append(t.ReferencedBy, f)
				} else {
					f.Schema, f.Table = "", ""
					t.ForeignKeys = append(t.ForeignKeys, f)
				}
			}
		}
	}
	const fkq = `SELECT con.conname, ns.nspname, cs.relname,
			array_to_string(ARRAY(SELECT attname FROM unnest(con.conkey) WITH ORDINALITY k(n, o) JOIN pg_attribute ON attrelid = con.conrelid AND attnum = k.n ORDER BY o), chr(31)),
			nf.nspname, cf.relname,
			array_to_string(ARRAY(SELECT attname FROM unnest(con.confkey) WITH ORDINALITY k(n, o) JOIN pg_attribute ON attrelid = con.confrelid AND attnum = k.n ORDER BY o), chr(31))
		FROM pg_constraint con
		JOIN pg_class cs ON cs.oid = con.conrelid JOIN pg_namespace ns ON ns.oid = cs.relnamespace
		JOIN pg_class cf ON cf.oid = con.confrelid JOIN pg_namespace nf ON nf.oid = cf.relnamespace
		WHERE con.contype = 'f' AND `
	fk(fkq+`con.conrelid = $1::regclass ORDER BY 1`, false)
	fk(fkq+`con.confrelid = $1::regclass ORDER BY 2, 3`, true)
	t.DDL = d.ddl(ctx, db, t, reg)
	return t, nil
}

// ddl rebuilds a CREATE statement (PostgreSQL has no SHOW CREATE TABLE).
func (d pgDialect) ddl(ctx context.Context, db *sql.DB, t *TableInfo, reg string) string {
	if t.Kind == "view" || t.Kind == "matview" {
		var def string
		if db.QueryRowContext(ctx, `SELECT pg_get_viewdef($1::regclass, true)`, reg).Scan(&def) != nil {
			return ""
		}
		kw := "VIEW"
		if t.Kind == "matview" {
			kw = "MATERIALIZED VIEW"
		}
		return fmt.Sprintf("CREATE %s %s AS\n%s", kw, reg, strings.TrimRight(def, "; \n"))
	}
	var b strings.Builder
	fmt.Fprintf(&b, "CREATE TABLE %s (\n", reg)
	var lines []string
	for _, c := range t.Columns {
		l := "  " + d.quote(c.Name) + " " + c.Type
		if c.Default != nil {
			l += " DEFAULT " + *c.Default
		}
		if !c.Nullable {
			l += " NOT NULL"
		}
		lines = append(lines, l)
	}
	if rows, err := db.QueryContext(ctx, `SELECT conname, pg_get_constraintdef(oid) FROM pg_constraint WHERE conrelid = $1::regclass ORDER BY contype = 'p' DESC, contype, conname`, reg); err == nil {
		for rows.Next() {
			var n, def string
			if rows.Scan(&n, &def) == nil {
				lines = append(lines, "  CONSTRAINT "+d.quote(n)+" "+def)
			}
		}
		rows.Close()
	}
	b.WriteString(strings.Join(lines, ",\n"))
	b.WriteString("\n);")
	if rows, err := db.QueryContext(ctx, `SELECT pg_get_indexdef(i.indexrelid) FROM pg_index i
		WHERE i.indrelid = $1::regclass AND NOT EXISTS (SELECT 1 FROM pg_constraint c WHERE c.conindid = i.indexrelid) ORDER BY 1`, reg); err == nil {
		for rows.Next() {
			var def string
			if rows.Scan(&def) == nil {
				b.WriteString("\n" + def + ";")
			}
		}
		rows.Close()
	}
	if t.Comment != "" {
		fmt.Fprintf(&b, "\nCOMMENT ON TABLE %s IS '%s';", reg, strings.ReplaceAll(t.Comment, "'", "''"))
	}
	return b.String()
}

func firstNonEmpty(vals ...string) string {
	for _, v := range vals {
		if v != "" {
			return v
		}
	}
	return ""
}

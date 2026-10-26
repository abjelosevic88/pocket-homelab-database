package engine

import (
	"context"
	"database/sql"
	"fmt"
	"strconv"
	"strings"
	"time"

	"github.com/go-sql-driver/mysql"
)

func openMySQL(s Spec) (*sql.DB, error) {
	cfg := mysql.NewConfig()
	cfg.User, cfg.Passwd = s.User, s.Password
	cfg.Net, cfg.Addr = "tcp", s.Addr()
	cfg.DBName = s.Database
	cfg.Timeout = s.timeout()
	cfg.ParseTime = true
	cfg.InterpolateParams = true
	cfg.AllowNativePasswords = true
	cfg.AllowCleartextPasswords = false
	switch s.SSLMode {
	case "require":
		cfg.TLSConfig = "skip-verify"
	case "verify-full":
		cfg.TLSConfig = "true"
	case "disable":
		cfg.TLSConfig = "false"
	default:
		cfg.TLSConfig = "preferred"
	}
	cfg.Collation = "utf8mb4_general_ci"
	conn, err := mysql.NewConnector(cfg)
	if err != nil {
		return nil, fmt.Errorf("connection settings: %w", err)
	}
	return sql.OpenDB(conn), nil
}

type myDialect struct{}

func (myDialect) quote(n string) string { return "`" + strings.ReplaceAll(n, "`", "``") + "`" }
func (d myDialect) table(_, n string) string {
	return d.quote(n)
}
func (myDialect) ph(int) string { return "?" }
func (myDialect) likeText(col, ph string) string {
	return "CAST(" + col + " AS CHAR) LIKE " + ph
}
func (d myDialect) search(_ string, cols []Column, like func() string) string {
	parts := make([]string, len(cols))
	for i, c := range cols {
		parts[i] = "CAST(" + d.quote(c.Name) + " AS CHAR)"
	}
	return "CONCAT_WS(CHAR(31), " + strings.Join(parts, ", ") + ") LIKE " + like()
}

func (myDialect) prepare(ctx context.Context, tx *sql.Tx, _ bool, timeout time.Duration) error {
	if timeout > 0 {
		// MySQL counts milliseconds, MariaDB seconds; each ignores the other's
		_, _ = tx.ExecContext(ctx, fmt.Sprintf("SET SESSION max_execution_time = %d", timeout.Milliseconds()))
		_, _ = tx.ExecContext(ctx, fmt.Sprintf("SET SESSION max_statement_time = %d", int(timeout.Seconds())))
	}
	return nil
}

func (myDialect) server(ctx context.Context, db *sql.DB) (ServerInfo, error) {
	var si ServerInfo
	si.Extra = map[string]string{}
	var cur sql.NullString
	if err := db.QueryRowContext(ctx, `SELECT VERSION(), DATABASE()`).Scan(&si.Version, &cur); err != nil {
		return si, err
	}
	si.Extra["database"] = cur.String
	var user string
	if db.QueryRowContext(ctx, `SELECT CURRENT_USER()`).Scan(&user) == nil {
		si.Extra["user"] = user
	}
	if strings.Contains(strings.ToLower(si.Version), "mariadb") {
		si.Extra["flavor"] = "MariaDB"
		si.Extra["build"] = si.Version
		si.Version, _, _ = strings.Cut(si.Version, "-") // "12.3.3-MariaDB-ubu2404"
	} else {
		si.Extra["flavor"] = "MySQL"
	}
	var name, up string
	if db.QueryRowContext(ctx, `SHOW GLOBAL STATUS LIKE 'Uptime'`).Scan(&name, &up) == nil {
		if secs, err := strconv.Atoi(up); err == nil {
			si.Uptime = (time.Duration(secs) * time.Second).String()
		}
	}
	var th string
	if db.QueryRowContext(ctx, `SHOW GLOBAL STATUS LIKE 'Threads_connected'`).Scan(&name, &th) == nil {
		si.Extra["connections"] = th
	}
	_ = db.QueryRowContext(ctx, `SELECT COUNT(*) FROM information_schema.schemata`).Scan(&si.Databases)
	if cur.Valid {
		_ = db.QueryRowContext(ctx, `SELECT COALESCE(SUM(data_length + index_length), 0) FROM information_schema.tables WHERE table_schema = DATABASE()`).Scan(&si.SizeBytes)
	}
	return si, nil
}

var mySystem = map[string]bool{"information_schema": true, "performance_schema": true, "mysql": true, "sys": true}

func (myDialect) databases(ctx context.Context, db *sql.DB) ([]DBInfo, error) {
	var cur sql.NullString
	_ = db.QueryRowContext(ctx, `SELECT DATABASE()`).Scan(&cur)
	rows, err := db.QueryContext(ctx, `SELECT s.schema_name, COALESCE(SUM(t.data_length + t.index_length), 0), COUNT(t.table_name)
		FROM information_schema.schemata s LEFT JOIN information_schema.tables t ON t.table_schema = s.schema_name
		GROUP BY s.schema_name ORDER BY s.schema_name`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var user, system []DBInfo
	for rows.Next() {
		var d DBInfo
		if err := rows.Scan(&d.Name, &d.SizeBytes, &d.Tables); err != nil {
			return nil, err
		}
		d.Current = cur.Valid && d.Name == cur.String
		if mySystem[d.Name] {
			system = append(system, d)
		} else {
			user = append(user, d)
		}
	}
	return append(user, system...), rows.Err()
}

func (myDialect) schemas(ctx context.Context, db *sql.DB) ([]Schema, error) {
	var cur sql.NullString
	if err := db.QueryRowContext(ctx, `SELECT DATABASE()`).Scan(&cur); err != nil {
		return nil, err
	}
	if !cur.Valid {
		return []Schema{}, nil
	}
	rows, err := db.QueryContext(ctx, `SELECT table_name, table_type, COALESCE(table_rows, -1), COALESCE(data_length + index_length, 0), COALESCE(table_comment, '')
		FROM information_schema.tables WHERE table_schema = DATABASE() ORDER BY table_name`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	s := Schema{Name: cur.String, Objects: []Object{}}
	for rows.Next() {
		o := Object{Schema: cur.String}
		var typ string
		if err := rows.Scan(&o.Name, &typ, &o.Rows, &o.SizeBytes, &o.Comment); err != nil {
			return nil, err
		}
		o.Kind = "table"
		if strings.Contains(typ, "VIEW") {
			o.Kind, o.Rows = "view", -1
		}
		s.Objects = append(s.Objects, o)
	}
	return []Schema{s}, rows.Err()
}

func (d myDialect) tableInfo(ctx context.Context, db *sql.DB, schema, name string) (*TableInfo, error) {
	t := &TableInfo{Schema: schema, Name: name}
	var typ string
	err := db.QueryRowContext(ctx, `SELECT table_type, COALESCE(table_rows, -1), COALESCE(data_length + index_length, 0), COALESCE(table_comment, '')
		FROM information_schema.tables WHERE table_schema = DATABASE() AND table_name = ?`, name).Scan(&typ, &t.Rows, &t.SizeBytes, &t.Comment)
	if err == sql.ErrNoRows {
		return nil, userErr("there is no table %q here", name)
	}
	if err != nil {
		return nil, err
	}
	t.Kind = "table"
	if strings.Contains(typ, "VIEW") {
		t.Kind, t.Rows = "view", -1
	}
	rows, err := db.QueryContext(ctx, `SELECT column_name, column_type, is_nullable = 'YES', column_default, ordinal_position, COALESCE(column_comment, ''),
			extra LIKE '%auto_increment%', extra LIKE '%GENERATED%' AND extra NOT LIKE '%DEFAULT_GENERATED%', column_key = 'PRI'
		FROM information_schema.columns WHERE table_schema = DATABASE() AND table_name = ? ORDER BY ordinal_position`, name)
	if err != nil {
		return nil, err
	}
	for rows.Next() {
		var c Column
		var def sql.NullString
		if err := rows.Scan(&c.Name, &c.Type, &c.Nullable, &def, &c.Position, &c.Comment, &c.Auto, &c.Generated, &c.PK); err != nil {
			rows.Close()
			return nil, err
		}
		if def.Valid {
			c.Default = &def.String
		}
		t.Columns = append(t.Columns, c)
	}
	rows.Close()
	// indexes (the primary key is the index called PRIMARY)
	if rows, err := db.QueryContext(ctx, `SELECT index_name, non_unique = 0, column_name FROM information_schema.statistics
		WHERE table_schema = DATABASE() AND table_name = ? ORDER BY index_name = 'PRIMARY' DESC, index_name, seq_in_index`, name); err == nil {
		byName := map[string]int{}
		for rows.Next() {
			var n, col string
			var uniq bool
			if rows.Scan(&n, &uniq, &col) != nil {
				continue
			}
			i, ok := byName[n]
			if !ok {
				t.Indexes = append(t.Indexes, Index{Name: n, Unique: uniq, Primary: n == "PRIMARY"})
				i = len(t.Indexes) - 1
				byName[n] = i
			}
			t.Indexes[i].Columns = append(t.Indexes[i].Columns, col)
			if n == "PRIMARY" {
				t.PrimaryKey = append(t.PrimaryKey, col)
			}
		}
		rows.Close()
	}
	fk := func(q string, incoming bool) {
		rows, err := db.QueryContext(ctx, q, name)
		if err != nil {
			return
		}
		defer rows.Close()
		byName := map[string]int{}
		for rows.Next() {
			var n, sch, tbl, col, rsch, rtbl, rcol string
			if rows.Scan(&n, &sch, &tbl, &col, &rsch, &rtbl, &rcol) != nil {
				continue
			}
			list := &t.ForeignKeys
			if incoming {
				list = &t.ReferencedBy
			}
			key := sch + "." + tbl + "." + n
			i, ok := byName[key]
			if !ok {
				f := ForeignKey{Name: n, RefSchema: rsch, RefTable: rtbl}
				if incoming {
					f.Schema, f.Table = sch, tbl
				}
				*list = append(*list, f)
				i = len(*list) - 1
				byName[key] = i
			}
			(*list)[i].Columns = append((*list)[i].Columns, col)
			(*list)[i].RefColumns = append((*list)[i].RefColumns, rcol)
		}
	}
	const fkq = `SELECT constraint_name, table_schema, table_name, column_name, referenced_table_schema, referenced_table_name, referenced_column_name
		FROM information_schema.key_column_usage WHERE referenced_table_name IS NOT NULL AND `
	fk(fkq+`table_schema = DATABASE() AND table_name = ? ORDER BY constraint_name, ordinal_position`, false)
	fk(fkq+`referenced_table_schema = DATABASE() AND referenced_table_name = ? ORDER BY table_name, constraint_name, ordinal_position`, true)
	// the server's own CREATE statement
	var n1, ddl string
	q := "SHOW CREATE TABLE " + d.quote(name)
	if t.Kind == "view" {
		var cs, coll string
		if db.QueryRowContext(ctx, "SHOW CREATE VIEW "+d.quote(name)).Scan(&n1, &ddl, &cs, &coll) == nil {
			t.DDL = ddl
		}
	} else if db.QueryRowContext(ctx, q).Scan(&n1, &ddl) == nil {
		t.DDL = ddl
	}
	return t, nil
}

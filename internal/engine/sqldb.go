package engine

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"sort"
	"strings"
	"time"
)

// dialect is what differs between the SQL engines.
type dialect interface {
	quote(name string) string
	table(schema, name string) string
	ph(i int) string // the i-th placeholder (1-based)
	server(ctx context.Context, db *sql.DB) (ServerInfo, error)
	databases(ctx context.Context, db *sql.DB) ([]DBInfo, error)
	schemas(ctx context.Context, db *sql.DB) ([]Schema, error)
	tableInfo(ctx context.Context, db *sql.DB, schema, name string) (*TableInfo, error)
	// prepare runs once per transaction (read-only, statement timeout).
	prepare(ctx context.Context, tx *sql.Tx, readOnly bool, timeout time.Duration) error
	// likeText is a condition "column as text matches the placeholder".
	likeText(col, ph string) string
	// search is a condition "any column of the row matches the placeholder"
	// (name is the table's own name, the row's alias).
	// like() adds the pattern as an argument and returns its placeholder.
	search(name string, cols []Column, like func() string) string
}

// SQL is an open SQL database.
type SQL struct {
	db   *sql.DB
	d    dialect
	spec Spec
}

// OpenSQL opens a SQL connection (lazily: Ping to check it).
func OpenSQL(spec Spec) (*SQL, error) {
	var (
		db  *sql.DB
		d   dialect
		err error
	)
	switch spec.Engine {
	case Postgres:
		db, err = openPostgres(spec)
		d = pgDialect{}
	case MySQL:
		db, err = openMySQL(spec)
		d = myDialect{}
	case SQLite:
		db, err = openSQLite(spec)
		d = liteDialect{path: spec.Path}
	default:
		return nil, fmt.Errorf("%s does not speak SQL", spec.Engine)
	}
	if err != nil {
		return nil, err
	}
	db.SetMaxOpenConns(4)
	db.SetMaxIdleConns(2)
	db.SetConnMaxIdleTime(2 * time.Minute)
	return &SQL{db: db, d: d, spec: spec}, nil
}

// Close closes the pool.
func (c *SQL) Close() error { return c.db.Close() }

// Spec is what the connection was opened with.
func (c *SQL) Spec() Spec { return c.spec }

// Ping checks the connection.
func (c *SQL) Ping(ctx context.Context) error { return c.db.PingContext(ctx) }

// Server describes the server.
func (c *SQL) Server(ctx context.Context) (ServerInfo, error) {
	si, err := c.d.server(ctx, c.db)
	si.Engine = c.spec.Engine
	if si.Extra == nil {
		si.Extra = map[string]string{}
	}
	return si, err
}

// Databases lists the databases on the server.
func (c *SQL) Databases(ctx context.Context) ([]DBInfo, error) { return c.d.databases(ctx, c.db) }

// Schemas lists schemas and their tables in the open database.
func (c *SQL) Schemas(ctx context.Context) ([]Schema, error) { return c.d.schemas(ctx, c.db) }

// Table describes one table.
func (c *SQL) Table(ctx context.Context, schema, name string) (*TableInfo, error) {
	t, err := c.d.tableInfo(ctx, c.db, schema, name)
	if err != nil {
		return nil, err
	}
	if t == nil || len(t.Columns) == 0 {
		return nil, userErr("there is no table %q here", name)
	}
	if t.PrimaryKey == nil {
		t.PrimaryKey = []string{}
	}
	if t.Indexes == nil {
		t.Indexes = []Index{}
	}
	if t.ForeignKeys == nil {
		t.ForeignKeys = []ForeignKey{}
	}
	if t.ReferencedBy == nil {
		t.ReferencedBy = []ForeignKey{}
	}
	return t, nil
}

func (c *SQL) begin(ctx context.Context, readOnly bool, timeout time.Duration) (*sql.Tx, error) {
	tx, err := c.db.BeginTx(ctx, &sql.TxOptions{ReadOnly: readOnly && c.spec.Engine != SQLite})
	if err != nil {
		return nil, err
	}
	if err := c.d.prepare(ctx, tx, readOnly, timeout); err != nil {
		_ = tx.Rollback()
		return nil, err
	}
	return tx, nil
}

// Rows reads a page of a table.
func (c *SQL) Rows(ctx context.Context, t *TableInfo, q RowsQuery) (*Page, error) {
	known := map[string]bool{}
	for _, col := range t.Columns {
		known[col.Name] = true
	}
	var where []string
	var args []any
	arg := func(v any) string { args = append(args, v); return c.d.ph(len(args)) }
	for _, f := range q.Filters {
		if !known[f.Column] {
			return nil, userErr("unknown column %q", f.Column)
		}
		col := c.d.quote(f.Column)
		switch f.Op {
		case "eq":
			where = append(where, col+" = "+arg(f.Value))
		case "ne":
			where = append(where, "("+col+" <> "+arg(f.Value)+" OR "+col+" IS NULL)")
		case "lt", "le", "gt", "ge":
			op := map[string]string{"lt": "<", "le": "<=", "gt": ">", "ge": ">="}[f.Op]
			where = append(where, col+" "+op+" "+arg(f.Value))
		case "contains":
			where = append(where, c.d.likeText(col, arg("%"+escapeLike(f.Value)+"%")))
		case "starts":
			where = append(where, c.d.likeText(col, arg(escapeLike(f.Value)+"%")))
		case "ends":
			where = append(where, c.d.likeText(col, arg("%"+escapeLike(f.Value))))
		case "null":
			where = append(where, col+" IS NULL")
		case "notnull":
			where = append(where, col+" IS NOT NULL")
		default:
			return nil, userErr("unknown filter %q", f.Op)
		}
	}
	tbl := c.d.table(t.Schema, t.Name)
	if s := strings.TrimSpace(q.Search); s != "" {
		pattern := "%" + escapeLike(s) + "%"
		where = append(where, c.d.search(t.Name, t.Columns, func() string { return arg(pattern) }))
	}
	cond := ""
	if len(where) > 0 {
		cond = " WHERE " + strings.Join(where, " AND ")
	}
	sel := "*"
	if t.RowID {
		sel = "rowid AS " + c.d.quote(RowIDColumn) + ", *"
	}
	order := ""
	if q.Sort != "" {
		if !known[q.Sort] && !(t.RowID && q.Sort == RowIDColumn) {
			return nil, userErr("unknown column %q", q.Sort)
		}
		order = " ORDER BY " + c.d.quote(q.Sort)
		if q.Desc {
			order += " DESC"
		}
	} else if key := t.Key(); len(key) > 0 {
		parts := make([]string, len(key))
		for i, k := range key {
			if k == RowIDColumn {
				parts[i] = "rowid"
			} else {
				parts[i] = c.d.quote(k)
			}
		}
		order = " ORDER BY " + strings.Join(parts, ", ")
	}
	if q.Limit <= 0 || q.Limit > 5000 {
		q.Limit = 100
	}
	if q.Offset < 0 {
		q.Offset = 0
	}
	tx, err := c.begin(ctx, true, 0)
	if err != nil {
		return nil, err
	}
	defer tx.Rollback() //nolint:errcheck
	start := time.Now()
	query := fmt.Sprintf("SELECT %s FROM %s%s%s LIMIT %d OFFSET %d", sel, tbl, cond, order, q.Limit, q.Offset)
	rows, err := tx.QueryContext(ctx, query, args...)
	if err != nil {
		return nil, err
	}
	page := &Page{Offset: q.Offset, Limit: q.Limit, Total: -1}
	page.Statement = query
	page.Kind = "read"
	err = scanRows(rows, &page.Result, 0, q.MaxCell)
	rows.Close()
	if err != nil {
		return nil, err
	}
	page.DurationMs = time.Since(start).Milliseconds()
	// the total: exact when it is quick, else the planner's estimate
	if len(page.Rows) < q.Limit && q.Offset == 0 {
		page.Total = int64(len(page.Rows))
	} else {
		cctx, cancel := context.WithTimeout(ctx, 2500*time.Millisecond)
		var n int64
		err := tx.QueryRowContext(cctx, "SELECT COUNT(*) FROM "+tbl+cond, args...).Scan(&n)
		cancel()
		if err == nil {
			page.Total = n
		} else if cond == "" && t.Rows >= 0 {
			page.Total, page.TotalApprox = t.Rows, true
		}
	}
	return page, nil
}

func escapeLike(s string) string {
	return strings.NewReplacer(`\`, `\\`, `%`, `\%`, `_`, `\_`).Replace(s)
}

// RunOptions shape one run of the SQL editor.
type RunOptions struct {
	ReadOnly bool
	MaxRows  int
	MaxCell  int
	Timeout  time.Duration
}

// Run executes a script statement by statement in one transaction: all of
// it happens or none of it (MySQL commits structure changes on its own).
// A read-only run refuses anything but reading before it starts.
func (c *SQL) Run(ctx context.Context, script string, o RunOptions) []Result {
	stmts := Split(script, c.spec.Engine)
	out := make([]Result, len(stmts))
	for i, s := range stmts {
		out[i] = Result{Statement: s, Kind: Classify(s), Rows: [][]any{}, Columns: []string{}, Types: []string{}}
	}
	if len(stmts) == 0 {
		return out
	}
	fail := func(i int, msg string) []Result {
		out[i].Error = msg
		for j := range out {
			if j != i {
				out[j].Skipped = true
			}
		}
		return out
	}
	for i, s := range stmts {
		if TxControl(s) {
			return fail(i, "Transactions are handled for you: everything you run together is one transaction, kept only when every statement works.")
		}
		if firstWord(s) == "use" {
			return fail(i, "Pick the database at the top instead of USE.")
		}
		if o.ReadOnly && out[i].Kind != "read" {
			return fail(i, ErrReadOnly.Error())
		}
	}
	if o.Timeout <= 0 {
		o.Timeout = 30 * time.Second
	}
	if o.MaxRows <= 0 {
		o.MaxRows = 1000
	}
	ctx, cancel := context.WithTimeout(ctx, o.Timeout+2*time.Second)
	defer cancel()
	// a statement that cannot run in a transaction runs alone
	if len(stmts) == 1 && NoTransaction(stmts[0]) && !o.ReadOnly {
		start := time.Now()
		res, err := c.db.ExecContext(ctx, stmts[0])
		out[0].DurationMs = time.Since(start).Milliseconds()
		if err != nil {
			out[0].Error = cleanErr(err)
		} else if n, err := res.RowsAffected(); err == nil {
			out[0].RowsAffected = n
		}
		return out
	}
	for i, s := range stmts {
		if NoTransaction(s) {
			return fail(i, "Run this statement on its own: it cannot run inside a transaction.")
		}
	}
	tx, err := c.begin(ctx, o.ReadOnly, o.Timeout)
	if err != nil {
		return fail(0, cleanErr(err))
	}
	for i, s := range stmts {
		start := time.Now()
		err := c.exec(ctx, tx, s, &out[i], o)
		out[i].DurationMs = time.Since(start).Milliseconds()
		if err != nil {
			_ = tx.Rollback()
			out[i].Error = cleanErr(err)
			for j := i + 1; j < len(out); j++ {
				out[j].Skipped = true
			}
			if i > 0 && !o.ReadOnly {
				out[i].Error += " — nothing before it was kept"
			}
			return out
		}
	}
	if err := tx.Commit(); err != nil {
		out[len(out)-1].Error = "could not save the changes: " + cleanErr(err)
	}
	return out
}

func (c *SQL) exec(ctx context.Context, tx *sql.Tx, s string, r *Result, o RunOptions) error {
	if c.spec.Engine == Postgres && cursorable(s) {
		// a cursor stops the server after the rows we keep
		if _, err := tx.ExecContext(ctx, "DECLARE pocket_q NO SCROLL CURSOR FOR "+s); err != nil {
			return err
		}
		rows, err := tx.QueryContext(ctx, fmt.Sprintf("FETCH FORWARD %d FROM pocket_q", o.MaxRows+1))
		if err != nil {
			return err
		}
		err = scanRows(rows, r, o.MaxRows, o.MaxCell)
		rows.Close()
		if err != nil {
			return err
		}
		_, err = tx.ExecContext(ctx, "CLOSE pocket_q")
		return err
	}
	if Returns(s, r.Kind) {
		rows, err := tx.QueryContext(ctx, s)
		if err != nil {
			return err
		}
		err = scanRows(rows, r, o.MaxRows, o.MaxCell)
		rows.Close()
		if err == nil && r.Kind != "read" {
			r.RowsAffected = int64(len(r.Rows))
		}
		return err
	}
	res, err := tx.ExecContext(ctx, s)
	if err != nil {
		return err
	}
	if n, err := res.RowsAffected(); err == nil {
		r.RowsAffected = n
	}
	return nil
}

// Stream runs one read statement and hands every row to fn (exports).
func (c *SQL) Stream(ctx context.Context, stmt string, maxRows int, fn func(cols []string, row []any) error) error {
	if Classify(stmt) != "read" {
		return userErr("only a query that reads can be exported")
	}
	tx, err := c.begin(ctx, true, 0)
	if err != nil {
		return err
	}
	defer tx.Rollback() //nolint:errcheck
	rows, err := tx.QueryContext(ctx, stmt)
	if err != nil {
		return err
	}
	defer rows.Close()
	cols, _ := rows.Columns()
	types := make([]string, len(cols))
	if ct, err := rows.ColumnTypes(); err == nil {
		for i, x := range ct {
			types[i] = x.DatabaseTypeName()
		}
	}
	vals := make([]any, len(cols))
	ptrs := make([]any, len(cols))
	for i := range vals {
		ptrs[i] = &vals[i]
	}
	n := 0
	for rows.Next() {
		if maxRows > 0 && n >= maxRows {
			break
		}
		if err := rows.Scan(ptrs...); err != nil {
			return err
		}
		row := make([]any, len(cols))
		for i, v := range vals {
			row[i] = cell(v, types[i], 0)
		}
		if err := fn(cols, row); err != nil {
			return err
		}
		n++
	}
	return rows.Err()
}

// SelectAll is the query that reads a whole table (exports).
func (c *SQL) SelectAll(t *TableInfo) string { return "SELECT * FROM " + c.d.table(t.Schema, t.Name) }

// ---- row changes ----

func (c *SQL) writable(t *TableInfo, cols map[string]any) error {
	if c.spec.ReadOnly {
		return ErrReadOnly
	}
	if t.Kind != "table" {
		return userErr("a %s cannot be changed row by row", t.Kind)
	}
	byName := map[string]Column{}
	for _, col := range t.Columns {
		byName[col.Name] = col
	}
	for k := range cols {
		col, ok := byName[k]
		if !ok {
			return userErr("unknown column %q", k)
		}
		if col.Generated {
			return userErr("%q is computed by the database and cannot be written", k)
		}
	}
	return nil
}

// sortedKeys keeps statements stable (and testable).
func sortedKeys(m map[string]any) []string {
	ks := make([]string, 0, len(m))
	for k := range m {
		ks = append(ks, k)
	}
	sort.Strings(ks)
	return ks
}

func (c *SQL) keyCond(t *TableInfo, key map[string]any, args *[]any) (string, error) {
	cols := t.Key()
	if len(cols) == 0 {
		return "", ErrNoKey
	}
	parts := make([]string, 0, len(cols))
	for _, k := range cols {
		v, ok := key[k]
		if !ok {
			return "", userErr("the row's %q is missing", k)
		}
		name := c.d.quote(k)
		if k == RowIDColumn {
			name = "rowid"
		}
		if v == nil {
			parts = append(parts, name+" IS NULL")
			continue
		}
		*args = append(*args, plain(c.spec.Engine, v))
		parts = append(parts, name+" = "+c.d.ph(len(*args)))
	}
	return strings.Join(parts, " AND "), nil
}

// Change is one row change, done in one transaction with the others.
type Change struct {
	Op     string         `json:"op"` // insert | update | delete
	Key    map[string]any `json:"key"`
	Values map[string]any `json:"values"`
}

// Apply makes row changes in one transaction and returns the rows touched
// and the statements run (for the activity log).
func (c *SQL) Apply(ctx context.Context, t *TableInfo, changes []Change) (int64, []string, error) {
	if c.spec.ReadOnly {
		return 0, nil, ErrReadOnly
	}
	tx, err := c.begin(ctx, false, 30*time.Second)
	if err != nil {
		return 0, nil, err
	}
	defer tx.Rollback() //nolint:errcheck
	var total int64
	var stmts []string
	tbl := c.d.table(t.Schema, t.Name)
	for _, ch := range changes {
		if err := c.writable(t, ch.Values); err != nil {
			return 0, nil, err
		}
		var args []any
		var q string
		switch ch.Op {
		case "insert":
			if len(ch.Values) == 0 {
				if c.spec.Engine == MySQL {
					q = "INSERT INTO " + tbl + " () VALUES ()"
				} else {
					q = "INSERT INTO " + tbl + " DEFAULT VALUES"
				}
				break
			}
			cols, phs := []string{}, []string{}
			for _, k := range sortedKeys(ch.Values) {
				args = append(args, plain(c.spec.Engine, ch.Values[k]))
				cols = append(cols, c.d.quote(k))
				phs = append(phs, c.d.ph(len(args)))
			}
			q = "INSERT INTO " + tbl + " (" + strings.Join(cols, ", ") + ") VALUES (" + strings.Join(phs, ", ") + ")"
		case "update":
			if len(ch.Values) == 0 {
				continue
			}
			sets := []string{}
			for _, k := range sortedKeys(ch.Values) {
				args = append(args, plain(c.spec.Engine, ch.Values[k]))
				sets = append(sets, c.d.quote(k)+" = "+c.d.ph(len(args)))
			}
			cond, err := c.keyCond(t, ch.Key, &args)
			if err != nil {
				return 0, nil, err
			}
			q = "UPDATE " + tbl + " SET " + strings.Join(sets, ", ") + " WHERE " + cond
		case "delete":
			cond, err := c.keyCond(t, ch.Key, &args)
			if err != nil {
				return 0, nil, err
			}
			q = "DELETE FROM " + tbl + " WHERE " + cond
		default:
			return 0, nil, userErr("unknown change %q", ch.Op)
		}
		res, err := tx.ExecContext(ctx, q, args...)
		if err != nil {
			return 0, nil, errors.New(cleanErr(err))
		}
		n, _ := res.RowsAffected()
		if ch.Op != "insert" && n > 1 {
			return 0, nil, userErr("that would have changed %d rows instead of one, so nothing was changed", n)
		}
		total += n
		stmts = append(stmts, q)
	}
	if err := tx.Commit(); err != nil {
		return 0, nil, err
	}
	return total, stmts, nil
}

// cleanErr makes a driver error readable.
func cleanErr(err error) string {
	if err == nil {
		return ""
	}
	if errors.Is(err, context.DeadlineExceeded) {
		return "the query took too long and was stopped (Settings → General → Query time limit)"
	}
	if errors.Is(err, context.Canceled) {
		return "stopped"
	}
	s := err.Error()
	for _, p := range []string{"ERROR: ", "sqlite: ", "Error "} {
		s = strings.TrimPrefix(s, p)
	}
	return s
}

// CleanErr is cleanErr for callers outside the package.
func CleanErr(err error) string { return cleanErr(err) }

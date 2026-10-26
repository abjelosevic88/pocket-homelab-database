// Package engine talks to the databases Pocket Database opens: PostgreSQL,
// MySQL / MariaDB and SQLite through one SQL interface, Redis / Valkey
// through its own. Every call takes a context with a deadline; nothing here
// knows about users or settings.
package engine

import (
	"errors"
	"fmt"
	"strings"
	"time"
)

// Engines Pocket Database can open.
const (
	Postgres = "postgres"
	MySQL    = "mysql"
	SQLite   = "sqlite"
	Redis    = "redis"
)

// Supported reports whether the engine can be opened.
func Supported(e string) bool {
	switch e {
	case Postgres, MySQL, SQLite, Redis:
		return true
	}
	return false
}

// IsSQL reports whether the engine speaks SQL.
func IsSQL(e string) bool { return e == Postgres || e == MySQL || e == SQLite }

// DefaultPort is the usual port of an engine.
func DefaultPort(e string) int {
	switch e {
	case Postgres:
		return 5432
	case MySQL:
		return 3306
	case Redis:
		return 6379
	}
	return 0
}

// EngineName is the engine's display name.
func EngineName(e string) string {
	switch e {
	case Postgres:
		return "PostgreSQL"
	case MySQL:
		return "MySQL / MariaDB"
	case SQLite:
		return "SQLite"
	case Redis:
		return "Redis"
	}
	return e
}

// Spec is everything needed to open one connection.
type Spec struct {
	Engine   string
	Host     string
	Port     int
	User     string
	Password string
	Database string // the database to open ("" = the server's default); a Redis db number
	SSLMode  string // disable | prefer | require | verify-full ("" = the engine's default)
	Path     string // SQLite file
	ReadOnly bool
	Timeout  time.Duration // connect timeout
}

// Addr is host:port.
func (s Spec) Addr() string {
	p := s.Port
	if p == 0 {
		p = DefaultPort(s.Engine)
	}
	host := s.Host
	if strings.Contains(host, ":") && !strings.HasPrefix(host, "[") {
		host = "[" + host + "]"
	}
	return fmt.Sprintf("%s:%d", host, p)
}

func (s Spec) timeout() time.Duration {
	if s.Timeout <= 0 {
		return 8 * time.Second
	}
	return s.Timeout
}

// ServerInfo describes the server (or file) behind a connection.
type ServerInfo struct {
	Engine    string            `json:"engine"`
	Version   string            `json:"version"`
	SizeBytes int64             `json:"size_bytes"` // the open database (all of Redis' memory)
	Databases int               `json:"databases"`
	Uptime    string            `json:"uptime"`
	Extra     map[string]string `json:"extra"`
}

// DBInfo is one database on a server.
type DBInfo struct {
	Name      string `json:"name"`
	SizeBytes int64  `json:"size_bytes"` // -1 unknown
	Tables    int    `json:"tables"`     // -1 unknown; Redis: keys
	Current   bool   `json:"current"`
}

// Object is a table, view or materialized view.
type Object struct {
	Schema    string `json:"schema"`
	Name      string `json:"name"`
	Kind      string `json:"kind"` // table | view | matview | foreign
	Rows      int64  `json:"rows"` // an estimate; -1 unknown
	SizeBytes int64  `json:"size_bytes"`
	Comment   string `json:"comment,omitempty"`
}

// Schema groups objects (SQLite and MySQL have one per database).
type Schema struct {
	Name    string   `json:"name"`
	Objects []Object `json:"objects"`
}

// Column is one column of a table.
type Column struct {
	Name      string  `json:"name"`
	Type      string  `json:"type"`
	Nullable  bool    `json:"nullable"`
	Default   *string `json:"default"`
	PK        bool    `json:"pk"`
	Position  int     `json:"position"`
	Comment   string  `json:"comment,omitempty"`
	Auto      bool    `json:"auto"`      // filled by the database (serial, identity, auto_increment)
	Generated bool    `json:"generated"` // computed, cannot be written
}

// Index is one index of a table.
type Index struct {
	Name       string   `json:"name"`
	Columns    []string `json:"columns"`
	Unique     bool     `json:"unique"`
	Primary    bool     `json:"primary"`
	Definition string   `json:"definition,omitempty"`
}

// ForeignKey links columns to another table.
type ForeignKey struct {
	Name       string   `json:"name"`
	Schema     string   `json:"schema,omitempty"` // the referencing table (incoming keys)
	Table      string   `json:"table,omitempty"`
	Columns    []string `json:"columns"`
	RefSchema  string   `json:"ref_schema"`
	RefTable   string   `json:"ref_table"`
	RefColumns []string `json:"ref_columns"`
}

// TableInfo is the structure of one table or view.
type TableInfo struct {
	Schema       string       `json:"schema"`
	Name         string       `json:"name"`
	Kind         string       `json:"kind"`
	Columns      []Column     `json:"columns"`
	PrimaryKey   []string     `json:"primary_key"`
	Indexes      []Index      `json:"indexes"`
	ForeignKeys  []ForeignKey `json:"foreign_keys"`
	ReferencedBy []ForeignKey `json:"referenced_by"`
	DDL          string       `json:"ddl"`
	Rows         int64        `json:"rows"`
	SizeBytes    int64        `json:"size_bytes"`
	RowID        bool         `json:"rowid"` // SQLite: rows are keyed by rowid (no primary key)
	Comment      string       `json:"comment,omitempty"`
}

// Key is the column list that identifies a row ("" = not editable).
func (t *TableInfo) Key() []string {
	if t.RowID {
		return []string{RowIDColumn}
	}
	return t.PrimaryKey
}

// RowIDColumn is the extra column SQLite tables without a primary key get.
const RowIDColumn = "__rowid"

// Result is the outcome of one statement.
type Result struct {
	Statement    string   `json:"statement"`
	Kind         string   `json:"kind"` // read | write | ddl | other
	Columns      []string `json:"columns"`
	Types        []string `json:"types"`
	Rows         [][]any  `json:"rows"`
	HasRows      bool     `json:"has_rows"`
	RowsAffected int64    `json:"rows_affected"`
	Truncated    bool     `json:"truncated"`
	DurationMs   int64    `json:"duration_ms"`
	Error        string   `json:"error,omitempty"`
	Skipped      bool     `json:"skipped,omitempty"`
}

// Filter narrows rows: column op value.
type Filter struct {
	Column string `json:"column"`
	Op     string `json:"op"` // eq ne lt le gt ge contains starts ends null notnull
	Value  string `json:"value"`
}

// RowsQuery is a page of a table.
type RowsQuery struct {
	Filters []Filter
	Search  string
	Sort    string
	Desc    bool
	Limit   int
	Offset  int
	MaxCell int // characters kept per text value
}

// Page is a page of table rows.
type Page struct {
	Result
	Total       int64 `json:"total"` // -1 unknown
	TotalApprox bool  `json:"total_approx"`
	Offset      int   `json:"offset"`
	Limit       int   `json:"limit"`
}

// ErrReadOnly is returned when a change is asked of a read-only connection.
var ErrReadOnly = errors.New("this connection is read-only: switch on “Allow changes” for it first")

// ErrNoKey is returned when a row cannot be identified.
var ErrNoKey = errors.New("this table has no primary key, so single rows cannot be changed here; use SQL")

// UserError is a mistake in what was asked (an unknown column, a bad filter).
type UserError struct{ Msg string }

func (e *UserError) Error() string { return e.Msg }

func userErr(format string, a ...any) error { return &UserError{fmt.Sprintf(format, a...)} }

// IsUserError reports whether err is a UserError.
func IsUserError(err error) bool {
	var u *UserError
	return errors.As(err, &u)
}

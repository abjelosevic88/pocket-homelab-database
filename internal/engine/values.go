package engine

import (
	"database/sql"
	"encoding/hex"
	"fmt"
	"math"
	"strconv"
	"strings"
	"time"
	"unicode/utf8"
)

// Binary is how a value that is not text reaches the browser.
type Binary struct {
	Bin  string `json:"$bin"` // hex of the first bytes
	Size int    `json:"size"`
}

// Long is a text value cut short for the grid.
type Long struct {
	Text string `json:"$trunc"`
	Size int    `json:"size"`
}

// cell turns a scanned value into something JSON carries without loss.
func cell(v any, dbType string, maxCell int) any {
	switch x := v.(type) {
	case nil:
		return nil
	case []byte:
		if utf8.Valid(x) && !looksBinary(dbType) {
			return text(string(x), maxCell)
		}
		n := len(x)
		if n > 32 {
			x = x[:32]
		}
		return Binary{Bin: hex.EncodeToString(x), Size: n}
	case string:
		return text(x, maxCell)
	case int64:
		if x > 1<<53 || x < -(1<<53) {
			return strconv.FormatInt(x, 10)
		}
		return x
	case int32, int16, int8, int, uint8, uint16, uint32:
		return x
	case uint64:
		if x > 1<<53 {
			return strconv.FormatUint(x, 10)
		}
		return x
	case float64:
		if math.IsNaN(x) || math.IsInf(x, 0) {
			return fmt.Sprint(x)
		}
		return x
	case float32:
		return float64(x)
	case bool:
		return x
	case time.Time:
		return fmtTime(x, dbType)
	}
	return text(fmt.Sprint(v), maxCell)
}

func text(s string, maxCell int) any {
	if maxCell > 0 && len(s) > maxCell {
		cut := maxCell
		for cut > 0 && !utf8.RuneStart(s[cut]) {
			cut--
		}
		return Long{Text: s[:cut], Size: len(s)}
	}
	return s
}

func looksBinary(t string) bool {
	t = strings.ToUpper(t)
	return t == "BYTEA" || strings.Contains(t, "BLOB") || strings.Contains(t, "BINARY") || t == "BIT"
}

func fmtTime(t time.Time, dbType string) string {
	switch strings.ToUpper(dbType) {
	case "DATE":
		return t.Format("2006-01-02")
	case "TIME":
		return t.Format("15:04:05.999999")
	case "TIMESTAMP", "DATETIME":
		return t.Format("2006-01-02 15:04:05.999999")
	}
	return t.Format("2006-01-02 15:04:05.999999-07:00")
}

// scanRows reads up to max rows (max <= 0: all) into a Result.
func scanRows(rows *sql.Rows, res *Result, max, maxCell int) error {
	cols, err := rows.Columns()
	if err != nil {
		return err
	}
	types := make([]string, len(cols))
	if ct, err := rows.ColumnTypes(); err == nil {
		for i, c := range ct {
			types[i] = c.DatabaseTypeName()
		}
	}
	res.Columns, res.Types, res.HasRows = cols, types, true
	res.Rows = [][]any{}
	vals := make([]any, len(cols))
	ptrs := make([]any, len(cols))
	for i := range vals {
		ptrs[i] = &vals[i]
	}
	for rows.Next() {
		if max > 0 && len(res.Rows) >= max {
			res.Truncated = true
			break
		}
		if err := rows.Scan(ptrs...); err != nil {
			return err
		}
		row := make([]any, len(cols))
		for i, v := range vals {
			row[i] = cell(v, types[i], maxCell)
		}
		res.Rows = append(res.Rows, row)
	}
	if res.Truncated {
		return nil
	}
	return rows.Err()
}

// plain turns a JSON value from the browser into a query argument: text
// stays text and the database converts it to the column's type.
func plain(engine string, v any) any {
	switch x := v.(type) {
	case nil:
		return nil
	case string:
		return x
	case bool:
		if engine == Postgres {
			return strconv.FormatBool(x)
		}
		if x {
			return int64(1)
		}
		return int64(0)
	case float64:
		if x == math.Trunc(x) && math.Abs(x) < 1<<53 {
			return int64(x)
		}
		return x
	case map[string]any:
		if s, ok := x["$trunc"].(string); ok {
			return s
		}
	}
	return fmt.Sprint(v)
}

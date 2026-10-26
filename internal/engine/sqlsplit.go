package engine

import (
	"regexp"
	"strings"
	"unicode"
)

// Split cuts a script into statements at semicolons that are not inside a
// string, a quoted name, a comment, a dollar-quoted body (PostgreSQL) or a
// SQLite trigger body. Empty statements are dropped; comments stay.
func Split(script, engine string) []string {
	var out []string
	var cur strings.Builder
	rs := []rune(script)
	n := len(rs)
	flush := func() {
		s := strings.TrimSpace(cur.String())
		if strip(s) != "" {
			out = append(out, s)
		}
		cur.Reset()
	}
	for i := 0; i < n; i++ {
		c := rs[i]
		switch {
		case c == '\'' || c == '"' || (c == '`' && engine == MySQL):
			j := i + 1
			for j < n {
				if rs[j] == '\\' && engine == MySQL && c != '`' {
					j += 2
					continue
				}
				if rs[j] == c {
					if j+1 < n && rs[j+1] == c {
						j += 2
						continue
					}
					break
				}
				j++
			}
			if j >= n {
				j = n - 1
			}
			cur.WriteString(string(rs[i : j+1]))
			i = j
		case c == '-' && i+1 < n && rs[i+1] == '-', c == '#' && engine == MySQL:
			j := i
			for j < n && rs[j] != '\n' {
				j++
			}
			cur.WriteString(string(rs[i:j]))
			i = j - 1
		case c == '/' && i+1 < n && rs[i+1] == '*':
			j := i + 2
			for j+1 < n && !(rs[j] == '*' && rs[j+1] == '/') {
				j++
			}
			end := j + 2
			if end > n {
				end = n
			}
			cur.WriteString(string(rs[i:end]))
			i = end - 1
		case c == '$' && engine == Postgres:
			// $tag$ … $tag$ (tag may be empty); a $1 parameter is not one
			j := i + 1
			for j < n && (rs[j] == '_' || unicode.IsLetter(rs[j]) || (j > i+1 && unicode.IsDigit(rs[j]))) {
				j++
			}
			if j < n && rs[j] == '$' {
				tag := string(rs[i : j+1])
				rest := string(rs[j+1:])
				if k := strings.Index(rest, tag); k >= 0 {
					body := string(rs[i:j+1]) + rest[:k] + tag
					cur.WriteString(body)
					i += len([]rune(body)) - 1
					continue
				}
			}
			cur.WriteRune(c)
		case c == ';':
			if engine == SQLite && inTrigger(cur.String()) {
				cur.WriteRune(c)
				continue
			}
			flush()
		default:
			cur.WriteRune(c)
		}
	}
	flush()
	return out
}

var triggerStart = regexp.MustCompile(`(?is)^\s*create\s+(temp\s+|temporary\s+)?trigger\b`)
var endWord = regexp.MustCompile(`(?is)\bend\s*$`)

// inTrigger: a SQLite CREATE TRIGGER body ends at "END;".
func inTrigger(sofar string) bool {
	s := strip(sofar)
	return triggerStart.MatchString(s) && !endWord.MatchString(s)
}

var commentRe = regexp.MustCompile(`(?s)/\*.*?\*/|--[^\n]*`)
var stringRe = regexp.MustCompile(`'(?:[^']|'')*'`)

// strip removes comments and surrounding space.
func strip(s string) string { return strings.TrimSpace(commentRe.ReplaceAllString(s, " ")) }

// bare is the statement without comments and string contents, lower case,
// for keyword checks.
func bare(s string) string {
	return strings.ToLower(stringRe.ReplaceAllString(strip(s), "''"))
}

// firstWord is the statement's first keyword, lower case.
func firstWord(s string) string {
	b := strings.TrimLeft(bare(s), "( \t\r\n")
	end := strings.IndexFunc(b, func(r rune) bool { return !unicode.IsLetter(r) && r != '_' })
	if end < 0 {
		return b
	}
	return b[:end]
}

var writeWords = regexp.MustCompile(`\b(insert|update|delete|merge|upsert)\b`)
var intoRe = regexp.MustCompile(`\binto\b`)

// Classify says what a statement does: read, write (rows), ddl (structure)
// or other (session and transaction control).
func Classify(stmt string) string {
	w := firstWord(stmt)
	b := bare(stmt)
	switch w {
	case "select":
		if intoRe.MatchString(b) {
			return "write" // SELECT … INTO a new table, a file or a variable
		}
		return "read"
	case "explain":
		if regexp.MustCompile(`\banaly[sz]e\b`).MatchString(b) && writeWords.MatchString(b) {
			return "write" // EXPLAIN ANALYZE runs the statement
		}
		return "read"
	case "show", "describe", "desc", "values", "table", "help":
		return "read"
	case "set", "reset":
		if regexp.MustCompile(`\b(global|persist|persist_only|password|role|session authorization)\b`).MatchString(b) && !strings.HasPrefix(b, "set local") {
			return "ddl" // the server's settings, a password or who you are
		}
		return "read" // this session's settings only
	case "with":
		if writeWords.MatchString(b) {
			return "write"
		}
		return "read"
	case "pragma":
		if strings.Contains(b, "=") {
			return "write" // PRAGMA x = y changes the file or the connection
		}
		return "read"
	case "insert", "update", "delete", "merge", "replace", "upsert", "copy", "call", "do", "lock", "load", "handler", "exec", "execute":
		return "write"
	case "create", "alter", "drop", "truncate", "rename", "comment", "grant", "revoke", "reindex", "vacuum", "analyze", "analyse", "cluster", "refresh", "attach", "detach", "optimize", "repair", "flush", "security", "import":
		return "ddl"
	}
	return "other"
}

// TxControl reports a statement that would fight the transaction the app
// runs everything in.
func TxControl(stmt string) bool {
	switch firstWord(stmt) {
	case "begin", "commit", "rollback", "start", "end", "savepoint", "release", "abort":
		return !triggerStart.MatchString(strip(stmt))
	}
	return false
}

// NoTransaction reports a statement that cannot run inside a transaction.
func NoTransaction(stmt string) bool {
	b := bare(stmt)
	switch firstWord(stmt) {
	case "vacuum":
		return true
	case "create", "drop", "alter", "reindex":
		return regexp.MustCompile(`^(create|drop)\s+(database|tablespace)\b|\bconcurrently\b|^alter\s+system\b|^reindex\s+(database|system)\b`).MatchString(b)
	}
	return false
}

var whereRe = regexp.MustCompile(`\bwhere\b`)

// Destructive explains why a statement deserves a second look ("" = it does not).
func Destructive(stmt string) string {
	b := bare(stmt)
	switch firstWord(stmt) {
	case "drop":
		return "it deletes " + dropWhat(b) + " for good"
	case "truncate":
		return "it empties the whole table"
	case "delete":
		if !whereRe.MatchString(b) {
			return "it deletes every row (there is no WHERE)"
		}
	case "update":
		if !whereRe.MatchString(b) {
			return "it changes every row (there is no WHERE)"
		}
	case "alter":
		if regexp.MustCompile(`\bdrop\b`).MatchString(b) {
			return "it removes part of a table"
		}
	}
	return ""
}

func dropWhat(b string) string {
	m := regexp.MustCompile(`^drop\s+(table|view|materialized view|database|schema|index|sequence|function|trigger|type|extension|user|role)\b`).FindStringSubmatch(b)
	if m == nil {
		return "something"
	}
	switch m[1] {
	case "database":
		return "a whole database"
	case "schema":
		return "a schema and everything in it"
	case "table":
		return "a table and its rows"
	}
	return "a " + m[1]
}

// Verb is the statement's first keyword in upper case, for the activity log.
func Verb(stmt string) string { return strings.ToUpper(firstWord(stmt)) }

// Returns reports whether a statement gives rows back.
func Returns(stmt, kind string) bool {
	if kind == "read" {
		return true
	}
	b := bare(stmt)
	return regexp.MustCompile(`\breturning\b`).MatchString(b) || (firstWord(stmt) == "pragma") || (firstWord(stmt) == "call") || strings.HasPrefix(b, "with") && regexp.MustCompile(`\)\s*select\b`).MatchString(b)
}

// cursorable: a plain query PostgreSQL can page through with a cursor.
func cursorable(stmt string) bool {
	switch firstWord(stmt) {
	case "select", "values", "table":
		return !regexp.MustCompile(`\binto\b|\bfor\s+(update|share|no key update|key share)\b`).MatchString(bare(stmt))
	case "with":
		return Classify(stmt) == "read"
	}
	return false
}

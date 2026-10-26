package discover

import (
	"context"
	"io/fs"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"time"
)

// Found is one database discovery turned up.
type Found struct {
	Key           string   `json:"key"` // stable: docker:<container> or file:<host path>
	Engine        string   `json:"engine"`
	Supported     bool     `json:"supported"`
	Name          string   `json:"name"`
	Group         string   `json:"group"`
	Container     string   `json:"container,omitempty"`
	Image         string   `json:"image,omitempty"`
	Running       bool     `json:"running"`
	Host          string   `json:"host,omitempty"`
	Port          int      `json:"port,omitempty"`
	User          string   `json:"user,omitempty"`
	Password      string   `json:"-"`
	HasPassword   bool     `json:"has_password"`
	Database      string   `json:"database,omitempty"`
	NeedsPassword bool     `json:"needs_password"`
	Path          string   `json:"path,omitempty"`      // as this app sees the file
	HostPath      string   `json:"host_path,omitempty"` // as the server sees it
	SizeBytes     int64    `json:"size_bytes"`
	Modified      string   `json:"modified,omitempty"`
	Owners        []string `json:"owners,omitempty"` // containers using the file
	Backup        bool     `json:"backup"`           // looks like a backup copy
	Note          string   `json:"note,omitempty"`
}

// Options shape a scan.
type Options struct {
	HostRoot   string   // where the server's file system appears inside this app ("" = same paths)
	ExtraPaths []string // more folders (server paths) to look for SQLite files in
	Skip       []string // containers to leave out (this app itself)
	SkipPaths  []string // files and folders to leave out (this app's data)
	MaxDepth   int
	NoFiles    bool
}

// Local maps a server path to this app's view of it.
func (o Options) Local(p string) string {
	if o.HostRoot == "" {
		return p
	}
	return filepath.Join(o.HostRoot, p)
}

// Server maps this app's view of a path back to the server's.
func (o Options) Server(p string) string {
	if o.HostRoot == "" {
		return p
	}
	if rel, err := filepath.Rel(o.HostRoot, p); err == nil && !strings.HasPrefix(rel, "..") {
		return "/" + rel
	}
	return p
}

var images = []struct {
	re     *regexp.Regexp
	engine string
	ok     bool
}{
	{regexp.MustCompile(`(?i)(^|/)(postgres|postgresql|postgis|timescaledb|pgvecto-rs|vectorchord|pgvector|supabase/postgres|cloudnative-pg|postgres-documentdb)([:@-]|$)|immich-app/postgres|bitnami/postgresql`), "postgres", true},
	{regexp.MustCompile(`(?i)(^|/)(mariadb|mysql|percona|percona-server)([:@]|$)|bitnami/(mariadb|mysql)|linuxserver/mariadb`), "mysql", true},
	{regexp.MustCompile(`(?i)(^|/)(redis|valkey|keydb|redis-stack-server|redis-stack|dragonfly)([:@]|$)|bitnami/(redis|valkey)|dragonflydb`), "redis", true},
	{regexp.MustCompile(`(?i)(^|/)(mongo|mongodb|mongodb-community-server)([:@]|$)|ferretdb/ferretdb`), "mongodb", false},
	{regexp.MustCompile(`(?i)elasticsearch|opensearchproject/opensearch`), "elasticsearch", false},
	{regexp.MustCompile(`(?i)clickhouse`), "clickhouse", false},
	{regexp.MustCompile(`(?i)(^|/)influxdb([:@]|$)`), "influxdb", false},
	{regexp.MustCompile(`(?i)(^|/)(couchdb|cassandra|neo4j|surrealdb|cockroach)([:@]|$)`), "other", false},
}

// Engine says which database an image runs ("" = none we know).
func Engine(image string) (engine string, supported bool) {
	for _, im := range images {
		if im.re.MatchString(image) {
			return im.engine, im.ok
		}
	}
	return "", false
}

func defaultPort(engine string) int {
	switch engine {
	case "postgres":
		return 5432
	case "mysql":
		return 3306
	case "redis":
		return 6379
	case "mongodb":
		return 27017
	case "elasticsearch":
		return 9200
	case "clickhouse":
		return 8123
	case "influxdb":
		return 8086
	}
	return 0
}

var requirepass = regexp.MustCompile(`--requirepass[= ]+("[^"]*"|'[^']*'|\S+)`)

// credentials reads the user, password and database from a database
// container's environment (and, for Redis, its command line).
func credentials(engine string, c *Container) (user, pass, db string, needs bool, note string) {
	env := c.Env
	get := func(keys ...string) string {
		for _, k := range keys {
			if v := env[k]; v != "" {
				return v
			}
		}
		return ""
	}
	fileHint := func(keys ...string) bool {
		for _, k := range keys {
			if env[k+"_FILE"] != "" {
				return true
			}
		}
		return false
	}
	switch engine {
	case "postgres":
		user = firstNonEmpty(get("POSTGRES_USER", "POSTGRESQL_USERNAME", "PGUSER"), "postgres")
		pass = get("POSTGRES_PASSWORD", "POSTGRESQL_PASSWORD", "PGPASSWORD")
		db = firstNonEmpty(get("POSTGRES_DB", "POSTGRESQL_DATABASE", "PGDATABASE"), user)
		trust := strings.EqualFold(env["POSTGRES_HOST_AUTH_METHOD"], "trust")
		needs = pass == "" && !trust
		if needs && fileHint("POSTGRES_PASSWORD") {
			note = "The password is in a Docker secret file; type it in."
		}
	case "mysql":
		if p := get("MARIADB_ROOT_PASSWORD", "MYSQL_ROOT_PASSWORD"); p != "" {
			user, pass = "root", p
		} else if u := get("MARIADB_USER", "MYSQL_USER"); u != "" {
			user, pass = u, get("MARIADB_PASSWORD", "MYSQL_PASSWORD")
		} else {
			user = "root"
		}
		db = get("MARIADB_DATABASE", "MYSQL_DATABASE")
		empty := get("MARIADB_ALLOW_EMPTY_ROOT_PASSWORD", "MYSQL_ALLOW_EMPTY_PASSWORD", "ALLOW_EMPTY_PASSWORD") != ""
		needs = pass == "" && !empty
		if needs && fileHint("MARIADB_ROOT_PASSWORD", "MYSQL_ROOT_PASSWORD", "MARIADB_PASSWORD", "MYSQL_PASSWORD") {
			note = "The password is in a Docker secret file; type it in."
		}
	case "redis":
		pass = get("REDIS_PASSWORD", "VALKEY_PASSWORD", "KEYDB_PASSWORD")
		if pass == "" {
			if m := requirepass.FindStringSubmatch(strings.Join(c.Cmd, " ")); m != nil {
				pass = strings.Trim(m[1], `"'`)
			}
		}
		if u := get("REDIS_USERNAME"); u != "" {
			user = u
		}
		db = "0"
	}
	return
}

// port is the port the database listens on inside its container.
func port(engine string, c *Container) int {
	if engine == "postgres" {
		if p, err := strconv.Atoi(c.Env["PGPORT"]); err == nil && p > 0 {
			return p
		}
	}
	if engine == "mysql" {
		if p, err := strconv.Atoi(c.Env["MYSQL_TCP_PORT"]); err == nil && p > 0 {
			return p
		}
	}
	def := defaultPort(engine)
	for _, p := range c.Ports {
		if p == def {
			return p
		}
	}
	if len(c.Ports) == 1 {
		return c.Ports[0]
	}
	return def
}

func group(c Container) string {
	if c.Project != "" {
		return c.Project
	}
	return c.Name
}

// Scan finds database containers and SQLite files.
func Scan(ctx context.Context, d *Docker, o Options) ([]Found, error) {
	if o.MaxDepth <= 0 {
		o.MaxDepth = 4
	}
	skip := map[string]bool{}
	for _, s := range o.Skip {
		skip[s] = true
	}
	var out []Found
	var containers []Container
	if d.Available() {
		list, err := d.Containers(ctx)
		if err != nil {
			return nil, err
		}
		for _, c := range list {
			if skip[c.Name] {
				continue
			}
			containers = append(containers, c)
			engine, ok := Engine(c.Image)
			if engine == "" {
				continue
			}
			full, err := d.Inspect(ctx, c.Name)
			if err != nil {
				continue
			}
			f := Found{Key: "docker:" + c.Name, Engine: engine, Supported: ok, Name: c.Name, Group: group(c), Container: c.Name,
				Image: c.Image, Running: full.Running}
			f.Port = port(engine, full)
			f.Host, f.Port = full.Address(f.Port)
			if ok {
				f.User, f.Password, f.Database, f.NeedsPassword, f.Note = credentials(engine, full)
				f.HasPassword = f.Password != ""
			} else {
				f.Note = "Pocket Database cannot open " + engine + " yet."
			}
			if !full.Running {
				f.Note = strings.TrimSpace("The container is stopped. " + f.Note)
			}
			out = append(out, f)
		}
	}
	if !o.NoFiles {
		out = append(out, scanFiles(ctx, containers, o)...)
	}
	sort.SliceStable(out, func(i, j int) bool {
		if out[i].Group != out[j].Group {
			return out[i].Group < out[j].Group
		}
		return out[i].Name < out[j].Name
	})
	return out, nil
}

var sqliteExt = map[string]bool{".db": true, ".sqlite": true, ".sqlite3": true, ".db3": true, ".s3db": true, ".sl3": true}
var skipDirs = map[string]bool{"node_modules": true, ".git": true, "cache": true, "Cache": true, ".cache": true, "logs": true, "log": true, "tmp": true,
	"transcodes": true, "thumbs": true, "thumbnails": true, "metadata": true, "@eaDir": true, "lost+found": true, "proc": true, "sys": true, "dev": true}
var backupRe = regexp.MustCompile(`(?i)(^|/)(backups?|snapshots?|bak)[^/]*(/|$)|\.bak\b|(^|[/._-])pre-[a-z0-9.-]+|\.backup\b|-\d{8}-\d{6}\.db$`)
var mediaRe = regexp.MustCompile(`(?i)/(media|music|movies|tv|photos|pictures|videos|downloads|torrents|books|audiobooks|comics|podcasts|library)(/|$)`)

// scanFiles looks for SQLite files in the folders containers mount and in the extra paths.
func scanFiles(ctx context.Context, containers []Container, o Options) []Found {
	type root struct {
		server, group string
	}
	var roots []root
	owners := map[string][]string{} // server dir → running containers mounting it
	seenRoot := map[string]bool{}
	for _, c := range containers {
		for _, m := range c.Mounts {
			if m.Type != "bind" || broad(m.Source) {
				continue
			}
			if c.Running {
				owners[m.Source] = appendUnique(owners[m.Source], c.Name)
			}
			if seenRoot[m.Source] || mediaRe.MatchString(m.Source) || strings.HasPrefix(m.Source, "/var/run") || strings.HasPrefix(m.Source, "/run") ||
				strings.HasPrefix(m.Source, "/usr") || strings.HasPrefix(m.Source, "/etc") || strings.HasPrefix(m.Source, "/proc") || strings.HasPrefix(m.Source, "/sys") || strings.HasPrefix(m.Source, "/dev") {
				continue
			}
			seenRoot[m.Source] = true
			roots = append(roots, root{m.Source, group(c)})
		}
	}
	for _, p := range o.ExtraPaths {
		if p = strings.TrimSpace(p); p != "" && !seenRoot[p] {
			seenRoot[p] = true
			roots = append(roots, root{p, filepath.Base(filepath.Dir(p))})
		}
	}
	skipPath := func(local string) bool {
		for _, s := range o.SkipPaths {
			if s != "" && (local == s || strings.HasPrefix(local, strings.TrimRight(s, "/")+"/")) {
				return true
			}
		}
		return false
	}
	seen := map[string]bool{}
	var out []Found
	budget := 60000
	deadline := time.Now().Add(20 * time.Second)
	for _, r := range roots {
		if ctx.Err() != nil || time.Now().After(deadline) {
			break
		}
		local := o.Local(r.server)
		fi, err := os.Stat(local)
		if err != nil {
			continue
		}
		visit := func(path string, size int64, mod time.Time) {
			if seen[path] || skipPath(path) || !sqliteExt[strings.ToLower(filepath.Ext(path))] {
				return
			}
			seen[path] = true
			if !isSQLite(path) {
				return
			}
			server := o.Server(path)
			f := Found{Key: "file:" + server, Engine: "sqlite", Supported: true, Name: filepath.Base(path), Group: r.group,
				Path: path, HostPath: server, SizeBytes: size, Modified: mod.UTC().Format(time.RFC3339), Running: true}
			f.Backup = backupRe.MatchString(server)
			f.Owners = Owners(containers, server)
			out = append(out, f)
		}
		if !fi.IsDir() {
			visit(local, fi.Size(), fi.ModTime())
			continue
		}
		perRoot := 8000
		base := strings.Count(local, string(os.PathSeparator))
		_ = filepath.WalkDir(local, func(p string, e fs.DirEntry, err error) error {
			if err != nil {
				if e != nil && e.IsDir() {
					return fs.SkipDir
				}
				return nil
			}
			budget--
			perRoot--
			if budget <= 0 || perRoot <= 0 || time.Now().After(deadline) {
				return fs.SkipAll
			}
			if e.IsDir() {
				if p != local && (skipDirs[e.Name()] || strings.Count(p, string(os.PathSeparator))-base >= o.MaxDepth || skipPath(p) || mediaRe.MatchString(o.Server(p))) {
					return fs.SkipDir
				}
				return nil
			}
			if !e.Type().IsRegular() {
				return nil
			}
			if info, err := e.Info(); err == nil {
				visit(p, info.Size(), info.ModTime())
			}
			return nil
		})
	}
	return out
}

var broadRe = regexp.MustCompile(`^/((home|root|mnt|media|srv|opt|var|data)(/[^/]+)?)?/?$`)

// broad is a mount of a whole home folder or disk (a tool that sees
// everything): not a sign that the container uses a file under it.
func broad(src string) bool { return src == "" || broadRe.MatchString(src) }

func isSQLite(path string) bool {
	f, err := os.Open(path)
	if err != nil {
		return false
	}
	defer f.Close()
	b := make([]byte, 16)
	n, _ := f.Read(b)
	return n == 16 && string(b) == "SQLite format 3\x00"
}

// Owners lists the running containers that mount the folder holding a server path.
func Owners(containers []Container, serverPath string) []string {
	var out []string
	for _, c := range containers {
		if !c.Running {
			continue
		}
		for _, m := range c.Mounts {
			if m.Type != "bind" || broad(m.Source) {
				continue
			}
			src := strings.TrimRight(m.Source, "/")
			if serverPath == src || strings.HasPrefix(serverPath, src+"/") {
				out = appendUnique(out, c.Name)
				break
			}
		}
	}
	sort.Strings(out)
	return out
}

// InUse reports signs that a program has a SQLite file open right now: a
// write-ahead log or shared-memory file next to it.
func InUse(path string) bool {
	for _, suffix := range []string{"-wal", "-shm", "-journal"} {
		if fi, err := os.Stat(path + suffix); err == nil && (fi.Size() > 0 || suffix == "-shm") {
			return true
		}
	}
	return false
}

func appendUnique(list []string, s string) []string {
	for _, x := range list {
		if x == s {
			return list
		}
	}
	return append(list, s)
}

func firstNonEmpty(vals ...string) string {
	for _, v := range vals {
		if v != "" {
			return v
		}
	}
	return ""
}

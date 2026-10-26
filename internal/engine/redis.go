package engine

import (
	"context"
	"crypto/tls"
	"errors"
	"fmt"
	"sort"
	"strconv"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/redis/go-redis/v9"
)

// KV is an open Redis / Valkey database.
type KV struct {
	c    *redis.Client
	spec Spec
	db   int
}

// OpenRedis opens a Redis connection (lazily: Ping to check it).
func OpenRedis(spec Spec) (*KV, error) {
	db := 0
	if spec.Database != "" {
		n, err := strconv.Atoi(spec.Database)
		if err != nil || n < 0 || n > 1000 {
			return nil, userErr("a Redis database is a number (0, 1, 2…)")
		}
		db = n
	}
	o := &redis.Options{Addr: spec.Addr(), Username: spec.User, Password: spec.Password, DB: db,
		DialTimeout: spec.timeout(), ReadTimeout: 20 * time.Second, WriteTimeout: 20 * time.Second, PoolSize: 4,
		DisableIdentity: true, Protocol: 2}
	switch spec.SSLMode {
	case "require":
		o.TLSConfig = &tls.Config{InsecureSkipVerify: true} //nolint:gosec // "require" means encrypt without checking, like libpq
	case "verify-full":
		o.TLSConfig = &tls.Config{ServerName: spec.Host}
	}
	return &KV{c: redis.NewClient(o), spec: spec, db: db}, nil
}

// Close closes the pool.
func (k *KV) Close() error { return k.c.Close() }

// Spec is what the connection was opened with.
func (k *KV) Spec() Spec { return k.spec }

// Ping checks the connection.
func (k *KV) Ping(ctx context.Context) error { return k.c.Ping(ctx).Err() }

func parseInfo(s string) map[string]string {
	out := map[string]string{}
	for _, l := range strings.Split(s, "\n") {
		l = strings.TrimSpace(l)
		if l == "" || l[0] == '#' {
			continue
		}
		if k, v, ok := strings.Cut(l, ":"); ok {
			out[k] = v
		}
	}
	return out
}

// Server describes the server.
func (k *KV) Server(ctx context.Context) (ServerInfo, error) {
	si := ServerInfo{Engine: Redis, Extra: map[string]string{}}
	raw, err := k.c.Info(ctx, "server", "memory", "clients", "keyspace", "replication", "persistence").Result()
	if err != nil {
		return si, err
	}
	in := parseInfo(raw)
	si.Version = firstNonEmpty(in["valkey_version"], in["redis_version"])
	if in["valkey_version"] != "" {
		si.Extra["flavor"] = "Valkey"
	} else {
		si.Extra["flavor"] = "Redis"
	}
	si.SizeBytes, _ = strconv.ParseInt(in["used_memory"], 10, 64)
	if s, err := strconv.Atoi(in["uptime_in_seconds"]); err == nil {
		si.Uptime = (time.Duration(s) * time.Second).String()
	}
	for _, key := range []string{"redis_mode", "role", "connected_clients", "used_memory_human", "maxmemory_human", "maxmemory_policy", "aof_enabled", "rdb_last_save_time"} {
		if v := in[key]; v != "" {
			si.Extra[key] = v
		}
	}
	dbs, _ := k.Databases(ctx)
	si.Databases = len(dbs)
	return si, nil
}

// Databases lists the numbered databases (the keys each holds in Tables).
func (k *KV) Databases(ctx context.Context) ([]DBInfo, error) {
	raw, err := k.c.Info(ctx, "keyspace").Result()
	if err != nil {
		return nil, err
	}
	keys := map[int]int{}
	for name, v := range parseInfo(raw) {
		n, err := strconv.Atoi(strings.TrimPrefix(name, "db"))
		if err != nil {
			continue
		}
		for _, part := range strings.Split(v, ",") {
			if strings.HasPrefix(part, "keys=") {
				keys[n], _ = strconv.Atoi(strings.TrimPrefix(part, "keys="))
			}
		}
	}
	count := 16
	if cfg, err := k.c.ConfigGet(ctx, "databases").Result(); err == nil {
		if n, err := strconv.Atoi(cfg["databases"]); err == nil && n > 0 && n <= 256 {
			count = n
		}
	}
	out := make([]DBInfo, 0, count)
	for i := 0; i < count; i++ {
		out = append(out, DBInfo{Name: strconv.Itoa(i), Tables: keys[i], SizeBytes: -1, Current: i == k.db})
	}
	return out, nil
}

// KeyInfo is one key in a listing.
type KeyInfo struct {
	Key  string `json:"key"`
	Type string `json:"type"`
	TTL  int64  `json:"ttl_ms"` // -1 never expires
}

// Scan lists keys matching pattern, count at a time; cursor 0 starts over
// and a returned cursor of 0 means the end.
func (k *KV) Scan(ctx context.Context, pattern string, cursor uint64, count int) ([]KeyInfo, uint64, error) {
	if pattern == "" {
		pattern = "*"
	}
	if count <= 0 || count > 2000 {
		count = 200
	}
	var keys []string
	next := cursor
	// SCAN may return few keys per call on a sparse match; go a few rounds
	for round := 0; round < 20 && len(keys) < count; round++ {
		ks, c, err := k.c.Scan(ctx, next, pattern, int64(count)).Result()
		if err != nil {
			return nil, 0, err
		}
		keys = append(keys, ks...)
		next = c
		if next == 0 {
			break
		}
	}
	sort.Strings(keys)
	out := make([]KeyInfo, len(keys))
	if len(keys) == 0 {
		return []KeyInfo{}, next, nil
	}
	pipe := k.c.Pipeline()
	types := make([]*redis.StatusCmd, len(keys))
	ttls := make([]*redis.DurationCmd, len(keys))
	for i, key := range keys {
		types[i] = pipe.Type(ctx, key)
		ttls[i] = pipe.PTTL(ctx, key)
	}
	_, _ = pipe.Exec(ctx)
	for i, key := range keys {
		out[i] = KeyInfo{Key: key, Type: types[i].Val(), TTL: ttlMs(ttls[i].Val())}
	}
	return out, next, nil
}

func ttlMs(d time.Duration) int64 {
	if d < 0 {
		return -1
	}
	return d.Milliseconds()
}

// KeyValue is one key with (the start of) its value.
type KeyValue struct {
	Key       string `json:"key"`
	Type      string `json:"type"`
	TTL       int64  `json:"ttl_ms"`
	Length    int64  `json:"length"` // characters, fields, items or members
	Value     any    `json:"value"`
	Truncated bool   `json:"truncated"`
}

func kvText(s string) any {
	if !utf8.ValidString(s) {
		return Binary{Bin: fmt.Sprintf("%x", []byte(s[:min(len(s), 32)])), Size: len(s)}
	}
	return s
}

// Get reads a key: a string whole (up to 256 KB), anything else up to limit items.
func (k *KV) Get(ctx context.Context, key string, limit int) (*KeyValue, error) {
	if limit <= 0 || limit > 5000 {
		limit = 500
	}
	typ, err := k.c.Type(ctx, key).Result()
	if err != nil {
		return nil, err
	}
	if typ == "none" {
		return nil, userErr("the key %q does not exist (any more)", key)
	}
	kv := &KeyValue{Key: key, Type: typ}
	ttl, _ := k.c.PTTL(ctx, key).Result()
	kv.TTL = ttlMs(ttl)
	switch typ {
	case "string":
		kv.Length, _ = k.c.StrLen(ctx, key).Result()
		v, err := k.c.GetRange(ctx, key, 0, 256<<10-1).Result()
		if err != nil {
			return nil, err
		}
		kv.Value, kv.Truncated = kvText(v), kv.Length > int64(len(v))
	case "hash":
		kv.Length, _ = k.c.HLen(ctx, key).Result()
		pairs := [][2]any{}
		var cur uint64
		for len(pairs) < limit {
			fs, c, err := k.c.HScan(ctx, key, cur, "*", int64(limit)).Result()
			if err != nil {
				return nil, err
			}
			for i := 0; i+1 < len(fs) && len(pairs) < limit; i += 2 {
				pairs = append(pairs, [2]any{kvText(fs[i]), kvText(fs[i+1])})
			}
			if cur = c; cur == 0 {
				break
			}
		}
		kv.Value, kv.Truncated = pairs, kv.Length > int64(len(pairs))
	case "list":
		kv.Length, _ = k.c.LLen(ctx, key).Result()
		items, err := k.c.LRange(ctx, key, 0, int64(limit-1)).Result()
		if err != nil {
			return nil, err
		}
		vals := make([]any, len(items))
		for i, it := range items {
			vals[i] = kvText(it)
		}
		kv.Value, kv.Truncated = vals, kv.Length > int64(len(items))
	case "set":
		kv.Length, _ = k.c.SCard(ctx, key).Result()
		var members []string
		var cur uint64
		for len(members) < limit {
			ms, c, err := k.c.SScan(ctx, key, cur, "*", int64(limit)).Result()
			if err != nil {
				return nil, err
			}
			members = append(members, ms...)
			if cur = c; cur == 0 {
				break
			}
		}
		if len(members) > limit {
			members = members[:limit]
		}
		sort.Strings(members)
		vals := make([]any, len(members))
		for i, m := range members {
			vals[i] = kvText(m)
		}
		kv.Value, kv.Truncated = vals, kv.Length > int64(len(members))
	case "zset":
		kv.Length, _ = k.c.ZCard(ctx, key).Result()
		zs, err := k.c.ZRangeWithScores(ctx, key, 0, int64(limit-1)).Result()
		if err != nil {
			return nil, err
		}
		vals := make([][2]any, len(zs))
		for i, z := range zs {
			vals[i] = [2]any{kvText(fmt.Sprint(z.Member)), z.Score}
		}
		kv.Value, kv.Truncated = vals, kv.Length > int64(len(zs))
	case "stream":
		kv.Length, _ = k.c.XLen(ctx, key).Result()
		msgs, err := k.c.XRevRangeN(ctx, key, "+", "-", int64(limit)).Result()
		if err != nil {
			return nil, err
		}
		vals := make([]map[string]any, len(msgs))
		for i, m := range msgs {
			vals[i] = map[string]any{"id": m.ID, "fields": m.Values}
		}
		kv.Value, kv.Truncated = vals, kv.Length > int64(len(msgs))
	default:
		kv.Value = nil
	}
	return kv, nil
}

func (k *KV) writable() error {
	if k.spec.ReadOnly {
		return ErrReadOnly
	}
	return nil
}

// SetString writes a string value; ttl < 0 keeps the current expiry, 0 none.
func (k *KV) SetString(ctx context.Context, key, value string, ttl time.Duration) error {
	if err := k.writable(); err != nil {
		return err
	}
	if ttl < 0 {
		return k.c.SetArgs(ctx, key, value, redis.SetArgs{KeepTTL: true}).Err()
	}
	return k.c.Set(ctx, key, value, ttl).Err()
}

// HashSet sets one field of a hash.
func (k *KV) HashSet(ctx context.Context, key, field, value string) error {
	if err := k.writable(); err != nil {
		return err
	}
	return k.c.HSet(ctx, key, field, value).Err()
}

// HashDel removes one field of a hash.
func (k *KV) HashDel(ctx context.Context, key, field string) error {
	if err := k.writable(); err != nil {
		return err
	}
	return k.c.HDel(ctx, key, field).Err()
}

// Delete removes keys and says how many existed.
func (k *KV) Delete(ctx context.Context, keys ...string) (int64, error) {
	if err := k.writable(); err != nil {
		return 0, err
	}
	return k.c.Del(ctx, keys...).Result()
}

// Expire sets a key's time to live (0 or less: never expires).
func (k *KV) Expire(ctx context.Context, key string, ttl time.Duration) error {
	if err := k.writable(); err != nil {
		return err
	}
	if ttl <= 0 {
		return k.c.Persist(ctx, key).Err()
	}
	return k.c.PExpire(ctx, key, ttl).Err()
}

// Rename gives a key a new name (refused when the new one exists).
func (k *KV) Rename(ctx context.Context, key, to string) error {
	if err := k.writable(); err != nil {
		return err
	}
	ok, err := k.c.RenameNX(ctx, key, to).Result()
	if err == nil && !ok {
		return userErr("a key called %q already exists", to)
	}
	return err
}

// redisRead are commands that never change data.
var redisRead = map[string]bool{
	"GET": true, "MGET": true, "GETRANGE": true, "STRLEN": true, "EXISTS": true, "TYPE": true, "TTL": true, "PTTL": true, "EXPIRETIME": true, "PEXPIRETIME": true,
	"KEYS": true, "SCAN": true, "RANDOMKEY": true, "DBSIZE": true, "INFO": true, "PING": true, "ECHO": true, "TIME": true, "LASTSAVE": true, "ROLE": true,
	"HGET": true, "HMGET": true, "HGETALL": true, "HKEYS": true, "HVALS": true, "HLEN": true, "HEXISTS": true, "HSTRLEN": true, "HSCAN": true, "HRANDFIELD": true,
	"LRANGE": true, "LLEN": true, "LINDEX": true, "LPOS": true,
	"SMEMBERS": true, "SCARD": true, "SISMEMBER": true, "SMISMEMBER": true, "SSCAN": true, "SRANDMEMBER": true, "SINTER": true, "SUNION": true, "SDIFF": true, "SINTERCARD": true,
	"ZRANGE": true, "ZREVRANGE": true, "ZRANGEBYSCORE": true, "ZREVRANGEBYSCORE": true, "ZRANGEBYLEX": true, "ZREVRANGEBYLEX": true, "ZCARD": true, "ZSCORE": true, "ZMSCORE": true,
	"ZRANK": true, "ZREVRANK": true, "ZCOUNT": true, "ZLEXCOUNT": true, "ZSCAN": true, "ZRANDMEMBER": true,
	"XRANGE": true, "XREVRANGE": true, "XLEN": true, "XINFO": true, "XPENDING": true,
	"BITCOUNT": true, "BITPOS": true, "GETBIT": true, "PFCOUNT": true, "GEOPOS": true, "GEODIST": true, "GEOHASH": true, "GEOSEARCH": true,
	"OBJECT": true, "MEMORY": true, "SLOWLOG": true, "LATENCY": true, "COMMAND": true, "DUMP": true, "LOLWUT": true,
	"JSON.GET": true, "JSON.MGET": true, "JSON.TYPE": true, "JSON.STRLEN": true, "JSON.ARRLEN": true, "JSON.OBJKEYS": true, "JSON.OBJLEN": true,
	"FT.SEARCH": true, "FT.INFO": true, "FT._LIST": true, "FT.AGGREGATE": true,
}

// redisRefused cannot work from a web page (they block or take over the connection).
var redisRefused = map[string]string{
	"MONITOR": "it never ends", "SUBSCRIBE": "it never ends", "PSUBSCRIBE": "it never ends", "SSUBSCRIBE": "it never ends",
	"SYNC": "it is for replicas", "PSYNC": "it is for replicas", "SELECT": "pick the database at the top instead", "QUIT": "", "RESET": "", "HELLO": "", "AUTH": "the connection signs in for you",
	"BLPOP": "it waits", "BRPOP": "it waits", "BLMOVE": "it waits", "BRPOPLPUSH": "it waits", "BZPOPMIN": "it waits", "BZPOPMAX": "it waits", "BLMPOP": "it waits", "BZMPOP": "it waits",
	"WAIT": "it waits", "WAITAOF": "it waits", "SHUTDOWN": "it stops the server", "DEBUG": "it can crash the server", "CLIENT": "", "MULTI": "", "EXEC": "", "DISCARD": "", "WATCH": "",
}

// redisDanger deserve a second look.
var redisDanger = map[string]string{
	"FLUSHALL": "it deletes every key in every database", "FLUSHDB": "it deletes every key in this database", "SWAPDB": "it swaps two whole databases",
	"CONFIG": "it changes the server's configuration", "SCRIPT": "", "FUNCTION": "", "DEL": "", "UNLINK": "", "RESTORE": "", "MIGRATE": "", "MOVE": "",
}

// RedisCommandKind says whether a console command reads, and why it may be risky.
func RedisCommandKind(args []string) (read bool, danger string, refused error) {
	if len(args) == 0 {
		return true, "", userErr("type a command")
	}
	cmd := strings.ToUpper(args[0])
	sub := ""
	if len(args) > 1 {
		sub = strings.ToUpper(args[1])
	}
	if why, ok := redisRefused[cmd]; ok {
		if why == "" {
			why = "it does not work from here"
		}
		return false, "", userErr("%s cannot be used here: %s", cmd, why)
	}
	if (cmd == "XREAD" || cmd == "XREADGROUP") && containsFold(args, "BLOCK") {
		return false, "", userErr("%s with BLOCK waits; leave BLOCK out", cmd)
	}
	read = redisRead[cmd] || cmd == "XREAD" || (cmd == "CONFIG" && sub == "GET") || (cmd == "CLUSTER" && sub == "INFO")
	if (cmd == "OBJECT" || cmd == "MEMORY") && sub != "" && sub != "ENCODING" && sub != "FREQ" && sub != "IDLETIME" && sub != "REFCOUNT" && sub != "USAGE" && sub != "STATS" && sub != "DOCTOR" && sub != "HELP" {
		read = false
	}
	if (cmd == "SLOWLOG" && sub == "RESET") || (cmd == "LATENCY" && sub == "RESET") {
		read = false
	}
	if why, ok := redisDanger[cmd]; ok && !read {
		switch cmd {
		case "CONFIG":
			if sub == "SET" || sub == "RESETSTAT" || sub == "REWRITE" {
				danger = why
			}
		case "SCRIPT", "FUNCTION":
			if sub == "FLUSH" {
				danger = "it removes every stored script"
			}
		case "DEL", "UNLINK":
			if len(args) > 4 {
				danger = fmt.Sprintf("it deletes %d keys", len(args)-1)
			}
		default:
			danger = why
		}
	}
	return read, danger, nil
}

func containsFold(list []string, s string) bool {
	for _, x := range list {
		if strings.EqualFold(x, s) {
			return true
		}
	}
	return false
}

// Command runs one console command.
func (k *KV) Command(ctx context.Context, args []string) (any, error) {
	read, _, refused := RedisCommandKind(args)
	if refused != nil {
		return nil, refused
	}
	if !read && k.spec.ReadOnly {
		return nil, ErrReadOnly
	}
	in := make([]any, len(args))
	for i, a := range args {
		in[i] = a
	}
	v, err := k.c.Do(ctx, in...).Result()
	if errors.Is(err, redis.Nil) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	return redisJSON(v), nil
}

func redisJSON(v any) any {
	switch x := v.(type) {
	case string:
		return kvText(x)
	case []any:
		out := make([]any, len(x))
		for i, e := range x {
			out[i] = redisJSON(e)
		}
		return out
	case map[any]any:
		out := map[string]any{}
		for k, e := range x {
			out[fmt.Sprint(k)] = redisJSON(e)
		}
		return out
	case map[string]any:
		for k, e := range x {
			x[k] = redisJSON(e)
		}
		return x
	}
	return v
}

// ParseArgs splits a console line like a shell: spaces separate, quotes group,
// a backslash escapes inside double quotes.
func ParseArgs(line string) ([]string, error) {
	var out []string
	var cur strings.Builder
	in := false
	quote := rune(0)
	esc := false
	for _, r := range line {
		switch {
		case esc:
			switch r {
			case 'n':
				cur.WriteRune('\n')
			case 't':
				cur.WriteRune('\t')
			default:
				cur.WriteRune(r)
			}
			esc = false
		case quote == '"' && r == '\\':
			esc = true
		case quote != 0 && r == quote:
			quote = 0
		case quote != 0:
			cur.WriteRune(r)
		case r == '"' || r == '\'':
			quote, in = r, true
		case r == ' ' || r == '\t':
			if in {
				out = append(out, cur.String())
				cur.Reset()
				in = false
			}
		default:
			cur.WriteRune(r)
			in = true
		}
	}
	if quote != 0 {
		return nil, userErr("a quote is not closed")
	}
	if in {
		out = append(out, cur.String())
	}
	return out, nil
}

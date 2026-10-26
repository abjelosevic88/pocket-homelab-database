package server

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/abjelosevic88/pocket-homelab-database/internal/engine"
	"github.com/abjelosevic88/pocket-homelab-database/internal/store"
)

// pools keeps database connections open between requests (closed after
// five idle minutes, or at once when the connection is edited or paused).
type pools struct {
	mu sync.Mutex
	m  map[string]*poolEntry
}

type poolEntry struct {
	sql  *engine.SQL
	kv   *engine.KV
	used time.Time
}

func newPools() *pools { return &pools{m: map[string]*poolEntry{}} }

func (p *pools) get(key string, open func() (*poolEntry, error)) (*poolEntry, error) {
	p.mu.Lock()
	if e, ok := p.m[key]; ok {
		e.used = time.Now()
		p.mu.Unlock()
		return e, nil
	}
	p.mu.Unlock()
	e, err := open()
	if err != nil {
		return nil, err
	}
	e.used = time.Now()
	p.mu.Lock()
	defer p.mu.Unlock()
	if old, ok := p.m[key]; ok { // opened twice at once: keep the first
		e.close()
		return old, nil
	}
	p.m[key] = e
	return e, nil
}

func (e *poolEntry) close() {
	if e.sql != nil {
		_ = e.sql.Close()
	}
	if e.kv != nil {
		_ = e.kv.Close()
	}
}

// drop closes every pool of one connection.
func (p *pools) drop(connID int64) {
	prefix := "c" + strconv.FormatInt(connID, 10) + "|"
	p.mu.Lock()
	defer p.mu.Unlock()
	for k, e := range p.m {
		if strings.HasPrefix(k, prefix) {
			e.close()
			delete(p.m, k)
		}
	}
}

func (p *pools) closeIdle(d time.Duration) {
	p.mu.Lock()
	defer p.mu.Unlock()
	for k, e := range p.m {
		if time.Since(e.used) > d {
			e.close()
			delete(p.m, k)
		}
	}
}

func (p *pools) closeAll() {
	p.mu.Lock()
	defer p.mu.Unlock()
	for k, e := range p.m {
		e.close()
		delete(p.m, k)
	}
}

// errNotReachable explains why a connection cannot be opened right now.
type errNotReachable struct{ msg string }

func (e *errNotReachable) Error() string { return e.msg }

// spec turns a saved connection into what the engine needs: the password
// unsealed and, for a container, its current address.
func (s *Server) spec(ctx context.Context, c *store.Connection, db string, readOnly bool) (engine.Spec, error) {
	pass, err := s.box.Open(c.Password)
	if err != nil {
		return engine.Spec{}, err
	}
	sp := engine.Spec{Engine: c.Engine, Host: c.Host, Port: c.Port, User: c.Username, Password: pass, Database: firstNonEmpty(db, c.Database),
		SSLMode: c.SSLMode, Path: c.Path, ReadOnly: readOnly, Timeout: 8 * time.Second}
	if c.Container != "" && s.docker.Available() && c.Engine != engine.SQLite {
		ct, err := s.docker.Inspect(ctx, c.Container)
		switch {
		case err != nil && c.Host == "":
			return sp, &errNotReachable{fmt.Sprintf("the container %s was not found: %v", c.Container, err)}
		case err == nil && !ct.Running:
			return sp, &errNotReachable{fmt.Sprintf("the container %s is stopped", c.Container)}
		case err == nil:
			port := c.Port
			if port == 0 {
				port = engine.DefaultPort(c.Engine)
			}
			if host, p := ct.Address(port); host != "" {
				sp.Host, sp.Port = host, p
			}
		}
	}
	if sp.Engine == engine.SQLite {
		if sp.Path == "" {
			return sp, &errNotReachable{"no file is set"}
		}
	} else if sp.Host == "" {
		return sp, &errNotReachable{"no address is set"}
	}
	return sp, nil
}

func poolKey(c *store.Connection, sp engine.Spec) string {
	return fmt.Sprintf("c%d|%s|%s|%s|%v", c.ID, c.UpdatedAt, sp.Addr()+sp.Path, sp.Database, sp.ReadOnly)
}

// openSQL returns an open SQL connection to one database of c.
func (s *Server) openSQL(ctx context.Context, c *store.Connection, db string, readOnly bool) (*engine.SQL, error) {
	if !engine.IsSQL(c.Engine) {
		return nil, &engine.UserError{Msg: engine.EngineName(c.Engine) + " does not speak SQL"}
	}
	sp, err := s.spec(ctx, c, db, readOnly)
	if err != nil {
		return nil, err
	}
	e, err := s.pools.get(poolKey(c, sp), func() (*poolEntry, error) {
		q, err := engine.OpenSQL(sp)
		if err != nil {
			return nil, err
		}
		pctx, cancel := context.WithTimeout(ctx, sp.Timeout+2*time.Second)
		defer cancel()
		if err := q.Ping(pctx); err != nil {
			_ = q.Close()
			return nil, &errNotReachable{"cannot connect: " + engine.CleanErr(err)}
		}
		return &poolEntry{sql: q}, nil
	})
	if err != nil {
		return nil, err
	}
	return e.sql, nil
}

// openKV returns an open Redis connection to one numbered database of c.
func (s *Server) openKV(ctx context.Context, c *store.Connection, db string, readOnly bool) (*engine.KV, error) {
	if c.Engine != engine.Redis {
		return nil, &engine.UserError{Msg: "this is not a Redis connection"}
	}
	sp, err := s.spec(ctx, c, db, readOnly)
	if err != nil {
		return nil, err
	}
	e, err := s.pools.get(poolKey(c, sp), func() (*poolEntry, error) {
		k, err := engine.OpenRedis(sp)
		if err != nil {
			return nil, err
		}
		pctx, cancel := context.WithTimeout(ctx, sp.Timeout+2*time.Second)
		defer cancel()
		if err := k.Ping(pctx); err != nil {
			_ = k.Close()
			return nil, &errNotReachable{"cannot connect: " + engine.CleanErr(err)}
		}
		return &poolEntry{kv: k}, nil
	})
	if err != nil {
		return nil, err
	}
	return e.kv, nil
}

// dbErr answers an engine error: the person's mistake (400), a database
// that cannot be reached (502), or what the database said (422).
func (s *Server) dbErr(w http.ResponseWriter, err error) {
	var nr *errNotReachable
	var ge *gateError
	switch {
	case errors.As(err, &ge):
		writeJSON(w, 409, map[string]any{"error": ge.msg, "code": ge.code})
	case errors.As(err, &nr):
		writeErr(w, 502, nr.msg)
	case errors.Is(err, engine.ErrReadOnly), errors.Is(err, engine.ErrNoKey), engine.IsUserError(err):
		writeErr(w, 400, err.Error())
	case errors.Is(err, store.ErrNotFound):
		writeErr(w, 404, "not found")
	default:
		writeErr(w, 422, engine.CleanErr(err))
	}
}

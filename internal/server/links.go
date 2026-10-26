package server

import (
	"context"
	"errors"
	"net/http"
	"regexp"
	"strings"

	core "github.com/abjelosevic88/pocket-homelab-core/sdk"
	"github.com/abjelosevic88/pocket-homelab-database/internal/store"
)

// Pocket Core app links (docs/links.md in pocket-homelab-core), Pocket
// Database as the target: another Pocket app the person allowed in Core may
// act here for them with a pcx_ token, only within these scopes. Every route
// not listed in linkRoutes stays closed to it (403), and a linked app can
// never change anything: queries run read-only, writes are refused.

var linkOffers = []core.Offer{
	{Scope: "databases:read", Name: "See your databases", Description: "List your database connections with their health, size and growth, and read their structure — for dashboards, monitoring and Pocket AI. Never passwords, addresses or file paths."},
	{Scope: "databases:query", Name: "Read data", Description: "Run read-only queries and browse tables in your databases (Pocket AI answering a question about your data). It can never change anything."},
}

type linkRoute struct {
	methods string
	path    *regexp.Regexp
	scope   string // "" = any linked app
}

var linkRoutes = []linkRoute{
	{"GET", regexp.MustCompile(`^/api/v1/auth/me$`), ""},
	{"GET", regexp.MustCompile(`^/api/v1/(overview|connections)$`), "databases:read"},
	{"GET", regexp.MustCompile(`^/api/v1/connections/\d+(/(server|databases|schemas|table|sizes))?$`), "databases:read"},
	{"GET", regexp.MustCompile(`^/api/v1/connections/\d+/(rows|keys|key)$`), "databases:query"},
	{"POST", regexp.MustCompile(`^/api/v1/connections/\d+/query$`), "databases:query"},
}

var errLinkScope = errors.New("outside the link's scope")

// linkAllows reports whether a link token with these scopes may make this request.
func linkAllows(scopes []string, r *http.Request) bool {
	for _, lr := range linkRoutes {
		if !strings.Contains(" "+lr.methods+" ", " "+r.Method+" ") || !lr.path.MatchString(r.URL.Path) {
			continue
		}
		if lr.scope == "" {
			return true
		}
		for _, sc := range scopes {
			if sc == lr.scope {
				return true
			}
		}
	}
	return false
}

// linkUser validates a pcx_ token, checks the route against its scopes and
// maps it to the person it acts for. The second value is the acting app's slug.
func (s *Server) linkUser(ctx context.Context, tok string, r *http.Request) (*store.User, string, error) {
	in, err := s.core.Introspect(ctx, tok)
	if err != nil {
		return nil, "", err
	}
	if !in.Active || !in.Delegated() || in.Audience != "database" || in.User == nil || in.User.Email == "" || in.App == nil || in.App.Slug == "" {
		return nil, "", http.ErrNoCookie
	}
	if !linkAllows(strings.Fields(in.Scope), r) {
		return nil, "", errLinkScope
	}
	u, err := s.coreIdentityUser(ctx, in)
	return u, in.App.Slug, err
}

const linkAppKey ctxKey = 2

// linkApp is the acting app's slug when the request came in on a link token.
func linkApp(ctx context.Context) string {
	a, _ := ctx.Value(linkAppKey).(string)
	return a
}

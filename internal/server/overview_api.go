package server

import (
	"net/http"
	"sort"

	"github.com/abjelosevic88/pocket-homelab-database/internal/store"
)

// handleOverview is the home page: every connection's health and size,
// growth, open pauses and the latest changes. Also what Homepage widgets
// and other Pocket apps (databases:read) read.
func (s *Server) handleOverview(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	uid := actorID(ctx)
	list, err := s.store.ListConnections(ctx, uid)
	if err != nil {
		s.fail(w, err, "overview")
		return
	}
	paused := s.pausedSet(ctx)
	counts := map[string]int{"connections": len(list), "up": 0, "down": 0, "paused": 0, "unknown": 0, "watched": 0, "writable": 0}
	engines := map[string]int{}
	var total int64
	views := make([]connView, 0, len(list))
	for _, c := range list {
		v := s.view(ctx, c, paused)
		views = append(views, v)
		engines[c.Engine]++
		switch {
		case paused[c.ID] || c.Status == "paused":
			counts["paused"]++
		case c.Status == "up":
			counts["up"]++
		case c.Status == "down":
			counts["down"]++
		default:
			counts["unknown"]++
		}
		if c.Monitor {
			counts["watched"]++
		}
		if c.AllowWrites {
			counts["writable"]++
		}
		if c.SizeBytes > 0 {
			total += c.SizeBytes
		}
	}
	biggest := append([]connView(nil), views...)
	sort.SliceStable(biggest, func(i, j int) bool { return biggest[i].SizeBytes > biggest[j].SizeBytes })
	if len(biggest) > 6 {
		biggest = biggest[:6]
	}
	// growth: the summed size per day over 30 days, and the change per connection
	since := store.AddDays(store.Today(), -29)
	hist, _ := s.store.SizeHistory(ctx, uid, 0, since)
	byDay := map[string]int64{}
	first, last := map[int64]int64{}, map[int64]int64{}
	for _, p := range hist {
		byDay[p.Date] += p.SizeBytes
		if _, ok := first[p.ConnectionID]; !ok {
			first[p.ConnectionID] = p.SizeBytes
		}
		last[p.ConnectionID] = p.SizeBytes
	}
	type dayPoint struct {
		Date      string `json:"date"`
		SizeBytes int64  `json:"size_bytes"`
	}
	growth := []dayPoint{}
	for d := since; d <= store.Today(); d = store.AddDays(d, 1) {
		if v, ok := byDay[d]; ok {
			growth = append(growth, dayPoint{d, v})
		}
	}
	type mover struct {
		ID    int64  `json:"id"`
		Name  string `json:"name"`
		Delta int64  `json:"delta_bytes"`
		Size  int64  `json:"size_bytes"`
	}
	var movers []mover
	for _, c := range list {
		if a, ok := first[c.ID]; ok && last[c.ID] != a {
			movers = append(movers, mover{c.ID, c.Name, last[c.ID] - a, last[c.ID]})
		}
	}
	sort.Slice(movers, func(i, j int) bool { return abs64(movers[i].Delta) > abs64(movers[j].Delta) })
	if len(movers) > 5 {
		movers = movers[:5]
	}
	if movers == nil {
		movers = []mover{}
	}
	resp := map[string]any{"counts": counts, "engines": engines, "total_bytes": total, "connections": views, "biggest": biggest,
		"growth": growth, "movers": movers, "docker": s.docker.Available()}
	if linkApp(ctx) == "" {
		acts, _ := s.store.ListActivity(ctx, uid, 0, 8)
		hist, _ := s.store.ListHistory(ctx, uid, 0, "", 6)
		saved, _ := s.store.ListSaved(ctx, uid, 0)
		if len(saved) > 6 {
			saved = saved[:6]
		}
		var pauses []store.Maintenance
		if all, err := s.store.AllOpenMaintenance(ctx); err == nil {
			for _, m := range all {
				if m.UserID == uid {
					pauses = append(pauses, m)
				}
			}
		}
		if pauses == nil {
			pauses = []store.Maintenance{}
		}
		resp["activity"], resp["history"], resp["saved"], resp["pauses"] = acts, hist, saved, pauses
	}
	writeJSON(w, http.StatusOK, resp)
}

func abs64(n int64) int64 {
	if n < 0 {
		return -n
	}
	return n
}

// handleSizes is one connection's size history (90 days).
func (s *Server) handleSizes(w http.ResponseWriter, r *http.Request) {
	c, ok := s.conn(w, r)
	if !ok {
		return
	}
	days := queryInt(r, "days")
	if days <= 0 || days > 400 {
		days = 90
	}
	list, err := s.store.SizeHistory(r.Context(), c.UserID, c.ID, store.AddDays(store.Today(), -(days-1)))
	if err != nil {
		s.fail(w, err, "sizes")
		return
	}
	writeJSON(w, http.StatusOK, list)
}

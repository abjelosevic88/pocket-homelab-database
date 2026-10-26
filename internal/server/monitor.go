package server

import (
	"context"
	"fmt"
	"sync"
	"time"

	"github.com/abjelosevic88/pocket-homelab-database/internal/notify"
	"github.com/abjelosevic88/pocket-homelab-database/internal/store"
)

// ---- watching ----
//
// Every five minutes each connection with "monitor" on is opened (not
// pooled), asked for its version and size, and closed. Two failures in a
// row mark it down and send one push (and a database.down event); the first
// success after that sends "back up". The day's last size goes into the
// growth chart.

// checkAll checks every watched connection, four at a time.
func (s *Server) checkAll(ctx context.Context) {
	if !s.checkMu.TryLock() {
		return
	}
	defer s.checkMu.Unlock()
	list, err := s.store.MonitoredConnections(ctx)
	if err != nil {
		return
	}
	sem := make(chan struct{}, 4)
	var wg sync.WaitGroup
	for i := range list {
		c := list[i]
		if c.Source == "demo" && c.Engine != "sqlite" {
			continue // demo servers do not exist
		}
		wg.Add(1)
		sem <- struct{}{}
		go func() {
			defer wg.Done()
			defer func() { <-sem }()
			cctx, cancel := context.WithTimeout(ctx, 30*time.Second)
			defer cancel()
			s.checkOne(cctx, &c)
		}()
	}
	wg.Wait()
}

// checkOne checks one connection and stores the outcome.
func (s *Server) checkOne(ctx context.Context, c *store.Connection) {
	if m, err := s.store.OpenMaintenance(ctx, c.ID); err == nil {
		c.Status, c.StatusDetail = "paused", "Paused for changes until "+humanTime(m.ExpiresAt)
		c.CheckedAt = store.Now()
		_ = s.store.SetConnectionStatus(ctx, c)
		return
	}
	start := time.Now()
	info, err := s.probe(ctx, c)
	c.CheckedAt = store.Now()
	c.LatencyMs = time.Since(start).Milliseconds()
	wasDown := c.Status == "down"
	if err == nil {
		c.Status, c.StatusDetail, c.Fails, c.DownSince = "up", "", 0, ""
		c.Version, c.SizeBytes = info.Version, info.SizeBytes
		if f := info.Extra["flavor"]; f != "" && c.Engine != "sqlite" {
			c.Version = f + " " + info.Version
		}
		alerted := c.Alerted
		c.Alerted = false
		_ = s.store.SetConnectionStatus(ctx, c)
		if info.SizeBytes >= 0 {
			_ = s.store.AddSize(ctx, c.UserID, c.ID, store.Today(), info.SizeBytes)
		}
		if wasDown && alerted {
			s.alert(ctx, c, false)
		}
		return
	}
	c.Fails++
	c.StatusDetail = err.Error()
	c.SizeBytes = -1
	if c.Fails >= 2 || c.Status == "" {
		if c.Status != "down" {
			c.DownSince = store.Now()
		}
		c.Status = "down"
	}
	if c.Status == "down" && c.Fails >= 2 && !c.Alerted && c.Monitor {
		c.Alerted = s.alert(ctx, c, true)
	}
	_ = s.store.SetConnectionStatus(ctx, c)
}

// alert sends the down / back-up push and event; it reports whether the
// person was told (so "back up" follows only a "down" they saw).
func (s *Server) alert(ctx context.Context, c *store.Connection, down bool) bool {
	st, _ := s.store.GetSettings(ctx, c.UserID)
	topic := "database.up"
	if down {
		topic = "database.down"
	}
	s.publish(ctx, c.UserID, topic, c.Name, map[string]any{"connection_id": c.ID, "connection": c.Name, "engine": c.Engine, "detail": c.StatusDetail})
	if (down && !st.NotifyDown) || (!down && !st.NotifyRecovered) {
		return true
	}
	n := notify.New(st.NotifyURL, st.NotifyFormat)
	if !n.Configured() {
		return true
	}
	m := notify.Message{Title: c.Name + " is back", Body: fmt.Sprintf("%s answers again.", c.Name), Priority: "info", Tags: []string{"white_check_mark"},
		Link: s.cfg.BaseURL + fmt.Sprintf("/c/%d", c.ID)}
	if down {
		m = notify.Message{Title: c.Name + " is down", Body: c.StatusDetail, Priority: "warning", Tags: []string{"rotating_light"}, Link: m.Link}
	}
	if err := n.Send(ctx, m); err != nil {
		s.log.Warn("push failed", "connection", c.Name, "err", err)
	}
	return true
}

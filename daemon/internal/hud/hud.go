// Package hud syncs the trader-hud (stock-trader PUSH-20 dashboard) summary
// into SignalDeck, so the live strategy panel renders even when the HUD
// process later goes down — last-known state with an honest fetched-at stamp.
package hud

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"time"

	"github.com/nyaungnicholas-wq/signaldeck/internal/store"
	"github.com/nyaungnicholas-wq/signaldeck/internal/workers"
)

// Sync is the periodic trader-hud fetch worker.
type Sync struct {
	st   *store.Store
	url  string
	http *http.Client

	// Consecutive-unreachable state. trader-hud is a SEPARATE project that is
	// often simply not running; at a 1-minute interval that produced 258 error
	// rows a day, pinned health.json to ok:false indefinitely, and filled 1,418
	// of the last 4,000 daemon log lines with one identical message — burying
	// every other signal in the audit log.
	misses   int
	retryAt  time.Time
	loggedAt time.Time
}

// New builds the worker; url is trader-hud's /api/summary.
func New(st *store.Store, url string) *Sync {
	return &Sync{st: st, url: url, http: &http.Client{Timeout: 15 * time.Second}}
}

// downBackoff spaces retries out while the HUD stays down: 1m, 2m, 4m … capped
// at 30m, so a HUD that comes back is still noticed within half an hour.
func downBackoff(misses int) time.Duration {
	if misses < 1 {
		return 0
	}
	if misses > 6 {
		misses = 6
	}
	d := time.Minute << uint(misses-1)
	if d > 30*time.Minute {
		d = 30 * time.Minute
	}
	return d
}

// Name implements workers.Worker.
func (s *Sync) Name() string { return "hud-sync" }

// Interval implements workers.Worker.
func (s *Sync) Interval() time.Duration { return time.Minute }

// Run fetches and stores the summary payload verbatim.
//
// A HUD that is not running is reported as DEGRADED, never as an error: the
// difference is a fix, not a nuance. "error" means SignalDeck broke and someone
// should look; "degraded" means an optional external process is off, which is
// the normal state on a machine where only SignalDeck is up. Filing it as an
// error made the fleet permanently red and taught the reader to ignore red.
func (s *Sync) Run(ctx context.Context) (string, error) {
	if now := time.Now(); s.misses > 0 && now.Before(s.retryAt) {
		return fmt.Sprintf("trader-hud down since %d checks ago; next try %s",
			s.misses, s.retryAt.Format(time.TimeOnly)), nil
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, s.url, nil)
	if err != nil {
		return "", err
	}
	res, err := s.http.Do(req)
	if err != nil {
		s.misses++
		s.retryAt = time.Now().Add(downBackoff(s.misses))
		// One line per escalation, not one per minute.
		if s.misses <= 3 || time.Since(s.loggedAt) > time.Hour {
			s.loggedAt = time.Now()
			return "", fmt.Errorf("trader-hud not reachable (is it running on :8787?), "+
				"backing off to %s: %w: %w", downBackoff(s.misses), err, workers.ErrDegraded)
		}
		return fmt.Sprintf("trader-hud still down (%d consecutive); next try in %s",
			s.misses, downBackoff(s.misses)), nil
	}
	defer res.Body.Close() //nolint:errcheck
	if res.StatusCode != http.StatusOK {
		return "", fmt.Errorf("trader-hud: HTTP %d", res.StatusCode)
	}
	body, err := io.ReadAll(io.LimitReader(res.Body, 4<<20))
	if err != nil {
		return "", err
	}
	if !json.Valid(body) {
		return "", fmt.Errorf("trader-hud: response is not valid JSON")
	}
	if err := s.st.SetHud(ctx, string(body)); err != nil {
		return "", err
	}
	recovered := s.misses
	s.misses, s.retryAt, s.loggedAt = 0, time.Time{}, time.Time{}
	if recovered > 0 {
		return fmt.Sprintf("synced %d bytes (trader-hud back after %d misses)", len(body), recovered), nil
	}
	return fmt.Sprintf("synced %d bytes", len(body)), nil
}

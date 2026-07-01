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
)

// Sync is the periodic trader-hud fetch worker.
type Sync struct {
	st   *store.Store
	url  string
	http *http.Client
}

// New builds the worker; url is trader-hud's /api/summary.
func New(st *store.Store, url string) *Sync {
	return &Sync{st: st, url: url, http: &http.Client{Timeout: 15 * time.Second}}
}

// Name implements workers.Worker.
func (s *Sync) Name() string { return "hud-sync" }

// Interval implements workers.Worker.
func (s *Sync) Interval() time.Duration { return time.Minute }

// Run fetches and stores the summary payload verbatim.
func (s *Sync) Run(ctx context.Context) (string, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, s.url, nil)
	if err != nil {
		return "", err
	}
	res, err := s.http.Do(req)
	if err != nil {
		return "", fmt.Errorf("trader-hud not reachable (is it running on :8787?): %w", err)
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
	return fmt.Sprintf("synced %d bytes", len(body)), nil
}

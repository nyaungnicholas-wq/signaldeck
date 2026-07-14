// Universe LIVE poller (live-everything wave, 2026-07-10): universe-live.
//
// Makes EVERY tracked stock minute-live, not just the websocket hot set. Each
// 60s tick during NYSE bar hours it pulls the last few minutes of 1m bars for
// ALL active daily-universe stocks via Alpaca's multi-symbol endpoint and
// upserts them — the same granularity the websocket delivers for the hot set,
// arriving at most ~1 minute later.
//
// RATE BUDGET: an incremental ~5-minute window returns ≤5 bars/symbol, so a
// 500-symbol pass is 5 batches × 1 page = ~5 requests/tick ≈ 5 req/min —
// trivially under Alpaca's free 200 req/min even stacked on the streamer,
// universe deep poller, and discovery. Off-hours ticks are marketcal-gated
// no-ops (the 6h deep poller still heals gaps and covers extended hours).
// Upserts are idempotent — overlap with the deep poller can never duplicate.
package universe

import (
	"context"
	"fmt"
	"strings"
	"time"

	"github.com/nyaungnicholas-wq/signaldeck/internal/ingest/alpaca"
	"github.com/nyaungnicholas-wq/signaldeck/internal/marketcal"
	"github.com/nyaungnicholas-wq/signaldeck/internal/store"
)

// liveWindow is how far back each tick fetches — wide enough to self-heal a
// few missed ticks, narrow enough to stay one page per batch.
const liveWindow = 5 * time.Minute

// LivePoller is the universe-live worker.
type LivePoller struct {
	St     *store.Store
	Alpaca *alpaca.Client // nil → worker degrades to a skip
	// Now is a test hook; nil = time.Now.
	Now func() time.Time
}

// Name implements workers.Worker.
func (p *LivePoller) Name() string { return "universe-live" }

// Interval implements workers.Worker.
func (p *LivePoller) Interval() time.Duration { return time.Minute }

func (p *LivePoller) now() time.Time {
	if p.Now != nil {
		return p.Now()
	}
	return time.Now()
}

// Run pulls the incremental minute window for the whole daily universe.
func (p *LivePoller) Run(ctx context.Context) (string, error) {
	if p.Alpaca == nil {
		return "skipped: no Alpaca keys", nil
	}
	now := p.now()
	if !marketcal.OpenForBars(now) {
		return "market closed — skipped (6h deep poller covers off-hours)", nil
	}
	daily := false
	syms, err := p.St.ActiveStockSymbols(ctx, &daily)
	if err != nil {
		return "", err
	}
	if len(syms) == 0 {
		return "no daily-universe symbols", nil
	}
	byName := make(map[string]int64, len(syms))
	names := make([]string, 0, len(syms))
	for _, s := range syms {
		byName[s.Symbol] = s.ID
		names = append(names, s.Symbol)
	}
	resolve := func(sym string) (int64, bool) {
		id, ok := byName[strings.ToUpper(sym)]
		return id, ok
	}
	counts, err := p.Alpaca.BackfillMinuteMulti(ctx, p.St, names, resolve, now.Add(-liveWindow))
	if err != nil {
		return "", err
	}
	total := 0
	for _, n := range counts {
		total += n
	}
	return fmt.Sprintf("live 1m: %d bars across %d/%d symbols", total, len(counts), len(names)), nil
}

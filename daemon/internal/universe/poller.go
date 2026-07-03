// Universe-poller worker (Stage 2 — broad free daily universe).
//
// It keeps the BROAD DAILY-ONLY universe fed: every UNIVERSE_INTERVAL it pulls
// split-adjusted DAILY bars for the whole registered daily universe via
// Alpaca's free multi-symbol bars endpoint and upserts them. These symbols are
// active-for-daily but never streamed, so cross-sectional
// ranking/correlation/regime and per-symbol daily models get wide, free
// coverage. Daily bars are tiny, so storage stays cheap.
//
// ── FREE-TIER RATE BUDGET ────────────────────────────────────────────────
// Alpaca free tier: 200 requests/minute; multi-symbol bars packs up to
// MaxBatchSymbols (100) symbols per request. With SIGNALDECK_UNIVERSE_CAP=500:
//
//	500 symbols ÷ 100/batch          = 5 requests per page
//	daily window is short → ~1 page  ⇒ ~5 requests per run
//	requests are spaced by pagePause (300ms) and the run happens every ~6h
//
// Even the once-a-day full ~2y backfill (a few pages) stays in the low tens of
// requests — a tiny fraction of the 200/min ceiling — and the client backs off
// on any 429. The streamer's live 1m pipeline and the discovery screener share
// the same account but run on their own cadences; none of them approaches the
// limit. Nothing here ever hits paid data: feed=iex, adjustment=split only.
package universe

import (
	"context"
	"fmt"
	"os"
	"strconv"
	"strings"
	"time"

	"github.com/nyaungnicholas-wq/signaldeck/internal/ingest/alpaca"
	"github.com/nyaungnicholas-wq/signaldeck/internal/store"
)

// DefaultUniverseCap is the default max size of the broad daily universe
// (env-tunable via SIGNALDECK_UNIVERSE_CAP). 500 ≈ the S&P 500.
const DefaultUniverseCap = 500

// DefaultInterval is how often the poller refreshes daily bars for the whole
// universe. Staggered by FirstRunDelay so it doesn't fire in lockstep with the
// other 6h workers on boot.
const DefaultInterval = 6 * time.Hour

// UniverseCap returns the broad-universe budget: SIGNALDECK_UNIVERSE_CAP when
// set to a positive integer, else DefaultUniverseCap.
func UniverseCap() int {
	if v := os.Getenv("SIGNALDECK_UNIVERSE_CAP"); v != "" {
		if n, err := strconv.Atoi(v); err == nil && n > 0 {
			return n
		}
	}
	return DefaultUniverseCap
}

// Curated returns the deduplicated, upper-cased broad-universe seed list,
// trimmed to cap (list order = priority). Defensive dedup so an accidental
// duplicate in the hardcoded slice can never double-register a symbol.
func Curated(cap int) []string {
	seen := make(map[string]bool, len(SP500))
	out := make([]string, 0, len(SP500))
	for _, s := range SP500 {
		s = strings.ToUpper(strings.TrimSpace(s))
		if s == "" || seen[s] {
			continue
		}
		seen[s] = true
		out = append(out, s)
		if cap > 0 && len(out) >= cap {
			break
		}
	}
	return out
}

// Poller is the broad-universe daily-bars worker.
type Poller struct {
	St     *store.Store
	Alpaca *alpaca.Client // nil when no Alpaca keys → worker degrades to a skip
	// Cap bounds the universe (0 → UniverseCap()); FirstRunDelay staggers the
	// first run off the other 6h workers (0 → 90s).
	Cap           int
	FirstRunDelay time.Duration
	Ival          time.Duration // 0 → DefaultInterval

	first bool // set after the first Run so the stagger only applies once
}

// Name implements workers.Worker.
func (p *Poller) Name() string { return "universe-poller" }

// Interval implements workers.Worker.
func (p *Poller) Interval() time.Duration {
	if p.Ival > 0 {
		return p.Ival
	}
	return DefaultInterval
}

func (p *Poller) cap() int {
	if p.Cap > 0 {
		return p.Cap
	}
	return UniverseCap()
}

// Run refreshes daily bars for the entire registered daily universe. On the
// very first invocation it waits FirstRunDelay first, so the poller doesn't
// stampede Alpaca alongside the other boot-time 6h workers.
func (p *Poller) Run(ctx context.Context) (string, error) {
	if p.Alpaca == nil {
		return "skipped: no Alpaca keys", nil
	}
	if !p.first {
		p.first = true
		delay := p.FirstRunDelay
		if delay == 0 {
			delay = 90 * time.Second
		}
		select {
		case <-ctx.Done():
			return "stopped before first run", ctx.Err()
		case <-time.After(delay):
		}
	}

	// The daily universe = active daily-only stock symbols (stream=0). We fetch
	// bars for exactly the registered set; seeding (below) is what populates it.
	daily := false
	syms, err := p.St.ActiveStockSymbols(ctx, &daily)
	if err != nil {
		return "", err
	}
	if len(syms) == 0 {
		return "no daily-universe symbols registered yet", nil
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

	// Short daily window each poll — heals gaps + adds the latest sessions
	// cheaply. (The one-time full history comes from Seed's deep backfill.)
	start := time.Now().UTC().AddDate(0, 0, -10)
	counts, err := p.Alpaca.BackfillDailyMulti(ctx, p.St, names, resolve, start)
	if err != nil {
		return "", err
	}
	total := 0
	for _, n := range counts {
		total += n
	}
	return fmt.Sprintf("refreshed daily bars for %d/%d universe symbols (%d bars)", len(counts), len(names), total), nil
}

// Seed registers the curated broad universe as active-for-daily (stream=0) and
// pulls a one-time deep (~2y) daily-bars backfill so the cross-sectional and
// per-symbol daily models have immediate history. Idempotent + guarded by a
// meta key by the caller (run.go) so it runs ONCE. Symbols Alpaca can't serve
// (delisted/dotted class shares off the IEX feed) are simply absent from the
// backfill — never fatal. Already-streamed hot-set symbols keep their stream
// flag (UpsertDailyUniverseSymbol never demotes).
func Seed(ctx context.Context, st *store.Store, ac *alpaca.Client, cap int) (registered, bars int, err error) {
	names := Curated(cap)
	byName := make(map[string]int64, len(names))
	registerNames := make([]string, 0, len(names))
	for _, sym := range names {
		s, err := st.UpsertDailyUniverseSymbol(ctx, sym, "")
		if err != nil {
			return registered, bars, fmt.Errorf("register universe %s: %w", sym, err)
		}
		byName[s.Symbol] = s.ID
		registerNames = append(registerNames, s.Symbol)
		registered++
	}
	if ac == nil {
		return registered, 0, nil // registered for later; no keys to backfill now
	}
	resolve := func(sym string) (int64, bool) {
		id, ok := byName[strings.ToUpper(sym)]
		return id, ok
	}
	// Deep (~2y) history: start zero → BackfillDailyMulti defaults to 2y ago.
	counts, err := ac.BackfillDailyMulti(ctx, st, registerNames, resolve, time.Time{})
	if err != nil {
		return registered, bars, err
	}
	for _, n := range counts {
		bars += n
	}
	return registered, bars, nil
}

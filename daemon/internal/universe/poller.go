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
// MaxBatchSymbols (100) symbols per request. With SIGNALDECK_UNIVERSE_CAP=500,
// each ~6h run fetches THREE timeframes (live-coverage extension):
//
//	1d (-10d window): 5 batches × ~1 page            ≈  5 requests
//	1h (-10d window): 5 batches × ~1-2 pages         ≈  5-10 requests
//	1m (-4d steady / -8d first-run heal): 5 batches
//	   × several 10k-bar pages                        ≈ 40-100 requests
//
// All pages are spaced by pagePause (300ms), so even the heaviest run spreads
// its requests over well under a minute of wall time at <200/min, and the
// client backs off on any 429. The streamer's live 1m pipeline and the
// discovery screener share the same account but run on their own cadences;
// none of them approaches the limit. Nothing here ever hits paid data:
// feed=iex, adjustment=split only. Storage: broad 1m bars are bounded by the
// same tiered retention as everything else (60d → compact to 1h → archive).
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
// (env-tunable via SIGNALDECK_UNIVERSE_CAP). The ~500-name S&P seed ALREADY
// consumes half of a 1000 cap, so with 500+ discovered candidates the user
// hits the ceiling almost immediately — which silently blocks further
// monitoring. 3000 gives room for the seed PLUS the entire liquid discovery
// universe (most-actives + movers + whole-market top-volume, at most ~1-2k
// names). Rate stays safe: the 60s live poller batches 100 symbols/request, so
// 3000 monitored = ~30 req/min, far under Alpaca's free 200/min; storage is
// bounded by the same tiered retention as everything else.
const DefaultUniverseCap = 3000

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

	first        bool // set after the first Run so the stagger only applies once
	minuteHealed bool // first minute fetch uses a deeper heal window
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
	now := time.Now().UTC()
	counts, err := p.Alpaca.BackfillDailyMulti(ctx, p.St, names, resolve, now.AddDate(0, 0, -10))
	if err != nil {
		return "", err
	}

	// LIVE-COVERAGE EXTENSION: the broad universe also gets 1h and 1m bars so
	// symbol pages never show intraday coverage frozen at some old date (the
	// "bars end 6 days ago" complaint). Same batched multi-symbol endpoint —
	// see the rate-budget note above; hourly adds ~1 page per batch and minute
	// a handful, all paced. Windows: 1h shares the daily heal window; 1m uses
	// a deeper first-run heal (covers a week-long gap after downtime), then a
	// short steady-state window that still spans weekends + Monday holidays.
	// Upserts are idempotent, so overlapping windows never duplicate bars.
	hCounts, err := p.Alpaca.BackfillHourlyMulti(ctx, p.St, names, resolve, now.AddDate(0, 0, -10))
	if err != nil {
		return "", fmt.Errorf("hourly: %w", err)
	}
	minStart := now.AddDate(0, 0, -4)
	if !p.minuteHealed {
		minStart = now.AddDate(0, 0, -8)
		p.minuteHealed = true
	}
	mCounts, err := p.Alpaca.BackfillMinuteMulti(ctx, p.St, names, resolve, minStart)
	if err != nil {
		return "", fmt.Errorf("minute: %w", err)
	}

	tally := func(m map[string]int) (int, int) {
		t := 0
		for _, n := range m {
			t += n
		}
		return len(m), t
	}
	dn, dt := tally(counts)
	hn, ht := tally(hCounts)
	mn, mt := tally(mCounts)
	return fmt.Sprintf("refreshed %d/%d universe symbols (1d %d, 1h %d, 1m %d bars; 1h %d, 1m %d syms)",
		dn, len(names), dt, ht, mt, hn, mn), nil
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

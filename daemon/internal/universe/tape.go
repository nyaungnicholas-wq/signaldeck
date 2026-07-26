// Signal8 wave — Stage 4: TICKER-TAPE ETF seeding.
//
// The home-page ticker tape shows the broad indices (SPY/QQQ/DIA/IWM) and the
// headline sector ETFs (XLK/XLF/XLE/XLV). SPY and QQQ are in the first-boot
// streamed hot set, but the others are NOT in the SP500 company seed list, so
// a fresh (or existing) install has no bars for them. TapeSeeder is a tiny,
// idempotent worker that registers every tape ETF into the BROAD DAILY-ONLY
// universe (stream=0 — never the streamed hot set, per the stream-cap budget)
// and deep-backfills daily bars for any that have none. After registration the
// regular universe-poller keeps them fresh like every other daily-only symbol,
// so this worker's steady-state run is a cheap no-op sweep.
package universe

import (
	"context"
	"fmt"
	"strings"
	"time"

	"github.com/nyaungnicholas-wq/signaldeck/internal/ingest/alpaca"
	md "github.com/nyaungnicholas-wq/signaldeck/internal/marketdata"
	"github.com/nyaungnicholas-wq/signaldeck/internal/store"
)

// TapeETF is one ticker-tape strip member (an index or sector ETF).
type TapeETF struct {
	Symbol string
	Name   string
	Kind   string // "index" | "sector" — the tape groups by this
}

// TapeETFs is the fixed tape membership. Order = display order on the strip.
// BTC and VIX join the strip in the API layer (crypto bars + FRED VIXCLS);
// they are not equities so they don't belong in this seed list.
var TapeETFs = []TapeETF{
	{Symbol: "SPY", Name: "SPDR S&P 500 ETF", Kind: "index"},
	{Symbol: "QQQ", Name: "Invesco Nasdaq-100 ETF", Kind: "index"},
	{Symbol: "DIA", Name: "SPDR Dow Jones Industrial Average ETF", Kind: "index"},
	{Symbol: "IWM", Name: "iShares Russell 2000 ETF", Kind: "index"},
	{Symbol: "XLK", Name: "Technology Select Sector SPDR", Kind: "sector"},
	{Symbol: "XLF", Name: "Financial Select Sector SPDR", Kind: "sector"},
	{Symbol: "XLE", Name: "Energy Select Sector SPDR", Kind: "sector"},
	{Symbol: "XLV", Name: "Health Care Select Sector SPDR", Kind: "sector"},
}

// SectorETFs completes the SPDR sector set beyond the four on the tape strip.
//
// The strip stays short on purpose — it is a glanceable header, not a sector
// map — but a MARKET-LEVEL regime read needs every sector or it silently
// answers a different question: "how are the sectors I happened to list doing"
// rather than "how is the market split". XLP, XLU and XLRE were the three
// missing from the tracked universe entirely, so no regime call existed for
// staples, utilities or real estate at all.
var SectorETFs = []TapeETF{
	{Symbol: "XLY", Name: "Consumer Discretionary Select Sector SPDR", Kind: "sector"},
	{Symbol: "XLP", Name: "Consumer Staples Select Sector SPDR", Kind: "sector"},
	{Symbol: "XLI", Name: "Industrial Select Sector SPDR", Kind: "sector"},
	{Symbol: "XLB", Name: "Materials Select Sector SPDR", Kind: "sector"},
	{Symbol: "XLU", Name: "Utilities Select Sector SPDR", Kind: "sector"},
	{Symbol: "XLRE", Name: "Real Estate Select Sector SPDR", Kind: "sector"},
	{Symbol: "XLC", Name: "Communication Services Select Sector SPDR", Kind: "sector"},
}

// MarketETFs is every basket the platform tracks: the tape strip plus the rest
// of the sector set. This is what gets REGISTERED and what the market-regime
// surface groups over.
func MarketETFs() []TapeETF {
	out := make([]TapeETF, 0, len(TapeETFs)+len(SectorETFs))
	out = append(out, TapeETFs...)
	return append(out, SectorETFs...)
}

// IsTapeETF reports whether symbol (upper-cased) is one of the tracked baskets.
// The movers endpoint uses this to exclude index/sector ETFs from the
// gainers/losers tables (they are baskets, not single-name moves) — which is
// why it covers the FULL basket set, not only the strip: a sector SPDR topping
// the gainers table is a sector rotating, not a stock moving, and it crowds out
// the single-name move the table exists to surface.
func IsTapeETF(symbol string) bool {
	u := strings.ToUpper(strings.TrimSpace(symbol))
	for _, e := range MarketETFs() {
		if e.Symbol == u {
			return true
		}
	}
	return false
}

// TapeSeeder registers the tape ETFs as daily-only universe symbols and
// backfills daily bars for any that have none. Idempotent + self-healing:
// every run re-asserts registration (UpsertDailyUniverseSymbol never demotes
// a streamed symbol) and only symbols with ZERO daily bars trigger a
// backfill, so the steady-state run costs nothing.
type TapeSeeder struct {
	St     *store.Store
	Alpaca *alpaca.Client // nil when no Alpaca keys → registration only, no backfill
	Ival   time.Duration  // 0 → 6h
}

// Name implements workers.Worker.
func (t *TapeSeeder) Name() string { return "tape-seeder" }

// Interval implements workers.Worker.
func (t *TapeSeeder) Interval() time.Duration {
	if t.Ival > 0 {
		return t.Ival
	}
	return 6 * time.Hour
}

// Run registers every tape ETF (daily-only, never demoting hot-set members)
// and deep-backfills daily bars for the ones that have none yet.
func (t *TapeSeeder) Run(ctx context.Context) (string, error) {
	etfs := MarketETFs()
	byName := make(map[string]int64, len(etfs))
	var missing []string
	for _, e := range etfs {
		sym, err := t.St.UpsertDailyUniverseSymbol(ctx, e.Symbol, e.Name)
		if err != nil {
			return "", fmt.Errorf("register tape ETF %s: %w", e.Symbol, err)
		}
		byName[sym.Symbol] = sym.ID
		last, err := t.St.LatestBarTs(ctx, sym.ID, md.TF1d)
		if err != nil {
			return "", err
		}
		if last == 0 {
			missing = append(missing, sym.Symbol)
		}
	}
	if len(missing) == 0 {
		return fmt.Sprintf("all %d market ETFs registered with daily bars", len(etfs)), nil
	}
	if t.Alpaca == nil {
		return fmt.Sprintf("registered %d market ETFs; %d lack daily bars (no Alpaca keys to backfill)",
			len(etfs), len(missing)), nil
	}
	resolve := func(sym string) (int64, bool) {
		id, ok := byName[strings.ToUpper(sym)]
		return id, ok
	}
	// Deep history for the empty ones only: zero start → ~2y default window.
	counts, err := t.Alpaca.BackfillDailyMulti(ctx, t.St, missing, resolve, time.Time{})
	if err != nil {
		return "", err
	}
	bars := 0
	for _, n := range counts {
		bars += n
	}
	return fmt.Sprintf("registered %d market ETFs; backfilled %d bars for %d missing (%s)",
		len(etfs), bars, len(missing), strings.Join(missing, ",")), nil
}

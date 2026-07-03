package pipeline

import (
	"context"
	"fmt"
	"strings"
	"time"

	"github.com/nyaungnicholas-wq/signaldeck/internal/expectancy"
	"github.com/nyaungnicholas-wq/signaldeck/internal/ingest/alpaca"
	"github.com/nyaungnicholas-wq/signaldeck/internal/ingest/cryptohist"
	md "github.com/nyaungnicholas-wq/signaldeck/internal/marketdata"
	"github.com/nyaungnicholas-wq/signaldeck/internal/store"
)

// Backfiller pulls history for newly-subscribed symbols: 2y daily + 60d
// minute for stocks (Alpaca), ~2y daily + 30d hourly + 12h minute for crypto
// (Kraken public OHLC). Long-running; fed by Enqueue from the subscribe API.
type Backfiller struct {
	St     *store.Store
	Alpaca *alpaca.Client     // nil when no keys: stock backfills error visibly
	Kraken *cryptohist.Client
	queue  chan md.Symbol
}

// NewBackfiller builds the worker (queue capacity 64).
func NewBackfiller(st *store.Store, a *alpaca.Client, k *cryptohist.Client) *Backfiller {
	return &Backfiller{St: st, Alpaca: a, Kraken: k, queue: make(chan md.Symbol, 64)}
}

// Enqueue schedules a backfill; drops with an error when the queue is full
// (the caller surfaces it to the user rather than blocking an API request).
func (b *Backfiller) Enqueue(s md.Symbol) error {
	select {
	case b.queue <- s:
		return nil
	default:
		return fmt.Errorf("backfill queue full — try again shortly")
	}
}

// Name implements workers.Worker.
func (b *Backfiller) Name() string { return "backfiller" }

// Interval implements workers.Worker: 0 = long-running.
func (b *Backfiller) Interval() time.Duration { return 0 }

// Run drains the queue until ctx ends. A single symbol's failure is recorded
// and skipped — never fatal to the drain loop (which would strand every other
// queued symbol). The BackfillReconciler re-enqueues anything left
// under-covered (transient failure, or a queue lost across a daemon restart —
// the channel is in-memory), so no symbol is permanently dropped.
func (b *Backfiller) Run(ctx context.Context) (string, error) {
	done, failed := 0, 0
	for {
		select {
		case <-ctx.Done():
			return fmt.Sprintf("stopped; %d ok, %d failed this run", done, failed), nil
		case s := <-b.queue:
			if err := b.backfillAndPrime(ctx, s); err != nil {
				sid := s.ID
				_ = b.St.InsertDQ(ctx, md.DQEvent{
					SymbolID: &sid, Ts: time.Now().Unix(),
					Kind: "error", Detail: "backfill: " + err.Error(),
				})
				failed++
				continue // keep draining; the reconciler will retry this one
			}
			done++
		}
	}
}

// backfillAndPrime pulls history, then immediately primes the derived layers
// (1m→1h rollup + expectancy tables) so a fresh symbol is fully usable within
// seconds instead of waiting for the next scheduled downsampler/expectancy
// tick (5m/1h).
func (b *Backfiller) backfillAndPrime(ctx context.Context, s md.Symbol) error {
	if err := b.backfill(ctx, s); err != nil {
		return err
	}
	now := time.Now().Unix()
	if err := b.St.Rollup(ctx, s.ID, md.TF1m, md.TF1h, 3600, 0, now); err != nil {
		return fmt.Errorf("prime rollup: %w", err)
	}
	daily, err := b.St.LastBars(ctx, s.ID, md.TF1d, dailyLookback)
	if err != nil {
		return err
	}
	minute, err := b.St.LastBars(ctx, s.ID, md.TF1m, minuteLookback)
	if err != nil {
		return err
	}
	for h, rows := range expectancy.Build(daily, minute) {
		if err := b.St.ReplaceExpectancy(ctx, s.ID, h, rows); err != nil {
			return fmt.Errorf("prime expectancy: %w", err)
		}
	}
	return nil
}

// BackfillReconciler is the self-healing backstop: it re-enqueues any active
// symbol whose stored history is missing or thin. This covers both transient
// backfill failures and — crucially — symbols whose enqueue was lost when the
// daemon restarted mid-seed (the queue is in-memory, and seeded_v1 is set at
// enqueue time, so those symbols would otherwise sit empty forever).
type BackfillReconciler struct {
	St *store.Store
	BF *Backfiller
}

// Name implements workers.Worker.
func (r *BackfillReconciler) Name() string { return "backfill-reconciler" }

// Interval implements workers.Worker.
func (r *BackfillReconciler) Interval() time.Duration { return 20 * time.Minute }

// minCoverage is the "clearly backfilled" floor; a brand-new symbol has ~500
// daily bars, so anything under 100 means history never landed.
const minCoverage = 100

// Run re-enqueues under-covered active symbols. A persistently unbackfillable
// symbol (delisted, bad pair) keeps surfacing here and on the quality page —
// which is the honest outcome, not a hidden one.
func (r *BackfillReconciler) Run(ctx context.Context) (string, error) {
	syms, err := r.St.ListSymbols(ctx, true)
	if err != nil {
		return "", err
	}
	requeued := 0
	for _, s := range syms {
		nDaily, _, _, err := r.St.BarCount(ctx, s.ID, md.TF1d)
		if err != nil {
			return "", err
		}
		need := nDaily < minCoverage
		if s.Market == md.Stocks {
			nMin, _, _, err := r.St.BarCount(ctx, s.ID, md.TF1m)
			if err != nil {
				return "", err
			}
			need = need || nMin < minCoverage
		}
		if need {
			if err := r.BF.Enqueue(s); err == nil {
				requeued++
			}
		}
	}
	return fmt.Sprintf("checked %d active symbols, re-enqueued %d under-covered", len(syms), requeued), nil
}

func (b *Backfiller) backfill(ctx context.Context, s md.Symbol) error {
	switch s.Market {
	case md.Stocks:
		if b.Alpaca == nil {
			return fmt.Errorf("no Alpaca keys configured (stock-trader/.env)")
		}
		nd, err := b.Alpaca.BackfillDaily(ctx, b.St, s.ID, s.Symbol)
		if err != nil {
			return fmt.Errorf("daily: %w", err)
		}
		nm, err := b.Alpaca.BackfillMinute(ctx, b.St, s.ID, s.Symbol)
		if err != nil {
			return fmt.Errorf("minute: %w", err)
		}
		_ = nd
		_ = nm
		return nil
	case md.Crypto:
		_, err := b.Kraken.Backfill(ctx, b.St, s.ID, s.Symbol)
		return err
	default:
		return fmt.Errorf("unknown market %q", s.Market)
	}
}

// ── periodic top-ups ────────────────────────────────────────────────────

// CryptoBars refreshes crypto OHLC from Kraken every 15 minutes: the newest
// ~12h of minutes, 30d of hours, 2y of days — idempotent upserts, so this is
// both the live-bar path and the gap healer. (Live 1s microstructure comes
// from TickStream; bars deliberately come from ONE canonical source.)
type CryptoBars struct {
	St     *store.Store
	Kraken *cryptohist.Client
}

// Name implements workers.Worker.
func (w *CryptoBars) Name() string { return "crypto-bars" }

// Interval implements workers.Worker.
func (w *CryptoBars) Interval() time.Duration { return 15 * time.Minute }

// Run refreshes every active crypto symbol.
func (w *CryptoBars) Run(ctx context.Context) (string, error) {
	syms, err := w.St.ListSymbols(ctx, true)
	if err != nil {
		return "", err
	}
	refreshed := 0
	var counts []string
	for _, s := range syms {
		if s.Market != md.Crypto {
			continue
		}
		got, err := w.Kraken.Backfill(ctx, w.St, s.ID, s.Symbol)
		if err != nil {
			return "", fmt.Errorf("%s: %w", s.Symbol, err)
		}
		refreshed++
		counts = append(counts, fmt.Sprintf("%s: %d/1m", s.Symbol, got[md.TF1m]))
	}
	return fmt.Sprintf("refreshed %d crypto symbols (%s)", refreshed, strings.Join(counts, ", ")), nil
}

// StockBars tops up daily bars for the STREAMED hot set every 6h (heals gaps;
// the live minute stream doesn't produce official daily bars).
//
// Broad-universe wave: it deliberately covers ONLY the streamed hot stocks
// (stream=1). The hundreds of broad DAILY-ONLY universe symbols get their daily
// bars from the universe-poller, which uses Alpaca's efficient multi-symbol
// endpoint (≤100 symbols/request) — re-fetching them here one symbol at a time
// would be redundant and could pressure the free 200/min rate budget.
type StockBars struct {
	St     *store.Store
	Alpaca *alpaca.Client
}

// Name implements workers.Worker.
func (w *StockBars) Name() string { return "stock-bars" }

// Interval implements workers.Worker.
func (w *StockBars) Interval() time.Duration { return 6 * time.Hour }

// Run refreshes daily bars for every streamed (hot-set) stock.
func (w *StockBars) Run(ctx context.Context) (string, error) {
	if w.Alpaca == nil {
		return "skipped: no Alpaca keys", nil
	}
	streamed := true
	syms, err := w.St.ActiveStockSymbols(ctx, &streamed)
	if err != nil {
		return "", err
	}
	n := 0
	for _, s := range syms {
		if _, err := w.Alpaca.BackfillDaily(ctx, w.St, s.ID, s.Symbol); err != nil {
			return "", fmt.Errorf("%s: %w", s.Symbol, err)
		}
		n++
	}
	return fmt.Sprintf("topped up daily bars for %d streamed stocks", n), nil
}

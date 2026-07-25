// Historical daily-bar backfill — the hist-backfill worker (research
// discovery engine wave). Once per UTC day it deepens the active stock
// universe's daily bars back to the warmup year before the research window
// (Alpaca multi-symbol endpoint), then recomputes the research_weeks
// evidence base: point-in-time weekly feature rows with realized forward
// labels (histfeat.WeeklyRows), joined to a shared market context anchored
// on SPY bars + the VIX series.
//
// Honesty: crypto is skipped outright — Kraken's public depth is ~2 years,
// and fabricating pre-listing history would poison every era grade. The
// recompute is idempotent (research_weeks REPLACEs on (symbol, week)).
package pipeline

import (
	"context"
	"fmt"
	"strings"
	"time"

	"github.com/nyaungnicholas-wq/signaldeck/internal/histfeat"
	"github.com/nyaungnicholas-wq/signaldeck/internal/ingest/alpaca"
	md "github.com/nyaungnicholas-wq/signaldeck/internal/marketdata"
	"github.com/nyaungnicholas-wq/signaldeck/internal/store"
)

const (
	histBackfillDayKey = "hist_backfill_last_day"

	// histBackfillBatch caps one research_weeks upsert transaction.
	histBackfillBatch = 2000

	// histMinDailyBars gates a symbol into the weekly-row pass: fewer daily
	// bars cannot yield a warmed-up research row (60-bar feature warmup plus
	// a usable stretch of labeled anchors).
	histMinDailyBars = 260

	// histShallowSlackSecs: a symbol whose earliest stored daily bar sits
	// more than this far after BarsFrom has NOT been deepened yet. 45 days
	// absorbs listing dates and holiday gaps without re-fetching everyone.
	histShallowSlackSecs = 45 * 86400
)

// HistoryBackfillWorker deepens stock daily-bar history and recomputes the
// research_weeks evidence base once per UTC day.
type HistoryBackfillWorker struct {
	St     *store.Store
	Alpaca *alpaca.Client   // nil ⇒ skip fetching; rows come from bars already on disk
	Now    func() time.Time // injectable clock; nil ⇒ time.Now

	BarsFrom time.Time // deep-fetch start; zero ⇒ 2019-01-01 UTC (warmup year before rows)
	RowsFrom time.Time // first research-row anchor; zero ⇒ 2020-01-01 UTC
}

// compile-time worker-contract check (internal/workers.Worker, mirrored so
// the pipeline package doesn't import the runner).
var _ interface {
	Name() string
	Interval() time.Duration
	Run(context.Context) (string, error)
} = (*HistoryBackfillWorker)(nil)

// Name implements workers.Worker.
func (w *HistoryBackfillWorker) Name() string { return "hist-backfill" }

// Interval implements workers.Worker.
func (w *HistoryBackfillWorker) Interval() time.Duration { return 6 * time.Hour }

func (w *HistoryBackfillWorker) now() time.Time {
	if w.Now != nil {
		return w.Now()
	}
	return time.Now()
}

// Run deepens shallow symbols (Alpaca permitting), rebuilds the SPY+VIX
// market context, and recomputes weekly research rows for every active stock
// with enough daily history. Gated to once per UTC day; the day meta is set
// on every terminal path so a completed pass never re-runs the same day.
func (w *HistoryBackfillWorker) Run(ctx context.Context) (string, error) {
	now := w.now()
	day := now.UTC().Format("2006-01-02")
	if last, _ := w.St.GetMeta(ctx, histBackfillDayKey); last == day {
		return "already ran today", nil
	}
	barsFrom := w.BarsFrom
	if barsFrom.IsZero() {
		barsFrom = time.Date(2019, 1, 1, 0, 0, 0, 0, time.UTC)
	}
	rowsFrom := w.RowsFrom
	if rowsFrom.IsZero() {
		rowsFrom = time.Date(2020, 1, 1, 0, 0, 0, 0, time.UTC)
	}

	syms, err := w.St.ActiveStockSymbols(ctx, nil)
	if err != nil {
		return "", err
	}
	// SPY anchors the shared market context; register it (daily-only, never
	// streamed) when the universe doesn't already carry it.
	spy, ok := findSymbol(syms, "SPY")
	if !ok {
		spy, err = w.St.UpsertDailyUniverseSymbol(ctx, "SPY", "SPDR S&P 500 ETF Trust")
		if err != nil {
			return "", err
		}
		syms = append(syms, spy)
	}

	// (1) deepen shallow symbols. A fetch failure is recorded and skipped —
	// the rows pass still runs on whatever bars exist.
	byName := make(map[string]int64, len(syms))
	var shallow []string
	shallowCut := barsFrom.Unix() + histShallowSlackSecs
	for _, s := range syms {
		byName[s.Symbol] = s.ID
		earliest, err := w.St.EarliestBarTs(ctx, s.ID, md.TF1d)
		if err != nil {
			return "", err
		}
		if earliest == 0 || earliest > shallowCut {
			shallow = append(shallow, s.Symbol)
		}
	}
	deepened := 0
	if w.Alpaca != nil && len(shallow) > 0 {
		counts, err := w.Alpaca.BackfillDailyMulti(ctx, w.St, shallow,
			func(sym string) (int64, bool) { id, ok := byName[sym]; return id, ok }, barsFrom)
		if err != nil {
			_ = w.St.InsertDQ(ctx, md.DQEvent{Ts: now.Unix(), Kind: "hist_backfill_error",
				Detail: "deep daily fetch: " + err.Error()})
		}
		deepened = len(counts) // pages persist as they arrive: partial coverage still counts
	}

	// (2) shared market context, built once for every symbol's pass.
	spyDaily, err := w.St.Bars(ctx, spy.ID, md.TF1d, 0, now.Unix()+1, 0)
	if err != nil {
		return "", err
	}
	vixRaw, err := w.St.MacroSeries(ctx, "VIXCLS", 0)
	if err != nil {
		return "", err
	}
	vix := make([]histfeat.Point, len(vixRaw))
	for i, p := range vixRaw {
		vix[i] = histfeat.Point{Ts: p.Ts, Value: p.Value}
	}
	mkt := histfeat.BuildMarketCtx(spyDaily, vix)

	// (3) weekly research rows per symbol, upserted in bounded batches.
	totalRows, rowSymbols := 0, 0
	weekSet := map[int64]struct{}{}
	eraCount := map[string]int{}
	for _, s := range syms {
		daily, err := w.St.Bars(ctx, s.ID, md.TF1d, 0, now.Unix()+1, 0)
		if err != nil {
			return "", err
		}
		if len(daily) < histMinDailyBars {
			continue
		}
		rows := histfeat.WeeklyRows(daily, mkt, rowsFrom.Unix())
		if len(rows) == 0 {
			continue
		}
		batch := make([]store.ResearchWeek, 0, histBackfillBatch)
		flush := func() error {
			if len(batch) == 0 {
				return nil
			}
			if err := w.St.UpsertResearchWeeks(ctx, batch, now.Unix()); err != nil {
				return err
			}
			batch = batch[:0]
			return nil
		}
		for _, r := range rows {
			batch = append(batch, store.ResearchWeek{
				SymbolID: s.ID, Week: r.Week, Ts: r.Ts, Vec: r.Vec,
				FwdReturn: r.FwdReturn, Up: r.Up, Era: r.Era, HighVol: r.HighVol,
			})
			weekSet[r.Week] = struct{}{}
			eraCount[r.Era]++
			if len(batch) == histBackfillBatch {
				if err := flush(); err != nil {
					return "", err
				}
			}
		}
		if err := flush(); err != nil {
			return "", err
		}
		totalRows += len(rows)
		rowSymbols++
	}

	if err := w.St.SetMeta(ctx, histBackfillDayKey, day); err != nil {
		return "", err
	}
	return fmt.Sprintf(
		"bars: deepened %d/%d symbols; weeks: %d rows / %d symbols / %d weeks; eras: %s; crypto skipped (Kraken depth ~2y — no fake history)",
		deepened, len(shallow), totalRows, rowSymbols, len(weekSet), eraSummary(eraCount)), nil
}

// findSymbol locates one symbol string in a listed set.
func findSymbol(syms []md.Symbol, symbol string) (md.Symbol, bool) {
	for _, s := range syms {
		if s.Symbol == symbol {
			return s, true
		}
	}
	return md.Symbol{}, false
}

// eraSummary renders per-era row counts in chronological era order.
func eraSummary(counts map[string]int) string {
	if len(counts) == 0 {
		return "none"
	}
	order := append([]string{histfeat.EraPreCovid}, histfeat.EraOrder()...)
	var parts []string
	for _, era := range order {
		if n := counts[era]; n > 0 {
			parts = append(parts, fmt.Sprintf("%s=%d", era, n))
		}
	}
	return strings.Join(parts, " ")
}

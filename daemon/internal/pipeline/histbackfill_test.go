package pipeline

import (
	"context"
	"strings"
	"testing"
	"time"

	"github.com/nyaungnicholas-wq/signaldeck/internal/histfeat"
	md "github.com/nyaungnicholas-wq/signaldeck/internal/marketdata"
	"github.com/nyaungnicholas-wq/signaldeck/internal/store"
)

// seedHistDailyBars writes nDays consecutive daily bars starting at start
// with a deterministic monotone close (up or down) so every forward label is
// known.
func seedHistDailyBars(t *testing.T, st *store.Store, id int64, start int64, nDays int, up bool) {
	t.Helper()
	bars := make([]md.Bar, nDays)
	for i := range bars {
		c := 100 + 0.5*float64(i)
		if !up {
			c = 500 - 0.5*float64(i)
		}
		bars[i] = md.Bar{
			SymbolID: id, TF: md.TF1d, Ts: start + int64(i)*86400,
			Open: c - 0.2, High: c + 0.5, Low: c - 0.5, Close: c, Volume: 1000 + float64(i%7),
		}
	}
	if err := st.UpsertBars(context.Background(), bars); err != nil {
		t.Fatalf("upsert bars: %v", err)
	}
}

// With Alpaca nil the worker computes labeled research_weeks rows purely from
// bars on disk, detects shallow symbols honestly, and gates to once per UTC
// day; a next-day recompute is idempotent.
func TestHistBackfillComputesLabeledWeeks(t *testing.T) {
	ctx := context.Background()
	st := newLedgerStore(t)

	start := time.Date(2019, 1, 1, 0, 0, 0, 0, time.UTC).Unix()
	const nDays = 590 // 2019-01-01 → 2020-08-12: a warmup year plus labeled 2020 anchors
	spy, err := st.UpsertSymbol(ctx, "SPY", md.Stocks, "")
	if err != nil {
		t.Fatalf("sym: %v", err)
	}
	upup, err := st.UpsertSymbol(ctx, "UPUP", md.Stocks, "")
	if err != nil {
		t.Fatalf("sym: %v", err)
	}
	dndn, err := st.UpsertSymbol(ctx, "DNDN", md.Stocks, "")
	if err != nil {
		t.Fatalf("sym: %v", err)
	}
	if _, err := st.UpsertSymbol(ctx, "SHLW", md.Stocks, ""); err != nil {
		t.Fatalf("sym: %v", err)
	}
	seedHistDailyBars(t, st, spy.ID, start, nDays, true)
	seedHistDailyBars(t, st, upup.ID, start, nDays, true)
	seedHistDailyBars(t, st, dndn.ID, start, nDays, false)
	// SHLW gets no bars: shallow, and far too thin for rows.

	// One VIX print before the window ⇒ every anchor joins high-vol context.
	if err := st.InsertMacro(ctx, "VIXCLS", start-86400, 30); err != nil {
		t.Fatalf("macro: %v", err)
	}

	w := &HistoryBackfillWorker{St: st, Now: func() time.Time { return time.Unix(1_600_000_000, 0) }}
	msg, err := w.Run(ctx)
	if err != nil {
		t.Fatalf("run: %v", err)
	}
	if !strings.Contains(msg, "deepened 0/1") {
		t.Errorf("detail %q: want shallow detection 'deepened 0/1' (SHLW only, no Alpaca client)", msg)
	}
	if !strings.Contains(msg, "crypto skipped") {
		t.Errorf("detail %q lacks the honest crypto-skip note", msg)
	}

	stats, err := st.ResearchWeeksStats(ctx)
	if err != nil {
		t.Fatalf("stats: %v", err)
	}
	if stats.Symbols != 3 {
		t.Errorf("stats.Symbols = %d, want 3 (SPY, UPUP, DNDN; SHLW has no bars)", stats.Symbols)
	}
	if stats.Rows < 60 {
		t.Errorf("stats.Rows = %d, want >= 60 (~30 labeled 2020 weeks per symbol)", stats.Rows)
	}
	if stats.ByEra[histfeat.EraCovidCrash] == 0 || stats.ByEra[histfeat.EraBull2021] == 0 {
		t.Errorf("ByEra lacks covid_crash/bull rows: %v", stats.ByEra)
	}

	rowsFrom := time.Date(2020, 1, 1, 0, 0, 0, 0, time.UTC).Unix()
	rows, err := st.ResearchWeeks(ctx, 0, stats.MaxTs/histfeat.WeekSecs, nil)
	if err != nil {
		t.Fatalf("rows: %v", err)
	}
	for _, r := range rows {
		switch r.SymbolID {
		case upup.ID:
			if !r.Up || r.FwdReturn <= 0 {
				t.Fatalf("UPUP week %d: Up=%v fwd=%.4f, want strictly positive label", r.Week, r.Up, r.FwdReturn)
			}
		case dndn.ID:
			if r.Up || r.FwdReturn >= 0 {
				t.Fatalf("DNDN week %d: Up=%v fwd=%.4f, want strictly negative label", r.Week, r.Up, r.FwdReturn)
			}
		}
		if !r.HighVol {
			t.Fatalf("week %d sym %d: HighVol=false with VIX 30 in context", r.Week, r.SymbolID)
		}
		if r.Ts < rowsFrom {
			t.Fatalf("row anchored before RowsFrom: ts %d", r.Ts)
		}
		if _, ok := r.Vec["pressure_score"]; !ok {
			t.Fatalf("week %d sym %d: row lacks pressure_score", r.Week, r.SymbolID)
		}
	}

	// Same day: gated.
	msg, err = w.Run(ctx)
	if err != nil || msg != "already ran today" {
		t.Fatalf("second run = (%q, %v), want the day gate", msg, err)
	}
	// Next day: recompute is idempotent (REPLACE on (symbol, week)).
	w.Now = func() time.Time { return time.Unix(1_600_000_000+86_400, 0) }
	if _, err := w.Run(ctx); err != nil {
		t.Fatalf("next-day run: %v", err)
	}
	stats2, err := st.ResearchWeeksStats(ctx)
	if err != nil {
		t.Fatalf("stats: %v", err)
	}
	if stats2.Rows != stats.Rows || stats2.Symbols != stats.Symbols {
		t.Errorf("recompute changed the evidence base: %d rows/%d syms -> %d rows/%d syms",
			stats.Rows, stats.Symbols, stats2.Rows, stats2.Symbols)
	}
}

// An empty universe still registers SPY (the MarketCtx anchor) as a
// daily-only symbol, reports the honest zero state, and consumes the day.
func TestHistBackfillEnsuresSPY(t *testing.T) {
	ctx := context.Background()
	st := newLedgerStore(t)
	w := &HistoryBackfillWorker{St: st, Now: func() time.Time { return time.Unix(1_600_000_000, 0) }}
	msg, err := w.Run(ctx)
	if err != nil {
		t.Fatalf("run: %v", err)
	}
	spy, err := st.GetSymbol(ctx, "SPY", md.Stocks)
	if err != nil {
		t.Fatalf("SPY not registered: %v", err)
	}
	if !spy.Active || spy.Stream {
		t.Errorf("SPY = active %v stream %v, want active daily-only", spy.Active, spy.Stream)
	}
	if !strings.Contains(msg, "deepened 0/1") {
		t.Errorf("detail %q: want SPY counted shallow with no Alpaca client", msg)
	}
	if !strings.Contains(msg, "0 rows") {
		t.Errorf("detail %q: want the honest zero-row state", msg)
	}
}

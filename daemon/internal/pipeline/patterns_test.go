// CANDLESTICK-PATTERNS wave pipeline test: the pattern-stats worker measures a
// hot symbol's candlestick edge from stored daily bars, honors the
// once-per-UTC-day gate, and skips the broad daily-only universe. Fixture data
// only.
package pipeline

import (
	"context"
	"strings"
	"testing"

	md "github.com/nyaungnicholas-wq/signaldeck/internal/marketdata"
	"github.com/nyaungnicholas-wq/signaldeck/internal/store"
)

// seedEngulfingBars writes ~180 daily bars whose repeating dip-then-rise motif
// prints a bullish engulfing at each local low — enough firings (~29) to clear
// candles.MinPatternN so a measured-edge row is produced.
func seedEngulfingBars(t *testing.T, st *store.Store, id int64, motifs int) {
	t.Helper()
	var bars []md.Bar
	ts := int64(1)
	L := 100.0
	push := func(o, h, l, c float64) {
		bars = append(bars, md.Bar{SymbolID: id, TF: md.TF1d, Ts: ts * 86400, Open: o, High: h, Low: l, Close: c, Volume: 1000})
		ts++
	}
	for m := 0; m < motifs; m++ {
		push(L+2, L+2.5, L-0.5, L)   // bearish
		push(L-1, L+4, L-1.5, L+3.5) // bullish engulfing at the low
		push(L+3, L+5, L+2.5, L+4.5) // steady rise
		push(L+4, L+6, L+3.5, L+5.5)
		push(L+5, L+7, L+4.5, L+6.5)
		push(L+6, L+8, L+5.5, L+7.5)
		L += 7
	}
	if err := st.UpsertBars(context.Background(), bars); err != nil {
		t.Fatalf("seed bars: %v", err)
	}
}

func hasPattern(rows []store.PatternStat, name string) (store.PatternStat, bool) {
	for _, r := range rows {
		if r.Pattern == name {
			return r, true
		}
	}
	return store.PatternStat{}, false
}

func TestPatternStatsRunner_WritesMeasuredEdge(t *testing.T) {
	st := openStore(t)
	ctx := context.Background()
	sym, _ := st.UpsertSymbol(ctx, "BTC/USD", md.Crypto, "Bitcoin") // crypto = hot
	seedEngulfingBars(t, st, sym.ID, 30)

	w := &PatternStatsRunner{St: st}
	detail, err := w.Run(ctx)
	if err != nil {
		t.Fatalf("run: %v", err)
	}
	if !strings.Contains(detail, "stat row(s) written") {
		t.Fatalf("detail=%q missing honest summary", detail)
	}
	rows, err := st.PatternStatsForSymbol(ctx, sym.ID)
	if err != nil {
		t.Fatalf("read stats: %v", err)
	}
	be, ok := hasPattern(rows, "bullish_engulfing")
	if !ok {
		t.Fatalf("bullish_engulfing edge not stored; rows=%v", rows)
	}
	if be.N < 15 {
		t.Fatalf("stored N=%d below MinPatternN gate", be.N)
	}
	if be.Horizon != patternHorizon {
		t.Fatalf("stored horizon=%d, want %d", be.Horizon, patternHorizon)
	}
}

func TestPatternStatsRunner_OncePerUTCDayGate(t *testing.T) {
	st := openStore(t)
	ctx := context.Background()
	sym, _ := st.UpsertSymbol(ctx, "BTC/USD", md.Crypto, "Bitcoin")
	seedEngulfingBars(t, st, sym.ID, 30)

	w := &PatternStatsRunner{St: st}
	if _, err := w.Run(ctx); err != nil {
		t.Fatalf("first run: %v", err)
	}
	detail, err := w.Run(ctx)
	if err != nil {
		t.Fatalf("second run: %v", err)
	}
	if !strings.Contains(detail, "already computed today") {
		t.Fatalf("second same-day run should be gated, got %q", detail)
	}
}

func TestPatternStatsRunner_SkipsDailyOnlyUniverse(t *testing.T) {
	st := openStore(t)
	ctx := context.Background()
	// A fresh stock is stream=0 (broad daily-only universe) → must be skipped.
	sym, _ := st.UpsertSymbol(ctx, "AAPL", md.Stocks, "Apple")
	seedEngulfingBars(t, st, sym.ID, 30)

	w := &PatternStatsRunner{St: st}
	if _, err := w.Run(ctx); err != nil {
		t.Fatalf("run: %v", err)
	}
	rows, err := st.PatternStatsForSymbol(ctx, sym.ID)
	if err != nil {
		t.Fatalf("read stats: %v", err)
	}
	if len(rows) != 0 {
		t.Fatalf("daily-only universe symbol should be skipped, got %d rows", len(rows))
	}
}

func TestPatternStatsRunner_HotStockProcessed(t *testing.T) {
	st := openStore(t)
	ctx := context.Background()
	sym, _ := st.UpsertSymbol(ctx, "AAPL", md.Stocks, "Apple")
	if err := st.SetSymbolStream(ctx, sym.ID, true); err != nil { // promote to hot set
		t.Fatalf("set stream: %v", err)
	}
	seedEngulfingBars(t, st, sym.ID, 30)

	w := &PatternStatsRunner{St: st}
	if _, err := w.Run(ctx); err != nil {
		t.Fatalf("run: %v", err)
	}
	rows, err := st.PatternStatsForSymbol(ctx, sym.ID)
	if err != nil {
		t.Fatalf("read stats: %v", err)
	}
	if _, ok := hasPattern(rows, "bullish_engulfing"); !ok {
		t.Fatalf("streamed hot stock should be measured; rows=%v", rows)
	}
}

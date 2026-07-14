// STRATEGY-LAB wave worker tests: the hot-set + crypto scope bound, the
// thin-bars skip, the once-per-UTC-day meta gate, the per-(symbol,strategy)
// result rows (all 8 classics, engine honesty flags carried), and the weekly
// fleet insight with the verbatim caveat. t.TempDir stores + deterministic
// fixture bars only.
package pipeline

import (
	"context"
	"math"
	"path/filepath"
	"strings"
	"testing"
	"time"

	md "github.com/nyaungnicholas-wq/signaldeck/internal/marketdata"
	"github.com/nyaungnicholas-wq/signaldeck/internal/store"
	"github.com/nyaungnicholas-wq/signaldeck/internal/stratlib"
)

func openStratLabStore(t *testing.T) *store.Store {
	t.Helper()
	st, err := store.Open(filepath.Join(t.TempDir(), "stratlab.db"))
	if err != nil {
		t.Fatalf("open store: %v", err)
	}
	t.Cleanup(func() { _ = st.Close() })
	return st
}

// seedDailyBars writes n ascending daily bars ending today (deterministic
// zig-zag uptrend so several strategies actually trade).
func seedDailyBars(t *testing.T, st *store.Store, symbolID int64, n int) {
	t.Helper()
	bars := make([]md.Bar, n)
	px := 100.0
	now := time.Now().UTC().Truncate(24 * time.Hour)
	for i := 0; i < n; i++ {
		px *= 1 + 0.008*math.Sin(float64(i)/5) + 0.001
		bars[i] = md.Bar{
			SymbolID: symbolID, TF: md.TF1d,
			Ts:   now.AddDate(0, 0, i-n+1).Unix(),
			Open: px, High: px * 1.004, Low: px * 0.996, Close: px, Volume: 1000,
		}
	}
	if err := st.UpsertBars(context.Background(), bars); err != nil {
		t.Fatalf("seed bars: %v", err)
	}
}

func TestStrategyLabWorker_EndToEnd(t *testing.T) {
	st := openStratLabStore(t)
	ctx := context.Background()

	hot, _ := st.UpsertSymbol(ctx, "NVDA", md.Stocks, "NVIDIA")
	if err := st.SetSymbolStream(ctx, hot.ID, true); err != nil {
		t.Fatalf("stream: %v", err)
	}
	thin, _ := st.UpsertSymbol(ctx, "AAPL", md.Stocks, "Apple")
	if err := st.SetSymbolStream(ctx, thin.ID, true); err != nil {
		t.Fatalf("stream: %v", err)
	}
	broad, _ := st.UpsertSymbol(ctx, "TSLA", md.Stocks, "Tesla") // stream=0
	crypto, _ := st.UpsertSymbol(ctx, "BTC/USD", md.Crypto, "Bitcoin")

	seedDailyBars(t, st, hot.ID, 504)
	seedDailyBars(t, st, thin.ID, 50) // below stratLabMinBars -> skipped
	seedDailyBars(t, st, broad.ID, 504)
	seedDailyBars(t, st, crypto.ID, 300)

	w := &StrategyLab{St: st}
	detail, err := w.Run(ctx)
	if err != nil {
		t.Fatalf("run: %v", err)
	}
	// Scope: hot + crypto ran; thin skipped; broad (stream=0 stock) NOT run.
	if !strings.Contains(detail, "over 2 symbol(s)") ||
		!strings.Contains(detail, "16 result rows") ||
		!strings.Contains(detail, "1 skipped") {
		t.Fatalf("detail wrong: %q", detail)
	}

	rows, err := st.StrategyResultsBySymbol(ctx, hot.ID)
	if err != nil {
		t.Fatalf("rows: %v", err)
	}
	if len(rows) != len(stratlib.All()) {
		t.Fatalf("want %d strategy rows for the hot symbol, got %d", len(stratlib.All()), len(rows))
	}
	for _, r := range rows {
		if r.NBars != 504 || r.Ts == 0 {
			t.Fatalf("row provenance wrong: %+v", r)
		}
		// HONESTY: a strategy that never traded must not report a meaningful
		// win rate, and a low-trade result must not report CAGR.
		if r.NTrades < 20 && r.CAGRReported {
			t.Fatalf("CAGR reported on %d trades: %+v", r.NTrades, r)
		}
	}
	if got, _ := st.StrategyResultsBySymbol(ctx, broad.ID); len(got) != 0 {
		t.Fatalf("broad-universe symbol must not be backtested (bound the compute): %d rows", len(got))
	}
	if got, _ := st.StrategyResultsBySymbol(ctx, thin.ID); len(got) != 0 {
		t.Fatalf("thin symbol must be skipped: %d rows", len(got))
	}

	// Weekly insight written with the verbatim caveat.
	if !strings.Contains(detail, "weekly insight written") {
		t.Fatalf("expected the weekly insight on first run: %q", detail)
	}
	ins, err := st.RecentInsights(ctx, 0, 5)
	if err != nil || len(ins) == 0 {
		t.Fatalf("insights: %v %v", ins, err)
	}
	if !strings.Contains(ins[0].Body, "Top strategies fleet-wide by median Sharpe") ||
		!strings.Contains(ins[0].Body, "classic published strategies backtested walk-forward on our own bars with costs — in-sample history, not live performance and not advice; a strategy is only as good as its next trade") {
		t.Fatalf("insight body must carry top-3 + verbatim caveat: %q", ins[0].Body)
	}
	if !strings.Contains(ins[0].Data, `"kind":"strategy_lab"`) {
		t.Fatalf("insight kind missing: %q", ins[0].Data)
	}

	// Once-per-UTC-day gate: the second run is an honest no-op.
	detail2, err := w.Run(ctx)
	if err != nil {
		t.Fatalf("run 2: %v", err)
	}
	if !strings.Contains(detail2, "up to date") {
		t.Fatalf("day gate broken: %q", detail2)
	}
}

func TestStrategyLabWorker_EmptyFleet(t *testing.T) {
	st := openStratLabStore(t)
	w := &StrategyLab{St: st}
	detail, err := w.Run(context.Background())
	if err != nil {
		t.Fatalf("run: %v", err)
	}
	if !strings.Contains(detail, "over 0 symbol(s)") ||
		!strings.Contains(detail, "insight skipped: no strategy results") {
		t.Fatalf("expected an honest empty pass: %q", detail)
	}
}

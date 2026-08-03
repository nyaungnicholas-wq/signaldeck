// Tests for the remaining prediction/regime/ranking read paths, the linear
// forecast (quant) row, the self-audit latest-per-metric reads, and the
// regime-forecast surfaces.
package store

import (
	"context"
	"testing"

	md "github.com/nyaungnicholas-wq/signaldeck/internal/marketdata"
	"github.com/nyaungnicholas-wq/signaldeck/internal/structregime"
)

func TestLatestPredictionAndUnresolvedQueue(t *testing.T) {
	st := openTemp(t)
	ctx := context.Background()

	sym, _ := st.UpsertSymbol(ctx, "AAPL", md.Stocks, "Apple")
	if _, ok, err := st.LatestPrediction(ctx, sym.ID, md.H1d); err != nil || ok {
		t.Fatalf("absent prediction = ok=%v, %v; want false", ok, err)
	}

	for _, ts := range []int64{1000, 2000} {
		if err := st.UpsertPrediction(ctx, Prediction{SymbolID: sym.ID,
			Horizon: md.H1d, Ts: ts, RawProb: 0.55, CalProb: 0.60, NUsed: 12,
			Components: `{"gbm":0.6}`}); err != nil {
			t.Fatalf("upsert prediction: %v", err)
		}
	}
	p, ok, err := st.LatestPrediction(ctx, sym.ID, md.H1d)
	if err != nil || !ok || p.Ts != 2000 || p.CalProb != 0.60 || p.Components != `{"gbm":0.6}` {
		t.Fatalf("LatestPrediction = %+v, %v, %v", p, ok, err)
	}

	// A row is only offered once a bar exists at/after its target, so give the
	// symbol forward bars past both predictions' horizons.
	const hs = int64(100) // horizon seconds used by this test's arithmetic
	if err := st.UpsertBars(ctx, []md.Bar{
		{SymbolID: sym.ID, TF: md.TF1d, Ts: 1000 + hs, Open: 1, High: 1, Low: 1, Close: 1, Volume: 1},
		{SymbolID: sym.ID, TF: md.TF1d, Ts: 2000 + hs, Open: 1, High: 1, Low: 1, Close: 1, Volume: 1},
	}); err != nil {
		t.Fatalf("upsert bars: %v", err)
	}

	// Both rows pend; the cutoff hides the newer one.
	pend, err := st.UnresolvedPredictions(ctx, md.H1d, 1500, hs, 10)
	if err != nil || len(pend) != 1 || pend[0].Ts != 1000 || pend[0].Prob != 0.60 {
		t.Fatalf("UnresolvedPredictions cutoff = %+v, %v", pend, err)
	}
	if err := st.ResolvePrediction(ctx, sym.ID, md.H1d, 1000, 0.01); err != nil {
		t.Fatalf("resolve: %v", err)
	}
	pend, err = st.UnresolvedPredictions(ctx, md.H1d, 5000, hs, 10)
	if err != nil || len(pend) != 1 || pend[0].Ts != 2000 {
		t.Fatalf("resolved row still pending: %+v, %v", pend, err)
	}
}

// TestUnresolvedPredictionsSkipsSymbolsWithNoForwardBar pins the head-of-line
// fix: a prediction on a symbol whose bars stopped can never be graded, and it
// must not occupy a slot in the oldest-first batch. 992 such rows (WBA, PARA,
// MRO and other delisted tickers) were consuming two thirds of every pass.
func TestUnresolvedPredictionsSkipsSymbolsWithNoForwardBar(t *testing.T) {
	st := openTemp(t)
	ctx := context.Background()
	const hs = int64(100)

	dead, _ := st.UpsertSymbol(ctx, "WBA", md.Stocks, "Walgreens")
	live, _ := st.UpsertSymbol(ctx, "AAPL", md.Stocks, "Apple")

	// The dead symbol's prediction is OLDER, so oldest-first ordering would put
	// it first if it were offered at all.
	for _, p := range []struct {
		id int64
		ts int64
	}{{dead.ID, 1000}, {live.ID, 2000}} {
		if err := st.UpsertPrediction(ctx, Prediction{SymbolID: p.id,
			Horizon: md.H1d, Ts: p.ts, RawProb: 0.5, CalProb: 0.5, NUsed: 1}); err != nil {
			t.Fatalf("upsert prediction: %v", err)
		}
	}
	// Bars stop BEFORE the dead symbol's target; the live one has a forward bar.
	if err := st.UpsertBars(ctx, []md.Bar{
		{SymbolID: dead.ID, TF: md.TF1d, Ts: 1000, Open: 1, High: 1, Low: 1, Close: 1, Volume: 1},
		{SymbolID: live.ID, TF: md.TF1d, Ts: 2000 + hs, Open: 1, High: 1, Low: 1, Close: 1, Volume: 1},
	}); err != nil {
		t.Fatalf("upsert bars: %v", err)
	}

	pend, err := st.UnresolvedPredictions(ctx, md.H1d, 9000, hs, 10)
	if err != nil {
		t.Fatalf("UnresolvedPredictions: %v", err)
	}
	if len(pend) != 1 {
		t.Fatalf("got %d pending rows, want 1 — the ungradable row must be skipped, not queued", len(pend))
	}
	if pend[0].SymbolID != live.ID {
		t.Fatalf("queued symbol %d, want the live one (%d)", pend[0].SymbolID, live.ID)
	}

	// It is skipped, NOT resolved: the outcome is genuinely unknown and the row
	// must stay on the books.
	var n int
	if err := st.DB().QueryRow(
		`SELECT COUNT(*) FROM prediction_outcomes WHERE symbol_id=? AND resolved_at IS NULL`,
		dead.ID).Scan(&n); err != nil {
		t.Fatalf("count dead rows: %v", err)
	}
	if n != 1 {
		t.Fatalf("the ungradable row was altered (%d unresolved rows left); it must be preserved", n)
	}
}

func TestRegimesAndRecentChangesJoins(t *testing.T) {
	st := openTemp(t)
	ctx := context.Background()

	a, _ := st.UpsertSymbol(ctx, "AAPL", md.Stocks, "Apple")
	b, _ := st.UpsertSymbol(ctx, "BTC/USD", md.Crypto, "Bitcoin")
	if err := st.UpsertRegime(ctx, a.ID, 1000, "range", 0.3, "n1"); err != nil {
		t.Fatalf("upsert regime: %v", err)
	}
	if err := st.UpsertRegime(ctx, a.ID, 2000, "uptrend", 0.9, "n2"); err != nil {
		t.Fatalf("upsert regime: %v", err)
	}
	if err := st.UpsertRegime(ctx, b.ID, 1500, "downtrend", 0.7, ""); err != nil {
		t.Fatalf("upsert regime: %v", err)
	}

	regs, err := st.Regimes(ctx)
	if err != nil || len(regs) != 2 {
		t.Fatalf("Regimes = %d rows, %v; want 2 (latest per symbol)", len(regs), err)
	}
	// Ordered market then symbol: crypto BTC first, then stocks AAPL.
	if regs[0].Symbol != "BTC/USD" || regs[1].Symbol != "AAPL" || regs[1].Label != "uptrend" {
		t.Fatalf("Regimes order/content wrong: %+v", regs)
	}

	chs, err := st.RecentRegimeChanges(ctx, 10)
	if err != nil || len(chs) != 1 {
		t.Fatalf("RecentRegimeChanges = %d rows, %v; want 1", len(chs), err)
	}
	if chs[0].Symbol != "AAPL" || chs[0].From != "range" || chs[0].To != "uptrend" {
		t.Fatalf("change row mangled: %+v", chs[0])
	}
}

func TestLatestRankingAndBreakoutReads(t *testing.T) {
	st := openTemp(t)
	ctx := context.Background()

	a, _ := st.UpsertSymbol(ctx, "AAPL", md.Stocks, "Apple")
	b, _ := st.UpsertSymbol(ctx, "MSFT", md.Stocks, "Microsoft")
	type rrow = struct {
		SymbolID     int64
		Score        float64
		Rank         int
		Ret1M, Ret3M float64
	}
	if err := st.ReplaceRanking(ctx, 1000, []rrow{
		{SymbolID: b.ID, Score: 50, Rank: 1, Ret1M: 0.1, Ret3M: 0.3},
	}); err != nil {
		t.Fatalf("replace ranking: %v", err)
	}
	if err := st.ReplaceRanking(ctx, 2000, []rrow{
		{SymbolID: a.ID, Score: 90, Rank: 1, Ret1M: 0.2, Ret3M: 0.4},
		{SymbolID: b.ID, Score: 40, Rank: 2, Ret1M: 0.0, Ret3M: 0.1},
	}); err != nil {
		t.Fatalf("replace ranking: %v", err)
	}
	rk, err := st.LatestRanking(ctx)
	if err != nil || len(rk) != 2 {
		t.Fatalf("LatestRanking = %d rows, %v; want 2 (newest snapshot only)", len(rk), err)
	}
	if rk[0].Symbol != "AAPL" || rk[0].Rank != 1 || rk[0].Ret3M != 0.4 || rk[1].Symbol != "MSFT" {
		t.Fatalf("ranking rows wrong: %+v", rk)
	}

	if err := st.InsertBreakout(ctx, &a.ID, 1000, "volume_spike", "d1", 2.0); err != nil {
		t.Fatalf("insert breakout: %v", err)
	}
	if err := st.InsertBreakout(ctx, &a.ID, 3000, "volume_spike", "d2", 2.5); err != nil {
		t.Fatalf("insert breakout: %v", err)
	}
	if err := st.InsertBreakout(ctx, nil, 2000, "corr_break", "d3", 1.5); err != nil {
		t.Fatalf("insert breakout: %v", err)
	}
	bs, err := st.RecentBreakouts(ctx, 2)
	if err != nil || len(bs) != 2 {
		t.Fatalf("RecentBreakouts = %d rows, %v; want 2", len(bs), err)
	}
	// Newest first; the watchlist-wide row joins to an empty symbol.
	if bs[0].Ts != 3000 || bs[0].Symbol != "AAPL" || bs[1].Ts != 2000 || bs[1].Symbol != "" {
		t.Fatalf("breakout rows wrong: %+v", bs)
	}
	ts, err := st.LastBreakoutTs(ctx, a.ID, "volume_spike")
	if err != nil || ts != 3000 {
		t.Fatalf("LastBreakoutTs = %d, %v; want 3000", ts, err)
	}
	if ts, _ := st.LastBreakoutTs(ctx, a.ID, "corr_break"); ts != 0 {
		t.Fatalf("LastBreakoutTs wrong-kind = %d; want 0", ts)
	}
}

func TestQuantForecastRoundTrip(t *testing.T) {
	st := openTemp(t)
	ctx := context.Background()

	sym, _ := st.UpsertSymbol(ctx, "AAPL", md.Stocks, "Apple")
	f := Forecast{SymbolID: sym.ID, Horizon: md.H1d, Ts: 1000, Prob: 0.58,
		Accuracy: 0.54, Brier: 0.24, AUC: 0.55, BaseRate: 0.52, Lift: 0.02,
		NTrain: 300, NEval: 60}
	if err := st.UpsertForecast(ctx, f); err != nil {
		t.Fatalf("upsert forecast: %v", err)
	}
	// Replace in place on (symbol, horizon).
	f.Ts, f.Prob = 2000, 0.61
	if err := st.UpsertForecast(ctx, f); err != nil {
		t.Fatalf("re-upsert forecast: %v", err)
	}
	fs, err := st.Forecasts(ctx, sym.ID)
	if err != nil || len(fs) != 1 {
		t.Fatalf("Forecasts = %d rows, %v; want 1", len(fs), err)
	}
	if fs[0].Ts != 2000 || fs[0].Prob != 0.61 || fs[0].NTrain != 300 {
		t.Fatalf("forecast mangled: %+v", fs[0])
	}
}

func TestSelfAudit_LatestPerMetricAndLastMeasured(t *testing.T) {
	st := openTemp(t)
	ctx := context.Background()

	put := func(ts int64, metric, status string, v float64) {
		t.Helper()
		if err := st.InsertSelfAudit(ctx, SelfAuditRow{Ts: ts, Metric: metric,
			Value: v, Status: status, Detail: "d"}); err != nil {
			t.Fatalf("insert self audit: %v", err)
		}
	}
	put(100, "calibration", "ok", 0.90)
	put(200, "calibration", "ok", 0.85)
	put(100, "factor_ic:gbm", "ok", 0.04)
	put(200, "factor_ic:gbm", "insufficient", 0)

	latest, err := st.LatestSelfAudit(ctx)
	if err != nil || len(latest) != 2 {
		t.Fatalf("LatestSelfAudit = %d rows, %v; want 2", len(latest), err)
	}
	for _, r := range latest {
		if r.Metric == "calibration" && r.Value != 0.85 {
			t.Fatalf("latest calibration = %+v; want ts=200 value", r)
		}
	}

	// The drift comparator must skip the insufficient placeholder.
	if v, ok := st.LastSelfAuditValue(ctx, "factor_ic:gbm"); !ok || v != 0.04 {
		t.Fatalf("LastSelfAuditValue = %v, %v; want 0.04 (measured)", v, ok)
	}
	if _, ok := st.LastSelfAuditValue(ctx, "never_measured"); ok {
		t.Fatal("LastSelfAuditValue invented a prior")
	}
}

func TestRegimeForecastSurfacesAndDelete(t *testing.T) {
	st := openTemp(t)
	ctx := context.Background()

	a, _ := st.UpsertSymbol(ctx, "AAPL", md.Stocks, "Apple")
	b, _ := st.UpsertSymbol(ctx, "MSFT", md.Stocks, "Microsoft")
	put := func(symID int64, kind, regime string, conviction float64) {
		t.Helper()
		if err := st.UpsertRegimeForecast(ctx, symID, 1000, structregime.Forecast{
			Kind: structregime.Kind(kind), HorizonDays: 21, Regime: regime,
			Conviction: conviction, HistoricalAccuracy: 0.6, Tier: "high",
			Rank: 0.8, N: 40}); err != nil {
			t.Fatalf("upsert regime forecast: %v", err)
		}
	}
	put(a.ID, "trend21", "uptrend", 0.9)
	put(a.ID, "gapfill", "fills", 0.5)
	put(b.ID, "trend21", "downtrend", 0.7)

	mine, err := st.RegimeForecastsForSymbol(ctx, a.ID)
	if err != nil || len(mine) != 2 {
		t.Fatalf("forecasts for symbol = %d rows, %v; want 2", len(mine), err)
	}

	all, err := st.RegimeForecasts(ctx)
	if err != nil || len(all) != 3 {
		t.Fatalf("all forecasts = %d rows, %v; want 3", len(all), err)
	}
	// Within a kind, highest conviction first.
	var trendRows []RegimeForecast
	for _, r := range all {
		if r.Kind == "trend21" {
			trendRows = append(trendRows, r)
		}
	}
	if len(trendRows) != 2 || trendRows[0].Symbol != "AAPL" || trendRows[0].Conviction != 0.9 {
		t.Fatalf("conviction ordering wrong: %+v", trendRows)
	}

	// A stale event forecast is deleted outright, not left to lie.
	if err := st.DeleteRegimeForecast(ctx, a.ID, structregime.Kind("gapfill")); err != nil {
		t.Fatalf("delete: %v", err)
	}
	if mine, _ := st.RegimeForecastsForSymbol(ctx, a.ID); len(mine) != 1 || mine[0].Kind != "trend21" {
		t.Fatalf("delete removed the wrong row: %+v", mine)
	}
}

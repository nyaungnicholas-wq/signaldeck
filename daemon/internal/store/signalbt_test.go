package store

import (
	"context"
	"testing"

	md "github.com/nyaungnicholas-wq/signaldeck/internal/marketdata"
)

// TestSignalBacktestObs_AssemblesResolvedWithMultiLag verifies the own-signal
// backtest assembly: only RESOLVED predictions become observations, the signal
// is the calibrated prob, the primary-lag forward return is the stored
// fwd_return, and the EXTRA lags are recomputed from realized daily bars by
// trading-day offset — with a still-open lag left ABSENT (no lookahead).
func TestSignalBacktestObs_AssemblesResolvedWithMultiLag(t *testing.T) {
	st := openTemp(t)
	ctx := context.Background()
	sym, err := st.UpsertSymbol(ctx, "AAPL", md.Stocks, "Apple")
	if err != nil {
		t.Fatal(err)
	}

	// Six consecutive daily bars: closes 100,101,103,106,110,115. Trading-day
	// lags off day 0 (ts=day0): lag1=101/100-1, lag3=106/100-1, lag5=115/100-1.
	day := int64(86400)
	closes := []float64{100, 101, 103, 106, 110, 115}
	bars := make([]md.Bar, len(closes))
	for i, c := range closes {
		bars[i] = md.Bar{SymbolID: sym.ID, TF: md.TF1d, Ts: int64(i) * day, Open: c, High: c, Low: c, Close: c}
	}
	if err := st.UpsertBars(ctx, bars); err != nil {
		t.Fatal(err)
	}

	// A resolved prediction at ts=day0 with calibrated prob 0.7 and the stored
	// primary (1d) fwd_return = 101/100-1 = 0.01.
	if err := st.UpsertPrediction(ctx, Prediction{
		SymbolID: sym.ID, Horizon: md.H1d, Ts: 0, RawProb: 0.72, CalProb: 0.7, NUsed: 2, Components: "{}",
	}); err != nil {
		t.Fatal(err)
	}
	if err := st.ResolvePrediction(ctx, sym.ID, md.H1d, 0, 0.01); err != nil {
		t.Fatal(err)
	}
	// An UNRESOLVED prediction at ts=day1 must NOT appear.
	if err := st.UpsertPrediction(ctx, Prediction{
		SymbolID: sym.ID, Horizon: md.H1d, Ts: day, RawProb: 0.5, CalProb: 0.5, NUsed: 1, Components: "{}",
	}); err != nil {
		t.Fatal(err)
	}

	obs, err := st.SignalBacktestObs(ctx, md.H1d, []int{3, 5, 10}, 100)
	if err != nil {
		t.Fatalf("assemble: %v", err)
	}
	if len(obs) != 1 {
		t.Fatalf("want exactly the 1 resolved obs, got %d", len(obs))
	}
	o := obs[0]
	if o.SymbolID != sym.ID || o.Ts != 0 {
		t.Fatalf("bad obs identity: %+v", o)
	}
	if o.Signal != 0.7 {
		t.Fatalf("signal = %v, want the calibrated 0.7", o.Signal)
	}
	// Primary lag (1) from the stored fwd_return.
	if !approxEq(o.FwdByLag[1], 0.01) {
		t.Fatalf("primary lag fwd = %v, want 0.01", o.FwdByLag[1])
	}
	// Extra lag 3: 106/100-1 = 0.06.
	if !approxEq(o.FwdByLag[3], 0.06) {
		t.Fatalf("lag3 fwd = %v, want 0.06", o.FwdByLag[3])
	}
	// Extra lag 5: 115/100-1 = 0.15.
	if !approxEq(o.FwdByLag[5], 0.15) {
		t.Fatalf("lag5 fwd = %v, want 0.15", o.FwdByLag[5])
	}
	// Lag 10 has no realized forward bar (series is only 6 bars) → ABSENT.
	if _, ok := o.FwdByLag[10]; ok {
		t.Fatalf("lag10 must be absent (forward bar not realized) — no lookahead")
	}
}

// TestSignalBacktestObs_1wPrimaryIsFiveBars checks that a 1w horizon's primary
// lag is 5 trading-day bars (matching md.BarsPerHorizon).
func TestSignalBacktestObs_1wPrimaryIsFiveBars(t *testing.T) {
	st := openTemp(t)
	ctx := context.Background()
	sym, err := st.UpsertSymbol(ctx, "MSFT", md.Stocks, "")
	if err != nil {
		t.Fatal(err)
	}
	if err := st.UpsertPrediction(ctx, Prediction{
		SymbolID: sym.ID, Horizon: md.H1w, Ts: 0, RawProb: 0.6, CalProb: 0.58, NUsed: 2, Components: "{}",
	}); err != nil {
		t.Fatal(err)
	}
	if err := st.ResolvePrediction(ctx, sym.ID, md.H1w, 0, 0.05); err != nil {
		t.Fatal(err)
	}
	obs, err := st.SignalBacktestObs(ctx, md.H1w, nil, 100)
	if err != nil {
		t.Fatal(err)
	}
	if len(obs) != 1 {
		t.Fatalf("want 1 obs, got %d", len(obs))
	}
	if _, ok := obs[0].FwdByLag[5]; !ok {
		t.Fatalf("1w primary lag must be 5 (bars), got keys %v", obs[0].FwdByLag)
	}
	if !approxEq(obs[0].FwdByLag[5], 0.05) {
		t.Fatalf("1w primary fwd = %v, want 0.05", obs[0].FwdByLag[5])
	}
}

// TestSignalBacktestObs_VoidedOutcomeExcluded ensures a resolved-but-voided
// outcome (up/fwd_return NULL) never becomes a labeled observation.
func TestSignalBacktestObs_VoidedOutcomeExcluded(t *testing.T) {
	st := openTemp(t)
	ctx := context.Background()
	sym, _ := st.UpsertSymbol(ctx, "AAPL", md.Stocks, "")
	if err := st.UpsertPrediction(ctx, Prediction{
		SymbolID: sym.ID, Horizon: md.H1d, Ts: 0, RawProb: 0.5, CalProb: 0.5, NUsed: 1, Components: "{}",
	}); err != nil {
		t.Fatal(err)
	}
	// No ResolvePrediction call → unresolved → excluded.
	obs, err := st.SignalBacktestObs(ctx, md.H1d, nil, 100)
	if err != nil {
		t.Fatal(err)
	}
	if len(obs) != 0 {
		t.Fatalf("unresolved prediction leaked into the obs set: %d", len(obs))
	}
}

// TestSPYDailyCloses_EmptyWhenUntracked confirms an honest empty benchmark when
// SPY is not a tracked symbol (no error, no fabricated data).
func TestSPYDailyCloses_EmptyWhenUntracked(t *testing.T) {
	st := openTemp(t)
	ctx := context.Background()
	ts, closes, err := st.SPYDailyCloses(ctx, 100)
	if err != nil {
		t.Fatalf("SPY lookup should not error when untracked: %v", err)
	}
	if len(ts) != 0 || len(closes) != 0 {
		t.Fatalf("want empty SPY series when untracked, got %d/%d", len(ts), len(closes))
	}
}

func TestSPYDailyCloses_ReturnsAscendingSeries(t *testing.T) {
	st := openTemp(t)
	ctx := context.Background()
	sym, _ := st.UpsertSymbol(ctx, "SPY", md.Stocks, "S&P 500 ETF")
	day := int64(86400)
	if err := st.UpsertBars(ctx, []md.Bar{
		{SymbolID: sym.ID, TF: md.TF1d, Ts: 2 * day, Close: 420},
		{SymbolID: sym.ID, TF: md.TF1d, Ts: 0 * day, Close: 400},
		{SymbolID: sym.ID, TF: md.TF1d, Ts: 1 * day, Close: 410},
	}); err != nil {
		t.Fatal(err)
	}
	ts, closes, err := st.SPYDailyCloses(ctx, 100)
	if err != nil {
		t.Fatal(err)
	}
	if len(ts) != 3 || len(closes) != 3 {
		t.Fatalf("want 3 SPY bars, got %d/%d", len(ts), len(closes))
	}
	// Ascending by ts.
	if ts[0] >= ts[1] || ts[1] >= ts[2] {
		t.Fatalf("SPY series not ascending: %v", ts)
	}
	if closes[0] != 400 || closes[2] != 420 {
		t.Fatalf("SPY closes not aligned to ascending ts: %v", closes)
	}
}

func approxEq(a, b float64) bool {
	d := a - b
	if d < 0 {
		d = -d
	}
	return d <= 1e-9
}

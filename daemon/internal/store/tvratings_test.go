package store

import (
	"context"
	"math"
	"testing"

	md "github.com/nyaungnicholas-wq/signaldeck/internal/marketdata"
)

// TestTVRatings_ExchangeCacheAndRatings covers the TradingView-scanner store
// surface: the missing-exchange sweep, the exchange cache upsert + bulk lookup,
// and rating upsert + latest read (incl. same-second REPLACE and honest absence).
func TestTVRatings_ExchangeCacheAndRatings(t *testing.T) {
	st := openTemp(t)
	ctx := context.Background()

	nvda, err := st.UpsertSymbol(ctx, "NVDA", md.Stocks, "NVIDIA")
	if err != nil {
		t.Fatalf("upsert NVDA: %v", err)
	}
	aapl, err := st.UpsertSymbol(ctx, "AAPL", md.Stocks, "Apple")
	if err != nil {
		t.Fatalf("upsert AAPL: %v", err)
	}

	// Both start with NO exchange row → both pending.
	missing, err := st.SymbolsMissingExchange(ctx, string(md.Stocks), 60)
	if err != nil {
		t.Fatalf("missing: %v", err)
	}
	if len(missing) != 2 {
		t.Fatalf("want 2 missing, got %d", len(missing))
	}

	// Resolve NVDA → NASDAQ; now only AAPL is pending.
	if err := st.UpsertExchange(ctx, nvda.ID, "NASDAQ"); err != nil {
		t.Fatalf("upsert exch: %v", err)
	}
	missing, _ = st.SymbolsMissingExchange(ctx, string(md.Stocks), 60)
	if len(missing) != 1 || missing[0].ID != aapl.ID {
		t.Fatalf("want only AAPL pending, got %+v", missing)
	}

	// Bulk exchange lookup returns only the resolved one.
	exMap, err := st.ExchangesFor(ctx, []int64{nvda.ID, aapl.ID})
	if err != nil {
		t.Fatalf("ExchangesFor: %v", err)
	}
	if exMap[nvda.ID] != "NASDAQ" {
		t.Errorf("NVDA exch = %q", exMap[nvda.ID])
	}
	if _, ok := exMap[aapl.ID]; ok {
		t.Error("AAPL should have no cached exchange")
	}

	// No rating yet → honest absence.
	if _, ok := st.LatestTVRating(ctx, nvda.ID); ok {
		t.Error("expected no rating yet")
	}

	// Upsert a rating, then a newer one; latest wins.
	if err := st.UpsertTVRating(ctx, TVRatingRow{
		SymbolID: nvda.ID, Ts: 1000, RecoAll: 0.55, RecoMA: 0.9, RecoOther: 0.18,
		RSI: 57, Close: 210, Label: "Strong Buy",
	}); err != nil {
		t.Fatalf("upsert rating: %v", err)
	}
	if err := st.UpsertTVRating(ctx, TVRatingRow{
		SymbolID: nvda.ID, Ts: 2000, RecoAll: -0.6, RecoMA: -0.7, RecoOther: -0.5,
		RSI: 40, Close: 200, Label: "Strong Sell",
	}); err != nil {
		t.Fatalf("upsert rating 2: %v", err)
	}
	got, ok := st.LatestTVRating(ctx, nvda.ID)
	if !ok {
		t.Fatal("expected a rating")
	}
	if got.Ts != 2000 || got.Label != "Strong Sell" || got.RecoAll != -0.6 || got.Close != 200 {
		t.Errorf("latest mismatch: %+v", got)
	}

	// Same-second REPLACE: re-upsert Ts=2000 with a corrected value.
	if err := st.UpsertTVRating(ctx, TVRatingRow{
		SymbolID: nvda.ID, Ts: 2000, RecoAll: -0.55, RSI: 41, Close: 201, Label: "Strong Sell",
	}); err != nil {
		t.Fatalf("re-upsert: %v", err)
	}
	got, _ = st.LatestTVRating(ctx, nvda.ID)
	if got.RecoAll != -0.55 || got.Close != 201 {
		t.Errorf("REPLACE didn't win: %+v", got)
	}
}

// TestTVRatingSkill measures the external rating against realized next-day
// returns: an empty history is honest zero (N=0), and a rating whose sign
// perfectly tracks the next day's move yields a positive IC + full hit rate
// over the independent (symbol, UTC-day) observations.
func TestTVRatingSkill(t *testing.T) {
	st := openTemp(t)
	ctx := context.Background()

	// No ratings yet → honest absence (the composite renders the gate).
	if sk, err := st.TVRatingSkill(ctx); err != nil || sk.N != 0 {
		t.Fatalf("empty skill = %+v err=%v, want N=0", sk, err)
	}

	sym, err := st.UpsertSymbol(ctx, "SPY", md.Stocks, "S&P 500 ETF")
	if err != nil {
		t.Fatal(err)
	}

	// 45 daily bars at 05:00 UTC (US daily open), returns alternating ±2%.
	const day = 86400
	base := int64(1_600_000_000)/day*day + 5*3600
	closes := make([]float64, 45)
	closes[0] = 100
	bars := []md.Bar{{SymbolID: sym.ID, TF: md.TF1d, Ts: base, Open: 100, High: 100, Low: 100, Close: 100}}
	for i := 1; i < 45; i++ {
		r := 0.02
		if i%2 == 0 {
			r = -0.02
		}
		closes[i] = closes[i-1] * (1 + r)
		ts := base + int64(i)*day
		closes[i] = math.Round(closes[i]*1e6) / 1e6
		bars = append(bars, md.Bar{SymbolID: sym.ID, TF: md.TF1d, Ts: ts,
			Open: closes[i], High: closes[i], Low: closes[i], Close: closes[i]})
	}
	if err := st.UpsertBars(ctx, bars); err != nil {
		t.Fatal(err)
	}

	// A rating just after each bar (except the last, which has no next day) whose
	// sign matches the realized next-day move — perfect directional skill.
	resolvable := 0
	for i := 0; i < 44; i++ {
		fwd := closes[i+1]/closes[i] - 1
		reco := 0.5
		if fwd < 0 {
			reco = -0.5
		}
		if err := st.UpsertTVRating(ctx, TVRatingRow{
			SymbolID: sym.ID, Ts: base + int64(i)*day + 100, RecoAll: reco, Label: "x",
		}); err != nil {
			t.Fatal(err)
		}
		resolvable++
	}

	sk, err := st.TVRatingSkill(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if sk.N != resolvable {
		t.Errorf("N = %d, want %d independent obs", sk.N, resolvable)
	}
	if sk.HitRate < 0.999 {
		t.Errorf("hitRate = %.3f, want ~1.0 (rating sign tracks the move)", sk.HitRate)
	}
	if sk.IC <= 0.5 {
		t.Errorf("IC = %.3f, want strongly positive", sk.IC)
	}
	if sk.N < TVRatingSkillGateN {
		t.Errorf("N=%d should clear the 30-obs gate for this fixture", sk.N)
	}
}

// TVRatingSkillGateN mirrors composite.TVRatingMinN for the test's gate check
// (kept local so the store package stays free of a composite import).
const TVRatingSkillGateN = 30

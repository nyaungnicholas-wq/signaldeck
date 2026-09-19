package store

import (
	"context"
	"testing"

	"github.com/nyaungnicholas-wq/signaldeck/internal/marketdata"
	"github.com/nyaungnicholas-wq/signaldeck/internal/volregime"
)

func TestVolForecastForSymbol_MissingIsNotAnError(t *testing.T) {
	st := openTestStore(t)
	ctx := context.Background()

	sym, err := st.UpsertSymbol(ctx, "AAPL", marketdata.Stocks, "Apple")
	if err != nil {
		t.Fatalf("got = %v, want nil", err)
	}

	fc, ok, err := st.VolForecastForSymbol(ctx, sym.ID)
	if err != nil {
		t.Fatalf("got = %v, want nil", err)
	}
	if ok {
		t.Fatalf("got = %v, want false", ok)
	}
	if fc.Symbol != "" || fc.Market != "" || fc.Ts != 0 || fc.Regime != "" || fc.Conviction != 0 || fc.HistoricalAccuracy != 0 || fc.Tier != "" || fc.Rank != 0 || fc.N != 0 {
		t.Fatalf("got = %v, want zero VolForecast", fc)
	}
}

func TestUpsertVolForecast_ReplacesInPlace(t *testing.T) {
	st := openTestStore(t)
	ctx := context.Background()

	sym, err := st.UpsertSymbol(ctx, "AAPL", marketdata.Stocks, "Apple")
	if err != nil {
		t.Fatalf("got = %v, want nil", err)
	}

	f1 := volregime.Forecast{
		Regime:             "high",
		Conviction:         0.8,
		HistoricalAccuracy: 0.7,
		Tier:               "tier1",
		Rank:               1.5,
		N:                  10,
	}
	if err := st.UpsertVolForecast(ctx, sym.ID, 100, f1); err != nil {
		t.Fatalf("got = %v, want nil", err)
	}

	fc, ok, err := st.VolForecastForSymbol(ctx, sym.ID)
	if err != nil {
		t.Fatalf("got = %v, want nil", err)
	}
	if !ok {
		t.Fatalf("got = %v, want true", ok)
	}
	if fc.Symbol != sym.Symbol {
		t.Fatalf("got = %v, want %v", fc.Symbol, sym.Symbol)
	}
	if fc.Market != string(sym.Market) {
		t.Fatalf("got = %v, want %v", fc.Market, string(sym.Market))
	}
	if fc.Ts != 100 {
		t.Fatalf("got = %v, want 100", fc.Ts)
	}
	if fc.Regime != f1.Regime {
		t.Fatalf("got = %v, want %v", fc.Regime, f1.Regime)
	}
	if fc.Conviction != f1.Conviction {
		t.Fatalf("got = %v, want %v", fc.Conviction, f1.Conviction)
	}
	if fc.HistoricalAccuracy != f1.HistoricalAccuracy {
		t.Fatalf("got = %v, want %v", fc.HistoricalAccuracy, f1.HistoricalAccuracy)
	}
	if fc.Tier != f1.Tier {
		t.Fatalf("got = %v, want %v", fc.Tier, f1.Tier)
	}
	if fc.Rank != f1.Rank {
		t.Fatalf("got = %v, want %v", fc.Rank, f1.Rank)
	}
	if fc.N != f1.N {
		t.Fatalf("got = %v, want %v", fc.N, f1.N)
	}

	f2 := volregime.Forecast{
		Regime:             "low",
		Conviction:         0.3,
		HistoricalAccuracy: 0.2,
		Tier:               "tier3",
		Rank:               0.5,
		N:                  5,
	}
	if err := st.UpsertVolForecast(ctx, sym.ID, 200, f2); err != nil {
		t.Fatalf("got = %v, want nil", err)
	}

	fc, ok, err = st.VolForecastForSymbol(ctx, sym.ID)
	if err != nil {
		t.Fatalf("got = %v, want nil", err)
	}
	if !ok {
		t.Fatalf("got = %v, want true", ok)
	}
	if fc.Ts != 200 {
		t.Fatalf("got = %v, want 200", fc.Ts)
	}
	if fc.Regime != f2.Regime {
		t.Fatalf("got = %v, want %v", fc.Regime, f2.Regime)
	}
	if fc.Conviction != f2.Conviction {
		t.Fatalf("got = %v, want %v", fc.Conviction, f2.Conviction)
	}
	if fc.HistoricalAccuracy != f2.HistoricalAccuracy {
		t.Fatalf("got = %v, want %v", fc.HistoricalAccuracy, f2.HistoricalAccuracy)
	}
	if fc.Tier != f2.Tier {
		t.Fatalf("got = %v, want %v", fc.Tier, f2.Tier)
	}
	if fc.Rank != f2.Rank {
		t.Fatalf("got = %v, want %v", fc.Rank, f2.Rank)
	}
	if fc.N != f2.N {
		t.Fatalf("got = %v, want %v", fc.N, f2.N)
	}

	fcs, err := st.VolForecasts(ctx)
	if err != nil {
		t.Fatalf("got = %v, want nil", err)
	}
	if len(fcs) != 1 {
		t.Fatalf("got = %v, want 1", len(fcs))
	}
	if fcs[0].Symbol != sym.Symbol {
		t.Fatalf("got = %v, want %v", fcs[0].Symbol, sym.Symbol)
	}
	if fcs[0].Market != string(sym.Market) {
		t.Fatalf("got = %v, want %v", fcs[0].Market, string(sym.Market))
	}
	if fcs[0].Ts != 200 {
		t.Fatalf("got = %v, want 200", fcs[0].Ts)
	}
	if fcs[0].Regime != f2.Regime {
		t.Fatalf("got = %v, want %v", fcs[0].Regime, f2.Regime)
	}
	if fcs[0].Conviction != f2.Conviction {
		t.Fatalf("got = %v, want %v", fcs[0].Conviction, f2.Conviction)
	}
	if fcs[0].HistoricalAccuracy != f2.HistoricalAccuracy {
		t.Fatalf("got = %v, want %v", fcs[0].HistoricalAccuracy, f2.HistoricalAccuracy)
	}
	if fcs[0].Tier != f2.Tier {
		t.Fatalf("got = %v, want %v", fcs[0].Tier, f2.Tier)
	}
	if fcs[0].Rank != f2.Rank {
		t.Fatalf("got = %v, want %v", fcs[0].Rank, f2.Rank)
	}
	if fcs[0].N != f2.N {
		t.Fatalf("got = %v, want %v", fcs[0].N, f2.N)
	}
}

func TestDeleteVolForecast_RemovesTheRow(t *testing.T) {
	st := openTestStore(t)
	ctx := context.Background()

	sym, err := st.UpsertSymbol(ctx, "AAPL", marketdata.Stocks, "Apple")
	if err != nil {
		t.Fatalf("got = %v, want nil", err)
	}

	f := volregime.Forecast{
		Regime:             "medium",
		Conviction:         0.5,
		HistoricalAccuracy: 0.6,
		Tier:               "tier2",
		Rank:               1.0,
		N:                  7,
	}
	if err := st.UpsertVolForecast(ctx, sym.ID, 150, f); err != nil {
		t.Fatalf("got = %v, want nil", err)
	}

	fc, ok, err := st.VolForecastForSymbol(ctx, sym.ID)
	if err != nil {
		t.Fatalf("got = %v, want nil", err)
	}
	if !ok {
		t.Fatalf("got = %v, want true", ok)
	}
	if fc.Ts != 150 {
		t.Fatalf("got = %v, want 150", fc.Ts)
	}

	if err := st.DeleteVolForecast(ctx, sym.ID); err != nil {
		t.Fatalf("got = %v, want nil", err)
	}

	fc, ok, err = st.VolForecastForSymbol(ctx, sym.ID)
	if err != nil {
		t.Fatalf("got = %v, want nil", err)
	}
	if ok {
		t.Fatalf("got = %v, want false", ok)
	}
	if fc.Symbol != "" || fc.Market != "" || fc.Ts != 0 || fc.Regime != "" || fc.Conviction != 0 || fc.HistoricalAccuracy != 0 || fc.Tier != "" || fc.Rank != 0 || fc.N != 0 {
		t.Fatalf("got = %v, want zero VolForecast", fc)
	}

	if err := st.DeleteVolForecast(ctx, sym.ID); err != nil {
		t.Fatalf("got = %v, want nil", err)
	}
}

func TestVolForecasts_OrdersByConvictionThenSymbol(t *testing.T) {
	st := openTestStore(t)
	ctx := context.Background()

	symA, err := st.UpsertSymbol(ctx, "A", marketdata.Stocks, "A")
	if err != nil {
		t.Fatalf("got = %v, want nil", err)
	}
	symB, err := st.UpsertSymbol(ctx, "B", marketdata.Stocks, "B")
	if err != nil {
		t.Fatalf("got = %v, want nil", err)
	}
	symC, err := st.UpsertSymbol(ctx, "C", marketdata.Stocks, "C")
	if err != nil {
		t.Fatalf("got = %v, want nil", err)
	}
	symD, err := st.UpsertSymbol(ctx, "D", marketdata.Stocks, "D")
	if err != nil {
		t.Fatalf("got = %v, want nil", err)
	}

	// A and B share a conviction, so the symbol tiebreak decides their order.
	// B is written FIRST on purpose: inserted alphabetically, insertion order
	// and symbol order agree and the ORDER BY's second key is never exercised.
	fB := volregime.Forecast{
		Regime:             "regB",
		Conviction:         0.5,
		HistoricalAccuracy: 0.5,
		Tier:               "tierB",
		Rank:               2.0,
		N:                  2,
	}
	if err := st.UpsertVolForecast(ctx, symB.ID, 2000, fB); err != nil {
		t.Fatalf("got = %v, want nil", err)
	}

	fA := volregime.Forecast{
		Regime:             "regA",
		Conviction:         0.5,
		HistoricalAccuracy: 0.5,
		Tier:               "tierA",
		Rank:               1.0,
		N:                  1,
	}
	if err := st.UpsertVolForecast(ctx, symA.ID, 1000, fA); err != nil {
		t.Fatalf("got = %v, want nil", err)
	}

	fC := volregime.Forecast{
		Regime:             "regC",
		Conviction:         0.3,
		HistoricalAccuracy: 0.3,
		Tier:               "tierC",
		Rank:               3.0,
		N:                  3,
	}
	if err := st.UpsertVolForecast(ctx, symC.ID, 3000, fC); err != nil {
		t.Fatalf("got = %v, want nil", err)
	}

	fD := volregime.Forecast{
		Regime:             "regD",
		Conviction:         0.7,
		HistoricalAccuracy: 0.7,
		Tier:               "tierD",
		Rank:               4.0,
		N:                  4,
	}
	if err := st.UpsertVolForecast(ctx, symD.ID, 4000, fD); err != nil {
		t.Fatalf("got = %v, want nil", err)
	}

	fcs, err := st.VolForecasts(ctx)
	if err != nil {
		t.Fatalf("got = %v, want nil", err)
	}
	if len(fcs) != 4 {
		t.Fatalf("got = %v, want 4", len(fcs))
	}
	wantOrder := []string{symD.Symbol, symA.Symbol, symB.Symbol, symC.Symbol}
	for i, fc := range fcs {
		if fc.Symbol != wantOrder[i] {
			t.Fatalf("got = %v, want %v at index %d", fc.Symbol, wantOrder[i], i)
		}
		if fc.Market != string(symD.Market) && i == 0 {
			t.Fatalf("got = %v, want %v for D", fc.Market, string(symD.Market))
		}
		if i == 0 && fc.Conviction != 0.7 {
			t.Fatalf("got = %v, want 0.7 for D", fc.Conviction)
		}
		if i == 1 && fc.Conviction != 0.5 {
			t.Fatalf("got = %v, want 0.5 for A", fc.Conviction)
		}
		if i == 2 && fc.Conviction != 0.5 {
			t.Fatalf("got = %v, want 0.5 for B", fc.Conviction)
		}
		if i == 3 && fc.Conviction != 0.3 {
			t.Fatalf("got = %v, want 0.3 for C", fc.Conviction)
		}
	}
}

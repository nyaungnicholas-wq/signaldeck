package store

import (
	"context"
	"testing"

	md "github.com/nyaungnicholas-wq/signaldeck/internal/marketdata"
)

// seedStockFeature writes one stock feature row at ts.
func seedStockFeature(t *testing.T, st *Store, symbolID, ts int64) {
	t.Helper()
	if err := st.InsertFeatures(context.Background(), symbolID, md.H1d, ts, 12,
		map[string]float64{"pressure_score": 0.1}); err != nil {
		t.Fatalf("features ts=%d: %v", ts, err)
	}
}

// TestStockFeatureDayFold_CatchesTheStraddle is the guard's own check. Rows
// confined to regular hours must fold identically under both boundaries (so the
// measurement never cries wolf on honest data), and rows that continue past the
// 20:00 ET close must fold to MORE UTC days than trading days — that excess is
// the phantom-day inflation the audit reports.
func TestStockFeatureDayFold_CatchesTheStraddle(t *testing.T) {
	ctx := context.Background()

	// A UTC midnight, far enough back to be a stable anchor.
	const base = int64(20000) * md.SecondsPerDay

	t.Run("regular hours only — folds agree", func(t *testing.T) {
		st := openTestStore(t)
		sym, err := st.UpsertSymbol(ctx, "AAPL", md.Stocks, "")
		if err != nil {
			t.Fatal(err)
		}
		// 5 days, each 13:30Z–20:00Z (US regular session under EDT).
		for d := int64(0); d < 5; d++ {
			for _, off := range []int64{13*3600 + 1800, 16 * 3600, 20 * 3600} {
				seedStockFeature(t, st, sym.ID, base+d*md.SecondsPerDay+off)
			}
		}
		utcDays, tradingDays, err := st.StockFeatureDayFold(ctx, 0)
		if err != nil {
			t.Fatal(err)
		}
		if utcDays != 5 || tradingDays != 5 {
			t.Fatalf("regular-hours rows must fold to 5 days both ways, got utc=%d trading=%d — "+
				"a disagreement here means the audit would flag honest data", utcDays, tradingDays)
		}
	})

	t.Run("extended session tail — UTC fold invents a day", func(t *testing.T) {
		st := openTestStore(t)
		sym, err := st.UpsertSymbol(ctx, "AAPL", md.Stocks, "")
		if err != nil {
			t.Fatal(err)
		}
		// Same 5 sessions, but each now runs to the 20:00 ET extended close,
		// which is 00:00Z the NEXT day under EDT.
		for d := int64(0); d < 5; d++ {
			day := base + d*md.SecondsPerDay
			for _, off := range []int64{13*3600 + 1800, 20 * 3600, 24 * 3600} {
				seedStockFeature(t, st, sym.ID, day+off)
			}
		}
		utcDays, tradingDays, err := st.StockFeatureDayFold(ctx, 0)
		if err != nil {
			t.Fatal(err)
		}
		if tradingDays != 5 {
			t.Fatalf("5 sessions must fold to 5 trading days, got %d", tradingDays)
		}
		// Each session's 00:00Z tail lands in the next UTC day; the 5 tails span
		// 5 further UTC days, one of which is shared with no session.
		if utcDays <= tradingDays {
			t.Fatalf("the UTC fold must OVERSTATE here: got utc=%d trading=%d. If these agree "+
				"the audit is blind to exactly the split it exists to catch", utcDays, tradingDays)
		}
		t.Logf("utc=%d trading=%d → %d phantom days", utcDays, tradingDays, utcDays-tradingDays)
	})

	t.Run("crypto is excluded", func(t *testing.T) {
		st := openTestStore(t)
		sym, err := st.UpsertSymbol(ctx, "BTCUSD", md.Crypto, "")
		if err != nil {
			t.Fatal(err)
		}
		// Crypto trades continuously; a UTC day IS its natural unit, so these
		// rows must not reach the stocks-only measurement at all.
		for h := int64(0); h < 24; h++ {
			seedStockFeature(t, st, sym.ID, base+h*3600)
		}
		utcDays, tradingDays, err := st.StockFeatureDayFold(ctx, 0)
		if err != nil {
			t.Fatal(err)
		}
		if utcDays != 0 || tradingDays != 0 {
			t.Fatalf("crypto rows must not be counted, got utc=%d trading=%d", utcDays, tradingDays)
		}
	})

	t.Run("since bound is applied", func(t *testing.T) {
		st := openTestStore(t)
		sym, err := st.UpsertSymbol(ctx, "AAPL", md.Stocks, "")
		if err != nil {
			t.Fatal(err)
		}
		for d := int64(0); d < 5; d++ {
			seedStockFeature(t, st, sym.ID, base+d*md.SecondsPerDay+16*3600)
		}
		// Cut off the first three days.
		utcDays, _, err := st.StockFeatureDayFold(ctx, base+3*md.SecondsPerDay)
		if err != nil {
			t.Fatal(err)
		}
		if utcDays != 2 {
			t.Fatalf("since bound must leave 2 days, got %d", utcDays)
		}
	})
}

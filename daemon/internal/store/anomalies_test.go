package store

import (
	"context"
	"path/filepath"
	"testing"

	md "github.com/nyaungnicholas-wq/signaldeck/internal/marketdata"
)

func newAnomalyTestStore(t *testing.T) *Store {
	t.Helper()
	st, err := Open(filepath.Join(t.TempDir(), "anomalies.db"))
	if err != nil {
		t.Fatalf("open store: %v", err)
	}
	t.Cleanup(func() { _ = st.Close() })
	return st
}

func TestInsertAnomaly_HourDedup(t *testing.T) {
	ctx := context.Background()
	st := newAnomalyTestStore(t)
	sym, err := st.UpsertSymbol(ctx, "BTC/USD", md.Crypto, "")
	if err != nil {
		t.Fatalf("upsert symbol: %v", err)
	}

	base := int64(1_760_000_400) // arbitrary instant inside some hour bucket
	fresh, err := st.InsertAnomaly(ctx, AnomalyRow{
		SymbolID: sym.ID, Ts: base, Kind: "anomaly_imbalance", Z: 3.1, Detail: "d1",
	})
	if err != nil || !fresh {
		t.Fatalf("first insert: fresh=%v err=%v", fresh, err)
	}

	// Same symbol+kind, 5 minutes later, same hour bucket → deduped even
	// though ts and detail differ (one open anomaly per symbol+kind+hour).
	fresh, err = st.InsertAnomaly(ctx, AnomalyRow{
		SymbolID: sym.ID, Ts: base + 300, Kind: "anomaly_imbalance", Z: 3.4, Detail: "d2",
	})
	if err != nil || fresh {
		t.Fatalf("same-hour insert: fresh=%v err=%v", fresh, err)
	}

	// Different KIND in the same hour is its own row.
	fresh, err = st.InsertAnomaly(ctx, AnomalyRow{
		SymbolID: sym.ID, Ts: base + 300, Kind: "anomaly_vol", Z: 2.9, Detail: "d3",
	})
	if err != nil || !fresh {
		t.Fatalf("other-kind insert: fresh=%v err=%v", fresh, err)
	}

	// Next hour bucket → fresh row again.
	fresh, err = st.InsertAnomaly(ctx, AnomalyRow{
		SymbolID: sym.ID, Ts: base + 3600, Kind: "anomaly_imbalance", Z: 2.8, Detail: "d4",
	})
	if err != nil || !fresh {
		t.Fatalf("next-hour insert: fresh=%v err=%v", fresh, err)
	}

	rows, err := st.Anomalies(ctx, 0, "", 100)
	if err != nil {
		t.Fatalf("anomalies: %v", err)
	}
	if len(rows) != 3 {
		t.Fatalf("rows = %d want 3 (%+v)", len(rows), rows)
	}
	// Newest-first and joined to the symbol.
	if rows[0].Detail != "d4" || rows[0].Symbol != "BTC/USD" || rows[0].Market != "crypto" {
		t.Errorf("rows[0] = %+v", rows[0])
	}
}

func TestAnomalies_FiltersAndCursor(t *testing.T) {
	ctx := context.Background()
	st := newAnomalyTestStore(t)
	btc, _ := st.UpsertSymbol(ctx, "BTC/USD", md.Crypto, "")
	nvda, _ := st.UpsertSymbol(ctx, "NVDA", md.Stocks, "")

	seed := []AnomalyRow{
		{SymbolID: btc.ID, Ts: 7200, Kind: "anomaly_imbalance", Z: 3.0, Detail: "a"},
		{SymbolID: nvda.ID, Ts: 10800, Kind: "anomaly_volume", Z: 2.7, Detail: "b"},
		{SymbolID: nvda.ID, Ts: 14400, Kind: "anomaly_vol", Z: 4.1, Detail: "c"},
	}
	for _, a := range seed {
		if fresh, err := st.InsertAnomaly(ctx, a); err != nil || !fresh {
			t.Fatalf("seed %+v: fresh=%v err=%v", a, fresh, err)
		}
	}

	// Symbol filter.
	rows, err := st.Anomalies(ctx, nvda.ID, "", 100)
	if err != nil || len(rows) != 2 {
		t.Fatalf("nvda rows = %+v err=%v", rows, err)
	}
	// Kind filter.
	rows, err = st.Anomalies(ctx, 0, "anomaly_imbalance", 100)
	if err != nil || len(rows) != 1 || rows[0].Symbol != "BTC/USD" {
		t.Fatalf("imbalance rows = %+v err=%v", rows, err)
	}
	// Combined + limit.
	rows, err = st.Anomalies(ctx, nvda.ID, "anomaly_vol", 1)
	if err != nil || len(rows) != 1 || rows[0].Detail != "c" {
		t.Fatalf("combined rows = %+v err=%v", rows, err)
	}

	// Cursor sweep: MaxAnomalyID then AfterID sees only newer rows.
	maxID, err := st.MaxAnomalyID(ctx)
	if err != nil || maxID == 0 {
		t.Fatalf("max id = %d err=%v", maxID, err)
	}
	after, err := st.AnomaliesAfterID(ctx, maxID, 0, 100)
	if err != nil || len(after) != 0 {
		t.Fatalf("after max = %+v err=%v", after, err)
	}
	if fresh, err := st.InsertAnomaly(ctx, AnomalyRow{
		SymbolID: btc.ID, Ts: 18000, Kind: "anomaly_volume", Z: 2.6, Detail: "d",
	}); err != nil || !fresh {
		t.Fatalf("insert d: fresh=%v err=%v", fresh, err)
	}
	after, err = st.AnomaliesAfterID(ctx, maxID, 0, 100)
	if err != nil || len(after) != 1 || after[0].Detail != "d" || after[0].Symbol != "BTC/USD" {
		t.Fatalf("after rows = %+v err=%v", after, err)
	}
	// sinceTs floor excludes old rows on a cold cursor.
	after, err = st.AnomaliesAfterID(ctx, 0, 14400, 100)
	if err != nil || len(after) != 2 {
		t.Fatalf("sinceTs rows = %+v err=%v", after, err)
	}
}

func TestMaxAnomalyID_Empty(t *testing.T) {
	st := newAnomalyTestStore(t)
	id, err := st.MaxAnomalyID(context.Background())
	if err != nil || id != 0 {
		t.Fatalf("empty max = %d err=%v", id, err)
	}
}

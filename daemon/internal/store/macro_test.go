package store

import (
	"context"
	"testing"

	md "github.com/nyaungnicholas-wq/signaldeck/internal/marketdata"
)

func TestMacro_InsertLatestSeries(t *testing.T) {
	st := openTemp(t)
	ctx := context.Background()

	// insert out of order to prove ordering is by ts, not insert order
	day := int64(86400)
	if err := st.InsertMacro(ctx, "VIXCLS", 3*day, 14.10); err != nil {
		t.Fatal(err)
	}
	if err := st.InsertMacro(ctx, "VIXCLS", 1*day, 12.00); err != nil {
		t.Fatal(err)
	}
	if err := st.InsertMacro(ctx, "VIXCLS", 2*day, 13.00); err != nil {
		t.Fatal(err)
	}

	// LatestMacro / LatestVIX must return the newest ts.
	p, ok, err := st.LatestMacro(ctx, "VIXCLS")
	if err != nil || !ok {
		t.Fatalf("latest: ok=%v err=%v", ok, err)
	}
	if p.Ts != 3*day || p.Value != 14.10 {
		t.Fatalf("latest = %+v, want ts=%d val=14.10", p, 3*day)
	}
	if v, ok, _ := st.LatestVIX(ctx); !ok || v != 14.10 {
		t.Fatalf("LatestVIX = %v ok=%v", v, ok)
	}

	// MacroSeries is oldest-first.
	pts, err := st.MacroSeries(ctx, "VIXCLS", 0)
	if err != nil {
		t.Fatal(err)
	}
	if len(pts) != 3 || pts[0].Value != 12.00 || pts[2].Value != 14.10 {
		t.Fatalf("series not oldest-first: %+v", pts)
	}

	// limit keeps the most-recent N but still returns them oldest-first.
	lim, err := st.MacroSeries(ctx, "VIXCLS", 2)
	if err != nil {
		t.Fatal(err)
	}
	if len(lim) != 2 || lim[0].Value != 13.00 || lim[1].Value != 14.10 {
		t.Fatalf("limited series wrong: %+v", lim)
	}
}

func TestMacro_InsertIdempotent(t *testing.T) {
	st := openTemp(t)
	ctx := context.Background()
	for i := 0; i < 3; i++ {
		if err := st.InsertMacro(ctx, "DGS10", 86400, 4.28); err != nil {
			t.Fatal(err)
		}
	}
	pts, _ := st.MacroSeries(ctx, "DGS10", 0)
	if len(pts) != 1 {
		t.Fatalf("insert not idempotent: %d rows", len(pts))
	}
}

func TestMacro_LatestAll(t *testing.T) {
	st := openTemp(t)
	ctx := context.Background()
	day := int64(86400)
	_ = st.InsertMacro(ctx, "VIXCLS", day, 12.0)
	_ = st.InsertMacro(ctx, "VIXCLS", 2*day, 13.0)
	_ = st.InsertMacro(ctx, "DGS10", day, 4.0)

	all, err := st.LatestMacroAll(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if all["VIXCLS"].Value != 13.0 || all["VIXCLS"].Ts != 2*day {
		t.Fatalf("latest VIXCLS wrong: %+v", all["VIXCLS"])
	}
	if all["DGS10"].Value != 4.0 {
		t.Fatalf("latest DGS10 wrong: %+v", all["DGS10"])
	}
}

func TestMacro_AbsentIsGraceful(t *testing.T) {
	st := openTemp(t)
	ctx := context.Background()
	if _, ok, err := st.LatestMacro(ctx, "NOPE"); ok || err != nil {
		t.Fatalf("absent series should be ok=false err=nil, got ok=%v err=%v", ok, err)
	}
	if _, ok, err := st.LatestVIX(ctx); ok || err != nil {
		t.Fatalf("absent VIX should be ok=false err=nil, got ok=%v err=%v", ok, err)
	}
	pts, err := st.MacroSeries(ctx, "NOPE", 0)
	if err != nil || len(pts) != 0 {
		t.Fatalf("absent series: pts=%d err=%v", len(pts), err)
	}
}

func TestFundamentals_UpsertLatestHistory(t *testing.T) {
	st := openTemp(t)
	ctx := context.Background()
	sym, err := st.UpsertSymbol(ctx, "AAPL", md.Stocks, "Apple")
	if err != nil {
		t.Fatal(err)
	}
	q1 := int64(1_700_000_000)
	q2 := int64(1_710_000_000)

	// two revenue periods + one EPS
	rows := []FundamentalRow{
		{SymbolID: sym.ID, Metric: "Revenues", Value: 100, AsOf: q1, FetchedAt: 1},
		{SymbolID: sym.ID, Metric: "Revenues", Value: 120, AsOf: q2, FetchedAt: 1},
		{SymbolID: sym.ID, Metric: "EPS", Value: 1.5, AsOf: q2, FetchedAt: 1},
	}
	for _, r := range rows {
		if err := st.UpsertFundamental(ctx, r); err != nil {
			t.Fatal(err)
		}
	}

	// LatestFundamentals returns one row per metric, newest as_of.
	latest, err := st.LatestFundamentals(ctx, sym.ID)
	if err != nil {
		t.Fatal(err)
	}
	got := map[string]FundamentalRow{}
	for _, r := range latest {
		got[r.Metric] = r
	}
	if got["Revenues"].Value != 120 || got["Revenues"].AsOf != q2 {
		t.Fatalf("latest revenue = %+v, want 120@q2", got["Revenues"])
	}
	if got["EPS"].Value != 1.5 {
		t.Fatalf("latest eps = %+v", got["EPS"])
	}

	// FundamentalHistory returns full metric series oldest-first.
	hist, err := st.FundamentalHistory(ctx, sym.ID, "Revenues")
	if err != nil {
		t.Fatal(err)
	}
	if len(hist) != 2 || hist[0].Value != 100 || hist[1].Value != 120 {
		t.Fatalf("history wrong: %+v", hist)
	}

	// Re-upsert same (metric, as_of) with a new value + fetched_at ⇒ updates in
	// place, does not add a row.
	if err := st.UpsertFundamental(ctx, FundamentalRow{
		SymbolID: sym.ID, Metric: "Revenues", Value: 121, AsOf: q2, FetchedAt: 999,
	}); err != nil {
		t.Fatal(err)
	}
	hist2, _ := st.FundamentalHistory(ctx, sym.ID, "Revenues")
	if len(hist2) != 2 {
		t.Fatalf("upsert added a row: %d", len(hist2))
	}
	latest2, _ := st.LatestFundamentals(ctx, sym.ID)
	for _, r := range latest2 {
		if r.Metric == "Revenues" && (r.Value != 121 || r.FetchedAt != 999) {
			t.Fatalf("upsert didn't update: %+v", r)
		}
	}
}

func TestFundamentals_AbsentIsGraceful(t *testing.T) {
	st := openTemp(t)
	ctx := context.Background()
	sym, _ := st.UpsertSymbol(ctx, "AAPL", md.Stocks, "Apple")
	rows, err := st.LatestFundamentals(ctx, sym.ID)
	if err != nil || len(rows) != 0 {
		t.Fatalf("absent fundamentals: rows=%d err=%v", len(rows), err)
	}
}

package store

import (
	"context"
	"strings"
	"testing"

	md "github.com/nyaungnicholas-wq/signaldeck/internal/marketdata"
)

// Rollup's open/close probes must seek their bucket. Matched by ts/bucket they
// walked every bar of the symbol's timeframe per bucket, and the Downsampler's
// all-symbol rollup ran past its 15m deadline on every pass (2026-10-02).
func TestRollupProbesSeekTheirBucket(t *testing.T) {
	st := openTemp(t)
	rows, err := st.w.QueryContext(context.Background(), "EXPLAIN QUERY PLAN "+rollupSelect,
		rollupArgs(1, md.TF1m, md.TF1h, 3600, 0, 7200)...)
	if err != nil {
		t.Fatal(err)
	}
	var plan []string
	probes := 0
	for rows.Next() {
		var id, parent, unused int
		var detail string
		if err := rows.Scan(&id, &parent, &unused, &detail); err != nil {
			t.Fatal(err)
		}
		plan = append(plan, detail)
		if strings.Contains(detail, " b2 ") || strings.Contains(detail, " b3 ") {
			probes++
			if !strings.Contains(detail, "ts>? AND ts<?") {
				t.Errorf("open/close probe does not seek its bucket: %s", detail)
			}
		}
	}
	_ = rows.Close()
	if probes != 2 {
		t.Fatalf("found %d open/close probes in the plan, want 2: %s", probes, strings.Join(plan, "; "))
	}
}

// The range form must give the same bars as ts/bucket at the bucket edges: a bar
// at the first second belongs to its bucket, one at the next bucket's first
// second does not.
func TestRollupBucketEdges(t *testing.T) {
	st := openTemp(t)
	ctx := context.Background()
	sym, err := st.UpsertSymbol(ctx, "EDGE", md.Stocks, "")
	if err != nil {
		t.Fatal(err)
	}
	if err := st.UpsertBars(ctx, []md.Bar{
		{SymbolID: sym.ID, TF: md.TF1m, Ts: 3600, Open: 10, High: 11, Low: 9, Close: 10.5, Volume: 1},
		{SymbolID: sym.ID, TF: md.TF1m, Ts: 7140, Open: 12, High: 13, Low: 8, Close: 12.5, Volume: 2},
		{SymbolID: sym.ID, TF: md.TF1m, Ts: 7200, Open: 20, High: 21, Low: 19, Close: 20.5, Volume: 4},
	}); err != nil {
		t.Fatal(err)
	}
	if err := st.Rollup(ctx, sym.ID, md.TF1m, md.TF1h, 3600, 0, 10800); err != nil {
		t.Fatal(err)
	}
	hours, err := st.Bars(ctx, sym.ID, md.TF1h, 0, 10800, 0)
	if err != nil || len(hours) != 2 {
		t.Fatalf("want 2 hourly bars: %v %d", err, len(hours))
	}
	if h := hours[0]; h.Ts != 3600 || h.Open != 10 || h.High != 13 || h.Low != 8 || h.Close != 12.5 || h.Volume != 3 {
		t.Fatalf("hour 1: %+v, want ts 3600 O10 H13 L8 C12.5 V3", h)
	}
	if h := hours[1]; h.Ts != 7200 || h.Open != 20 || h.Close != 20.5 || h.Volume != 4 {
		t.Fatalf("hour 2: %+v, want ts 7200 O20 C20.5 V4", h)
	}
}

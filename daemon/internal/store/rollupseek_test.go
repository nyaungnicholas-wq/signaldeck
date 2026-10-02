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

	// The 1h -> 1d compaction binds the same query with an 86400 bucket.
	if err := st.UpsertBars(ctx, []md.Bar{
		{SymbolID: sym.ID, TF: md.TF1h, Ts: 86400, Open: 30, High: 31, Low: 29, Close: 30.5, Volume: 1},
		{SymbolID: sym.ID, TF: md.TF1h, Ts: 172800 - 3600, Open: 32, High: 35, Low: 28, Close: 33, Volume: 2},
		{SymbolID: sym.ID, TF: md.TF1h, Ts: 172800, Open: 40, High: 41, Low: 39, Close: 40.5, Volume: 4},
	}); err != nil {
		t.Fatal(err)
	}
	if err := st.RollupMissing(ctx, sym.ID, md.TF1h, md.TF1d, 86400, 86400, 259200); err != nil {
		t.Fatal(err)
	}
	days, err := st.Bars(ctx, sym.ID, md.TF1d, 86400, 259200, 0)
	if err != nil || len(days) != 2 {
		t.Fatalf("want 2 daily bars: %v %d", err, len(days))
	}
	if d := days[0]; d.Ts != 86400 || d.Open != 30 || d.High != 35 || d.Low != 28 || d.Close != 33 || d.Volume != 3 {
		t.Fatalf("day 1: %+v, want ts 86400 O30 H35 L28 C33 V3", d)
	}
	if d := days[1]; d.Ts != 172800 || d.Open != 40 || d.Close != 40.5 {
		t.Fatalf("day 2: %+v, want ts 172800 O40 C40.5", d)
	}
}

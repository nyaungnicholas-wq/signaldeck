package maintain

import (
	"context"
	"testing"
	"time"

	md "github.com/nyaungnicholas-wq/signaldeck/internal/marketdata"
)

// A score whose base bar is so old that the forward window ended more than 3
// horizons before the score's own timestamp — and no forward bar exists to
// settle it — must be VOIDED on the first resolver pass. Parking it for 30
// days was head-of-line blocking the queue with sediment.
func TestOutcomeResolverVoidsStaleBaseImmediately(t *testing.T) {
	ctx := context.Background()
	st := openStore(t)

	sym, err := st.UpsertSymbol(ctx, "DEAD", md.Stocks, "")
	if err != nil {
		t.Fatal(err)
	}

	now := time.Now().UTC()
	dayStart := time.Date(now.Year(), now.Month(), now.Day(), 0, 0, 0, 0, time.UTC).Unix()
	old := dayStart - 400*86400 + 5*3600
	if err := st.UpsertBars(ctx, []md.Bar{
		{SymbolID: sym.ID, TF: md.TF1d, Ts: old, Open: 100, High: 100, Low: 100, Close: 100},
		{SymbolID: sym.ID, TF: md.TF1d, Ts: old + 86400, Open: 101, High: 101, Low: 101, Close: 101},
	}); err != nil {
		t.Fatal(err)
	}

	scoreTs := now.Unix() - 2*86400
	if err := st.InsertScore(ctx, md.Score{
		SymbolID: sym.ID, Horizon: md.H1d, Ts: scoreTs, Score: 0.5,
	}); err != nil {
		t.Fatal(err)
	}

	if _, err := (&OutcomeResolver{St: st}).Run(ctx); err != nil {
		t.Fatal(err)
	}

	outcomes, err := st.ResolvedOutcomes(ctx, sym.ID, md.H1d, 10)
	if err != nil {
		t.Fatal(err)
	}
	if len(outcomes) != 1 {
		t.Fatalf("want exactly 1 resolved row, got %d: %+v", len(outcomes), outcomes)
	}
	row := outcomes[0]
	if row.ResolvedAt == nil {
		t.Fatalf("stale-base row must be voided on the first pass rather than parked for 30 days; got %+v", row)
	}
	if row.FwdReturn != nil {
		t.Fatalf("voided row must have FwdReturn == nil, got %+v", row)
	}
}

// US daily bars are stamped at ET midnight (05:00Z under EST, 04:00Z under EDT).
// A fixed +7d from an EST-stamped base overshoots an EDT-stamped forward bar by
// 1h and grades an 8-session move as 1w. The resolver must subtract 6h of DST
// slack on daily-bar horizons so the 7th forward bar is the one picked.
func TestOutcomeResolverOneWeekAcrossDSTStamp(t *testing.T) {
	ctx := context.Background()
	st := openStore(t)

	sym, err := st.UpsertSymbol(ctx, "DST", md.Stocks, "")
	if err != nil {
		t.Fatal(err)
	}

	now := time.Now().UTC()
	dayStart := time.Date(now.Year(), now.Month(), now.Day(), 0, 0, 0, 0, time.UTC).Unix()
	baseTs := dayStart - 30*86400 + 5*3600 // EST-style stamp
	if err := st.UpsertBars(ctx, []md.Bar{
		{SymbolID: sym.ID, TF: md.TF1d, Ts: baseTs, Open: 100, High: 100, Low: 100, Close: 100},
	}); err != nil {
		t.Fatal(err)
	}
	var fwd []md.Bar
	for k := int64(1); k <= 9; k++ {
		fwd = append(fwd, md.Bar{
			SymbolID: sym.ID, TF: md.TF1d, Ts: baseTs + k*86400 - 3600, // EDT-style stamp
			Open: float64(100 + k), High: float64(100 + k), Low: float64(100 + k), Close: float64(100 + k),
		})
	}
	if err := st.UpsertBars(ctx, fwd); err != nil {
		t.Fatal(err)
	}

	if err := st.InsertScore(ctx, md.Score{
		SymbolID: sym.ID, Horizon: md.H1w, Ts: baseTs + 60, Score: 0.5,
	}); err != nil {
		t.Fatal(err)
	}

	if _, err := (&OutcomeResolver{St: st}).Run(ctx); err != nil {
		t.Fatal(err)
	}

	outcomes, err := st.ResolvedOutcomes(ctx, sym.ID, md.H1w, 10)
	if err != nil {
		t.Fatal(err)
	}
	if len(outcomes) != 1 {
		t.Fatalf("want exactly 1 resolved row, got %d: %+v", len(outcomes), outcomes)
	}
	row := outcomes[0]
	if row.FwdReturn == nil {
		t.Fatalf("want a non-nil forward return, got %+v", row)
	}
	if got := *row.FwdReturn; got < 0.07-1e-9 || got > 0.07+1e-9 {
		t.Fatalf("fwd return = %.6f, want ~0.07 (1w); ~0.08 means the DST-stamp overshoot graded an 8-session return as one week", got)
	}
}

// The new immediate-void rule must not void live rows whose forward window is
// simply not mature yet. The successor bar that would settle the forward bar
// does not exist, so the resolver must wait — not void.
func TestOutcomeResolverStillWaitsForImmatureFreshRow(t *testing.T) {
	ctx := context.Background()
	st := openStore(t)

	sym, err := st.UpsertSymbol(ctx, "LIVE", md.Stocks, "")
	if err != nil {
		t.Fatal(err)
	}

	now := time.Now().UTC()
	dayStart := time.Date(now.Year(), now.Month(), now.Day(), 0, 0, 0, 0, time.UTC).Unix()
	baseTs := dayStart - 3*86400 + 4*3600
	if err := st.UpsertBars(ctx, []md.Bar{
		{SymbolID: sym.ID, TF: md.TF1d, Ts: baseTs, Open: 100, High: 100, Low: 100, Close: 100},
		{SymbolID: sym.ID, TF: md.TF1d, Ts: baseTs + 86400, Open: 102, High: 102, Low: 102, Close: 102},
	}); err != nil {
		t.Fatal(err)
	}

	if err := st.InsertScore(ctx, md.Score{
		SymbolID: sym.ID, Horizon: md.H1d, Ts: baseTs + 60, Score: 0.5,
	}); err != nil {
		t.Fatal(err)
	}

	if _, err := (&OutcomeResolver{St: st}).Run(ctx); err != nil {
		t.Fatal(err)
	}

	outcomes, err := st.ResolvedOutcomes(ctx, sym.ID, md.H1d, 10)
	if err != nil {
		t.Fatal(err)
	}
	if len(outcomes) != 0 {
		t.Fatalf("live row must wait for its forward window to mature, not be voided; got %+v", outcomes)
	}
}

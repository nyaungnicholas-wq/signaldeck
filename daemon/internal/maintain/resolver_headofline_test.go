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

// A HOLIDAY IS NOT A GAP (SD-55). A pre-holiday Friday's next session is
// Tuesday; from the DST-slackened target that bar is 3d6h out, so the
// calendar-seconds guard VOIDED every such 1d score outcome for good. For stocks
// the NYSE calendar decides; crypto trades every day and keeps the rule.
func TestOutcomeResolverGradesAcrossAHolidayNotAGap(t *testing.T) {
	ctx := context.Background()
	st := openStore(t)
	// Labor Day 2026 is Monday 09-07. ET-midnight (EDT) stamps: Fri, Tue, Wed.
	fri := time.Date(2026, 9, 4, 4, 0, 0, 0, time.UTC).Unix()
	tue, wed := fri+4*86400, fri+5*86400

	outcome := func(ticker string, market md.Market) md.ScoreOutcome {
		t.Helper()
		sym, err := st.UpsertSymbol(ctx, ticker, market, "")
		if err != nil {
			t.Fatal(err)
		}
		if err := st.UpsertBars(ctx, []md.Bar{
			{SymbolID: sym.ID, TF: md.TF1d, Ts: fri, Open: 100, High: 100, Low: 100, Close: 100},
			{SymbolID: sym.ID, TF: md.TF1d, Ts: tue, Open: 110, High: 110, Low: 110, Close: 110},
			{SymbolID: sym.ID, TF: md.TF1d, Ts: wed, Open: 111, High: 111, Low: 111, Close: 111}, // settles Tue
		}); err != nil {
			t.Fatal(err)
		}
		if err := st.InsertScore(ctx, md.Score{SymbolID: sym.ID, Horizon: md.H1d, Ts: fri + 60, Score: 0.5}); err != nil {
			t.Fatal(err)
		}
		if _, err := (&OutcomeResolver{St: st}).Run(ctx); err != nil {
			t.Fatal(err)
		}
		got, err := st.ResolvedOutcomes(ctx, sym.ID, md.H1d, 10)
		if err != nil {
			t.Fatal(err)
		}
		if len(got) != 1 || got[0].ResolvedAt == nil {
			t.Fatalf("%s: want 1 resolved row, got %+v", ticker, got)
		}
		return got[0]
	}

	if row := outcome("HOLI", md.Stocks); row.FwdReturn == nil || *row.FwdReturn < 0.099 || *row.FwdReturn > 0.101 {
		t.Fatalf("stock row across Labor Day must grade Fri->Tue (+10%%), got %+v", row)
	}
	if row := outcome("HOLICOIN", md.Crypto); row.FwdReturn != nil {
		t.Fatalf("crypto has no holidays: a 4-day hole must still void, got %+v", row)
	}
}

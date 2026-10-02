package maintain

import (
	"context"
	"testing"
	"time"

	md "github.com/nyaungnicholas-wq/signaldeck/internal/marketdata"
)

// H1-1H-GAP. The SD-55 holiday exemption is for daily bars. On 1m bars no whole
// NYSE session closes overnight, so applied to 1h it graded a 17:39 ET -> 09:19 ET
// move (14.7h) as a one-hour outcome. A 1h row whose next 1m print is more than
// 3 horizons past target must VOID; an in-session 1h row must still grade.
func TestOutcomeResolverVoidsOvernight1hGap(t *testing.T) {
	ctx := context.Background()
	st := openStore(t)

	base := time.Date(2026, 9, 30, 21, 39, 0, 0, time.UTC).Unix()     // Wed 17:39 EDT
	next := time.Date(2026, 10, 1, 13, 19, 0, 0, time.UTC).Unix()     // Thu 09:19 EDT, 14.7h past target
	inSession := time.Date(2026, 10, 1, 14, 0, 0, 0, time.UTC).Unix() // Thu 10:00 EDT

	resolve := func(ticker string, bars []int64, closes []float64, scoreTs int64) md.ScoreOutcome {
		t.Helper()
		sym, err := st.UpsertSymbol(ctx, ticker, md.Stocks, "")
		if err != nil {
			t.Fatal(err)
		}
		var bs []md.Bar
		for i := range bars {
			bs = append(bs, md.Bar{
				SymbolID: sym.ID,
				TF:       md.TF1m,
				Ts:       bars[i],
				Open:     closes[i],
				High:     closes[i],
				Low:      closes[i],
				Close:    closes[i],
			})
		}
		if err := st.UpsertBars(ctx, bs); err != nil {
			t.Fatal(err)
		}
		if err := st.InsertScore(ctx, md.Score{
			SymbolID: sym.ID,
			Horizon:  md.H1h,
			Ts:       scoreTs,
			Score:    0.5,
		}); err != nil {
			t.Fatal(err)
		}
		if _, err := (&OutcomeResolver{St: st}).Run(ctx); err != nil {
			t.Fatal(err)
		}
		got, err := st.ResolvedOutcomes(ctx, sym.ID, md.H1h, 10)
		if err != nil {
			t.Fatal(err)
		}
		if len(got) != 1 || got[0].ResolvedAt == nil {
			t.Fatalf("%s: want 1 resolved row, got %+v", ticker, got)
		}
		return got[0]
	}

	if row := resolve("EXPD", []int64{base, next, next + 60}, []float64{100, 100.83, 101}, base+30); row.FwdReturn != nil {
		t.Fatalf("a 1h row whose next 1m print is 14.7h past target must VOID, got fwd_return %v", *row.FwdReturn)
	}
	if row := resolve("INSESS", []int64{inSession, inSession + 3600, inSession + 3660}, []float64{100, 110, 111}, inSession+30); row.FwdReturn == nil || *row.FwdReturn < 0.099 || *row.FwdReturn > 0.101 {
		t.Fatalf("an in-session 1h row must grade +10%%, got %+v", row)
	}
}

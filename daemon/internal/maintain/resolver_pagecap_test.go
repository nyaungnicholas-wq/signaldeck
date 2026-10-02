package maintain

import (
	"context"
	"testing"
	"time"

	md "github.com/nyaungnicholas-wq/signaldeck/internal/marketdata"
)

// A pass reads at most maxPages pages per horizon, so a queue full of waiting
// rows costs bounded reads; where it stops, the next pass resumes. Seven
// never-settling rows lead the queue and a pass reads only four rows (page 2,
// 2 pages): restarting at the head every pass would never reach the live rows
// behind them.
func TestOutcomeResolverPageCapResumesInsteadOfWedging(t *testing.T) {
	ctx := context.Background()
	st := openStore(t)

	const day = int64(86400)
	d0 := time.Date(2026, 8, 24, 4, 0, 0, 0, time.UTC).Unix() // 00:00 EDT bar stamp

	stuckSym, err := st.UpsertSymbol(ctx, "STUCK", md.Stocks, "")
	if err != nil {
		t.Fatal(err)
	}
	if err := st.UpsertBars(ctx, []md.Bar{
		{SymbolID: stuckSym.ID, TF: md.TF1d, Ts: d0, Open: 100, High: 100, Low: 100, Close: 100},
		{SymbolID: stuckSym.ID, TF: md.TF1d, Ts: d0 + day, Open: 101, High: 101, Low: 101, Close: 101},
	}); err != nil {
		t.Fatal(err)
	}
	for i := int64(1); i <= 7; i++ {
		if err := st.InsertScore(ctx, md.Score{
			SymbolID: stuckSym.ID,
			Horizon:  md.H1d,
			Ts:       d0 + i*60,
			Score:    0.5,
		}); err != nil {
			t.Fatal(err)
		}
	}

	liveSym, err := st.UpsertSymbol(ctx, "LIVE", md.Stocks, "")
	if err != nil {
		t.Fatal(err)
	}
	var liveBars []md.Bar
	for k := int64(0); k <= 3; k++ {
		c := 100 + float64(k)
		liveBars = append(liveBars, md.Bar{
			SymbolID: liveSym.ID,
			TF:       md.TF1d,
			Ts:       d0 + k*day,
			Open:     c,
			High:     c,
			Low:      c,
			Close:    c,
		})
	}
	if err := st.UpsertBars(ctx, liveBars); err != nil {
		t.Fatal(err)
	}
	for i := int64(1); i <= 5; i++ {
		if err := st.InsertScore(ctx, md.Score{
			SymbolID: liveSym.ID,
			Horizon:  md.H1d,
			Ts:       d0 + 3600 + i*60,
			Score:    0.5,
		}); err != nil {
			t.Fatal(err)
		}
	}

	r := &OutcomeResolver{St: st, page: 2, maxPages: 2}

	graded := func(id int64) int {
		t.Helper()
		got, err := st.ResolvedOutcomes(ctx, id, md.H1d, 100)
		if err != nil {
			t.Fatal(err)
		}
		n := 0
		for _, o := range got {
			if o.FwdReturn != nil {
				n++
			}
		}
		return n
	}
	resolvedAny := func(id int64) int {
		t.Helper()
		got, err := st.ResolvedOutcomes(ctx, id, md.H1d, 100)
		if err != nil {
			t.Fatal(err)
		}
		return len(got)
	}

	// Pass 1 reads S1-S4 and stops at the cap; pass 2 resumes and reads S5-S7
	// and L1; pass 3 reads L2-L5; pass 4 finds the end of the queue and wraps;
	// pass 5 starts again at the head.
	want := []int{0, 1, 5, 5, 5}
	for pass, w := range want {
		detail, err := r.Run(ctx)
		if err != nil {
			t.Fatal(err)
		}
		if g := graded(liveSym.ID); g != w {
			t.Fatalf("pass %d: %d LIVE rows graded, want %d (page 2, 2 pages a pass, 7 waiting rows ahead): %s", pass+1, g, w, detail)
		}
		if n := resolvedAny(stuckSym.ID); n != 0 {
			t.Fatalf("pass %d: %d STUCK rows resolved; they must keep waiting", pass+1, n)
		}
	}
}

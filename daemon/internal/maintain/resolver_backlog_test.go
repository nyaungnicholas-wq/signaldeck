package maintain

import (
	"context"
	"strings"
	"testing"
	"time"

	md "github.com/nyaungnicholas-wq/signaldeck/internal/marketdata"
)

// The live wedge of 2026-10-02: CRNX's last 1d bar is the forward bar of
// ~3,000 1d rows, so they never settle, and as the oldest pending rows they
// filled 3/4 of every 4,000-row batch. A row that waits must cost a read, not
// a slot: with more stuck rows than one page, the live rows behind them drain
// at writeCap per pass until none are left, and the stuck rows keep waiting.
func TestOutcomeResolverPagesPastRowsThatNeverSettle(t *testing.T) {
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

	r := &OutcomeResolver{St: st, page: 3, writeCap: 2}

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

	want := []int{2, 4, 5, 5}
	for pass, w := range want {
		detail, err := r.Run(ctx)
		if err != nil {
			t.Fatal(err)
		}
		if g := graded(liveSym.ID); g != w {
			t.Fatalf("pass %d: %d LIVE rows graded, want %d (7 never-settling rows sit ahead of them, page 3, cap 2): %s",
				pass+1, g, w, detail)
		}
		if n := resolvedAny(stuckSym.ID); n != 0 {
			t.Fatalf("pass %d: %d STUCK rows resolved; a row with no settled forward bar must keep waiting",
				pass+1, n)
		}
		if pass == 0 && !strings.HasPrefix(detail, "resolved 2,") {
			t.Fatalf("first pass detail must start with 'resolved 2,', got: %q", detail)
		}
	}

	outcomes, err := st.ResolvedOutcomes(ctx, liveSym.ID, md.H1d, 100)
	if err != nil {
		t.Fatal(err)
	}
	for _, o := range outcomes {
		if o.FwdReturn == nil {
			t.Fatalf("LIVE row %d has nil FwdReturn, want a value", o.Ts)
		}
		ret := *o.FwdReturn
		if ret < 0.0099 || ret > 0.0101 {
			t.Fatalf("LIVE row %d: FwdReturn = %f, want between 0.0099 and 0.0101", o.Ts, ret)
		}
	}
}

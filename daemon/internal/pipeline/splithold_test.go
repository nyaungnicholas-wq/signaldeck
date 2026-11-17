package pipeline

import (
	"context"
	"math"
	"testing"
	"time"

	md "github.com/nyaungnicholas-wq/signaldeck/internal/marketdata"
	"github.com/nyaungnicholas-wq/signaldeck/internal/structregime"
)

// An exact 2:1 drop inside the forward window reads as an unadjusted split:
// the row waits until the split-repair worker has examined the move, then is
// graded on the bars the store holds (audit 2026-11-10).
func TestRegimeOutcomeHoldsOnUnexaminedSplit(t *testing.T) {
	ctx := context.Background()
	st := newRegimeOutcomeStore(t, "splithold.db")
	sym, _ := st.UpsertSymbol(ctx, "SPL", md.Stocks, "")
	const start = 1000
	splitAt := 290
	price := func(i int) (float64, float64) {
		p := 100.0 * math.Pow(1.002, float64(i))
		if i >= splitAt {
			p /= 2
		}
		return p, 1e6
	}
	callTs := int64(start+280) * 86400
	clock := callTs + int64(21*1.45*86400) + 10
	seedRegimeBars(t, st, sym.ID, start, 310, price)
	freezeCall(t, st, sym.ID, structregime.KindTrend21, callTs, 21, "uptrend", 0.91, 0.972)
	w := &RegimeOutcomeWorker{St: st, Now: func() time.Time { return time.Unix(clock, 0) }}
	if _, err := w.Run(ctx); err != nil {
		t.Fatal(err)
	}
	if res, _ := st.ResolvedRegimeOutcomes(ctx, 0); len(res) != 0 {
		t.Fatalf("graded across an unexamined split-shaped move: %+v", res)
	}
	// The worker looks at the move and records its verdict.
	if err := st.RecordSplitRepair(ctx, sym.ID, "x", "2:1", 1, false, "confirmed real",
		int64(start+splitAt+2)*86400); err != nil {
		t.Fatal(err)
	}
	if _, err := w.Run(ctx); err != nil {
		t.Fatal(err)
	}
	if res, _ := st.ResolvedRegimeOutcomes(ctx, 0); len(res) != 1 {
		t.Fatalf("after review the row must grade, got %d", len(res))
	}
}

// A review that never comes (inactive symbol, no client) must not hold a row
// forever: past splitfix.MaxHoldSecs the window is graded on the bars held.
func TestRegimeOutcomeSplitHoldIsBounded(t *testing.T) {
	ctx := context.Background()
	st := newRegimeOutcomeStore(t, "splithold2.db")
	sym, _ := st.UpsertSymbol(ctx, "SPM", md.Stocks, "")
	const start = 1000
	price := func(i int) (float64, float64) {
		p := 100.0 * math.Pow(1.002, float64(i))
		if i >= 290 {
			p /= 2
		}
		return p, 1e6
	}
	callTs := int64(start+280) * 86400
	seedRegimeBars(t, st, sym.ID, start, 310, price)
	freezeCall(t, st, sym.ID, structregime.KindTrend21, callTs, 21, "uptrend", 0.91, 0.972)
	late := int64(start+309)*86400 + 11*86400
	w := &RegimeOutcomeWorker{St: st, Now: func() time.Time { return time.Unix(late, 0) }}
	if _, err := w.Run(ctx); err != nil {
		t.Fatal(err)
	}
	if res, _ := st.ResolvedRegimeOutcomes(ctx, 0); len(res) != 1 {
		t.Fatalf("an unreviewed split hold past its bound must grade, got %d", len(res))
	}
}

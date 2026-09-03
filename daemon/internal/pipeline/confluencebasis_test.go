package pipeline

import (
	"context"
	"math"
	"testing"

	md "github.com/nyaungnicholas-wq/signaldeck/internal/marketdata"
	"github.com/nyaungnicholas-wq/signaldeck/internal/store"
)

// A forward return must be computed from TWO PRICES ON ONE BASIS.
//
// entry_px is frozen when a setup is flagged, but the bars underneath it can be
// rewritten afterwards: a reverse split triggers a full re-backfill that rescales
// the whole series. Dividing a live exit close by a frozen PRE-rescale entry does
// not cancel — the rescale factor lands whole in the return.
//
// This is not hypothetical. DFNS was flagged at entry_px 0.0493 and graded
// against a rescaled exit, reporting fwd_return +8541% — on an entry price no bar
// of that symbol has ever carried, its minimum close across all history being
// 3.90. Those rows reach a PUBLISHED money number: /api/confluence/track's
// expectancy read -3.429% against -1.217% for the basis-matched rows.
func TestConfluenceResolve_UsesTheEntryBarNotTheFrozenPrice(t *testing.T) {
	st := openStore(t)
	ctx := context.Background()
	sym, err := st.UpsertSymbol(ctx, "DFNS", md.Stocks, "")
	if err != nil {
		t.Fatalf("upsert: %v", err)
	}

	// The series AFTER a 100:1 reverse-split re-backfill. Entry day closes at 5.00,
	// the exit bar one horizon later at 5.50 — a clean +10%.
	const day = int64(86400)
	entryDay := int64(20000) * day
	seedDailyPx(t, st, sym.ID, [][3]float64{
		{float64(entryDay/day) - 1, 4.90, 4.95},
		{float64(entryDay / day), 4.95, 5.00},   // the entry bar, current basis
		{float64(entryDay/day) + 1, 5.10, 5.20}, // the exit bar: horizon is 1 day
		{float64(entryDay/day) + 2, 5.30, 5.50},
	})

	// The stored outcome still carries the PRE-rescale entry: 0.05, a hundredth of
	// the rebased 5.00. Nothing in the row marks it as stale.
	if err := st.InsertConfluenceOutcome(ctx, store.ConfluenceOutcome{
		SymbolID: sym.ID, Ts: entryDay, Horizon: confluenceHorizon,
		Direction: 1, Agree: 3, EntryPx: 0.05,
	}); err != nil {
		t.Fatalf("insert outcome: %v", err)
	}

	w := &ConfluenceResolver{St: st}
	if _, err := w.Run(ctx); err != nil {
		t.Fatalf("resolve: %v", err)
	}

	rows, err := st.ResolvedConfluenceOutcomes(ctx, 100)
	if err != nil {
		t.Fatalf("read back: %v", err)
	}
	var got *store.ConfluenceOutcome
	for i := range rows {
		if rows[i].SymbolID == sym.ID {
			got = &rows[i]
		}
	}
	if got == nil {
		t.Fatal("the outcome was never resolved")
	}

	// Both legs on the current basis: 5.20/5.00 - 1 = +4%.
	// The frozen entry would have given 5.20/0.05 - 1 = +10,300%.
	if math.Abs(got.FwdReturn-0.04) > 0.001 {
		t.Fatalf("fwd_return = %.4f (%.1f%%), want +0.04 (+4%%). The frozen pre-rescale "+
			"entry would give %.1f%% — a rescale factor landing whole in a published return",
			got.FwdReturn, got.FwdReturn*100, (5.20/0.05-1)*100)
	}
	// The frozen price stays as the audit record of what was seen at call time.
	if math.Abs(got.EntryPx-0.05) > 1e-9 {
		t.Fatalf("entry_px = %v, want it preserved at 0.05 as the audit record", got.EntryPx)
	}
}

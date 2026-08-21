package pipeline

import (
	"context"
	"errors"
	"testing"

	md "github.com/nyaungnicholas-wq/signaldeck/internal/marketdata"
)

// THE FILL WINDOW HAS TWO BOUNDS. ONLY ONE OF THEM CAN BIND.
//
// buildStep refuses a fill outside (LastBarTs, asof]:
//
//	LOWER  fillBar.Ts <= cur.LastBarTs. This is the defect that back-dated 46 of
//	       123 fills by up to 22 days and forced the 2026-07-22 integrity epoch.
//	       Pinned by TestPaperTrader_NeverFillsBehindTheBooksClock, and VERIFIED
//	       mutation-sensitive: deleting the guard fails that test with
//	       "BBB filled at ts=259200 ... at or behind the cursor".
//
//	UPPER  fillBar.Ts > asof. Deleting this guard breaks NO test, and the
//	       finding is that no test can break, because it is unreachable:
//
//	         LIVE     asof is the newest daily bar across the whole universe, so
//	                  no bar of any symbol can exceed it.
//	         REPLAY   universeAt admits only symbols with a universe_membership
//	                  row for that day, and RebuildUniverseMembership writes one
//	                  row per (bar day, symbol). A symbol in the replay universe
//	                  at asof therefore HAS a bar at asof, so the fill anchor
//	                  BarAtOrAfter(pred.Ts+1) lands at or before it.
//
//	       It is defence in depth, not dead code — but it cannot be given an
//	       honest regression test, and a fixture contrived until it "fails"
//	       would be testing the fixture. Recorded here so the next person does
//	       not spend the afternoon I spent.
//
// What actually prevents forward lookahead is the pair of guards that make the
// upper bound redundant. Both must therefore be pinned:
//
//   - predictionFor's strict as-of read (mutation-verified: falling back to
//     LatestPrediction fails TestReplay_UsesTheSignalThatExistedAtTheBarNotTheNewest);
//   - universeAt's refusal of an uncovered day, below — which had NO test, so
//     removing it was silent.

// TestReplay_UniverseRefusesAnUncoveredDay pins the second guard.
//
// Falling back to symbols.active on a day the membership table does not cover
// would judge that day against today's survivors — survivorship bias — AND admit
// symbols with no bar at asof, which is the only way the fill anchor could ever
// jump past the clock. The refusal has to be an error, not a default.
//
// MUTATION CHECK: replace `return nil, &ReplayGapError{AsOf: asof}` in universeAt
// with a fallthrough and this test fails with a nil error and a populated
// universe.
func TestReplay_UniverseRefusesAnUncoveredDay(t *testing.T) {
	w, _ := replayFixture(t)
	ctx := context.Background()

	// A day far beyond every bar, so no membership row can exist for it.
	uncovered := int64(replayBars+500) * 86400
	w.Replay = &ReplayConfig{AsOf: uncovered, MaxPredictionAge: 30 * 86400, StrategySuffix: "-replay"}

	syms, err := w.universeAt(ctx, uncovered)
	if err == nil {
		t.Fatalf("universeAt returned %d symbol(s) and no error for a day the membership table does not "+
			"cover. Falling back to symbols.active judges a past day against today's survivors, and admits "+
			"names with no bar at the clock — the only route by which a fill anchor could land past asof.",
			len(syms))
	}
	var gap *ReplayGapError
	if !errors.As(err, &gap) {
		t.Fatalf("error = %v, want a ReplayGapError naming the day so a caller can act on it", err)
	}
	if gap.AsOf != uncovered {
		t.Fatalf("ReplayGapError.AsOf = %d, want %d", gap.AsOf, uncovered)
	}

	// A COVERED day must still work: fail-closed must not mean fail-always.
	covered := int64(replayBars-20) * 86400
	got, err := w.universeAt(ctx, covered)
	if err != nil {
		t.Fatalf("a covered day was refused: %v", err)
	}
	if len(got) == 0 {
		t.Fatal("a covered day returned an empty universe; the replay would silently reconstruct nothing")
	}
}

// TestReplay_FillNeverExceedsTheAsOfClock asserts the INVARIANT the upper bound
// exists to guarantee, over a run that actually trades. It cannot distinguish
// the guard from its absence — see the file comment — but it does catch a future
// change to the universe or prediction rules that would let an anchor jump the
// clock, which is the failure the bound was written for.
func TestReplay_FillNeverExceedsTheAsOfClock(t *testing.T) {
	w, symID := replayFixture(t)
	ctx := context.Background()

	const asOfBar = replayBars - 20
	seedPrediction(t, w.St, symID, md.H1d, int64(asOfBar-1)*86400, 0.95)
	seedGoodForecast(t, w.St, symID, md.H1d, int64(asOfBar-1)*86400)

	asof := int64(asOfBar) * 86400
	w.Replay = &ReplayConfig{AsOf: asof, MaxPredictionAge: 30 * 86400, StrategySuffix: "-replay"}
	if _, err := w.Run(ctx); err != nil {
		t.Fatalf("run: %v", err)
	}

	traded := 0
	for _, strat := range []string{"flagship-1d-replay", "flagship-1w-replay"} {
		trades, _ := w.St.PaperTrades(ctx, strat, 50)
		traded += len(trades)
		for _, tr := range trades {
			if tr.Ts > asof {
				t.Fatalf("%s filled at day %d, past its as-of clock at day %d: %+v",
					strat, tr.Ts/86400, asOfBar, tr)
			}
		}
	}
	if traded == 0 {
		t.Fatal("no fills at all, so the invariant above held vacuously; the fixture no longer trades")
	}
}

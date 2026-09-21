// C4 (residual) — the fleet-wide live-edge verdict on /api/composite must not be
// decided by a Wilson interval evaluated at the RAW symbol-day count.
//
// Measured on the live 1d record (2026-07-25, data/signaldeck.db) by the
// estimator in internal/clusterstat, over exactly the rows this endpoint reads:
//
//	N = 13,058 symbol-days over 23 distinct days, accuracy 48.10%
//	naive Wilson 95%  : [0.47245, 0.48958]   width 0.01714
//	measured deff     : 14.73  ->  effective N 886.5, not 13,058
//	cluster Wilson 95%: [0.44827, 0.51391]   width 0.06564   (3.83x wider)
//
// Today's live record happens to fail BOTH intervals against its 54.57% naive
// baseline, so the shipped verdict ("no measured edge") is right by luck. The
// defect is the STRENGTH of the test, and it bites in the other direction: a
// record whose raw-N floor clears the naive baseline unlocks "edge proven live"
// on evidence the honest interval cannot support. That is what this file pins.
package api

import (
	"context"
	"strings"
	"testing"

	md "github.com/nyaungnicholas-wq/signaldeck/internal/marketdata"
	"github.com/nyaungnicholas-wq/signaldeck/internal/store"
)

// resetFleetSkillCache clears the package-level fleet grade so one test's
// verdict cannot be served to another. The cache is keyed by store pointer, but
// a stale entry from a previous test would still be returned within its TTL if
// that test used the same address for a different store.
func resetFleetSkillCache(t *testing.T) {
	t.Helper()
	clear := func() {
		fleetSkillCache.Lock()
		fleetSkillCache.valid = false
		fleetSkillCache.Unlock()
	}
	clear()
	t.Cleanup(clear)
}

// seedCompositeClusteredRecord writes a resolved 1d record with STRONG day clustering:
// symbolsPerDay symbols on each of days distinct UTC days, where the model is
// right on almost every symbol on half the days and wrong on almost every symbol
// on the other half. That is the live pattern (98.6% / 99.4% / 8.5% breadth on
// consecutive days), and it is what makes a raw-N interval a fiction.
//
// The composition is chosen so that: pooled accuracy = 60%, market up-rate =
// 54% (so the naive "always up" baseline is 54%), and the two intervals land on
// opposite sides of it.
func seedCompositeClusteredRecord(t *testing.T, st *store.Store, days, symbolsPerDay int) {
	t.Helper()
	ctx := context.Background()
	syms := make([]int64, symbolsPerDay)
	for i := range syms {
		s, err := st.UpsertSymbol(ctx, "CL"+string(rune('A'+i/26))+string(rune('A'+i%26)), md.Stocks, "")
		if err != nil {
			t.Fatalf("upsert symbol %d: %v", i, err)
		}
		syms[i] = s.ID
	}
	// Per 100 symbols on a GOOD day: 50 called up and rose, 37 called down and
	// fell (87 hits), 4 called down but rose, 9 called up but fell — 54 up.
	// Per 100 on a BAD day: 25 up/rose, 8 down/fell (33 hits), 29 down but rose,
	// 38 up but fell — also 54 up. Same up-rate, opposite accuracy.
	type cell struct {
		predUp, actualUp bool
		per100           int
	}
	good := []cell{{true, true, 50}, {false, false, 37}, {false, true, 4}, {true, false, 9}}
	bad := []cell{{true, true, 25}, {false, false, 8}, {false, true, 29}, {true, false, 38}}

	for day := 0; day < days; day++ {
		mix := good
		if day%2 == 1 {
			mix = bad
		}
		// Mid-day UTC so ts/86400 is unambiguous. Day index 20000 (2024-10) sat
		// BEFORE the 2026-07-24 survivorship epoch that
		// ResolvedPredictionOutcomes now floors on, which emptied the fixture;
		// 20658 was the epoch's own day index; since the 2026-09-20 window
		// re-registration the floor is store.GradingEpoch (2026-08-07, day 20672).
		base := int64(20672+day)*86400 + 43200
		i := 0
		for _, c := range mix {
			n := c.per100 * symbolsPerDay / 100
			for k := 0; k < n && i < symbolsPerDay; k++ {
				prob := 0.7
				if !c.predUp {
					prob = 0.3
				}
				fwd := -0.01
				if c.actualUp {
					fwd = 0.01
				}
				ts := base + int64(i)
				if err := st.UpsertPrediction(ctx, store.Prediction{
					SymbolID: syms[i], Horizon: md.H1d, Ts: ts,
					RawProb: prob, CalProb: prob, NUsed: 3, Components: "{}",
				}); err != nil {
					t.Fatalf("upsert prediction: %v", err)
				}
				if err := st.ResolvePrediction(ctx, syms[i], md.H1d, ts, fwd); err != nil {
					t.Fatalf("resolve prediction: %v", err)
				}
				i++
			}
		}
	}
}

// TestFleetEdgeSkill_RawWilsonFloorCannotUnlockProvenEdge is the failing test for
// C4 on this surface. The seeded record's raw-N Wilson floor (~57%) clears the
// 54% naive baseline, so the pre-fix code publishes "edge proven live" and lifts
// the conviction ceiling on every row of the SIGNALS leaderboard. The measured
// design effect on the same rows is ~33x — effective N ~36, floor ~44% — which
// does not clear the baseline. The honest verdict is NOT proven.
func TestFleetEdgeSkill_RawWilsonFloorCannotUnlockProvenEdge(t *testing.T) {
	resetFleetSkillCache(t)
	_, st := newCompositeServer(t)
	seedCompositeClusteredRecord(t, st, 12, 100)

	d := Deps{St: st}
	proven, winRate, note := d.fleetEdgeSkill(context.Background())

	if winRate < 0.59 || winRate > 0.61 {
		t.Fatalf("fixture drifted: pooled accuracy = %.4f, want ~0.60 (note=%q)", winRate, note)
	}
	if proven {
		t.Fatalf("edge declared PROVEN on a record whose honest interval cannot support it: %s", note)
	}
	// The note must carry the sample-size facts the house rule requires beside
	// every N: distinct days, the measured design effect, and the effective N.
	for _, want := range []string{"12 day", "design effect", "effective N"} {
		if !strings.Contains(note, want) {
			t.Errorf("note missing %q: %s", want, note)
		}
	}
}

// TestFleetEdgeGrade_PublishesMeasuredClustering pins the reporting half of the
// rule: raw N alone is never a sample size, so the grade must carry the measured
// design effect, the effective N it implies, and the distinct-day count.
func TestFleetEdgeGrade_PublishesMeasuredClustering(t *testing.T) {
	resetFleetSkillCache(t)
	_, st := newCompositeServer(t)
	seedCompositeClusteredRecord(t, st, 12, 100)

	g := Deps{St: st}.fleetEdgeGrade(context.Background())
	if !g.graded {
		t.Fatalf("grade unavailable: %s", g.note)
	}
	cl := g.cluster
	if cl.N != 1200 || cl.DistinctDays != 12 {
		t.Fatalf("N/days = %d/%d, want 1200/12", cl.N, cl.DistinctDays)
	}
	if cl.DesignEffect <= 5 {
		t.Fatalf("design effect = %.2f, want the strong clustering this fixture encodes", cl.DesignEffect)
	}
	if cl.EffectiveN >= float64(cl.N) {
		t.Fatalf("effective N %.1f >= raw N %d — the correction did not apply", cl.EffectiveN, cl.N)
	}
	if cl.CI == nil || cl.NaiveCIDiscredited == nil {
		t.Fatal("both the corrected and the discredited naive interval must be present at 12 days")
	}
	if cl.CI.Width() <= cl.NaiveCIDiscredited.Width() {
		t.Fatalf("corrected width %.4f is not wider than the naive %.4f", cl.CI.Width(), cl.NaiveCIDiscredited.Width())
	}
}

// TestFleetEdgeSkill_WithheldBelowDayFloor: below the day floor there is no
// honest interval, so there is no "proven" verdict to give — and the reason must
// say which floor was missed rather than defaulting to "no measured edge", which
// is a claim this sample cannot support either.
func TestFleetEdgeSkill_WithheldBelowDayFloor(t *testing.T) {
	resetFleetSkillCache(t)
	_, st := newCompositeServer(t)
	seedCompositeClusteredRecord(t, st, 4, 100) // 400 obs, 4 days — clears N, misses days

	proven, _, note := Deps{St: st}.fleetEdgeSkill(context.Background())
	if proven {
		t.Fatalf("edge proven on 4 distinct days: %s", note)
	}
	if !strings.Contains(note, "4") || !strings.Contains(strings.ToLower(note), "day") {
		t.Fatalf("gate note does not name the day shortfall: %s", note)
	}
}

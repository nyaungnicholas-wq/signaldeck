package xsfactor

import (
	"math"
	"reflect"
	"testing"
)

// series builds a clean synthetic trailing series of n closes: a flat base with
// an alternating ±wobble so realized vol is a known, non-zero number, plus a
// per-bar drift so momentum-12-1 is controllable. dollarVol is repeated for
// every bar so the median is exactly that value.
func series(n int, base, drift, wobble, dollarVol float64) Input {
	in := Input{Closes: make([]float64, n), DollarVols: make([]float64, n)}
	for i := 0; i < n; i++ {
		w := wobble
		if i%2 == 1 {
			w = -wobble
		}
		in.Closes[i] = base + drift*float64(i) + w
		in.DollarVols[i] = dollarVol
	}
	return in
}

func named(sym string, in Input) Input {
	in.Symbol = sym
	in.Market = "stocks"
	return in
}

func rowBySymbol(t *testing.T, res Result, sym string) Row {
	t.Helper()
	for _, r := range res.Rows {
		if r.Symbol == sym {
			return r
		}
	}
	t.Fatalf("symbol %q missing from rows (%d rows, skipped=%v)", sym, len(res.Rows), res.Skipped)
	return Row{}
}

func skipReason(res Result, sym string) (string, bool) {
	for _, s := range res.Skipped {
		if s.Symbol == sym {
			return s.Reason, true
		}
	}
	return "", false
}

func mustRank(t *testing.T, h Horizon, in []Input) Result {
	t.Helper()
	res, err := Rank(h, in)
	if err != nil {
		t.Fatalf("Rank(%s): %v", h, err)
	}
	return res
}

// TestPercentiles pins the ranking contract: ascending, min 0, max 1, tied
// values share the average rank, and a one-member cross-section is 0.5.
func TestPercentiles(t *testing.T) {
	cases := []struct {
		name string
		in   map[int]float64
		want map[int]float64
	}{
		{"empty", map[int]float64{}, map[int]float64{}},
		{"single", map[int]float64{7: 42}, map[int]float64{7: 0.5}},
		{"distinct ascending", map[int]float64{0: 1, 1: 2, 2: 3, 3: 4, 4: 5},
			map[int]float64{0: 0, 1: 0.25, 2: 0.5, 3: 0.75, 4: 1}},
		{"input order irrelevant", map[int]float64{0: 5, 1: 1, 2: 3},
			map[int]float64{0: 1, 1: 0, 2: 0.5}},
		// Three-way tie at the bottom of a 4-set: 0-based ranks 0,1,2 average
		// to 1 → 1/3; the loner takes rank 3 → 1.
		{"ties share the average rank", map[int]float64{0: 1, 1: 1, 2: 1, 3: 9},
			map[int]float64{0: 1.0 / 3, 1: 1.0 / 3, 2: 1.0 / 3, 3: 1}},
		{"all equal", map[int]float64{0: 2, 1: 2, 2: 2},
			map[int]float64{0: 0.5, 1: 0.5, 2: 0.5}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := percentiles(tc.in)
			if len(got) != len(tc.want) {
				t.Fatalf("len=%d, want %d (%v)", len(got), len(tc.want), got)
			}
			for k, want := range tc.want {
				if math.Abs(got[k]-want) > 1e-12 {
					t.Errorf("percentile[%d]=%.6f, want %.6f", k, got[k], want)
				}
			}
		})
	}
}

// TestMetricsPointInTime checks the three metric definitions against hand
// computable series — especially that momentum-12-1 skips the last 21 bars.
func TestMetricsPointInTime(t *testing.T) {
	// Flat series: zero vol, momentum exactly 0.
	flat := series(momMinCloses, 100, 0, 0, 1e6)
	if v := realizedVol(flat.Closes); v == nil || *v != 0 {
		t.Fatalf("realizedVol(flat)=%v, want 0", v)
	}
	if m := mom121(flat.Closes); m == nil || *m != 0 {
		t.Fatalf("mom121(flat)=%v, want 0", m)
	}
	if d := medianDollarVol(flat.DollarVols); d == nil || *d != 1e6 {
		t.Fatalf("medianDollarVol=%v, want 1e6", d)
	}

	// momentum-12-1 must read closes[n-1-21] / closes[n-1-252], i.e. it must
	// IGNORE a spike planted inside the skipped last-21-day window.
	spiked := series(momMinCloses, 100, 0, 0, 1e6)
	for i := len(spiked.Closes) - momSkip; i < len(spiked.Closes); i++ {
		spiked.Closes[i] = 130 // +30% only inside the skipped window
	}
	if m := mom121(spiked.Closes); m == nil || math.Abs(*m) > 1e-12 {
		t.Fatalf("mom121 saw the skipped window: got %v, want 0", m)
	}

	// A step exactly at the 21-days-ago bar DOES count.
	stepped := series(momMinCloses, 100, 0, 0, 1e6)
	stepped.Closes[len(stepped.Closes)-1-momSkip] = 110
	if m := mom121(stepped.Closes); m == nil || math.Abs(*m-0.10) > 1e-12 {
		t.Fatalf("mom121(step to 110)=%v, want +0.10", m)
	}

	// Too-short series yield nil legs, never a zero.
	short := series(momMinCloses-1, 100, 0, 0.5, 1e6)
	if m := mom121(short.Closes); m != nil {
		t.Fatalf("mom121 on %d closes = %v, want nil", len(short.Closes), *m)
	}
	tiny := series(minVolCloses-1, 100, 0, 0.5, 1e6)
	if v := realizedVol(tiny.Closes); v != nil {
		t.Fatalf("realizedVol on %d closes = %v, want nil", len(tiny.Closes), *v)
	}
	// Zero dollar volume is ABSENT liquidity, not zero liquidity.
	if d := medianDollarVol(make([]float64, dollarVolWindow)); d != nil {
		t.Fatalf("medianDollarVol(all zero)=%v, want nil", d)
	}
}

// TestLowVolOutranksHighVol is the headline behavioral claim: all else equal,
// the calmer symbol must rank above the wilder one.
func TestLowVolOutranksHighVol(t *testing.T) {
	calm := named("CALM", series(momMinCloses, 100, 0, 0.10, 5e6))
	wild := named("WILD", series(momMinCloses, 100, 0, 6.00, 5e6))
	res := mustRank(t, H21d, []Input{wild, calm})

	if len(res.Rows) != 2 {
		t.Fatalf("rows=%d, want 2 (skipped=%v)", len(res.Rows), res.Skipped)
	}
	if res.Rows[0].Symbol != "CALM" {
		t.Fatalf("rank 1 = %q, want CALM (composites: %v/%v)",
			res.Rows[0].Symbol, res.Rows[0].Composite, res.Rows[1].Composite)
	}
	c, wl := rowBySymbol(t, res, "CALM"), rowBySymbol(t, res, "WILD")
	if *c.LowVolPct != 1 || *wl.LowVolPct != 0 {
		t.Fatalf("lowVolPct CALM=%v WILD=%v, want 1 and 0", *c.LowVolPct, *wl.LowVolPct)
	}
	if *c.Vol21dAnn <= 0 || *c.Vol21dAnn >= *wl.Vol21dAnn {
		t.Fatalf("vol CALM=%v not below WILD=%v", *c.Vol21dAnn, *wl.Vol21dAnn)
	}
	// Identical dollar volume ⇒ tied liquidity leg at 0.5 for both, so the
	// low-vol leg is what separates them.
	if *c.LiquidityPct != 0.5 || *wl.LiquidityPct != 0.5 {
		t.Fatalf("tied liquidity should be 0.5/0.5, got %v/%v", *c.LiquidityPct, *wl.LiquidityPct)
	}
	if c.Rank != 1 || wl.Rank != 2 {
		t.Fatalf("ranks CALM=%d WILD=%d, want 1 and 2", c.Rank, wl.Rank)
	}
}

// TestLiquidityLegIsDiagnosticOnly pins the RETRACTED size/liquidity leg.
//
// Its percentile is still computed and still points the documented way (the
// SMALLER median dollar volume scores high), because a reader auditing the
// retraction needs to see the quantity that was retracted. What it must never
// do again is move anyone's composite: the re-derivation measures it negative at
// every horizon, so it is reported and not weighted.
func TestLiquidityLegIsDiagnosticOnly(t *testing.T) {
	thin := named("THIN", series(momMinCloses, 100, 0, 0.5, 1e5))
	fat := named("FAT", series(momMinCloses, 100, 0, 0.5, 9e9))
	res := mustRank(t, H5d, []Input{fat, thin})
	if *rowBySymbol(t, res, "THIN").LiquidityPct != 1 {
		t.Fatalf("thin name liquidityPct=%v, want 1 — the diagnostic must survive the retraction",
			*rowBySymbol(t, res, "THIN").LiquidityPct)
	}
	for _, r := range res.Rows {
		if idxOf(r.LegsUsed, LegLiquidity) >= 0 {
			t.Fatalf("%s weighted the retracted liquidity leg (legsUsed=%v)", r.Symbol, r.LegsUsed)
		}
	}
	// Thin vs fat alone must no longer decide the ranking: with vol and
	// momentum tied, both composites are the tied 0.5.
	for _, r := range res.Rows {
		if math.Abs(r.Composite-0.5) > 1e-12 {
			t.Fatalf("%s composite=%.6f, want 0.5 — only the retracted leg separates these two",
				r.Symbol, r.Composite)
		}
	}
}

// TestNoSurvivingLegAtHorizon: at 63d the re-derivation left NOTHING, so every
// symbol is refused with a stated reason rather than scored on an empty average.
// The failure it prevents is a composite of zero legs rendering as 0.0 and being
// read as a rank.
func TestNoSurvivingLegAtHorizon(t *testing.T) {
	if got := CompositeLegs(H63d); len(got) != 0 {
		t.Fatalf("CompositeLegs(63d)=%v, want empty — no leg survived re-derivation there", got)
	}
	res := mustRank(t, H63d, []Input{
		named("AAA", series(momMinCloses, 100, 0, 0.10, 1e5)),
		named("BBB", series(momMinCloses, 100, 0, 6.00, 9e9)),
	})
	if len(res.Rows) != 0 {
		t.Fatalf("rows=%d at 63d, want 0", len(res.Rows))
	}
	for _, s := range res.Skipped {
		if !contains(s.Reason, "no measured-edge leg computable") {
			t.Fatalf("%s skipped without naming the cause: %q", s.Symbol, s.Reason)
		}
	}
	if len(res.Withheld) != 3 {
		t.Fatalf("withheld=%d at 63d, want all 3 legs reported with their reasons", len(res.Withheld))
	}
}

// TestRenormalizationOverPresentLegs: a symbol missing a leg must be scored on
// the mean of the legs it HAS — an absent leg must not drag it toward 0.
func TestRenormalizationOverPresentLegs(t *testing.T) {
	// SHORTY has just enough history for vol + median dollar volume, but not
	// for momentum-12-1. At 5d (where momentum IS a measured leg) it must be
	// scored on 2 legs, not 3.
	shorty := named("SHORTY", series(momMinCloses-1, 100, 0, 0.10, 1e5))
	full := named("FULL", series(momMinCloses, 100, 0.05, 6.00, 9e9))
	res := mustRank(t, H5d, []Input{shorty, full})

	s := rowBySymbol(t, res, "SHORTY")
	if s.Mom121 != nil || s.Mom121Pct != nil {
		t.Fatalf("SHORTY should have no momentum leg, got %v/%v", s.Mom121, s.Mom121Pct)
	}
	// Liquidity is retracted, so the only published leg SHORTY can carry is
	// low-vol; momentum is published at 5d but SHORTY is too short for it.
	wantLegs := []string{LegLowVol}
	if !reflect.DeepEqual(s.LegsUsed, wantLegs) {
		t.Fatalf("legsUsed=%v, want %v", s.LegsUsed, wantLegs)
	}
	// Its one present leg is 1 (the calmer of the two), so a correctly
	// renormalized composite is 1.0 — averaging over 2 published slots would
	// give 0.5 and an absent leg would have been scored as a zero.
	if math.Abs(s.Composite-1.0) > 1e-12 {
		t.Fatalf("composite=%.6f, want 1.0 (renormalized over %d present legs)", s.Composite, len(s.LegsUsed))
	}

	f := rowBySymbol(t, res, "FULL")
	if !reflect.DeepEqual(f.LegsUsed, []string{LegLowVol, LegMom121}) {
		t.Fatalf("FULL legsUsed=%v, want the two published legs at 5d", f.LegsUsed)
	}
	// FULL is the wildest (low-vol leg 0) but the only momentum holder, so its
	// momentum percentile is the single-member 0.5 → composite 0.25.
	if math.Abs(f.Composite-0.25) > 1e-12 {
		t.Fatalf("FULL composite=%.6f, want 0.25", f.Composite)
	}
}

// TestUnmeasuredLegNotWeighted: momentum has no measured edge at 21d/63d, so it
// must appear as a diagnostic percentile and stay OUT of the composite.
func TestUnmeasuredLegNotWeighted(t *testing.T) {
	// Both symbols share an IDENTICAL recent window (same realized vol, same
	// median dollar volume); they differ only in the 252-days-ago close, which
	// moves momentum-12-1 and nothing else. So the measured legs are tied and
	// only the unweighted diagnostic differs.
	a := named("AAA", series(momMinCloses, 100, 0, 0.10, 1e5))
	b := named("BBB", series(momMinCloses, 100, 0, 0.10, 1e5))
	a.Closes[0] = 80  // momentum base 252d ago → strong prior-year gain
	b.Closes[0] = 120 // → prior-year loss
	res := mustRank(t, H21d, []Input{a, b})

	if got := CompositeLegs(H21d); !reflect.DeepEqual(got, []string{LegLowVol}) {
		t.Fatalf("CompositeLegs(21d)=%v, want lowVol only (liquidity is retracted, "+
			"momentum's 21d interval spans zero)", got)
	}
	for _, r := range res.Rows {
		if r.Mom121Pct == nil {
			t.Fatalf("%s: momentum percentile should still be reported as a diagnostic", r.Symbol)
		}
		for _, leg := range r.LegsUsed {
			if leg == LegMom121 {
				t.Fatalf("%s: momentum must not be weighted at 21d (legsUsed=%v)", r.Symbol, r.LegsUsed)
			}
		}
	}
	// No measured-edge leg separates them here (identical vol and volume), so
	// both composites are the tied 0.5.
	for _, r := range res.Rows {
		if math.Abs(r.Composite-0.5) > 1e-12 {
			t.Fatalf("%s composite=%.6f, want 0.5", r.Symbol, r.Composite)
		}
	}
	if *rowBySymbol(t, res, "AAA").Mom121 <= *rowBySymbol(t, res, "BBB").Mom121 {
		t.Fatal("AAA should carry the stronger momentum-12-1")
	}
	// At 5d, where momentum IS measured, the same pair must separate.
	res5 := mustRank(t, H5d, []Input{a, b})
	if res5.Rows[0].Symbol != "AAA" {
		t.Fatalf("5d rank 1 = %q, want AAA (momentum is a measured leg at 5d)", res5.Rows[0].Symbol)
	}
}

// TestSplitGuardRejects: a single impossible daily move disqualifies the whole
// trailing series, the symbol is named with a stated reason, and it is counted.
func TestSplitGuardRejects(t *testing.T) {
	good := named("GOOD", series(momMinCloses, 100, 0, 0.10, 1e6))
	split := named("SPLIT", series(momMinCloses, 100, 0, 0.10, 1e6))
	split.Closes[100] = split.Closes[99] * 0.25 // 4:1 split artifact: -75%

	res := mustRank(t, H21d, []Input{good, split})
	if len(res.Rows) != 1 || res.Rows[0].Symbol != "GOOD" {
		t.Fatalf("rows=%v, want GOOD only", res.Rows)
	}
	reason, ok := skipReason(res, "SPLIT")
	if !ok {
		t.Fatalf("SPLIT missing from skipped: %v", res.Skipped)
	}
	if res.SplitRejected != 1 {
		t.Fatalf("splitRejected=%d, want 1", res.SplitRejected)
	}
	for _, want := range []string{"0.65", "split/data artifact"} {
		if !contains(reason, want) {
			t.Fatalf("skip reason %q missing %q", reason, want)
		}
	}

	// Just UNDER the guard is kept — the guard rejects artifacts, not volatility.
	edge := named("EDGE", series(momMinCloses, 100, 0, 0, 1e6))
	edge.Closes[100] = edge.Closes[99] * (1 + ImpossibleDailyMove - 0.01)
	res2 := mustRank(t, H21d, []Input{edge})
	if len(res2.Rows) != 1 || res2.SplitRejected != 0 {
		t.Fatalf("a %.2f move should be kept: rows=%d splitRejected=%d",
			ImpossibleDailyMove-0.01, len(res2.Rows), res2.SplitRejected)
	}
}

// TestUnavailableNotZeroScored: a symbol with no computable leg is reported as
// skipped with a reason, never ranked at composite 0.
func TestUnavailableNotZeroScored(t *testing.T) {
	empty := named("EMPTY", Input{})
	stub := named("STUB", series(3, 100, 0, 0.1, 1e6)) // too short for every leg
	good := named("GOOD", series(momMinCloses, 100, 0, 0.1, 1e6))
	res := mustRank(t, H21d, []Input{empty, stub, good})

	if len(res.Rows) != 1 || res.Rows[0].Symbol != "GOOD" {
		t.Fatalf("rows=%v, want GOOD only", res.Rows)
	}
	if res.UniverseN != 1 {
		t.Fatalf("universeN=%d, want 1", res.UniverseN)
	}
	for _, sym := range []string{"EMPTY", "STUB"} {
		reason, ok := skipReason(res, sym)
		if !ok {
			t.Fatalf("%s missing from skipped: %v", sym, res.Skipped)
		}
		if !contains(reason, "no leg computable") {
			t.Fatalf("%s reason=%q, want a stated no-leg reason", sym, reason)
		}
	}
}

// TestDeterminism: the same inputs in a different order must give the identical
// ranking, including tie order, and repeated calls must not drift.
func TestDeterminism(t *testing.T) {
	in := []Input{
		named("AAA", series(momMinCloses, 100, 0.01, 0.10, 1e5)),
		named("BBB", series(momMinCloses, 100, 0.01, 0.10, 1e5)), // byte-identical series to AAA → a real tie
		named("CCC", series(momMinCloses, 100, 0.01, 2.00, 7e8)),
		named("DDD", series(momMinCloses-40, 100, 0, 0.50, 3e6)),
		named("EEE", series(momMinCloses, 100, -0.02, 0.80, 2e7)),
	}
	base := mustRank(t, H5d, in)
	if len(base.Rows) != 5 {
		t.Fatalf("rows=%d, want 5 (skipped=%v)", len(base.Rows), base.Skipped)
	}
	// AAA and BBB are metric-identical, so the deterministic tiebreak (symbol
	// ascending) must put AAA above BBB every time.
	order := func(res Result) []string {
		out := make([]string, len(res.Rows))
		for i, r := range res.Rows {
			out[i] = r.Symbol
		}
		return out
	}
	want := order(base)
	if idxOf(want, "AAA") > idxOf(want, "BBB") {
		t.Fatalf("tie order %v does not break by symbol", want)
	}
	for i := 0; i < 5; i++ {
		shuffled := make([]Input, len(in))
		for j := range in { // deterministic rotation, not rand
			shuffled[j] = in[(j+i+1)%len(in)]
		}
		got := mustRank(t, H5d, shuffled)
		if !reflect.DeepEqual(order(got), want) {
			t.Fatalf("rotation %d order=%v, want %v", i, order(got), want)
		}
		for k := range got.Rows {
			if got.Rows[k].Composite != base.Rows[k].Composite {
				t.Fatalf("rotation %d composite[%d]=%v, want %v",
					i, k, got.Rows[k].Composite, base.Rows[k].Composite)
			}
		}
	}
}

// TestMeasuredEdgeConstants: the PUBLISHED numbers ship exactly as re-derived,
// no leg is invented where nothing survived, and the block is copy-safe.
// TestConstantsAreRederivable in edge_test.go is what ties these to the script;
// this pins the exact set so a leg cannot be added or dropped unnoticed.
func TestMeasuredEdgeConstants(t *testing.T) {
	want := map[Horizon]map[string][3]float64{
		H5d: {
			LegLowVol: {1.10, 0.17, 2.06},
			LegMom121: {1.24, 0.34, 2.13},
		},
		H21d: {
			LegLowVol: {1.99, 0.17, 3.81},
		},
		H63d: {}, // nothing survived re-derivation at a quarterly horizon
	}
	for _, h := range Horizons {
		legs := MeasuredEdge(h)
		if len(legs) != len(want[h]) {
			t.Fatalf("%s: %d published legs, want %d (%v)", h, len(legs), len(want[h]), legs)
		}
		for _, l := range legs {
			w, ok := want[h][l.Leg]
			if !ok {
				t.Fatalf("%s: unexpected leg %q — no surviving measurement exists for it", h, l.Leg)
			}
			if l.EdgePP != w[0] || l.CILow != w[1] || l.CIHigh != w[2] {
				t.Fatalf("%s/%s = %+v, want %v", h, l.Leg, l, w)
			}
			if !l.Positive() {
				t.Fatalf("%s/%s CI touches zero (%v) — it would not be a published edge", h, l.Leg, l)
			}
			if l.Status != StatusPublished {
				t.Fatalf("%s/%s status=%q in the published block", h, l.Leg, l.Status)
			}
			if l.N <= 0 || l.DistinctDays <= 0 {
				t.Fatalf("%s/%s ships without an N or a distinct-day count: %+v", h, l.Leg, l)
			}
		}
		// Every leg is accounted for at every horizon: published + withheld
		// must be the full set of three, so nothing can vanish silently.
		if got := len(MeasuredEdge(h)) + len(WithheldEdge(h)); got != 3 {
			t.Fatalf("%s: %d legs accounted for, want all 3", h, got)
		}
		for _, l := range WithheldEdge(h) {
			if l.Reason == "" {
				t.Fatalf("%s/%s is withheld without a stated reason", h, l.Leg)
			}
			if l.Status == StatusPublished {
				t.Fatalf("%s/%s is in the withheld block with status published", h, l.Leg)
			}
		}
	}
	// Momentum is published ONLY at 5d.
	if idxOf(CompositeLegs(H21d), LegMom121) >= 0 || idxOf(CompositeLegs(H63d), LegMom121) >= 0 {
		t.Fatal("momentum reached a composite beyond 5d")
	}
	// Mutating the returned copy must not touch the constants.
	c := MeasuredEdge(H21d)
	c[0].EdgePP = 99
	if MeasuredEdge(H21d)[0].EdgePP == 99 {
		t.Fatal("MeasuredEdge leaked its backing array")
	}
	w := WithheldEdge(H21d)
	w[0].EdgePP = 99
	if WithheldEdge(H21d)[0].EdgePP == 99 {
		t.Fatal("WithheldEdge leaked its backing array")
	}
	if MeasuredEdge("7d") != nil || WithheldEdge("7d") != nil || AllMeasured("7d") != nil {
		t.Fatal("the edge block invented a leg for an unmeasured horizon")
	}
}

func TestParseHorizonAndUnknownRank(t *testing.T) {
	for _, s := range []string{"5d", "21d", "63d"} {
		if h, ok := ParseHorizon(s); !ok || string(h) != s {
			t.Fatalf("ParseHorizon(%q)=%v,%v", s, h, ok)
		}
	}
	for _, s := range []string{"", "1d", "5D", "21", "252d"} {
		if _, ok := ParseHorizon(s); ok {
			t.Fatalf("ParseHorizon(%q) accepted an unmeasured horizon", s)
		}
	}
	if _, err := Rank("1d", []Input{named("A", series(300, 100, 0, 1, 1e6))}); err == nil {
		t.Fatal("Rank accepted an unmeasured horizon")
	}
}

// TestCaveatCarriesHonestFraming keeps the non-negotiable framing in the
// payload: relative-not-directional, the named public factors, the measured
// ceiling, the retraction, and the name of the script that derives it all.
func TestCaveatCarriesHonestFraming(t *testing.T) {
	for _, want := range []string{
		"same-day universe MEDIAN forward return",
		"base rate is exactly 50%",
		"LOW-VOLATILITY ANOMALY",
		"SIZE/LIQUIDITY PREMIUM",
		"RETRACTED",
		"capacity-constrained",
		"risk-compensation rather than free alpha",
		"51.1-52.0%",
		"realistic ceiling, not an oracle",
		"Bonferroni",
	} {
		if !contains(Caveat, want) {
			t.Errorf("Caveat missing %q", want)
		}
	}
	// The provenance claim H2 was raised about: the payload must name the
	// script, and the script must be the one the constants are checked against.
	for _, want := range []string{DerivationScript, "NON-OVERLAPPING", "1,900 trading days",
		"one observation per (symbol, UTC-day)", "FORMATION DAYS"} {
		if !contains(EvidenceNote, want) {
			t.Errorf("EvidenceNote missing %q: %q", want, EvidenceNote)
		}
	}
	// The correction itself ships; it is not left to a changelog nobody reads.
	for _, want := range []string{"H2", "+2.50pp", "-1.65", "RETRACTED"} {
		if !contains(RetractionNote, want) {
			t.Errorf("RetractionNote missing %q: %q", want, RetractionNote)
		}
	}
	if !contains(MethodNote, "point-in-time") {
		t.Errorf("MethodNote lost the point-in-time statement: %q", MethodNote)
	}
	if !contains(GateNoMeasuredLeg, "no leg survives re-derivation") {
		t.Errorf("GateNoMeasuredLeg lost its reason: %q", GateNoMeasuredLeg)
	}
}

func contains(hay, needle string) bool {
	return len(needle) <= len(hay) && indexOf(hay, needle) >= 0
}

func indexOf(hay, needle string) int {
	for i := 0; i+len(needle) <= len(hay); i++ {
		if hay[i:i+len(needle)] == needle {
			return i
		}
	}
	return -1
}

func idxOf(xs []string, want string) int {
	for i, x := range xs {
		if x == want {
			return i
		}
	}
	return -1
}

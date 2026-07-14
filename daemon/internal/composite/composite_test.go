package composite

import (
	"fmt"
	"math"
	"strings"
	"testing"

	"github.com/nyaungnicholas-wq/signaldeck/internal/ensemble"
)

func fp(v float64) *float64 { return &v }

// TestForcedCurveDistribution: over 100 symbols the forced curve MUST hand out
// exactly the fixed band counts — that is what "forced" means.
func TestForcedCurveDistribution(t *testing.T) {
	edges := make([]Edge, 100)
	for i := range edges {
		// Descending edge: symbol 0 is the best.
		edges[i] = Edge{SymbolID: int64(i), Symbol: fmt.Sprintf("S%03d", i), Edge: 0.5 - float64(i)*0.001}
	}
	scores, ok := ForcedCurve(edges)
	if !ok {
		t.Fatal("curve gated with 100 symbols")
	}
	counts := map[int]int{}
	for _, s := range scores {
		counts[s.Score]++
	}
	want := map[int]int{10: 5, 9: 10, 8: 10, 7: 10, 6: 15, 5: 15, 4: 10, 3: 10, 2: 10, 1: 5}
	for sc, n := range want {
		if counts[sc] != n {
			t.Errorf("score %d: %d symbols, want %d (all: %v)", sc, counts[sc], n, counts)
		}
	}
	// The best edge gets a 10, the worst a 1.
	if scores[0].SymbolID != 0 || scores[0].Score != 10 {
		t.Errorf("best symbol: %+v, want id=0 score=10", scores[0])
	}
	if last := scores[len(scores)-1]; last.SymbolID != 99 || last.Score != 1 {
		t.Errorf("worst symbol: %+v, want id=99 score=1", last)
	}
}

// TestForcedCurveGate: below MinCurveN symbols NO scores are emitted.
func TestForcedCurveGate(t *testing.T) {
	edges := make([]Edge, MinCurveN-1)
	for i := range edges {
		edges[i] = Edge{SymbolID: int64(i), Symbol: fmt.Sprintf("S%d", i), Edge: float64(i)}
	}
	if _, ok := ForcedCurve(edges); ok {
		t.Fatalf("curve emitted over %d symbols, want gate at %d", len(edges), MinCurveN)
	}
	edges = append(edges, Edge{SymbolID: 999, Symbol: "S999", Edge: -1})
	if _, ok := ForcedCurve(edges); !ok {
		t.Fatalf("curve gated at exactly %d symbols, want emit", MinCurveN)
	}
}

// TestForcedCurveDeterministicTies: equal edges rank by symbol name, so a
// rerun over the same inputs assigns the same scores.
func TestForcedCurveDeterministicTies(t *testing.T) {
	edges := make([]Edge, MinCurveN)
	for i := range edges {
		edges[i] = Edge{SymbolID: int64(i), Symbol: fmt.Sprintf("S%02d", i), Edge: 0.1}
	}
	a, _ := ForcedCurve(edges)
	b, _ := ForcedCurve(edges)
	for i := range a {
		if a[i] != b[i] {
			t.Fatalf("tie ordering not deterministic at %d: %+v vs %+v", i, a[i], b[i])
		}
	}
	if a[0].SymbolID != 0 { // "S00" sorts first among equals
		t.Fatalf("tie-break by symbol: first = id %d, want 0", a[0].SymbolID)
	}
}

// TestBuildLedger: the ledger must sum EXACTLY to (calProb-0.5) in pp, and
// only claim exactness when the equal-weight decomposition reproduces rawProb.
func TestBuildLedger(t *testing.T) {
	cases := []struct {
		name      string
		c         ensemble.Components
		raw, cal  float64
		wantExact bool
	}{
		{
			name:      "single leg equal-weight is exact",
			c:         ensemble.Components{PressureScore: 0.4}, // leg = 0.7
			raw:       0.7,
			cal:       0.65,
			wantExact: true,
		},
		{
			name: "two legs equal-weight is exact",
			c: ensemble.Components{
				PressureScore:     0.4,      // leg 0.70
				ExpectancyHitRate: fp(0.60), // leg 0.60
			},
			raw:       0.65, // mean(0.70, 0.60)
			cal:       0.62,
			wantExact: true,
		},
		{
			name: "weighted blend falls back to proportional attribution",
			c: ensemble.Components{
				PressureScore:     0.4,
				ExpectancyHitRate: fp(0.60),
			},
			raw:       0.68, // NOT the equal-weight mean -> learned weights were used
			cal:       0.63,
			wantExact: false,
		},
		{
			name: "gated legs are absent from the ledger",
			c: ensemble.Components{
				PressureScore: 0.0,     // leg 0.5
				ForecastProb:  fp(0.9), // gated: lift <= 0
				ForecastLift:  fp(-0.02),
			},
			raw:       0.5,
			cal:       0.5,
			wantExact: true,
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			l := BuildLedger(tc.c, tc.raw, tc.cal)
			if l.Exact != tc.wantExact {
				t.Fatalf("exact = %v, want %v (method %q)", l.Exact, tc.wantExact, l.Method)
			}
			if tc.wantExact && !strings.Contains(l.Method, "exact") {
				t.Fatalf("exact ledger method %q does not say so", l.Method)
			}
			if !tc.wantExact && !strings.Contains(l.Method, "proportional attribution") {
				t.Fatalf("inexact ledger method %q must be labeled proportional attribution", l.Method)
			}
			target := (tc.cal - 0.5) * 100
			if math.Abs(l.SumPP-target) > 1e-9 {
				t.Fatalf("ledger sums to %.6fpp, want %.6fpp", l.SumPP, target)
			}
			if math.Abs(l.TargetPP-target) > 1e-12 {
				t.Fatalf("targetPp = %.6f, want %.6f", l.TargetPP, target)
			}
			// A gated forecast leg must not appear.
			for _, e := range l.Entries {
				if e.Leg == ensemble.LegForecast && tc.c.ForecastLift != nil && *tc.c.ForecastLift <= 0 {
					t.Fatalf("gated forecast leg leaked into the ledger: %+v", e)
				}
			}
			// Calibration line is always last and equals cal-raw.
			last := l.Entries[len(l.Entries)-1]
			if last.Leg != "calibration" || math.Abs(last.ContribPP-(tc.cal-tc.raw)*100) > 1e-9 {
				t.Fatalf("calibration line = %+v, want contrib %.4fpp", last, (tc.cal-tc.raw)*100)
			}
		})
	}
}

// TestBuildFactors: gates render as explicit reasons; verdict semantics hold.
func TestBuildFactors(t *testing.T) {
	byKey := func(fs []Factor, key string) Factor {
		t.Helper()
		for _, f := range fs {
			if f.Key == key {
				return f
			}
		}
		t.Fatalf("factor %q missing", key)
		return Factor{}
	}

	t.Run("everything absent gates with reasons", func(t *testing.T) {
		fs := BuildFactors(Inputs{Components: ensemble.Components{PressureScore: 0}})
		if len(fs) != 13 {
			t.Fatalf("got %d factors, want 13", len(fs))
		}
		for _, key := range []string{FactorExpectancy, FactorForecast, FactorSentiment,
			FactorGBM, FactorMeanRev, FactorAlphaX, FactorRanking, FactorRegime, FactorInsiders,
			FactorShortVol, FactorBreakout, FactorTVRating} {
			f := byKey(fs, key)
			if !f.Gated || f.GateReason == "" || f.Verdict != 0 {
				t.Errorf("%s: gated=%v reason=%q verdict=%d — want explicit gate", key, f.Gated, f.GateReason, f.Verdict)
			}
		}
		// Pressure is always present: neutral at score 0.
		if f := byKey(fs, FactorTechnical); f.Gated || f.Verdict != 0 {
			t.Errorf("technical at score 0: %+v, want present + neutral", f)
		}
	})

	t.Run("edgeless model leg gates with its measured lift", func(t *testing.T) {
		fs := BuildFactors(Inputs{Components: ensemble.Components{
			PressureScore: 0.5,
			ForecastProb:  fp(0.8),
			ForecastLift:  fp(-0.031),
		}})
		f := byKey(fs, FactorForecast)
		if !f.Gated || !strings.Contains(f.GateReason, "-0.031") {
			t.Fatalf("edgeless forecast: %+v — gate must state the measured lift", f)
		}
	})

	t.Run("alphax tile: relative framing, gate, and skill chip", func(t *testing.T) {
		fs := BuildFactors(Inputs{
			Components: ensemble.Components{
				PressureScore: 0,
				AlphaXProb:    fp(0.68),
				AlphaXLift:    fp(0.041),
			},
			SkillHitRates: map[string]float64{ensemble.LegAlphaX: 0.57},
			SkillICs:      map[string]float64{ensemble.LegAlphaX: 0.09},
			SkillLegN:     map[string]int{ensemble.LegAlphaX: 51},
		})
		if len(fs) != 13 {
			t.Fatalf("got %d factors, want 13", len(fs))
		}
		f := byKey(fs, FactorAlphaX)
		if f.Gated || f.Verdict != 1 {
			t.Fatalf("alphax with edge: %+v, want present +1", f)
		}
		// The evidence line MUST carry the category nuance: the prob is
		// relative to the universe, not an absolute P(up).
		if !strings.Contains(f.Evidence, "P(beat universe median) 68.0%") ||
			!strings.Contains(f.Evidence, "+0.041") ||
			!strings.Contains(f.Evidence, "relative-to-universe, blended as directional tilt") {
			t.Fatalf("alphax evidence must state the relative framing: %q", f.Evidence)
		}
		if f.SkillN == nil || *f.SkillN != 51 || f.SkillHitRate == nil || *f.SkillHitRate != 0.57 ||
			f.SkillIC == nil || *f.SkillIC != 0.09 {
			t.Fatalf("alphax skill chip: %+v, want (hr .57, ic .09, n 51)", f)
		}

		// Edgeless: gated with the measured lift, identical to the other legs.
		fs = BuildFactors(Inputs{Components: ensemble.Components{
			PressureScore: 0, AlphaXProb: fp(0.68), AlphaXLift: fp(-0.012),
		}})
		f = byKey(fs, FactorAlphaX)
		if !f.Gated || f.Verdict != 0 || !strings.Contains(f.GateReason, "-0.012") {
			t.Fatalf("edgeless alphax: %+v — gate must state the measured lift", f)
		}
	})

	t.Run("verdicts and skill chips", func(t *testing.T) {
		fs := BuildFactors(Inputs{
			Components: ensemble.Components{
				PressureScore:     0.4,      // leg 0.70 -> +1
				ExpectancyHitRate: fp(0.42), // -1 (<=0.45)
				GBMProb:           fp(0.52), // 0 (dead band)
				GBMLift:           fp(0.05),
			},
			SkillHitRates: map[string]float64{ensemble.LegPressure: 0.58},
			SkillICs:      map[string]float64{ensemble.LegPressure: 0.11},
			SkillLegN:     map[string]int{ensemble.LegPressure: 44},
			RankPct:       fp(82),
			RegimeLabel:   "trending_up",
			InsiderBuys:   500_000, InsiderNBuys: 3,
			InsiderSells: 88_000, InsiderNSells: 1,
		})
		if f := byKey(fs, FactorTechnical); f.Verdict != 1 || f.SkillN == nil || *f.SkillN != 44 ||
			f.SkillHitRate == nil || *f.SkillHitRate != 0.58 || f.SkillIC == nil || *f.SkillIC != 0.11 {
			t.Errorf("technical: %+v, want verdict +1 with skill chip (hr .58, ic .11, n 44)", f)
		}
		if f := byKey(fs, FactorExpectancy); f.Verdict != -1 {
			t.Errorf("expectancy at 42%%: verdict %d, want -1", f.Verdict)
		}
		if f := byKey(fs, FactorGBM); f.Verdict != 0 || f.Gated {
			t.Errorf("gbm at 52%% (dead band): %+v, want present + neutral", f)
		}
		if f := byKey(fs, FactorRanking); f.Verdict != 1 || !strings.Contains(f.Evidence, "82") {
			t.Errorf("ranking 82: %+v, want +1 with the raw number", f)
		}
		// Regime is context, NEVER scored.
		if f := byKey(fs, FactorRegime); f.Verdict != 0 || !strings.Contains(f.Evidence, "never scored") {
			t.Errorf("regime: %+v, want verdict 0 + 'never scored'", f)
		}
		if f := byKey(fs, FactorInsiders); f.Verdict != 1 ||
			!strings.Contains(f.Evidence, "+$412K") || !strings.Contains(f.Evidence, "$500K") {
			t.Errorf("insiders net +412K: %+v", f)
		}
	})

	t.Run("shortvol is never directional and carries the verbatim caveat", func(t *testing.T) {
		ratios := make([]float64, 30)
		for i := range ratios {
			ratios[i] = 0.40 + float64(i%5)*0.01
		}
		ratios[len(ratios)-1] = 0.60 // a genuinely high last ratio
		fs := BuildFactors(Inputs{Components: ensemble.Components{PressureScore: 0}, ShortRatios: ratios})
		f := byKey(fs, FactorShortVol)
		if f.Gated || f.Verdict != 0 {
			t.Fatalf("shortvol: %+v, want present + verdict 0 (high ratio is NOT directly bearish)", f)
		}
		if !strings.Contains(f.Evidence, ShortVolCaveat) {
			t.Fatalf("shortvol evidence %q missing the verbatim caveat", f.Evidence)
		}
	})

	t.Run("breakout verdict follows kind direction", func(t *testing.T) {
		for kind, want := range map[string]int{"donchian_up": 1, "donchian_down": -1, "volume_spike": 0} {
			fs := BuildFactors(Inputs{
				Components: ensemble.Components{PressureScore: 0},
				Breakout:   &BreakoutInfo{Kind: kind, Detail: "test", AgeDays: 1.5},
			})
			if f := byKey(fs, FactorBreakout); f.Verdict != want || f.Gated {
				t.Errorf("breakout %s: verdict %d gated=%v, want %d ungated", kind, f.Verdict, f.Gated, want)
			}
		}
	})

	t.Run("tvrating: absent → gated; unmeasured → context-only; measured → scores", func(t *testing.T) {
		// No rating stored: gated absent.
		fs := BuildFactors(Inputs{Components: ensemble.Components{PressureScore: 0}})
		if f := byKey(fs, FactorTVRating); !f.Gated || !strings.Contains(f.GateReason, "no TradingView rating") {
			t.Errorf("tvrating absent: %+v, want gated with reason", f)
		}

		// Rating present but skill below the gate (n<30): CONTEXT-only, verdict 0,
		// but the external evidence + the measured count are still shown.
		fs = BuildFactors(Inputs{
			Components:     ensemble.Components{PressureScore: 0},
			TVRating:       &TVRatingInfo{RecoAll: 0.42, Label: "Buy"},
			TVSkillIC:      0.20, // strong IC, but…
			TVSkillN:       12,   // …too few independent obs
			TVSkillHitRate: 0.6,
		})
		f := byKey(fs, FactorTVRating)
		if !f.Gated || f.Verdict != 0 || !strings.Contains(f.GateReason, "12/30") {
			t.Errorf("tvrating unmeasured: %+v, want gated context-only with (12/30)", f)
		}
		if !strings.Contains(f.Evidence, "TradingView rating: Buy (+0.42)") {
			t.Errorf("tvrating evidence %q missing the rating line", f.Evidence)
		}
		if f.SkillN != nil {
			t.Errorf("tvrating context-only must not carry a skill chip: %+v", f)
		}

		// Skill clears both gates: scores by sign(reco_all) with its skill chip.
		fs = BuildFactors(Inputs{
			Components:     ensemble.Components{PressureScore: 0},
			TVRating:       &TVRatingInfo{RecoAll: 0.42, Label: "Buy"},
			TVSkillIC:      0.11,
			TVSkillN:       44,
			TVSkillHitRate: 0.57,
		})
		f = byKey(fs, FactorTVRating)
		if f.Gated || f.Verdict != 1 || f.SkillN == nil || *f.SkillN != 44 ||
			f.SkillIC == nil || *f.SkillIC != 0.11 || f.SkillHitRate == nil || *f.SkillHitRate != 0.57 {
			t.Errorf("tvrating measured: %+v, want +1 with skill chip (hr .57, ic .11, n 44)", f)
		}

		// A cleared-N but flat |IC| below the floor stays context-only.
		fs = BuildFactors(Inputs{
			Components: ensemble.Components{PressureScore: 0},
			TVRating:   &TVRatingInfo{RecoAll: -0.9, Label: "Strong Sell"},
			TVSkillIC:  0.01, // below TVRatingICFloor
			TVSkillN:   80,
		})
		if f := byKey(fs, FactorTVRating); !f.Gated || f.Verdict != 0 {
			t.Errorf("tvrating |IC| below floor: %+v, want gated context-only", f)
		}
	})
}

// TestShortVolZ mirrors api/shorts.go's gates: needs 10+ prior days and a
// non-flat baseline; the z itself is last-vs-prior mean/sd.
func TestShortVolZ(t *testing.T) {
	if _, ok := ShortVolZ(make([]float64, ShortVolMinPrior)); ok {
		t.Fatal("z computed below the prior-days floor")
	}
	flat := make([]float64, ShortVolMinPrior+1)
	for i := range flat {
		flat[i] = 0.5
	}
	if _, ok := ShortVolZ(flat); ok {
		t.Fatal("z computed against a flat baseline")
	}
	// 10 prior alternating 0.4/0.6 (mean 0.5, sd 0.1), last 0.7 -> z = +2.
	ratios := []float64{0.4, 0.6, 0.4, 0.6, 0.4, 0.6, 0.4, 0.6, 0.4, 0.6, 0.7}
	z, ok := ShortVolZ(ratios)
	if !ok || math.Abs(z-2.0) > 1e-9 {
		t.Fatalf("z = %v ok=%v, want 2.0", z, ok)
	}
}

// Regression (adversarial review, HIGH): bit-identical edges are identical
// evidence — the whole tie block must share one percentile and one score.
// Alphabetical ordering alone must never spread identical evidence across
// the 1-10 curve.
func TestForcedCurveTiesShareScore(t *testing.T) {
	edges := make([]Edge, 0, 40)
	for i := 0; i < 40; i++ {
		edges = append(edges, Edge{SymbolID: int64(i + 1), Symbol: fmt.Sprintf("SYM%02d", i), Edge: 0.01})
	}
	out, ok := ForcedCurve(edges)
	if !ok {
		t.Fatal("expected curve to emit at n=40")
	}
	for _, c := range out {
		if c.Score != out[0].Score || c.Pct != out[0].Pct {
			t.Fatalf("tied edges diverged: got score=%d pct=%v vs score=%d pct=%v",
				c.Score, c.Pct, out[0].Score, out[0].Pct)
		}
	}
	// The shared percentile is the block mean (~50th) → a middle-band score.
	if out[0].Score < 5 || out[0].Score > 6 {
		t.Fatalf("all-tied cross-section should land mid-band, got %d", out[0].Score)
	}
}

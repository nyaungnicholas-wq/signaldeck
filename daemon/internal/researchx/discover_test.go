package researchx

import (
	"math"
	"testing"
)

// researchx is the anti-p-hacking apparatus — the bounded grid, the
// Bonferroni-corrected judge, the counterfactual, the regime gate — and until
// now it had NO tests at all. The tests below are the ones that would catch the
// failure it exists to prevent: a grid search that reports structure in noise,
// and a nightly loop that eventually promotes a noise rule by asking again.

// ── deterministic fixtures (no RNG: the package's purity rule applies here) ──

// noiseObs builds `weeks` calendar weeks × `perWeek` symbols where the label is
// a hash of (symbol, week) and every feature is a hash of the SAME pair under a
// different salt — so the features carry exactly zero information about the
// label, by construction rather than by luck.
func noiseObs(weeks, perWeek int, salt int64) []Obs {
	return synthObs(weeks, perWeek, salt, func(sym, wk int64, vec map[string]float64) bool {
		return coin(sym, wk, salt+900) // label independent of every feature
	})
}

// edgeObs is the CONTROL arm: the same shape, but the label follows the
// inverse-pressure rule 92% of the time. A harness that rejects noise is only
// interesting if it still finds an edge that is genuinely there.
func edgeObs(weeks, perWeek int, salt int64) []Obs {
	return synthObs(weeks, perWeek, salt, func(sym, wk int64, vec map[string]float64) bool {
		up := vec["pressure_score"] < 0 // the edge: price moves against pressure
		if hash64(sym*7+salt, wk*13)%100 < 8 {
			up = !up // 8% noise so nothing is degenerate
		}
		return up
	})
}

func synthObs(weeks, perWeek int, salt int64, label func(sym, wk int64, vec map[string]float64) bool) []Obs {
	var out []Obs
	for wk := int64(0); wk < int64(weeks); wk++ {
		era := "era_a"
		if wk >= int64(weeks)/2 {
			era = "era_b"
		}
		for s := int64(0); s < int64(perWeek); s++ {
			sym := s + 1
			vec := map[string]float64{
				"pressure_score": unit(sym, wk, salt+1)*2 - 1,
				"ext_score":      unit(sym, wk, salt+3),
				"rsi_pct":        unit(sym, wk, salt+4),
				"vol_pct":        unit(sym, wk, salt+5),
				"vol_anomaly":    unit(sym, wk, salt+6),
				"price_accel":    unit(sym, wk, salt+7)*2 - 1,
				"consec_dir":     unit(sym, wk, salt+8)*2 - 1,
				"vix_high_vol":   float64(hash64(sym, wk+salt+9) % 2),
			}
			vec["pressure_abs"] = math.Abs(vec["pressure_score"])
			out = append(out, Obs{
				SymbolID: sym, Week: wk, Ts: wk * 604800, Vec: vec,
				Up: label(sym, wk, vec), FwdRet: unit(sym, wk, salt+10) - 0.5,
				Era: era, HighVol: vec["vix_high_vol"] == 1,
			})
		}
	}
	return out
}

func unit(a, b, salt int64) float64 {
	return float64(hash64(a+salt, b)%1_000_003) / 1_000_003
}

func coin(a, b, salt int64) bool { return hash64(a+salt, b)&1 == 0 }

// ── the failure the module exists to prevent ────────────────────────────────

// A grid search over pure noise must return NOTHING. 48 rules at alpha=0.05
// uncorrected yields ~2 "significant" results from noise every single time; the
// Bonferroni-corrected Wilson bound is what stops them being published.
func TestDiscoverRejectsPureNoise(t *testing.T) {
	for _, salt := range []int64{0, 101, 202, 303, 404} {
		obs := noiseObs(80, 40, salt)
		if got := Discover(obs, DiscoverConfig{}); len(got) != 0 {
			for _, c := range got {
				t.Errorf("salt %d: noise rule survived: %s wl=%.4f weeks=%d/%d",
					salt, c.Desc, c.WilsonLower, c.Grade.WinWeeks, c.Grade.Weeks)
			}
		}
	}
}

// The control: the same harness on a planted edge must still find it. Without
// this, "rejects everything" would pass the test above for the wrong reason.
func TestDiscoverFindsAPlantedEdge(t *testing.T) {
	got := Discover(edgeObs(80, 40, 55), DiscoverConfig{})
	if len(got) == 0 {
		t.Fatal("planted 92% inverse-pressure edge found nothing — the harness rejects everything, " +
			"which would make the noise test vacuous")
	}
	for _, c := range got {
		if c.Rule.Call != "inverse_pressure" {
			t.Errorf("survivor calls %q, want inverse_pressure (the planted direction): %s", c.Rule.Call, c.Desc)
		}
		if c.WilsonLower <= 0.5 {
			t.Errorf("survivor %s reported wl=%.4f, must exceed 0.5", c.ID, c.WilsonLower)
		}
	}
}

// Re-running the same grid over the same data every night is many looks, not
// one. The divisor has to grow with the searches already conducted, or a noise
// rule eventually clears a bar that never moved.
func TestRepeatedSearchesTightenTheBar(t *testing.T) {
	obs := noiseObs(80, 40, 77)
	var lastDiv int
	for night := 0; night < 30; night++ {
		cfg := DiscoverConfig{PriorSearches: night}
		div := cfg.Divisor()
		if night > 0 && div <= lastDiv {
			t.Fatalf("night %d: divisor %d did not exceed %d — repeated searching must TIGHTEN",
				night+1, div, lastDiv)
		}
		lastDiv = div
		if got := Discover(obs, cfg); len(got) != 0 {
			t.Fatalf("night %d: noise rule survived %d re-searches: %s", night+1, night, got[0].Desc)
		}
	}
}

// Widening the grid must make every individual rule harder, never easier — the
// property that stops "test more things until one passes".
func TestWiderGridRaisesTheBar(t *testing.T) {
	narrow := DiscoverConfig{MaxCandidates: 4}.Divisor()
	wide := DiscoverConfig{MaxCandidates: 48}.Divisor()
	if wide <= narrow {
		t.Errorf("48-rule divisor %d <= 4-rule divisor %d", wide, narrow)
	}
}

// The significance level is a package constant. A DiscoverConfig field an
// operator could raise would be the whole guardrail, undone in one line.
func TestDiscoverAlphaIsNotConfigurable(t *testing.T) {
	cfg := DiscoverConfig{MaxCandidates: 48}
	if want := MaxAlpha / float64(cfg.Divisor()); math.Abs(cfg.CorrectedAlpha()-want) > 1e-15 {
		t.Errorf("corrected alpha = %g, want %g", cfg.CorrectedAlpha(), want)
	}
	if MaxAlpha > 0.05 {
		t.Errorf("MaxAlpha = %g, want <= 0.05", MaxAlpha)
	}
}

// A survivor must carry the correction it actually cleared, or the claim
// "survived Bonferroni" is unauditable from the record.
func TestCandidateRecordsItsDivisor(t *testing.T) {
	got := Discover(edgeObs(80, 40, 55), DiscoverConfig{PriorSearches: 2})
	if len(got) == 0 {
		t.Skip("planted edge did not survive at 3 searches — covered by TestDiscoverFindsAPlantedEdge")
	}
	want := DiscoverConfig{PriorSearches: 2}.Divisor()
	for _, c := range got {
		if c.Divisor != want {
			t.Errorf("%s recorded divisor %d, want %d", c.ID, c.Divisor, want)
		}
	}
}

// ── grid + identity ─────────────────────────────────────────────────────────

func TestDiscoverGridIsBoundedAndDeterministic(t *testing.T) {
	a, b := discoverGrid(48), discoverGrid(48)
	if len(a) == 0 || len(a) > 48 {
		t.Fatalf("grid size %d, want 1..48", len(a))
	}
	for i := range a {
		if canonicalSpec(a[i]) != canonicalSpec(b[i]) {
			t.Fatalf("grid nondeterministic at %d", i)
		}
	}
	if got := len(discoverGrid(5)); got != 5 {
		t.Errorf("cap not honored: %d rules for max 5", got)
	}
	// Every pair is anchored and never pairs a key with itself.
	for _, r := range a {
		if len(r.Conds) == 2 && r.Conds[0].Key == r.Conds[1].Key {
			t.Errorf("grid emitted a self-paired rule: %s", canonicalSpec(r))
		}
	}
}

func TestRuleIDIsOrderInvariant(t *testing.T) {
	c1 := Cond{Key: "rsi_pct", Op: ">=", Val: 0.8}
	c2 := Cond{Key: "vol_pct", Op: ">=", Val: 0.8}
	a := Rule{Conds: []Cond{c1, c2}, Call: "long"}
	b := Rule{Conds: []Cond{c2, c1}, Call: "long"}
	if ruleID(a) != ruleID(b) {
		t.Errorf("cond order changed the ID: %s vs %s", ruleID(a), ruleID(b))
	}
	if ruleID(a) == ruleID(Rule{Conds: []Cond{c1, c2}, Call: "short"}) {
		t.Error("call direction did not change the ID")
	}
	// The pct flag is part of identity: same key/op/val on ranks is a different rule.
	pct := c1
	pct.Pct = true
	if ruleID(Rule{Conds: []Cond{pct}, Call: "long"}) == ruleID(Rule{Conds: []Cond{c1}, Call: "long"}) {
		t.Error("Pct flag did not change the ID")
	}
}

// ── numerics ────────────────────────────────────────────────────────────────

func TestNormalQuantileReferencePoints(t *testing.T) {
	for _, c := range []struct{ p, want float64 }{
		{0.975, 1.959964}, {0.95, 1.644854}, {0.99, 2.326348}, {0.5, 0},
	} {
		if got := normalQuantile(c.p); math.Abs(got-c.want) > 1e-4 {
			t.Errorf("normalQuantile(%g) = %.6f, want %.6f", c.p, got, c.want)
		}
	}
	if !math.IsInf(normalQuantile(0), -1) || !math.IsInf(normalQuantile(1), 1) {
		t.Error("degenerate probabilities must return infinities, not garbage")
	}
}

func TestWilsonLowerBounds(t *testing.T) {
	z := 1.96
	if got := wilsonLower(0.7, 0, z); got != 0 {
		t.Errorf("n=0 wilson = %v, want 0", got)
	}
	small, large := wilsonLower(0.7, 20, z), wilsonLower(0.7, 2000, z)
	if !(small < large && large < 0.7) {
		t.Errorf("wilson not monotone toward phat: n=20 %.4f, n=2000 %.4f", small, large)
	}
	// A perfect record on a tiny sample must not certify: 3/3 has to stay well
	// under any usable bar.
	if got := wilsonLower(1.0, 3, z); got > 0.45 {
		t.Errorf("wilsonLower(1.0, 3) = %.4f — a 3-week perfect record must not clear a 0.5 bar", got)
	}
}

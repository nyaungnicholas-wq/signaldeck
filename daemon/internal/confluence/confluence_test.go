package confluence

import "testing"

// longInputs is a base with all five families present and pointing LONG. Tests
// mutate copies of it to exercise the gate, the dissent veto, and absence.
func longInputs() Inputs {
	return Inputs{
		SmartMoneyPresent: true, SmartMoneyLabel: "accumulation", SmartMoneyScore: 0.4,
		RegimePresent: true, RegimeLabel: "uptrend",
		PredictionPresent: true, CalProb: 0.62,
		RankPresent: true, RankPct: 0.85,
		BreakoutPresent: true, BreakoutKind: "donchian_up", BreakoutAgeDays: 1, BreakoutDir: +1,
	}
}

func TestAssess_AllAgreeLongIsSetup(t *testing.T) {
	s := Assess(longInputs())
	if !s.IsSetup {
		t.Fatalf("all five families long should be a setup: %+v", s)
	}
	if s.Direction != 1 {
		t.Fatalf("direction = %d, want +1", s.Direction)
	}
	if s.Agree != 5 || s.Dissent != 0 || s.Present != 5 {
		t.Fatalf("agree/dissent/present = %d/%d/%d, want 5/0/5", s.Agree, s.Dissent, s.Present)
	}
	if s.Score != 1.0 {
		t.Fatalf("score = %v, want 1.0 (5-0)/5", s.Score)
	}
	if len(s.Votes) != 5 {
		t.Fatalf("votes = %d, want 5 (every present family listed)", len(s.Votes))
	}
}

func TestAssess_AllAgreeShort(t *testing.T) {
	in := Inputs{
		SmartMoneyPresent: true, SmartMoneyLabel: "strong_distribution", SmartMoneyScore: -0.6,
		RegimePresent: true, RegimeLabel: "downtrend",
		PredictionPresent: true, CalProb: 0.38,
		RankPresent: true, RankPct: 0.10,
		BreakoutPresent: true, BreakoutKind: "donchian_down", BreakoutAgeDays: 2, BreakoutDir: -1,
	}
	s := Assess(in)
	if !s.IsSetup || s.Direction != -1 {
		t.Fatalf("all-short should be a SHORT setup: %+v", s)
	}
	if s.Score != -1.0 {
		t.Fatalf("score = %v, want -1.0", s.Score)
	}
}

func TestAssess_AgreementGate(t *testing.T) {
	// Exactly 3 agree (default min), 0 dissent, 2 neutral present → setup.
	in := Inputs{
		SmartMoneyPresent: true, SmartMoneyLabel: "accumulation", SmartMoneyScore: 0.3, // +1
		RegimePresent: true, RegimeLabel: "uptrend", // +1
		PredictionPresent: true, CalProb: 0.61, // +1
		RankPresent: true, RankPct: 0.50, // neutral present
		BreakoutPresent: true, BreakoutKind: "volume_spike", BreakoutAgeDays: 1, BreakoutDir: 0, // neutral present
	}
	s := Assess(in)
	if !s.IsSetup {
		t.Fatalf("3 agree / 0 dissent / 5 present should flag: %+v", s)
	}
	if s.Agree != 3 || s.Present != 5 {
		t.Fatalf("agree/present = %d/%d, want 3/5", s.Agree, s.Present)
	}
	// Score = (3 long - 0 short)/5 = 0.6.
	if s.Score < 0.599 || s.Score > 0.601 {
		t.Fatalf("score = %v, want 0.6", s.Score)
	}

	// Drop to 2 agreeing longs → below the min → NOT a setup.
	in.PredictionPresent = true
	in.CalProb = 0.50 // now neutral instead of +1
	s = Assess(in)
	if s.IsSetup {
		t.Fatalf("only 2 agreeing should NOT flag: %+v", s)
	}
	if s.Agree != 2 {
		t.Fatalf("agree = %d, want 2", s.Agree)
	}
}

func TestAssess_DissentVeto(t *testing.T) {
	// 3 long, 2 short → Agree 3 clears the floor, but Dissent 2 > maxDissent → veto.
	in := Inputs{
		SmartMoneyPresent: true, SmartMoneyLabel: "accumulation", SmartMoneyScore: 0.3, // +1
		RegimePresent: true, RegimeLabel: "uptrend", // +1
		PredictionPresent: true, CalProb: 0.60, // +1
		RankPresent: true, RankPct: 0.10, // -1
		BreakoutPresent: true, BreakoutKind: "donchian_down", BreakoutAgeDays: 1, BreakoutDir: -1, // -1
	}
	s := Assess(in)
	if s.Agree != 3 || s.Dissent != 2 {
		t.Fatalf("agree/dissent = %d/%d, want 3/2", s.Agree, s.Dissent)
	}
	if s.IsSetup {
		t.Fatalf("dissent 2 (> max 1) must veto the setup: %+v", s)
	}
	if s.Direction != 1 {
		t.Fatalf("direction should still be +1 (3 long vs 2 short): %+v", s)
	}
	// One dissenter is allowed: flip the breakout back to long → 4 long, 1 short.
	in.BreakoutKind = "donchian_up"
	in.BreakoutDir = +1
	s = Assess(in)
	if !s.IsSetup || s.Dissent != 1 {
		t.Fatalf("4 long / 1 short should flag with dissent 1: %+v", s)
	}
}

func TestAssess_AbsentFamiliesExcludedFromPresent(t *testing.T) {
	// Only three families present (smart_money, trend, prediction), all long.
	in := Inputs{
		SmartMoneyPresent: true, SmartMoneyLabel: "accumulation", SmartMoneyScore: 0.5,
		RegimePresent: true, RegimeLabel: "uptrend",
		PredictionPresent: true, CalProb: 0.7,
		// rel_strength + breakout absent.
	}
	s := Assess(in)
	if s.Present != 3 {
		t.Fatalf("present = %d, want 3 (absent families excluded)", s.Present)
	}
	if len(s.Votes) != 3 {
		t.Fatalf("votes = %d, want 3", len(s.Votes))
	}
	if s.Score != 1.0 { // (3-0)/3
		t.Fatalf("score = %v, want 1.0 over the 3 present families", s.Score)
	}
	if !s.IsSetup {
		t.Fatalf("3 present all-agree should still be a setup: %+v", s)
	}
}

func TestAssess_NoPresentFamilies(t *testing.T) {
	s := Assess(Inputs{})
	if s.Present != 0 || s.IsSetup || s.Direction != 0 || s.Score != 0 {
		t.Fatalf("empty inputs must yield an inert, non-setup result: %+v", s)
	}
	if len(s.Votes) != 0 {
		t.Fatalf("votes = %d, want 0", len(s.Votes))
	}
}

func TestAssess_DirectionSign(t *testing.T) {
	// 2 long, 1 short → net long even though it won't clear the agreement floor.
	in := Inputs{
		SmartMoneyPresent: true, SmartMoneyLabel: "accumulation", SmartMoneyScore: 0.3, // +1
		RegimePresent: true, RegimeLabel: "uptrend", // +1
		PredictionPresent: true, CalProb: 0.40, // -1
	}
	s := Assess(in)
	if s.Direction != 1 {
		t.Fatalf("direction = %d, want +1 (2 long vs 1 short)", s.Direction)
	}
	if s.IsSetup {
		t.Fatalf("2 agree is below the floor — must not flag: %+v", s)
	}
	// Perfect tie → direction 0.
	in.RankPresent, in.RankPct = true, 0.10 // add a -1 → 2 long, 2 short
	s = Assess(in)
	if s.Direction != 0 {
		t.Fatalf("direction = %d, want 0 on a 2/2 tie", s.Direction)
	}
}

func TestPredictionVote_CoinFlipIsPresentNeutral(t *testing.T) {
	// |edge| < predEdgeMin ⇒ present but neutral (a weak, non-decisive vote).
	d, present, _ := predictionVote(Inputs{PredictionPresent: true, CalProb: 0.52})
	if !present || d != 0 {
		t.Fatalf("near coin-flip: dir/present = %d/%v, want 0/true", d, present)
	}
	// Just past the band ⇒ a real vote.
	d, _, _ = predictionVote(Inputs{PredictionPresent: true, CalProb: 0.54})
	if d != 1 {
		t.Fatalf("edge +0.04 should vote +1, got %d", d)
	}
	d, present, _ = predictionVote(Inputs{PredictionPresent: false})
	if present || d != 0 {
		t.Fatalf("absent prediction must not be present")
	}
}

func TestBreakoutVote_FreshnessAndKind(t *testing.T) {
	// Stale breakout → absent (excluded), never a stale directional vote.
	_, present, _ := breakoutVote(Inputs{BreakoutPresent: true, BreakoutKind: "donchian_up", BreakoutAgeDays: 9, BreakoutDir: +1})
	if present {
		t.Fatalf("a %vd-old breakout should be absent (fresh window %v)", 9.0, breakoutFreshDays)
	}
	// Fresh donchian_up → +1.
	d, present, _ := breakoutVote(Inputs{BreakoutPresent: true, BreakoutKind: "donchian_up", BreakoutAgeDays: 2, BreakoutDir: +1})
	if !present || d != 1 {
		t.Fatalf("fresh donchian_up: dir/present = %d/%v, want +1/true", d, present)
	}
	// Fresh but directionless kind → present neutral.
	d, present, _ = breakoutVote(Inputs{BreakoutPresent: true, BreakoutKind: "volume_spike", BreakoutAgeDays: 1, BreakoutDir: 0})
	if !present || d != 0 {
		t.Fatalf("fresh volume_spike: dir/present = %d/%v, want 0/true", d, present)
	}
}

func TestTrendVote_RangeIsPresentNeutral(t *testing.T) {
	for _, lbl := range []string{"range", "squeeze"} {
		d, present, _ := trendVote(Inputs{RegimePresent: true, RegimeLabel: lbl})
		if !present || d != 0 {
			t.Fatalf("regime %q: dir/present = %d/%v, want 0/true", lbl, d, present)
		}
	}
	if d, _, _ := trendVote(Inputs{RegimePresent: true, RegimeLabel: "downtrend"}); d != -1 {
		t.Fatalf("downtrend should vote -1, got %d", d)
	}
}

func TestRelStrengthVote_Bands(t *testing.T) {
	if d, _, _ := relStrengthVote(Inputs{RankPresent: true, RankPct: 0.70}); d != 1 {
		t.Fatalf("0.70 pct should vote +1, got %d", d)
	}
	if d, _, _ := relStrengthVote(Inputs{RankPresent: true, RankPct: 0.30}); d != -1 {
		t.Fatalf("0.30 pct should vote -1, got %d", d)
	}
	if d, _, _ := relStrengthVote(Inputs{RankPresent: true, RankPct: 0.50}); d != 0 {
		t.Fatalf("mid-pack should vote 0, got %d", d)
	}
}

func TestMinAgree_EnvOverride(t *testing.T) {
	if got := minAgree(); got != defaultMinAgree {
		t.Fatalf("default minAgree = %d, want %d", got, defaultMinAgree)
	}
	t.Setenv("SIGNALDECK_CONFLUENCE_MIN", "4")
	if got := minAgree(); got != 4 {
		t.Fatalf("env override minAgree = %d, want 4", got)
	}
	// With min 4, the 3-agree base is no longer a setup.
	in := Inputs{
		SmartMoneyPresent: true, SmartMoneyLabel: "accumulation", SmartMoneyScore: 0.3,
		RegimePresent: true, RegimeLabel: "uptrend",
		PredictionPresent: true, CalProb: 0.61,
	}
	if Assess(in).IsSetup {
		t.Fatalf("3 agree must NOT flag when SIGNALDECK_CONFLUENCE_MIN=4")
	}
	t.Setenv("SIGNALDECK_CONFLUENCE_MIN", "bogus")
	if got := minAgree(); got != defaultMinAgree {
		t.Fatalf("invalid env should fall back to %d, got %d", defaultMinAgree, got)
	}
}

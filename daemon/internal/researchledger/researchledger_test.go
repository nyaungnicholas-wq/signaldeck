package researchledger

import (
	"math"
	"testing"
)

// ── numerics ──

func TestRegIncBetaKnownValues(t *testing.T) {
	// I_x(1,1) = x (uniform CDF).
	for _, x := range []float64{0.1, 0.25, 0.5, 0.9} {
		if got := regIncBeta(x, 1, 1); math.Abs(got-x) > 1e-12 {
			t.Errorf("I_%.2f(1,1) = %.15f, want %.2f", x, got, x)
		}
	}
	// I_x(1,b) = 1 - (1-x)^b.
	if got, want := regIncBeta(0.3, 1, 5), 1-math.Pow(0.7, 5); math.Abs(got-want) > 1e-12 {
		t.Errorf("I_0.3(1,5) = %.15f, want %.15f", got, want)
	}
	// I_x(a,1) = x^a.
	if got, want := regIncBeta(0.6, 4, 1), math.Pow(0.6, 4); math.Abs(got-want) > 1e-12 {
		t.Errorf("I_0.6(4,1) = %.15f, want %.15f", got, want)
	}
	// Symmetry: I_x(a,b) = 1 - I_{1-x}(b,a).
	if got, want := regIncBeta(0.37, 8, 13), 1-regIncBeta(0.63, 13, 8); math.Abs(got-want) > 1e-12 {
		t.Errorf("symmetry: %.15f vs %.15f", got, want)
	}
	// Large-argument case (the fleet's n≈7000 grades): the CDF of
	// Beta(3376, 3743) at its own mean must be ≈ 0.5.
	mean := 3376.0 / (3376 + 3743)
	if got := regIncBeta(mean, 3376, 3743); math.Abs(got-0.5) > 0.01 {
		t.Errorf("large-n CDF at mean = %.4f, want ~0.5", got)
	}
}

// ── Bayes factors ──

func TestBayesFactorRealCaseH002(t *testing.T) {
	// The measured H002 non-overlapping grade: 546/1006 vs naive 0.5070
	// (z≈2.27). A plausible-edge band of 0.10 should read this as moderate
	// evidence FOR — well above 1, nowhere near the cap.
	bf := BayesFactorAbove(546, 1006, 0.5070, 0.10)
	if bf < 2 || bf > 12 {
		t.Errorf("H002 case BF = %.2f, want moderate evidence in [2, 12]", bf)
	}
}

func TestBayesFactorAgainst(t *testing.T) {
	// The measured H001 case: pressure-direct 3375/7117 vs naive 0.5093 —
	// accuracy far BELOW baseline must floor at MinBF.
	if bf := BayesFactorAbove(3375, 7117, 0.5093, 0.10); bf != MinBF {
		t.Errorf("anti-predictive BF = %.4f, want floor %.4f", bf, MinBF)
	}
}

func TestBayesFactorNeutral(t *testing.T) {
	// k exactly at the baseline: BF must be below 1 (no-edge data should
	// slightly favor the point null over the edge band) but not floored.
	bf := BayesFactorAbove(507, 1000, 0.507, 0.10)
	if bf >= 1 || bf <= MinBF {
		t.Errorf("at-baseline BF = %.4f, want in (%.3f, 1)", bf, MinBF)
	}
}

func TestBayesFactorSuspiciouslyGood(t *testing.T) {
	// Accuracy above the entire plausible band returns the cap (and the
	// suspicious-edge ATTACK is what flags it — not unbounded confidence).
	if bf := BayesFactorAbove(700, 1000, 0.52, 0.10); bf != MaxBF {
		t.Errorf("beyond-band BF = %.4f, want cap %.4f", bf, MaxBF)
	}
}

func TestBayesFactorGrowsWithN(t *testing.T) {
	// Same observed edge, more data ⇒ more evidence (until the cap).
	small := BayesFactorAbove(109, 200, 0.50, 0.10)  // 54.5% on n=200
	large := BayesFactorAbove(545, 1000, 0.50, 0.10) // 54.5% on n=1000
	if large <= small {
		t.Errorf("BF(n=1000)=%.2f not > BF(n=200)=%.2f", large, small)
	}
}

func TestBayesFactorBelowMirror(t *testing.T) {
	// "Blend is WORSE than partner": blend 1330/2984 (44.6%) vs partner-alone
	// null 0.5563 — decisive evidence the rate sits below the null.
	if bf := BayesFactorBelow(1330, 2984, 0.5563, 0.15); bf != MaxBF {
		t.Errorf("contamination BF = %.4f, want cap %.4f", bf, MaxBF)
	}
	// Mirror sanity: data AT the null reads below 1, not floored.
	bf := BayesFactorBelow(556, 1000, 0.556, 0.10)
	if bf >= 1 || bf <= MinBF {
		t.Errorf("at-null below-BF = %.4f, want in (%.3f, 1)", bf, MinBF)
	}
}

// ── posterior chain ──

func TestPosteriorChain(t *testing.T) {
	// prior 0.25 → odds 1/3; evidence BF 5 then attack 0.6 → odds 1 → 0.5.
	// N is set on the grade row because a grade with no observation is an
	// assertion, and Posterior refuses an assertion's FOR-weight (AssertedMaxBF).
	ev := []Evidence{
		{Kind: KindExperiment, K: 60, N: 100, P0: 0.5, BF: 5},
		{Kind: KindAttack, BF: 0.6},
	}
	if got := Posterior(0.25, ev); math.Abs(got-0.5) > 1e-9 {
		t.Errorf("posterior = %.6f, want 0.500000", got)
	}
}

func TestPosteriorClamps(t *testing.T) {
	strong := make([]Evidence, 10)
	for i := range strong {
		strong[i] = Evidence{Kind: KindReplication, K: 60, N: 100, P0: 0.5, BF: MaxBF}
	}
	if got := Posterior(0.5, strong); got != MaxPosterior {
		t.Errorf("posterior = %.4f, want clamp %.2f (never certainty)", got, MaxPosterior)
	}
	weak := make([]Evidence, 10)
	for i := range weak {
		weak[i] = Evidence{Kind: KindReplication, K: 40, N: 100, P0: 0.5, BF: MinBF}
	}
	if got := Posterior(0.5, weak); got != MinPosterior {
		t.Errorf("posterior = %.4f, want clamp %.2f", got, MinPosterior)
	}
}

func TestPosteriorRecapsStoredBF(t *testing.T) {
	// A stored row with an out-of-cap BF (edited DB, legacy row) must be
	// re-clamped at recompute time.
	ev := []Evidence{{Kind: KindExperiment, K: 60, N: 100, P0: 0.5, BF: 1e6}}
	if got := Posterior(0.5, ev); got > MaxPosterior {
		t.Errorf("posterior = %.4f exceeded cap via un-clamped stored BF", got)
	}
	if got, want := Posterior(0.5, ev), MaxBF/(1+MaxBF); math.Abs(got-want) > 1e-9 {
		t.Errorf("posterior = %.6f, want %.6f (BF re-clamped to %.0f)", got, want, MaxBF)
	}
}

// ── counters + status gates ──

func TestCounters(t *testing.T) {
	ev := []Evidence{
		{Kind: KindExperiment, BF: 5}, // discovery grade: NOT a replication
		{Kind: KindReplication, BF: 3},
		{Kind: KindReplication, BF: 0.4}, // contradiction
		{Kind: KindExperiment, BF: 0.3},  // contradicting experiment counts as contradiction
		{Kind: KindAttack, BF: 0.6},      // attacks are neither
		{Kind: KindManual, BF: 2},
		{Kind: KindReplication, BF: 0}, // malformed: skipped, like Posterior skips it
	}
	reps, contras := Counters(ev)
	if reps != 2 || contras != 2 {
		t.Errorf("counters = (%d, %d), want (2, 2)", reps, contras)
	}
}

// Backtest rows are disjoint historical-window grades: they count as
// replications, and as contradictions when their BF argued against.
func TestCountersBacktest(t *testing.T) {
	ev := []Evidence{
		{Kind: KindExperiment, BF: 5},
		{Kind: KindBacktest, BF: 3},
		{Kind: KindBacktest, BF: 0.4}, // contradicting backtest
		{Kind: KindReplication, BF: 2},
		{Kind: KindBacktest, BF: 0}, // malformed: skipped
	}
	reps, contras := Counters(ev)
	if reps != 3 || contras != 1 {
		t.Errorf("counters = (%d, %d), want (3, 1)", reps, contras)
	}
}

// A tiny sample beyond the plausible band must grade as MILD evidence — the
// removed early-return used to hand 3/3 wins the full cap.
func TestBayesFactorSmallSampleBeyondBand(t *testing.T) {
	bf := BayesFactorAbove(3, 3, 0.5, 0.10)
	if bf >= 3 {
		t.Errorf("3/3 wins BF = %.2f, want small-sample-aware (<3), not the cap", bf)
	}
	if bf <= 1 {
		t.Errorf("3/3 wins BF = %.2f, want >1 (it IS weak evidence for)", bf)
	}
}

// EffectiveChain dedupes the static single-regime attack to the latest row so
// an unchanged concern is levied once, not once per grade.
func TestEffectiveChainDedupesSingleRegime(t *testing.T) {
	ev := []Evidence{
		{Kind: KindExperiment, K: 60, N: 100, P0: 0.5, BF: 5},
		{Kind: KindAttack, BF: PenaltySingleRegime, Note: "single-regime: calm only"},
		{Kind: KindReplication, K: 58, N: 100, P0: 0.5, BF: 4},
		{Kind: KindAttack, BF: PenaltySingleRegime, Note: "single-regime: calm only"},
		{Kind: KindAttack, BF: PenaltyTimeSplitFail, Note: "time-split: flip"},
	}
	eff := EffectiveChain(ev)
	if len(eff) != 4 {
		t.Fatalf("effective chain has %d rows, want 4 (one single-regime dropped)", len(eff))
	}
	singles := 0
	for _, e := range eff {
		if e.Kind == KindAttack && attackName(e.Note) == "single-regime" {
			singles++
		}
	}
	if singles != 1 {
		t.Errorf("single-regime rows in effective chain = %d, want 1", singles)
	}
	// Posterior over the effective chain levies the penalty once: prior 0.5,
	// odds 1*5*4*0.6*0.4 = 4.8 -> 0.828.
	if got, want := Posterior(0.5, eff), 4.8/5.8; math.Abs(got-want) > 1e-9 {
		t.Errorf("posterior over effective chain = %.4f, want %.4f", got, want)
	}
}

// EffectiveChain dedupes EACH static attack independently: survivorship and
// single-regime are both static concerns levied once, at their latest rows,
// while per-grade attacks all pass through.
func TestEffectiveChainDedupesSurvivorship(t *testing.T) {
	ev := []Evidence{
		{Kind: KindBacktest, K: 58, N: 100, P0: 0.5, BF: 4},
		{Kind: KindAttack, BF: PenaltySurvivorship, Note: "survivorship: backfilled universe is today's survivor set"},
		{Kind: KindAttack, BF: PenaltySingleRegime, Note: "single-regime: calm only"},
		{Kind: KindBacktest, K: 57, N: 100, P0: 0.5, BF: 3},
		{Kind: KindAttack, BF: PenaltySurvivorship, Note: "survivorship: backfilled universe is today's survivor set"},
		{Kind: KindAttack, BF: PenaltyTimeSplitFail, Note: "time-split: flip"},
	}
	eff := EffectiveChain(ev)
	if len(eff) != 5 {
		t.Fatalf("effective chain has %d rows, want 5 (one survivorship dropped)", len(eff))
	}
	counts := map[string]int{}
	for _, e := range eff {
		if e.Kind == KindAttack {
			counts[attackName(e.Note)]++
		}
	}
	if counts["survivorship"] != 1 || counts["single-regime"] != 1 || counts["time-split"] != 1 {
		t.Errorf("attack counts in effective chain = %v, want each static attack once", counts)
	}
	// The retained survivorship row is the LATEST one (index 4).
	if eff[len(eff)-2].Note[:12] != "survivorship" {
		t.Errorf("retained survivorship row not at its latest position: %+v", eff)
	}
	// Posterior levies each static penalty once: prior 0.5,
	// odds 1*4*0.6*3*0.65*0.4 = 1.872 -> 0.652.
	if got, want := Posterior(0.5, eff), 1.872/2.872; math.Abs(got-want) > 1e-9 {
		t.Errorf("posterior over effective chain = %.4f, want %.4f", got, want)
	}
}

// A NaN posterior must never read as supported.
func TestStatusNaN(t *testing.T) {
	if got := Status(math.NaN(), 5, 5); got != StatusUncertain {
		t.Errorf("Status(NaN) = %s, want uncertain", got)
	}
}

// A long chain of capped BFs saturates instead of overflowing to +Inf/NaN.
func TestPosteriorLongChainNoOverflow(t *testing.T) {
	ev := make([]Evidence, 500)
	for i := range ev {
		ev[i] = Evidence{Kind: KindReplication, K: 60, N: 100, P0: 0.5, BF: MaxBF}
	}
	got := Posterior(0.5, ev)
	if math.IsNaN(got) || got != MaxPosterior {
		t.Errorf("posterior over 500 capped BFs = %v, want exactly the %.2f clamp", got, MaxPosterior)
	}
}

func TestStatusBands(t *testing.T) {
	// Bands, exercised through StatusWithGates with the tradability gate met —
	// since the gate was added, that is the only path that can reach
	// "supported". Status() itself now caps at tentative by construction and is
	// covered by TestLegacyStatusCannotPromote.
	traded := func(reps, regs int) Gates {
		return Gates{
			Replications: reps, Regimes: regs,
			TradableForm: "a stated position",
			EconomicTest: "graded net of costs",
		}
	}
	cases := []struct {
		p    float64
		reps int
		regs int
		want string
	}{
		{0.05, 5, 5, StatusRejected},
		{0.20, 5, 5, StatusDoubtful},
		{0.50, 5, 5, StatusUncertain},
		{0.70, 5, 5, StatusTentative},
		{0.90, 2, 2, StatusSupported},
		{0.90, 1, 2, StatusTentative}, // replication gate unmet
		{0.90, 2, 1, StatusTentative}, // regime gate unmet — single-regime cap
	}
	for _, c := range cases {
		if got := StatusWithGates(c.p, traded(c.reps, c.regs)); got != c.want {
			t.Errorf("StatusWithGates(%.2f, %d reps, %d regimes) = %s, want %s",
				c.p, c.reps, c.regs, got, c.want)
		}
	}
}

// ── meta-analysis ──

func TestMeta(t *testing.T) {
	hyps := []Hypothesis{
		{Family: "meanrev", Posterior: 0.6, Status: StatusTentative},
		{Family: "meanrev", Posterior: 0.9, Status: StatusSupported},
		{Family: "momentum", Posterior: 0.02, Status: StatusRejected},
	}
	m := Meta(hyps)
	if len(m) != 2 || m[0].Family != "meanrev" {
		t.Fatalf("meta = %+v, want meanrev first", m)
	}
	if m[0].N != 2 || math.Abs(m[0].AvgPosterior-0.75) > 1e-9 || m[0].Supported != 1 {
		t.Errorf("meanrev stat = %+v", m[0])
	}
	if m[1].Rejected != 1 {
		t.Errorf("momentum stat = %+v", m[1])
	}
}

func TestAttackLethality(t *testing.T) {
	ev := []Evidence{
		{Kind: KindAttack, BF: 0.6, Note: "single-regime: only calm-vol data"},
		{Kind: KindAttack, BF: 0.6, Note: "single-regime: only calm-vol data"},
		{Kind: KindAttack, BF: 1.0, Note: "time-split: sign stable"},
		{Kind: KindExperiment, BF: 5, Note: "not an attack"},
	}
	as := AttackLethality(ev)
	if len(as) != 2 || as[0].Attack != "single-regime" || as[0].Failed != 2 || as[0].Run != 2 {
		t.Fatalf("lethality = %+v", as)
	}
	if as[1].Attack != "time-split" || as[1].Failed != 0 || as[1].Run != 1 {
		t.Errorf("time-split stat = %+v", as[1])
	}
}

// ── the tradability gate ─────────────────────────────────────────────────

// A statistic alone must not promote a hypothesis, however strong. H018 is the
// worked example: 73.1% with a tight interval, and a tradable form that turned
// out to be indistinguishable from picking pairs at random.
func TestStatusWithGatesRequiresATradableForm(t *testing.T) {
	strong := 0.95
	full := Gates{
		Replications: MinReplications, Regimes: MinRegimes,
		TradableForm: "dollar-neutral cointegration spread",
		EconomicTest: "2026-07-25 pairs test: FAILED",
	}
	if got := StatusWithGates(strong, full); got != StatusSupported {
		t.Errorf("every gate met → %q, want %q", got, StatusSupported)
	}

	noForm := full
	noForm.TradableForm = ""
	if got := StatusWithGates(strong, noForm); got != StatusTentative {
		t.Errorf("no tradable form → %q, want %q", got, StatusTentative)
	}

	untested := full
	untested.EconomicTest = ""
	if got := StatusWithGates(strong, untested); got != StatusTentative {
		t.Errorf("form stated but never graded → %q, want %q", got, StatusTentative)
	}

	// The independence gates still bind on their own.
	thin := full
	thin.Replications = MinReplications - 1
	if got := StatusWithGates(strong, thin); got != StatusTentative {
		t.Errorf("unreplicated → %q, want %q", got, StatusTentative)
	}
}

// Status (the legacy signature, still used by the seeding paths) must never be
// able to promote, because it cannot see whether a position was ever stated.
func TestLegacyStatusCannotPromote(t *testing.T) {
	if got := Status(0.99, 99, 99); got == StatusSupported {
		t.Error("Status promoted to supported without knowing the tradable form")
	}
}

// The reason a hypothesis is being held back has to be legible, or a high
// posterior sitting at "tentative" reads as arbitrary.
func TestUnmetGateNamesTheFirstFailure(t *testing.T) {
	cases := []struct {
		name string
		g    Gates
		want string
	}{
		{"unreplicated", Gates{Regimes: MinRegimes}, "needs independent replication on a fresh data window"},
		{"one regime", Gates{Replications: MinReplications, Regimes: 1}, "measured in only one volatility regime"},
		{"no position", Gates{Replications: MinReplications, Regimes: MinRegimes}, "no tradable form stated — the position that would earn the money is unspecified"},
		{"untested position", Gates{Replications: MinReplications, Regimes: MinRegimes, TradableForm: "spread"}, "tradable form stated but never graded net of costs"},
		{"all met", Gates{Replications: MinReplications, Regimes: MinRegimes, TradableForm: "spread", EconomicTest: "run"}, ""},
	}
	for _, c := range cases {
		if got := c.g.UnmetGate(); got != c.want {
			t.Errorf("%s: unmet gate = %q, want %q", c.name, got, c.want)
		}
	}
}

// Economic grades are a distinct evidence kind so a cost-aware grade of the
// POSITION is never confused with another grade of the statistic.
func TestEconomicGradesCountsOnlyEconomicRows(t *testing.T) {
	chain := []Evidence{
		{Kind: KindExperiment}, {Kind: KindReplication},
		{Kind: KindEconomic}, {Kind: KindAttack}, {Kind: KindEconomic},
	}
	if got := EconomicGrades(chain); got != 2 {
		t.Errorf("economic grades = %d, want 2", got)
	}
}

package clusterstat

import (
	"math"
	"testing"
)

// day is a tiny helper: build obs for one day with n observations of which hits
// are correct, all calling the same side unless dir is DirNone.
func dayObs(day int64, n, hits int, dir Direction) []Obs {
	out := make([]Obs, 0, n)
	for i := 0; i < n; i++ {
		out = append(out, Obs{Day: day, Hit: i < hits, Dir: dir})
	}
	return out
}

// TestDesignEffect_PerfectClustering is the property the whole package turns on.
// When every symbol on a day carries the SAME outcome, that day's 1,000
// observations contain exactly one observation's worth of information, and the
// measured design effect must equal the symbols-per-day count. Anything near 1
// here means the estimator is blind to clustering and every interval built on it
// is the too-narrow one this package was written to replace.
func TestDesignEffect_PerfectClustering(t *testing.T) {
	const symbolsPerDay = 200
	const days = 20

	var obs []Obs
	for d := 0; d < days; d++ {
		// Alternating whole-day outcomes: 10 days entirely right, 10 entirely
		// wrong. Pooled p = 0.5, but the sample holds 20 bets, not 4,000.
		hits := 0
		if d%2 == 0 {
			hits = symbolsPerDay
		}
		obs = append(obs, dayObs(int64(d), symbolsPerDay, hits, DirUp)...)
	}

	res := Grade(obs)
	if res.Refused {
		t.Fatalf("20 days must clear the floor, got refusal: %s", res.Reason)
	}
	if res.N != symbolsPerDay*days {
		t.Fatalf("N = %d, want %d", res.N, symbolsPerDay*days)
	}
	if res.DistinctDays != days {
		t.Fatalf("distinctDays = %d, want %d", res.DistinctDays, days)
	}
	// deff for a perfectly homogeneous cluster of size m is m*K/(K-1); with
	// K=20 that is 200*20/19 = 210.5. Accept a tight band around it.
	want := float64(symbolsPerDay) * days / float64(days-1)
	if math.Abs(res.DesignEffect-want) > 1 {
		t.Fatalf("designEffect = %.2f, want ~%.2f (one observation per day)", res.DesignEffect, want)
	}
	// Effective N must collapse to roughly the DAY count, not the row count.
	if res.EffectiveN > float64(days)+1 {
		t.Fatalf("effectiveN = %.1f, want ~%d — the sample is %d bets, not %d rows",
			res.EffectiveN, days, days, res.N)
	}
	// And the interval must be far wider than the naive one. sqrt(210) ~ 14.5x.
	if res.WidthRatio < 10 {
		t.Fatalf("corrected interval only %.2fx the naive width; a perfectly clustered "+
			"sample must widen by ~sqrt(deff)", res.WidthRatio)
	}
	if res.CI.Width() <= res.NaiveCIDiscredited.Width() {
		t.Fatal("corrected interval must be wider than the discredited naive one")
	}
	// Near-unanimous daily direction is the second half of the finding.
	if res.DailyAgreement < 0.99 {
		t.Fatalf("dailyAgreement = %.3f, want ~1.0 when every call on a day takes one side",
			res.DailyAgreement)
	}
}

// TestDesignEffect_Independent is the other half of the property: a genuinely
// independent sample must NOT be penalised. A correction that widens every
// interval regardless of the data would be as dishonest as one that never
// widens — it would just fail in the safe direction, and would make a real edge
// unpublishable.
func TestDesignEffect_Independent(t *testing.T) {
	const perDay = 200
	const days = 40

	// Deterministic pseudo-random hit assignment with NO day structure: the
	// per-day hit count varies only by binomial noise around p=0.5.
	rng := newLCG(0xABCDEF)
	var obs []Obs
	for d := 0; d < days; d++ {
		for i := 0; i < perDay; i++ {
			obs = append(obs, Obs{Day: int64(d), Hit: rng.next(2) == 1, Dir: DirNone})
		}
	}

	res := Grade(obs)
	if res.Refused {
		t.Fatalf("40 days must clear the floor, got refusal: %s", res.Reason)
	}
	// deff is floored at 1 and should not exceed ~1.3 on independent data. A
	// large value here would mean the estimator invents clustering that is not
	// in the data.
	if res.DesignEffect < 1 || res.DesignEffect > 1.4 {
		t.Fatalf("designEffect = %.3f on an independent sample, want ~1.0", res.DesignEffect)
	}
	if res.EffectiveN < 0.7*float64(res.N) {
		t.Fatalf("effectiveN = %.0f of N = %d — an independent sample must keep most of its N",
			res.EffectiveN, res.N)
	}
	if res.WidthRatio > 1.25 {
		t.Fatalf("widthRatio = %.3f on an independent sample, want ~1.0", res.WidthRatio)
	}
	// No directions supplied -> the breadth diagnostic must be absent, not a
	// fabricated 1.0 read off unset zero-value fields.
	if res.DayBet != nil {
		t.Fatal("dayBet must be nil when the caller supplied no directions")
	}
	if res.DailyAgreement != 0 {
		t.Fatalf("dailyAgreement = %.3f with no directions supplied, want 0", res.DailyAgreement)
	}
}

// TestRefusesBelowDayFloor: too few distinct days must return NO interval — nil,
// with a reason — never a narrow one. This is the regression for the platform's
// prior false unlock, where 995 clustered observations over 3 days ungated a
// skill claim. A big N cannot buy its way past the day floor.
func TestRefusesBelowDayFloor(t *testing.T) {
	var obs []Obs
	for d := 0; d < 3; d++ {
		obs = append(obs, dayObs(int64(d), 400, 250, DirUp)...) // 1,200 observations
	}

	res := GradeWith(obs, Defaults())
	if !res.Refused {
		t.Fatal("3 distinct days must be refused however many observations they hold")
	}
	if res.CI != nil || res.BootstrapCI != nil || res.NaiveCIDiscredited != nil {
		t.Fatalf("a refusal must return NO interval at all: ci=%v naive=%v boot=%v",
			res.CI, res.NaiveCIDiscredited, res.BootstrapCI)
	}
	if res.DayBet != nil {
		t.Fatal("the day-level framing must also be withheld below the day floor")
	}
	if res.Reason == "" {
		t.Fatal("a refusal must state its reason")
	}
	// The descriptive counts still travel, so the surface can say how far off it is.
	if res.N != 1200 || res.DistinctDays != 3 {
		t.Fatalf("counts must survive a refusal: N=%d days=%d", res.N, res.DistinctDays)
	}
	// One day below the floor still refuses; exactly at the floor it must not.
	var atFloor []Obs
	for d := 0; d < MinDistinctDays; d++ {
		atFloor = append(atFloor, dayObs(int64(d), 50, 25, DirUp)...)
	}
	if r := Grade(atFloor); r.Refused {
		t.Fatalf("exactly MinDistinctDays days must be gradable, got: %s", r.Reason)
	}
}

// liveDays is the LIVE 1d directional record as of 2026-07-25, tallied one
// observation per (symbol, UTC-day): {day, n, hits, predictedUp}. It is pinned
// here so the estimator's answer on the real data cannot drift silently — these
// are the numbers the hostile review measured, and two independent reviewers
// reached 14.7x (this estimator's answer) and 24.4x (a variant restricted to
// days with >=100 observations) on it.
var liveDays = [][4]int{
	{20637, 10, 6, 6},
	{20638, 489, 217, 333},
	{20639, 496, 217, 352},
	{20640, 498, 302, 491},
	{20641, 500, 310, 497},
	{20642, 502, 106, 494},
	{20643, 504, 217, 43},
	{20644, 508, 146, 35},
	{20645, 516, 292, 512},
	{20646, 516, 289, 512},
	{20647, 516, 254, 11},
	{20648, 1045, 483, 1028},
	{20649, 1046, 509, 16},
	{20650, 1045, 477, 529},
	{20651, 1046, 524, 414},
	{20652, 1045, 538, 391},
	{20653, 1046, 537, 388},
	{20654, 1046, 510, 396},
	{20655, 322, 165, 168},
	{20656, 322, 159, 167},
	{20657, 26, 18, 11},
	{20658, 7, 4, 4},
	{20659, 7, 3, 3},
}

// liveObs expands the pinned tallies into observations. Within a day the hits
// and the up-calls are laid out independently of each other, which is enough to
// reproduce every day-level statistic (all of them depend only on the counts).
func liveObs() []Obs {
	var out []Obs
	for _, d := range liveDays {
		day, n, hits, up := int64(d[0]), d[1], d[2], d[3]
		for i := 0; i < n; i++ {
			dir := DirDown
			if i < up {
				dir = DirUp
			}
			out = append(out, Obs{Day: day, Hit: i < hits, Dir: dir})
		}
	}
	return out
}

// TestLiveRecord_ReproducesReviewedNumbers pins the estimator against the live
// 1d record the hostile review measured. It is the reproduction step: the
// package must independently arrive at the reviewer's design effect and at the
// 3.8x width understatement, or the correction is not the one that was asked
// for.
func TestLiveRecord_ReproducesReviewedNumbers(t *testing.T) {
	res := Grade(liveObs())
	if res.Refused {
		t.Fatalf("the live record spans 23 days and must be gradable: %s", res.Reason)
	}
	if res.N != 13058 || res.DistinctDays != 23 {
		t.Fatalf("live fixture: N=%d days=%d, want 13058 / 23", res.N, res.DistinctDays)
	}
	if math.Abs(res.P-0.4812) > 0.0005 {
		t.Fatalf("pooled accuracy = %.4f, want 0.4812", res.P)
	}
	// The reviewer's second measurement, by a different method, was 14.7x ->
	// effective N 887. This estimator must land on it, not on 1.0 and not on the
	// unweighted-variance artifact of 2.15.
	if math.Abs(res.DesignEffect-14.73) > 0.15 {
		t.Fatalf("designEffect = %.2f on the live record, want ~14.7", res.DesignEffect)
	}
	if math.Abs(res.EffectiveN-887) > 15 {
		t.Fatalf("effectiveN = %.0f, want ~887 (not %d)", res.EffectiveN, res.N)
	}
	// The headline defect: the shipped interval was 3.8x too narrow.
	if math.Abs(res.WidthRatio-3.83) > 0.1 {
		t.Fatalf("widthRatio = %.2f, want ~3.8 (the review's '3.8-4.9x too narrow')", res.WidthRatio)
	}
	// The CONCLUSION must survive the correction — this is the point of the
	// whole exercise. 48.1% accuracy against the ~54.6% naive baseline stays
	// significantly below it even at the widened interval, so correcting the
	// interval does not rescue the model; it only stops overstating the finding.
	if res.CI.Hi >= 0.546 {
		t.Fatalf("corrected CI %v reaches the 54.6%% naive baseline — the platform's "+
			"no-edge conclusion is supposed to survive the widening", *res.CI)
	}
	if res.CI.Lo >= res.NaiveCIDiscredited.Lo || res.CI.Hi <= res.NaiveCIDiscredited.Hi {
		t.Fatalf("corrected CI %v must strictly contain the naive %v", *res.CI, *res.NaiveCIDiscredited)
	}
	// The bootstrap is the independent check on the analytic correction. Both
	// resample days; they should agree to well within their own width.
	if res.BootstrapCI == nil {
		t.Fatal("a 23-day sample must produce a day-clustered bootstrap interval")
	}
	if math.Abs(res.BootstrapCI.Width()-res.CI.Width()) > 0.5*res.CI.Width() {
		t.Fatalf("bootstrap %v and analytic %v disagree by more than half a width",
			*res.BootstrapCI, *res.CI)
	}
	// The bootstrap must ALSO be far wider than the naive interval; if it were
	// not, the analytic correction would be the odd one out.
	if res.BootstrapCI.Width() < 2*res.NaiveCIDiscredited.Width() {
		t.Fatalf("day-clustered bootstrap width %.4f barely exceeds the naive %.4f",
			res.BootstrapCI.Width(), res.NaiveCIDiscredited.Width())
	}
}

// TestLiveRecord_OneMarketWideBetPerDay pins the second half of the finding: the
// model does not make 13,058 cross-sectional calls, it makes one market-wide bet
// per day. The day-level framing must reproduce the review's 8-of-19 and produce
// an interval that includes a coin flip — the honest statement of how little the
// record establishes.
func TestLiveRecord_OneMarketWideBetPerDay(t *testing.T) {
	res := Grade(liveObs())
	if res.DailyAgreement < 0.75 {
		t.Fatalf("dailyAgreement = %.3f; the live record's daily breadth runs 98.6%%, "+
			"99.4%%, 2.1%% and similar, so the mean agreement is high", res.DailyAgreement)
	}
	if res.DayBet == nil {
		t.Fatal("23 directional days must produce the day-level framing")
	}
	// 4 of the 23 days are near-empty (10, 26, 7, 7 observations) but they are
	// still days, so the fixture grades 23; the review's table quoted the 19
	// days with a full cross-section. Both must show a coin-flip interval.
	if res.DayBet.Days != 23 {
		t.Fatalf("dayBet.Days = %d, want 23", res.DayBet.Days)
	}
	if res.DayBet.Accuracy > 0.55 {
		t.Fatalf("day-level accuracy = %.3f — the market-wide bet was right on fewer than "+
			"half the days", res.DayBet.Accuracy)
	}
	if res.DayBet.CI == nil || res.DayBet.CI.Lo > 0.5 || res.DayBet.CI.Hi < 0.5 {
		t.Fatalf("day-level CI %v must include 0.5 — %d bets cannot establish skill",
			res.DayBet.CI, res.DayBet.Days)
	}
	// And the day-level interval must be the widest framing of the three: it
	// throws away the cross-section entirely.
	if res.DayBet.CI.Width() <= res.CI.Width() {
		t.Fatalf("day-level width %.4f should exceed the symbol-day width %.4f",
			res.DayBet.CI.Width(), res.CI.Width())
	}
}

// TestDesignEffect_DegenerateProportion: an all-correct or all-wrong sample has
// no between-day variance to measure, but it is also maximally clustered. The
// honest answer is one observation per day, NOT the flattering deff of 1 the
// arithmetic would otherwise hand back.
func TestDesignEffect_DegenerateProportion(t *testing.T) {
	days := make([]Day, 0, 12)
	for d := 0; d < 12; d++ {
		days = append(days, Day{Day: int64(d), N: 100, Hits: 100})
	}
	deff, ok := DesignEffect(days)
	if !ok {
		t.Fatal("a degenerate proportion must still return an answer")
	}
	if math.Abs(deff-100) > 1e-9 {
		t.Fatalf("deff = %.2f on a perfectly uniform sample, want 100 (= obs per day)", deff)
	}
	// Fewer than 2 clusters is not estimable at all.
	if _, ok := DesignEffect(days[:1]); ok {
		t.Fatal("a single cluster must not yield a design effect")
	}
}

// TestDesignEffectFlooredAtOne: sampling noise across a small number of days can
// produce a measured deff below 1, which would make the interval NARROWER than
// the independence assumption it exists to correct. The floor is what stops this
// package from ever shipping a MORE overconfident number than the code it
// replaces.
func TestDesignEffectFlooredAtOne(t *testing.T) {
	// Deliberately anti-clustered: every day lands on exactly the pooled rate,
	// so the between-day variance is zero and the raw ratio would be ~0.
	days := make([]Day, 0, 20)
	for d := 0; d < 20; d++ {
		days = append(days, Day{Day: int64(d), N: 100, Hits: 50})
	}
	deff, ok := DesignEffect(days)
	if !ok {
		t.Fatal("estimable sample returned not-ok")
	}
	if deff < 1 {
		t.Fatalf("deff = %.4f must never fall below 1 — that would narrow the interval "+
			"below the independence assumption this package corrects", deff)
	}
}

// TestWilsonEff_RawNIsTheBug demonstrates the arithmetic of the defect in one
// assertion: the same proportion at raw N and at effective N differ by ~sqrt of
// the design effect, which is the entire 3.8-4.9x understatement.
func TestWilsonEff_RawNIsTheBug(t *testing.T) {
	const p = 0.4806
	raw := WilsonEff(p, 13008)
	eff := WilsonEff(p, 13008/14.73)
	ratio := eff.Width() / raw.Width()
	if math.Abs(ratio-math.Sqrt(14.73)) > 0.15 {
		t.Fatalf("width ratio %.2f should track sqrt(deff) = %.2f", ratio, math.Sqrt(14.73))
	}
	// Degenerate inputs must be safe, never NaN on a public surface.
	if z := (WilsonEff(0, 0)); z.Lo != 0 || z.Hi != 0 {
		t.Fatalf("WilsonEff(0,0) = %v, want the zero interval", z)
	}
	if z := WilsonEff(1, 20); z.Hi > 1.0000001 || z.Lo < 0 {
		t.Fatalf("WilsonEff(1,20) = %v escaped [0,1]", z)
	}
}

// TestBootstrapDeterminism: the same sample must produce the same interval on
// every run. A re-rollable bootstrap lets a borderline result be retried until
// it clears a gate, which is p-hacking with extra steps.
func TestBootstrapDeterminism(t *testing.T) {
	obs := liveObs()
	a := Grade(obs)
	b := Grade(obs)
	if *a.BootstrapCI != *b.BootstrapCI {
		t.Fatalf("bootstrap is not deterministic: %v vs %v", *a.BootstrapCI, *b.BootstrapCI)
	}
	// The exported entry point refuses below the day floor like everything else.
	if _, ok := BootstrapDays([]Day{{Day: 1, N: 10, Hits: 5}, {Day: 2, N: 10, Hits: 6}}, 2000, 0.05); ok {
		t.Fatal("BootstrapDays must refuse below MinDistinctDays clusters")
	}
}

// TestEmptyAndSingleDay: the degenerate callers a live surface will actually hit
// (nothing resolved yet, one day resolved) must refuse cleanly rather than panic
// or return a zero-width interval that reads as certainty.
func TestEmptyAndSingleDay(t *testing.T) {
	if r := Grade(nil); !r.Refused || r.CI != nil || r.Reason == "" {
		t.Fatalf("empty input must refuse with a reason, got %+v", r)
	}
	if r := Grade(dayObs(1, 500, 300, DirUp)); !r.Refused || r.CI != nil {
		t.Fatal("a single day must refuse however many observations it holds")
	}
}

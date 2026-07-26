package metalabel

import (
	"math"
	"strings"
	"testing"

	"github.com/nyaungnicholas-wq/signaldeck/internal/gbm"
)

// buildSamples makes a deterministic candidate set. ctxSignal controls a context
// feature that, when informative, separates the rows whose primary call wins from
// those whose call loses — exactly the structure meta-labeling is meant to find.
func buildSamples(n int, informative bool) []Sample {
	out := make([]Sample, 0, n)
	for i := 0; i < n; i++ {
		// Deterministic alternating regimes, no RNG (this repo forbids it in
		// anything that must reproduce).
		good := i%2 == 0
		ctx := 0.0
		if informative && good {
			ctx = 1.0
		} else if informative {
			ctx = -1.0
		}
		// A second, deliberately uninformative feature so the learner has to
		// choose rather than being handed a single column.
		noise := float64(i%7) / 7.0

		fwd := -0.02 // primary's call loses
		if good {
			fwd = 0.03 // primary's call wins comfortably net of 10bps
		}
		out = append(out, Sample{
			Ts:          int64(i * 86400),
			PrimaryProb: 0.62, // a directional long call
			Context:     []float64{ctx, noise},
			FwdReturn:   fwd,
		})
	}
	return out
}

// The headline guarantee: when the primary itself loses money after cost, NO
// meta-model may rescue it. This is the platform's own documented finding —
// filtering an edgeless signal yields fewer trades with the same lack of edge.
func TestEdgelessPrimaryIsRejectedOutright(t *testing.T) {
	n := 200
	samples := make([]Sample, 0, n)
	for i := 0; i < n; i++ {
		// Every call loses after cost.
		samples = append(samples, Sample{
			Ts:          int64(i * 86400),
			PrimaryProb: 0.62,
			Context:     []float64{float64(i % 3), float64(i%5) / 5.0},
			FwdReturn:   -0.01,
		})
	}
	g, err := Evaluate(samples, 4, 0.001, DefaultThreshold)
	if err != nil {
		t.Fatalf("Evaluate: %v", err)
	}
	if g.PrimaryHasEdge {
		t.Fatalf("primary should have no edge, expectancy=%v", g.PrimaryExpectancy)
	}
	if g.Verdict != VerdictRejected {
		t.Fatalf("verdict = %q, want %q", g.Verdict, VerdictRejected)
	}
	if g.Reason == "" {
		t.Fatal("a rejection must state its reason")
	}
}

// Precision going UP while expectancy goes DOWN must be scored a rejection.
// This is the trend21 trap encoded as a test: the most accurate band on this
// platform carries a negative forward return, so a filter that selects for
// being right rather than for being paid has earned nothing.
func TestPrecisionUpButExpectancyDownIsRejected(t *testing.T) {
	// Construct it directly on the Grade so the gate itself is under test,
	// independent of whether a learner would find this pattern.
	g := Grade{
		N:                  400,
		PrimaryPrecision:   0.50,
		PrimaryExpectancy:  0.004,
		TakenN:             120,
		TakenDays:          40, // ample sample, so the verdict turns purely on expectancy
		TotalDays:          60,
		TakeRate:           0.30,
		FilteredPrecision:  0.72,  // much more often right
		FilteredExpectancy: 0.001, // but worse per decision offered
		PrimaryHasEdge:     true,
	}
	g.ExpectancyLift = g.FilteredExpectancy - g.PrimaryExpectancy
	g.PrecisionLift = g.FilteredPrecision - g.PrimaryPrecision

	verdict, reason := judge(g)
	if verdict != VerdictRejected {
		t.Fatalf("verdict = %q, want %q — expectancy must overrule precision", verdict, VerdictRejected)
	}
	if !contains(reason, "trend21") {
		t.Fatalf("reason should name the precision/expectancy trap, got: %s", reason)
	}
}

// A filter that takes very few trades cannot claim a measured edge, however
// good those few look.
func TestThinFilterIsInsufficientNotEarned(t *testing.T) {
	g := Grade{
		N:                  400,
		PrimaryExpectancy:  0.002,
		TakenN:             MinTakenTrades - 1,
		TakenDays:          40,
		TotalDays:          60,
		FilteredExpectancy: 0.010,
		PrimaryHasEdge:     true,
	}
	g.ExpectancyLift = g.FilteredExpectancy - g.PrimaryExpectancy
	verdict, reason := judge(g)
	if verdict != VerdictInsufficient {
		t.Fatalf("verdict = %q, want %q", verdict, VerdictInsufficient)
	}
	if reason == "" {
		t.Fatal("reason required")
	}
}

// The gate the LIVE data forced into existence. Many trades concentrated on a
// handful of days are not independent evidence: on any given day every symbol
// shares one market move. Measured on this platform, 11,811 symbol-day rows
// spanned just 22 distinct days across 1,050 symbols, and a filter taking the
// best 4.8% showed a +17.7pp precision gain that is mostly "which days were
// good days". Trade count alone must NOT be able to satisfy the gate.
func TestDayClusteredTradesAreInsufficient(t *testing.T) {
	g := Grade{
		N:                  11811,
		PrimaryExpectancy:  0.002,
		TakenN:             567, // far above the trade floor
		TakenDays:          8,   // but concentrated on 8 days
		TotalDays:          22,
		FilteredExpectancy: 0.006,
		PrimaryHasEdge:     true,
	}
	g.ExpectancyLift = g.FilteredExpectancy - g.PrimaryExpectancy
	verdict, reason := judge(g)
	if verdict != VerdictInsufficient {
		t.Fatalf("verdict = %q, want %q — 567 trades on 8 days is not 567 observations", verdict, VerdictInsufficient)
	}
	if !contains(reason, "distinct days") {
		t.Fatalf("reason must name day clustering, got: %s", reason)
	}
	// And the same filter spread across enough days must be allowed through.
	g.TakenDays = MinTakenDays
	if v, _ := judge(g); v != VerdictEarned {
		t.Fatalf("verdict = %q, want %q once the day floor is met", v, VerdictEarned)
	}
}

// Expectancy is measured per DECISION OFFERED, so a filter cannot win simply by
// trading less. Halving the trades at identical per-trade quality must NOT
// register as an improvement.
func TestTradingLessIsNotAnImprovement(t *testing.T) {
	samples := buildSamples(300, false) // context carries no information
	g, err := Evaluate(samples, 4, 0.001, DefaultThreshold)
	if err != nil {
		t.Fatalf("Evaluate: %v", err)
	}
	if g.Verdict == VerdictEarned && g.ExpectancyLift <= 0 {
		t.Fatal("earned verdict with non-positive expectancy lift is contradictory")
	}
	// With uninformative context the filter must not manufacture an edge.
	if g.ExpectancyLift > 0.005 {
		t.Fatalf("uninformative context produced implausible lift %v", g.ExpectancyLift)
	}
}

// The no-lookahead guarantee: appending LATER rows must not change the
// out-of-sample probability assigned to an EARLIER row. If it does, some fold
// trained on the future.
func TestNoLookahead(t *testing.T) {
	base := buildSamples(200, true)
	extended := append(append([]Sample{}, base...), buildSamples(60, true)...)
	// Re-stamp the appended rows so time stays strictly ascending.
	for i := len(base); i < len(extended); i++ {
		extended[i].Ts = int64(i * 86400)
	}

	metaOf := func(ss []Sample) []float64 {
		cands := directional(ss)
		ms := make([]gbm.Sample, len(cands))
		for i, c := range cands {
			ms[i] = gbm.Sample{Ts: c.Ts, Feat: c.Context, Y: metaLabel(c, 0.001)}
		}
		probs, _, err := walkForward(ms, 4)
		if err != nil {
			t.Fatalf("walkForward: %v", err)
		}
		return probs
	}

	pBase := metaOf(base)
	pExt := metaOf(extended)
	for i := range pBase {
		if math.Abs(pBase[i]-pExt[i]) > 1e-12 {
			t.Fatalf("row %d changed when future rows were appended: %v -> %v", i, pBase[i], pExt[i])
		}
	}
}

// Non-ascending input must be refused rather than silently graded, since the
// walk-forward guarantee depends entirely on ordering.
func TestUnsortedInputRefused(t *testing.T) {
	s := buildSamples(200, true)
	s[10].Ts, s[11].Ts = s[11].Ts, s[10].Ts
	if _, err := Evaluate(s, 4, 0.001, DefaultThreshold); err != ErrNotAscending {
		t.Fatalf("err = %v, want ErrNotAscending", err)
	}
}

// A primary that never takes a side leaves nothing to filter.
func TestNoDirectionalCalls(t *testing.T) {
	s := buildSamples(200, true)
	for i := range s {
		s[i].PrimaryProb = 0.5 // pure abstention
	}
	if _, err := Evaluate(s, 4, 0.001, DefaultThreshold); err != ErrNoDirectionalCalls {
		t.Fatalf("err = %v, want ErrNoDirectionalCalls", err)
	}
}

// An unearned meta-model must never gate a live decision.
func TestRunRefusesWhenNotEarned(t *testing.T) {
	n := 200
	samples := make([]Sample, 0, n)
	for i := 0; i < n; i++ {
		samples = append(samples, Sample{
			Ts:          int64(i * 86400),
			PrimaryProb: 0.62,
			Context:     []float64{float64(i % 3), 0.5},
			FwdReturn:   -0.01, // edgeless primary
		})
	}
	take, _, g, ok := Run(samples, []float64{1, 0.5}, 4, 0.001, DefaultThreshold)
	if ok {
		t.Fatal("Run must not report ok on an unearned meta-model")
	}
	if take {
		t.Fatal("Run must not take a trade on an unearned meta-model")
	}
	if g.Reason == "" {
		t.Fatal("reason required even on refusal")
	}
}

// Cost is applied to both sides: a short call's return is negated before cost,
// so a falling price is a win and cost still subtracts.
func TestCostNetReturnBothSides(t *testing.T) {
	long := Sample{PrimaryProb: 0.7, FwdReturn: 0.02}
	short := Sample{PrimaryProb: 0.3, FwdReturn: -0.02}
	if got := costNetReturn(long, 0.001); math.Abs(got-0.019) > 1e-9 {
		t.Fatalf("long net = %v, want 0.019", got)
	}
	if got := costNetReturn(short, 0.001); math.Abs(got-0.019) > 1e-9 {
		t.Fatalf("short net = %v, want 0.019", got)
	}
	// A move too small to clear cost is not a win on either side.
	tiny := Sample{PrimaryProb: 0.7, FwdReturn: 0.0005}
	if metaLabel(tiny, 0.001) != 0 {
		t.Fatal("a sub-cost move must not be labelled a win")
	}
}

func contains(s, sub string) bool { return strings.Contains(s, sub) }

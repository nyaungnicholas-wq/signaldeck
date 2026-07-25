package sentcorr

import (
	"fmt"
	"math"
	"strings"
	"testing"
)

// synth builds a deterministic observation set. Returns are generated from a
// fixed LCG so every test is reproducible; no math/rand, no clock.
//
// sentFromPrice controls the trap under test: at 1.0 sentiment is a pure echo of
// the price move that already happened (the contamination this package exists to
// detect), at 0 it is independent of price.
// fwdFromSent controls genuine forward information.
func synth(nSym, nSessions int, sentFromPrice, fwdFromSent float64) []Obs {
	r := newLCG(0xABCDEF)
	// Session index -> month label. 21 sessions to a month keeps the era gate
	// satisfied for the long samples and lets short ones fail it honestly.
	month := func(i int) string {
		m := i/21 + 1
		return fmt.Sprintf("20%02d-%02d", 20+m/12, m%12+1)
	}
	unif := func() float64 { return float64(r.next(20001))/10000 - 1 } // [-1,1]

	var out []Obs
	for s := 0; s < nSym; s++ {
		rets := make([]float64, nSessions+30)
		for i := range rets {
			rets[i] = unif() * 0.02
		}
		for i := 10; i < nSessions; i++ {
			ret0 := rets[i]
			trail := 0.0
			for k := i - 4; k <= i; k++ {
				trail += rets[k]
			}
			// Independent component of sentiment.
			indep := unif()
			sent := sentFromPrice*clamp(ret0/0.02) + (1-sentFromPrice)*indep
			// Forward return: noise plus (optionally) a real dependence on the
			// INDEPENDENT part of sentiment, so a genuine signal is one that
			// survives residualisation.
			fwd := unif()*0.03 + fwdFromSent*indep*0.02
			out = append(out, Obs{
				SymbolID:   int64(s + 1),
				Day:        month(i) + "-15",
				SessionIdx: i,
				Sent:       clamp(sent),
				NHeadlines: 2,
				Ret0:       ret0,
				RetTrail:   trail,
				Fwd:        fwd,
			})
		}
	}
	return out
}

func clamp(f float64) float64 { return math.Max(-1, math.Min(1, f)) }

// THE HEADLINE TEST. Sentiment here is a pure restatement of the price move that
// already happened and has zero forward information. A naive study would find a
// correlation (because price is autocorrelated) and call it an edge. The partial
// IC must be indistinguishable from zero, and the verdict must SAY so.
func TestSentimentThatOnlyEchoesPriceShowsNoEdge(t *testing.T) {
	obs := synth(60, 400, 1.0, 0.0)
	res := Study(obs, DefaultConfig(5))
	if res.Gated {
		t.Fatalf("unexpectedly gated: %s", res.GateReason)
	}
	if res.PartialIC == nil {
		t.Fatal("PartialIC withheld on a sample large enough to measure")
	}
	if math.Abs(*res.PartialIC) > 0.05 {
		t.Errorf("PartialIC = %+.4f on a pure price echo; want ~0 — the control failed",
			*res.PartialIC)
	}
	if res.PartialLo == nil || *res.PartialLo > 0 || *res.PartialHi < 0 {
		t.Errorf("CI [%v, %v] should straddle zero on a pure price echo",
			deref(res.PartialLo), deref(res.PartialHi))
	}
	if !strings.Contains(res.Verdict, "NO measurable") {
		t.Errorf("verdict should state the negative result plainly, got: %s", res.Verdict)
	}
}

// The complement: when sentiment DOES carry information orthogonal to price, the
// tool has to find it. A test suite that only proves the tool says "no" proves
// the tool is broken, not that it is honest.
func TestGenuineOrthogonalInformationIsDetected(t *testing.T) {
	obs := synth(60, 400, 0.3, 0.6)
	res := Study(obs, DefaultConfig(5))
	if res.Gated {
		t.Fatalf("unexpectedly gated: %s", res.GateReason)
	}
	if res.PartialIC == nil {
		t.Fatal("PartialIC withheld")
	}
	if *res.PartialIC < 0.10 {
		t.Errorf("PartialIC = %+.4f, want clearly positive on planted orthogonal signal",
			*res.PartialIC)
	}
	if res.PartialLo == nil || *res.PartialLo <= 0 {
		t.Errorf("CI lower bound %v should exclude zero on a planted signal", deref(res.PartialLo))
	}
	if res.Monotonic == nil || !*res.Monotonic {
		t.Errorf("quintile means should be monotonic on a planted linear signal, got %v", res.Quintiles)
	}
	if !strings.Contains(res.Verdict, "excludes zero") {
		t.Errorf("verdict should report the positive finding, got: %s", res.Verdict)
	}
}

// Overlapping windows are the difference between 300 observations and 300
// re-reports of the same 15 forward moves.
func TestNonOverlapThinning(t *testing.T) {
	obs := []Obs{}
	for i := 0; i < 42; i++ {
		obs = append(obs, Obs{SymbolID: 1, Day: "2026-01-15", SessionIdx: i, NHeadlines: 1})
	}
	got := thinNonOverlapping(obs, 21)
	if len(got) != 2 {
		t.Fatalf("kept %d observations from 42 daily rows at horizon 21, want 2", len(got))
	}
	if got[0].SessionIdx != 0 || got[1].SessionIdx != 21 {
		t.Errorf("kept sessions %d,%d; want 0,21", got[0].SessionIdx, got[1].SessionIdx)
	}
}

func TestNonOverlapIsPerSymbol(t *testing.T) {
	// Two symbols on the same days do NOT overlap each other's windows — they
	// are separate forward moves. Thinning across symbols would throw away most
	// of a legitimate cross-sectional sample.
	obs := []Obs{
		{SymbolID: 1, SessionIdx: 0, NHeadlines: 1, Day: "2026-01-05"},
		{SymbolID: 2, SessionIdx: 0, NHeadlines: 1, Day: "2026-01-05"},
		{SymbolID: 3, SessionIdx: 0, NHeadlines: 1, Day: "2026-01-05"},
	}
	if got := thinNonOverlapping(obs, 21); len(got) != 3 {
		t.Errorf("kept %d, want 3 (different symbols, same day)", len(got))
	}
}

func TestGatesWithholdRatherThanEstimate(t *testing.T) {
	t.Run("too few observations", func(t *testing.T) {
		res := Study(synth(40, 40, 0.5, 0.5), DefaultConfig(21))
		if !res.Gated {
			t.Fatal("want gated")
		}
		if res.PartialIC != nil || res.RawIC != nil || res.HitRate != nil {
			t.Error("gated result must withhold every metric, not report zeros")
		}
		if !strings.Contains(res.GateReason, "independent observations") {
			t.Errorf("gate reason should name the cause, got %q", res.GateReason)
		}
		if res.Obs == 0 && res.RawObs == 0 {
			t.Error("sample description should still be reported when gated")
		}
	})

	t.Run("too few symbols", func(t *testing.T) {
		// Plenty of rows, only 5 names: not a fleet-wide finding.
		cfg := DefaultConfig(1)
		cfg.MinObs = 100
		res := Study(synth(5, 400, 0.5, 0.5), cfg)
		if !res.Gated || !strings.Contains(res.GateReason, "symbols") {
			t.Errorf("want symbol gate, got gated=%v reason=%q", res.Gated, res.GateReason)
		}
	})

	t.Run("insufficient era coverage", func(t *testing.T) {
		// 60 sessions ≈ 3 months of synthetic calendar: enough rows, not enough eras.
		cfg := DefaultConfig(1)
		cfg.MinObs = 100
		cfg.MinSymbols = 10
		res := Study(synth(40, 60, 0.5, 0.5), cfg)
		if !res.Gated || !strings.Contains(res.GateReason, "era coverage") {
			t.Errorf("want era gate, got gated=%v reason=%q", res.Gated, res.GateReason)
		}
	})
}

// The base rate must be the best CONSTANT call, not 50%. This is the correction
// that exposed the 52-week-high signal as proximity mechanics.
func TestBaseRateIsBestConstantNotFiftyPercent(t *testing.T) {
	// Forward returns are positive 80% of the time and sentiment is pure noise,
	// so a sign-agreement rate near 50% is WORSE than always saying "up".
	obs := []Obs{}
	r := newLCG(7)
	for s := 0; s < 40; s++ {
		for i := 0; i < 200; i++ {
			fwd := 0.01
			if r.next(10) < 2 {
				fwd = -0.01
			}
			obs = append(obs, Obs{
				SymbolID: int64(s + 1), SessionIdx: i, NHeadlines: 1,
				Day:  fmt.Sprintf("2026-%02d-15", i/21+1),
				Sent: float64(r.next(2001))/1000 - 1,
				Fwd:  fwd,
			})
		}
	}
	cfg := DefaultConfig(1)
	cfg.MinMonths = 3
	res := Study(obs, cfg)
	if res.Gated {
		t.Fatalf("unexpectedly gated: %s", res.GateReason)
	}
	if res.BaseRate == nil || *res.BaseRate < 0.7 {
		t.Fatalf("BaseRate = %v, want ~0.8 (the always-up constant)", deref(res.BaseRate))
	}
	if res.Edge == nil || *res.Edge > 0 {
		t.Errorf("Edge = %v; noise sentiment must not beat the best constant call", deref(res.Edge))
	}
}

func TestSpreadNetChargesCost(t *testing.T) {
	res := Study(synth(60, 400, 0.2, 0.8), DefaultConfig(5))
	if res.SpreadGross == nil || res.SpreadNet == nil {
		t.Fatal("spreads withheld")
	}
	want := *res.SpreadGross - 4*DefaultConfig(5).CostPerSide
	if math.Abs(*res.SpreadNet-want) > 1e-12 {
		t.Errorf("SpreadNet = %v, want gross minus four cost crossings (%v)", *res.SpreadNet, want)
	}
	if *res.SpreadNet >= *res.SpreadGross {
		t.Error("net spread must be below gross")
	}
}

func TestNoLookaheadAppendingLaterDataOnlyAdds(t *testing.T) {
	// Which observations get SELECTED must not depend on data that arrives
	// later. Thinning a prefix has to yield exactly the prefix of thinning the
	// whole series — if a future row could displace an earlier selection, the
	// study's sample would be chosen with hindsight.
	full := synth(40, 300, 0.4, 0.4)
	var prefix []Obs
	for _, o := range full {
		if o.SessionIdx < 200 {
			prefix = append(prefix, o)
		}
	}

	keptFull := thinNonOverlapping(full, 21)
	keptPrefix := thinNonOverlapping(prefix, 21)

	// Every row the prefix run selected must also have been selected by the full
	// run (the reverse does not hold: the full run legitimately selects more).
	inFull := map[string]bool{}
	for _, o := range keptFull {
		inFull[key(o)] = true
	}
	for _, o := range keptPrefix {
		if !inFull[key(o)] {
			t.Fatalf("prefix selected %s but the full series did not — selection depends on future data", key(o))
		}
	}
	if len(keptPrefix) >= len(keptFull) {
		t.Errorf("prefix kept %d, full kept %d; appending sessions should ADD observations",
			len(keptPrefix), len(keptFull))
	}
}

func TestDeterministicAcrossRuns(t *testing.T) {
	// A borderline CI must not be re-rollable until it looks significant.
	obs := synth(50, 300, 0.4, 0.2)
	first := Study(obs, DefaultConfig(5))
	for i := 0; i < 3; i++ {
		got := Study(obs, DefaultConfig(5))
		if deref(got.PartialIC) != deref(first.PartialIC) ||
			deref(got.PartialLo) != deref(first.PartialLo) ||
			deref(got.PartialHi) != deref(first.PartialHi) {
			t.Fatalf("run %d differs: %v vs %v", i, got.PartialIC, first.PartialIC)
		}
	}
}

func TestPartialCorrelationRemovesAKnownControl(t *testing.T) {
	// Direct unit check of the statistics: y is built entirely from c, and x
	// shares only c with y. The raw correlation is large, the partial is ~0.
	n := 500
	r := newLCG(99)
	x := make([]float64, n)
	y := make([]float64, n)
	c := make([]float64, n)
	for i := 0; i < n; i++ {
		c[i] = float64(r.next(2001))/1000 - 1
		x[i] = c[i] + 0.05*(float64(r.next(2001))/1000-1)
		y[i] = 2*c[i] + 0.05*(float64(r.next(2001))/1000-1)
	}
	raw, ok := pearson(x, y)
	if !ok || raw < 0.9 {
		t.Fatalf("raw pearson = %v, want strongly positive", raw)
	}
	p, ok := partial(x, y, c)
	if !ok {
		t.Fatal("partial not computable")
	}
	if math.Abs(p) > 0.1 {
		t.Errorf("partial = %+.4f after controlling for the shared driver, want ~0", p)
	}
}

func TestPartialWithheldOnCollinearControls(t *testing.T) {
	// A degenerate control column (no variance) makes the normal equations
	// singular. The engine must withhold rather than return a number produced by
	// dividing through a near-zero pivot.
	n := 100
	x := make([]float64, n)
	y := make([]float64, n)
	zero := make([]float64, n)
	for i := range x {
		x[i] = float64(i)
		y[i] = float64(i % 7)
	}
	if _, ok := partial(x, y, zero); ok {
		t.Error("partial should refuse a zero-variance control column")
	}
}

func TestPearsonWithheldOnZeroVariance(t *testing.T) {
	// Returning 0 for an undefined correlation would be a lie dressed as a
	// measurement.
	flat := []float64{1, 1, 1, 1, 1}
	if _, ok := pearson(flat, []float64{1, 2, 3, 4, 5}); ok {
		t.Error("pearson should refuse a zero-variance series")
	}
}

func TestSpearmanHandlesTies(t *testing.T) {
	// Lexicon scores tie constantly; ignoring ties biases the rank correlation.
	x := []float64{0, 0, 0, 1, 1, 2}
	y := []float64{1, 2, 3, 4, 5, 6}
	got, ok := spearman(x, y)
	if !ok {
		t.Fatal("spearman not computable")
	}
	if got <= 0 || got > 1 {
		t.Errorf("spearman = %v, want in (0,1]", got)
	}
}

func key(o Obs) string {
	return fmt.Sprintf("%d/%d/%.6f/%.6f", o.SymbolID, o.SessionIdx, o.Sent, o.Fwd)
}

func deref(f *float64) any {
	if f == nil {
		return "nil"
	}
	return *f
}

func TestFamilyCorrectionWidensTheInterval(t *testing.T) {
	// Testing several horizons at once and reporting whichever clears a nominal
	// 95% bound is p-hacking with extra steps. The corrected interval must be
	// strictly wider, so adding horizons makes each one HARDER to clear.
	obs := synth(60, 400, 0.3, 0.35)

	nominal := Study(obs, DefaultConfig(5))
	cfg := DefaultConfig(5)
	cfg.FamilySize = 3
	corrected := Study(obs, cfg)

	if nominal.PartialLo == nil || corrected.PartialLo == nil {
		t.Fatal("intervals withheld")
	}
	if !(*corrected.PartialLo < *nominal.PartialLo) || !(*corrected.PartialHi > *nominal.PartialHi) {
		t.Errorf("corrected [%.4f,%.4f] must be wider than nominal [%.4f,%.4f]",
			*corrected.PartialLo, *corrected.PartialHi, *nominal.PartialLo, *nominal.PartialHi)
	}
	// The nominal 95% bounds must still be reported, so the cost of the
	// correction is visible instead of hidden.
	if corrected.PartialLo95 == nil || *corrected.PartialLo95 <= *corrected.PartialLo {
		t.Error("nominal 95% bounds should be reported alongside the corrected ones")
	}
	if corrected.FamilySize != 3 || !strings.Contains(corrected.CILevel, "Bonferroni") {
		t.Errorf("CILevel = %q, familySize = %d; should name the correction",
			corrected.CILevel, corrected.FamilySize)
	}
}

func TestContrarianSignalIsJudgedInItsOwnDirection(t *testing.T) {
	// A reliably CONTRARIAN signal is still a signal. Judging it by a
	// long-top/short-bottom book would condemn a portfolio nobody would hold and
	// report a real finding as untradeable.
	obs := synth(60, 400, 0.2, -0.8) // negative dependence: good news precedes weakness
	res := Study(obs, DefaultConfig(5))
	if res.Gated {
		t.Fatalf("unexpectedly gated: %s", res.GateReason)
	}
	if res.PartialIC == nil || *res.PartialIC >= 0 {
		t.Fatalf("PartialIC = %v, want negative on planted contrarian signal", deref(res.PartialIC))
	}
	if res.SpreadAligned == nil || res.SpreadGross == nil {
		t.Fatal("spreads withheld")
	}
	// Aligned = reverse the book, then pay the cost.
	want := -*res.SpreadGross - 4*DefaultConfig(5).CostPerSide
	if math.Abs(*res.SpreadAligned-want) > 1e-12 {
		t.Errorf("SpreadAligned = %v, want the reversed book net of cost (%v)", *res.SpreadAligned, want)
	}
	if *res.SpreadAligned <= 0 {
		t.Errorf("aligned spread %v should be positive for a strong planted contrarian signal", *res.SpreadAligned)
	}
	if !strings.Contains(res.AlignedSide, "CONTRARIAN") {
		t.Errorf("AlignedSide = %q; the direction must be named so the number cannot be read backwards",
			res.AlignedSide)
	}
	if !strings.Contains(res.Verdict, "CONTRARIAN") {
		t.Errorf("verdict should state the direction, got: %s", res.Verdict)
	}
}

func TestContrarianFindingAlwaysCarriesTheSurvivorshipWarning(t *testing.T) {
	// "Buy the bad news" is the strategy shape this universe is least able to
	// evaluate, because the names whose bad news ended in delisting are absent.
	// The warning is generated from the sign of the IC so it cannot be omitted.
	res := Study(synth(60, 400, 0.2, -0.8), DefaultConfig(5))
	if res.PartialIC == nil || *res.PartialIC >= 0 {
		t.Fatalf("expected a negative IC, got %v", deref(res.PartialIC))
	}
	if !strings.Contains(res.Verdict, "SURVIVORSHIP WARNING") {
		t.Errorf("contrarian verdict must carry the survivorship warning, got: %s", res.Verdict)
	}

	// A POSITIVE finding does not get that particular warning — it does not have
	// the same exposure, and attaching it everywhere would make it noise.
	pos := Study(synth(60, 400, 0.2, 0.8), DefaultConfig(5))
	if pos.PartialIC == nil || *pos.PartialIC <= 0 {
		t.Fatalf("expected a positive IC, got %v", deref(pos.PartialIC))
	}
	if strings.Contains(pos.Verdict, "SURVIVORSHIP WARNING") {
		t.Error("the contrarian-specific warning should not fire on a positive finding")
	}
}

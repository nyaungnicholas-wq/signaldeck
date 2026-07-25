package options

import (
	"math"
	"testing"

	"github.com/nyaungnicholas-wq/signaldeck/internal/volregime"
)

// insts builds a walk of n instances alternating elevated/calm actual outcomes
// at the given vol levels, all calls correct unless wrong>0.
func insts(n int, elevVol, calmVol float64, wrong int) []volregime.Instance {
	out := make([]volregime.Instance, 0, n)
	for i := 0; i < n; i++ {
		actual, fv := "elevated", elevVol
		if i%2 == 1 {
			actual, fv = "calm", calmVol
		}
		correct := i >= wrong
		pred := actual
		if !correct {
			if actual == "elevated" {
				pred = "calm"
			} else {
				pred = "elevated"
			}
		}
		out = append(out, volregime.Instance{Ts: int64(i), Regime: pred, Conviction: 0.9,
			Actual: actual, Correct: correct, ForwardVol: fv})
	}
	return out
}

func TestStatsMeasuresConditionalLevels(t *testing.T) {
	rets := make([]float64, 300)
	for i := range rets {
		rets[i] = 0.01
		if i%2 == 0 {
			rets[i] = -0.01
		}
	}
	st, ok := Stats(insts(12, 0.60, 0.20, 0), rets, 63)
	if !ok {
		t.Fatal("stats refused a full walk")
	}
	approx(t, st.ElevatedVol, 0.60, 1e-12, "elevated mean")
	approx(t, st.CalmVol, 0.20, 1e-12, "calm mean")
	approx(t, st.Unconditional, 0.40, 1e-12, "unconditional mean")
	approx(t, st.Accuracy, 1.0, 1e-12, "own-walk accuracy")
	if st.ElevatedN != 6 || st.CalmN != 6 {
		t.Fatalf("group counts %d/%d, want 6/6", st.ElevatedN, st.CalmN)
	}
	if st.Realized21 <= 0 || st.Realized63 <= 0 {
		t.Fatal("trailing realized vols not computed")
	}
}

// Thin history and one-sided history are honest absences, not one-sided levels.
func TestStatsRefusesThinOrOneSidedHistory(t *testing.T) {
	rets := make([]float64, 100)
	if _, ok := Stats(insts(4, 0.5, 0.2, 0), rets, 63); ok {
		t.Fatal("accepted a walk below the instance gate")
	}
	oneSided := insts(12, 0.5, 0.2, 0)
	for i := range oneSided {
		oneSided[i].Actual = "elevated"
		oneSided[i].ForwardVol = 0.5
	}
	if _, ok := Stats(oneSided, rets, 63); ok {
		t.Fatal("accepted history with no calm windows — the calm level would be invented")
	}
}

// The expected vol must be the accuracy-weighted mixture, NOT the conditional
// level of the predicted regime: pricing in being wrong is the whole point.
func TestExpectMixesByMeasuredAccuracy(t *testing.T) {
	rets := make([]float64, 300)
	for i := range rets {
		rets[i] = float64(i%7)*0.002 - 0.005
	}
	st, ok := Stats(insts(12, 0.60, 0.20, 0), rets, 63)
	if !ok {
		t.Fatal("stats refused")
	}
	f := volregime.Forecast{Regime: "elevated", Conviction: 0.95, Tier: "very-high conviction"}
	e, ok := Expect(f, st)
	if !ok {
		t.Fatal("expect refused a high-conviction call with full stats")
	}
	acc := volregime.AccuracyForConviction(0.95) // 0.76
	approx(t, e.Accuracy, acc, 1e-12, "accuracy")
	approx(t, e.IfRight, 0.60, 1e-12, "if right")
	approx(t, e.IfWrong, 0.20, 1e-12, "if wrong")
	approx(t, e.Expected, acc*0.60+(1-acc)*0.20, 1e-12, "expected vol")
	if e.Expected >= e.IfRight {
		t.Fatal("expected vol must be pulled back from the if-right level by the error rate")
	}
	// A calm call mirrors it.
	calm, _ := Expect(volregime.Forecast{Regime: "calm", Conviction: 0.95}, st)
	approx(t, calm.IfRight, 0.20, 1e-12, "calm if-right")
	approx(t, calm.Expected, acc*0.20+(1-acc)*0.60, 1e-12, "calm expected")
}

// At the bottom conviction band the predictor has no measured edge, so the
// mixture is just the unconditional mean wearing a forecast's clothes.
func TestExpectRefusesNoEdgeBand(t *testing.T) {
	st, _ := Stats(insts(12, 0.6, 0.2, 0), make([]float64, 300), 63)
	if _, ok := Expect(volregime.Forecast{Regime: "elevated", Conviction: 0.1}, st); ok {
		t.Fatal("produced a forecast from a no-measurable-edge call")
	}
}

func TestAssessVerdicts(t *testing.T) {
	e := Expectation{Expected: 0.30, IfRight: 0.35, IfWrong: 0.20, Accuracy: 0.74}
	rich := Assess(0.45, e, DefaultVRP) // fair = 0.32
	if rich.Verdict != "iv-rich" {
		t.Fatalf("verdict %q, want iv-rich", rich.Verdict)
	}
	approx(t, rich.EdgeVol, 0.45-0.32, 1e-12, "edge vol")
	approx(t, rich.BreakevenVRP, 0.15, 1e-12, "breakeven vrp")

	cheap := Assess(0.25, e, DefaultVRP)
	if cheap.Verdict != "iv-cheap" {
		t.Fatalf("verdict %q, want iv-cheap", cheap.Verdict)
	}
	inLine := Assess(0.325, e, DefaultVRP)
	if inLine.Verdict != "in-line" {
		t.Fatalf("verdict %q, want in-line", inLine.Verdict)
	}
	if inLine.Caveat == "" || inLine.Expression == "" {
		t.Fatal("every verdict must carry its caveat and expression")
	}
}

// Robustness is the honesty flag that separates "the market is mispricing vol"
// from "I am betting on my own regime call".
func TestAssessRobustnessFlag(t *testing.T) {
	e := Expectation{Expected: 0.30, IfRight: 0.35, IfWrong: 0.20, Accuracy: 0.74}
	// Far above BOTH branches' fair implied (0.37 and 0.22) — verdict survives
	// the call being wrong.
	if ed := Assess(0.60, e, DefaultVRP); !ed.Robust {
		t.Fatal("a verdict clear of both branches should be robust")
	}
	// Between the branches: rich only if the call lands.
	ed := Assess(0.30, e, DefaultVRP)
	if ed.Robust {
		t.Fatal("a verdict that flips with the regime call must not be robust")
	}
	if ed.RobustNote == "" {
		t.Fatal("a conditional verdict must say so")
	}
}

func TestStraddleEdgeDollarGap(t *testing.T) {
	in := Inputs{Spot: 100, Strike: 100, T: 0.25, Rate: 0.04}
	tr := StraddleEdge(in, 0.45, 0.30)
	if tr.EdgePerContract <= 0 {
		t.Fatalf("market vol above model vol should leave a positive seller gap, got %v", tr.EdgePerContract)
	}
	approx(t, tr.EdgePerContract,
		(tr.MarketStraddle.Price-tr.ModelStraddle.Price)*100, 1e-9, "edge per contract")
	approx(t, tr.ForecastMovePct, 0.30*math.Sqrt(0.25), 1e-12, "forecast 1sd move")
	// Symmetry: flip the vols and the gap flips sign.
	if flip := StraddleEdge(in, 0.30, 0.45); flip.EdgePerContract >= 0 {
		t.Fatal("model vol above market vol should leave a negative seller gap")
	}
}

func TestRealizedVolAnnualizes(t *testing.T) {
	// An even-length alternating +-1% series has mean exactly 0, so its sample
	// stdev is 0.01*sqrt(n/(n-1)) analytically — no circular reference to the
	// implementation's own helper.
	rets := make([]float64, 64)
	for i := range rets {
		rets[i] = 0.01
		if i%2 == 0 {
			rets[i] = -0.01
		}
	}
	got := RealizedVol(rets, 64)
	want := 0.01 * math.Sqrt(64.0/63.0) * math.Sqrt(volregime.TradingDaysPerYear)
	approx(t, got, want, 1e-9, "realized vol")
	if RealizedVol(rets[:10], 63) != 0 {
		t.Fatal("a partial window must return 0, never a short-window vol passed off as 63d")
	}
}

// When the historical conditional level sits far from the symbol's CURRENT
// volatility, the forecast is being driven by the level extrapolation rather
// than by the validated regime call, and the payload must say so.
func TestExpectFlagsLevelDrift(t *testing.T) {
	// Quiet recent returns (~1.6% annualized-ish) but violent historical
	// "elevated" windows: the mixture lands far above where vol is today.
	rets := make([]float64, 300)
	for i := range rets {
		rets[i] = 0.0005
		if i%2 == 0 {
			rets[i] = -0.0005
		}
	}
	st, ok := Stats(insts(12, 0.90, 0.40, 0), rets, 63)
	if !ok {
		t.Fatal("stats refused")
	}
	e, ok := Expect(volregime.Forecast{Regime: "elevated", Conviction: 0.95}, st)
	if !ok {
		t.Fatal("expect refused")
	}
	if e.DriftWarning == "" {
		t.Fatalf("expected %.3f vs realized63 %.3f (ratio %.2f) must be flagged",
			e.Expected, st.Realized63, e.LevelDrift)
	}
	// A symbol whose history matches its present gets no warning.
	calm := st
	calm.Realized63 = e.Expected
	quiet, _ := Expect(volregime.Forecast{Regime: "elevated", Conviction: 0.95}, calm)
	if quiet.DriftWarning != "" {
		t.Fatalf("no drift should mean no warning, got %q", quiet.DriftWarning)
	}
}

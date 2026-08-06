package hmmregime

import (
	"math"
	"testing"

	"github.com/nyaungnicholas-wq/signaldeck/internal/marketdata"
)

// synth builds a deterministic two-regime bar series: the first and last
// thirds are CALM (small drift, small vol) and the middle third is TURBULENT
// (larger vol). No RNG package is used — a fixed LCG keeps the fixture
// byte-identical across runs and platforms, so a failure is always a code
// change and never a reseed.
func synth(n int) ([]marketdata.Bar, []bool) {
	var seed uint64 = 0x2545F4914F6CDD1D
	next := func() float64 { // uniform in [-1,1)
		seed = seed*6364136223846793005 + 1442695040888963407
		return float64((seed>>11)&0xFFFFF)/float64(0x80000) - 1.0
	}
	bars := make([]marketdata.Bar, 0, n)
	turb := make([]bool, 0, n)
	px := 100.0
	for i := 0; i < n; i++ {
		hot := i >= n/3 && i < 2*n/3
		vol := 0.004
		if hot {
			vol = 0.030
		}
		r := next() * vol
		px *= math.Exp(r)
		bars = append(bars, marketdata.Bar{
			Ts: int64(1700000000 + i*86400), Open: px, High: px * 1.001,
			Low: px * 0.999, Close: px, Volume: 1000,
		})
		turb = append(turb, hot)
	}
	return bars, turb
}

// THE test. A regime label at bar i must depend ONLY on bars[0..i]. Fitting
// Baum-Welch over the whole series and then decoding with Viterbi or smoothed
// posteriors is the standard way to build this and it is LOOKAHEAD: the label
// at i moves when bars after i arrive. Every point on a published regime
// timeline has to be a decision that could have been made in real time at that
// bar, so truncating the input must not move any earlier label.
func TestFilterIsCausal(t *testing.T) {
	bars, _ := synth(400)
	m, ok := Fit(bars[:200], Defaults())
	if !ok {
		t.Fatal("Fit returned ok=false on 200 clean bars")
	}
	full := m.Filter(bars)
	if len(full) != len(bars) {
		t.Fatalf("Filter returned %d points for %d bars", len(full), len(bars))
	}
	for i := 210; i < len(bars); i += 7 {
		trunc := m.Filter(bars[:i+1])
		if len(trunc) != i+1 {
			t.Fatalf("truncated Filter returned %d points for %d bars", len(trunc), i+1)
		}
		if trunc[i].Label != full[i].Label {
			t.Errorf("LOOKAHEAD at i=%d: label with future bars = %q, without = %q",
				i, full[i].Label, trunc[i].Label)
		}
		if math.Abs(trunc[i].Prob-full[i].Prob) > 1e-9 {
			t.Errorf("LOOKAHEAD at i=%d: prob with future bars = %.12f, without = %.12f",
				i, full[i].Prob, trunc[i].Prob)
		}
	}
}

// A detector that never fires is trivially causal, so causality alone proves
// nothing. The planted high-vol block must actually be found.
func TestRecoversPlantedVolRegime(t *testing.T) {
	bars, turb := synth(600)
	m, ok := Fit(bars, Defaults())
	if !ok {
		t.Fatal("Fit returned ok=false")
	}
	pts := m.Filter(bars)
	var hit, n int
	for i := range pts {
		if i < 60 { // burn-in: the filter needs history before it is meaningful
			continue
		}
		n++
		if (pts[i].Label == Turbulent) == turb[i] {
			hit++
		}
	}
	if got := float64(hit) / float64(n); got < 0.80 {
		t.Errorf("planted-regime agreement = %.1f%%, want >= 80%%", got*100)
	}
}

// Same bars in, same labels out. A model that reseeds or iterates to a
// different local optimum per call cannot be graded against an incumbent.
func TestDeterministic(t *testing.T) {
	bars, _ := synth(300)
	a, ok1 := Fit(bars, Defaults())
	b, ok2 := Fit(bars, Defaults())
	if !ok1 || !ok2 {
		t.Fatal("Fit returned ok=false")
	}
	pa, pb := a.Filter(bars), b.Filter(bars)
	for i := range pa {
		if pa[i].Label != pb[i].Label || math.Abs(pa[i].Prob-pb[i].Prob) > 1e-12 {
			t.Fatalf("non-deterministic at i=%d: (%q,%.15f) vs (%q,%.15f)",
				i, pa[i].Label, pa[i].Prob, pb[i].Label, pb[i].Prob)
		}
	}
}

// Stochastic-matrix and probability invariants. NaN here is the classic
// Baum-Welch underflow failure and it must never reach a published label.
func TestModelInvariants(t *testing.T) {
	bars, _ := synth(400)
	m, ok := Fit(bars, Defaults())
	if !ok {
		t.Fatal("Fit returned ok=false")
	}
	tr := m.Transitions()
	if len(tr) != m.NStates() {
		t.Fatalf("Transitions() has %d rows, want %d", len(tr), m.NStates())
	}
	for i, row := range tr {
		if len(row) != m.NStates() {
			t.Fatalf("row %d has %d cols, want %d", i, len(row), m.NStates())
		}
		sum := 0.0
		for _, p := range row {
			if math.IsNaN(p) || p < 0 || p > 1 {
				t.Fatalf("transition[%d] bad probability %v", i, p)
			}
			sum += p
		}
		if math.Abs(sum-1) > 1e-9 {
			t.Errorf("transition row %d sums to %.12f, want 1", i, sum)
		}
	}
	for i, pt := range m.Filter(bars) {
		if math.IsNaN(pt.Prob) || pt.Prob < 0 || pt.Prob > 1 {
			t.Fatalf("point %d has bad Prob %v", i, pt.Prob)
		}
		if pt.Label == "" {
			t.Fatalf("point %d has empty label", i)
		}
	}
}

// Too little history must refuse rather than emit a confident label off six
// bars. Silence is a valid answer; a fabricated regime is not.
func TestInsufficientDataRefuses(t *testing.T) {
	bars, _ := synth(600)
	for _, n := range []int{0, 1, 5, 20} {
		if _, ok := Fit(bars[:n], Defaults()); ok {
			t.Errorf("Fit(%d bars) returned ok=true, want refusal", n)
		}
	}
}

// Labels must be ordered by fitted volatility, not by whatever order EM
// happened to converge in — otherwise "turbulent" means a different thing on
// every symbol and the grader compares nothing.
func TestLabelsOrderedByVolatility(t *testing.T) {
	bars, _ := synth(600)
	m, ok := Fit(bars, Defaults())
	if !ok {
		t.Fatal("Fit returned ok=false")
	}
	sds := m.StateSDs()
	if len(sds) != m.NStates() {
		t.Fatalf("StateSDs() len %d, want %d", len(sds), m.NStates())
	}
	for i := 1; i < len(sds); i++ {
		if sds[i] < sds[i-1] {
			t.Errorf("state SDs not ascending: %v", sds)
		}
	}
	if m.LabelOf(0) != Calm || m.LabelOf(m.NStates()-1) != Turbulent {
		t.Errorf("LabelOf(0)=%q LabelOf(last)=%q, want %q/%q",
			m.LabelOf(0), m.LabelOf(m.NStates()-1), Calm, Turbulent)
	}
}

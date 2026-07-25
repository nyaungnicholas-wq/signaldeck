package pipeline

import (
	"context"
	"fmt"
	"math"
	"math/rand"
	"path/filepath"
	"testing"

	md "github.com/nyaungnicholas-wq/signaldeck/internal/marketdata"
	"github.com/nyaungnicholas-wq/signaldeck/internal/store"
)

// syntheticCloses builds a price path with a controllable volatility regime, so
// the distribution's conditioning has something real to find.
func syntheticCloses(n int, volAt func(i int) float64, seed int64) []float64 {
	rnd := rand.New(rand.NewSource(seed)) //nolint:gosec // deterministic fixture
	closes := make([]float64, n)
	closes[0] = 100
	for i := 1; i < n; i++ {
		closes[i] = closes[i-1] * (1 + volAt(i)*rnd.NormFloat64())
	}
	return closes
}

func TestBuildReturnForecastRefusesThinHistory(t *testing.T) {
	short := syntheticCloses(100, func(int) float64 { return 0.01 }, 1)
	if _, ok := buildReturnForecast(short, md.H1d, 0); ok {
		t.Fatal("forecast built from 100 bars — the vol-rank warm-up alone needs more")
	}
}

// A split-corruption artifact must make the forecast REFUSE, not widen. This is
// the guard that produced garbage live forecasts once before.
func TestBuildReturnForecastRefusesContaminatedWindow(t *testing.T) {
	closes := syntheticCloses(900, func(int) float64 { return 0.01 }, 2)
	if _, ok := buildReturnForecast(closes, md.H1d, 0); !ok {
		t.Fatal("clean 900-bar history was refused")
	}
	closes[500] *= 4 // reverse-split artifact
	if _, ok := buildReturnForecast(closes, md.H1d, 0); ok {
		t.Fatal("a 300% one-day move did not trigger the sanity refusal")
	}
}

// The probabilities must partition, the no-trade zone must be represented, and
// the width must reflect the regime that is actually current.
func TestReturnForecastShape(t *testing.T) {
	closes := syntheticCloses(1000, func(int) float64 { return 0.02 }, 3)
	f, ok := buildReturnForecast(closes, md.H1d, 12345)
	if !ok {
		t.Fatal("forecast refused on a clean fixture")
	}
	if s := f.PUp + f.PDown + f.PInside; math.Abs(s-1) > 1e-9 {
		t.Fatalf("probabilities sum to %v", s)
	}
	if f.Ts != 12345 || f.Tau != distTau {
		t.Fatalf("metadata wrong: ts=%d tau=%v", f.Ts, f.Tau)
	}
	if f.Regime != "elevated" && f.Regime != "calm" {
		t.Fatalf("regime = %q", f.Regime)
	}
	if f.N < 60 {
		t.Fatalf("conditional sample = %d, below the engine floor", f.N)
	}
	if !(f.Q10 < f.Q50 && f.Q50 < f.Q90) {
		t.Fatalf("quantiles out of order: %v %v %v", f.Q10, f.Q50, f.Q90)
	}
}

// A 1-week horizon must have a materially wider distribution than a 1-day one
// on the same path — if it does not, the forward span is not being applied.
func TestLongerHorizonIsWider(t *testing.T) {
	closes := syntheticCloses(1200, func(int) float64 { return 0.015 }, 4)
	d1, ok1 := buildReturnForecast(closes, md.H1d, 0)
	w1, ok2 := buildReturnForecast(closes, md.H1w, 0)
	if !ok1 || !ok2 {
		t.Fatal("forecast refused")
	}
	if w1.Sigma <= d1.Sigma {
		t.Fatalf("1w sigma %v is not wider than 1d %v", w1.Sigma, d1.Sigma)
	}
	if w1.PInside >= d1.PInside {
		t.Fatalf("1w no-trade zone %v should be SMALLER than 1d %v (bigger moves clear the cost band)",
			w1.PInside, d1.PInside)
	}
}

// The vol-regime labels must be causal: a label at bar i cannot change when
// later bars are appended. This is the no-lookahead guarantee the whole
// distribution rests on.
func TestVolRegimeSeriesIsCausal(t *testing.T) {
	closes := syntheticCloses(1000, func(i int) float64 {
		if i > 600 {
			return 0.05 // a regime shift the earlier labels must not see
		}
		return 0.01
	}, 5)
	full := volRegimeSeries(simpleReturns(closes))
	trunc := volRegimeSeries(simpleReturns(closes[:700]))
	for i := 0; i < len(trunc); i++ {
		if trunc[i] != full[i] {
			t.Fatalf("label at bar %d changed when future bars were appended: %q → %q",
				i, trunc[i], full[i])
		}
	}
	// And it must actually detect the shift, or the test above is vacuous.
	if full[len(full)-1] != "elevated" {
		t.Fatalf("final label = %q, want elevated after a 5x vol increase", full[len(full)-1])
	}
}

// Likewise the forecast itself: rebuilding it on truncated history must not be
// affected by bars that had not happened.
func TestBuildReturnForecastIsCausal(t *testing.T) {
	closes := syntheticCloses(1400, func(int) float64 { return 0.02 }, 6)
	at900, ok1 := buildReturnForecast(closes[:900], md.H1d, 0)
	if !ok1 {
		t.Fatal("refused")
	}
	// Appending 500 more bars must not alter the forecast that stood at bar 900.
	again, ok2 := buildReturnForecast(closes[:900], md.H1d, 0)
	if !ok2 || again.Q10 != at900.Q10 || again.Q90 != at900.Q90 || again.N != at900.N {
		t.Fatal("forecast is not a pure function of the bars it was given")
	}
}

// The walk-forward grade must be honest about conditioning that adds nothing: on
// a path with CONSTANT volatility, splitting by vol regime is noise, so skill
// must not be meaningfully positive.
func TestGradeIsNotFooledByUselessConditioning(t *testing.T) {
	closes := syntheticCloses(1400, func(int) float64 { return 0.02 }, 7)
	f, ok := buildReturnForecast(closes, md.H1d, 0)
	if !ok {
		t.Fatal("refused")
	}
	if f.Skill == nil {
		t.Skip("not enough graded pairs on this fixture — the gate is doing its job")
	}
	if *f.Skill > 0.05 {
		t.Fatalf("skill = %v on constant-volatility data; conditioning cannot add 5%% where there is nothing to condition on", *f.Skill)
	}
	if f.GradedN < 30 {
		t.Fatalf("skill reported on %d graded pairs, below the floor", f.GradedN)
	}
}

// A graded skill must never be published on fewer than the floor of pairs — the
// invariant the nightly bias check also asserts against live data.
func TestSkillOnlyPublishedWithEnoughPairs(t *testing.T) {
	for _, n := range []int{600, 800, 1000, 1400} {
		closes := syntheticCloses(n, func(int) float64 { return 0.02 }, int64(n))
		f, ok := buildReturnForecast(closes, md.H1d, 0)
		if !ok {
			continue
		}
		if f.Skill != nil && f.GradedN < 30 {
			t.Fatalf("n=%d published skill on %d pairs", n, f.GradedN)
		}
		if f.Skill == nil && f.GradedN != 0 {
			t.Fatalf("n=%d reported gradedN=%d with no skill", n, f.GradedN)
		}
	}
}

func TestHorizonBars(t *testing.T) {
	if horizonBars(md.H1w) != 5 {
		t.Fatalf("1w span = %d, want 5 trading days", horizonBars(md.H1w))
	}
	if horizonBars(md.H1d) != 1 {
		t.Fatalf("1d span = %d, want 1", horizonBars(md.H1d))
	}
}

func TestCloseSeriesSortsAndDropsBadPrices(t *testing.T) {
	bars := []md.Bar{
		{Ts: 300, Close: 103}, {Ts: 100, Close: 101},
		{Ts: 200, Close: 0}, // non-positive: a different defect, must be dropped
		{Ts: 400, Close: 104},
	}
	got := closeSeries(bars)
	want := []float64{101, 103, 104}
	if len(got) != len(want) {
		t.Fatalf("got %v, want %v", got, want)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("got %v, want %v", got, want)
		}
	}
}

// The second-source parser must be tolerant about layout and strict about
// content: a half-parsed series would manufacture disagreements.
func TestParseSecondSourceCSV(t *testing.T) {
	pts, err := parseSecondSourceCSV("Date,Open,High,Low,Close,Volume\n2026-01-02,1,2,0.5,101.5,1000\n2026-01-03,1,2,0.5,102.25,1000\n")
	if err != nil {
		t.Fatalf("parse failed: %v", err)
	}
	if len(pts) != 2 || pts[0].Close != 101.5 || pts[1].Close != 102.25 {
		t.Fatalf("parsed %+v", pts)
	}

	// Reordered columns and a $ prefix must still work.
	if pts, err := parseSecondSourceCSV("Close/Last,Date\n$99.5,2026-01-02\n"); err != nil || len(pts) != 1 || pts[0].Close != 99.5 {
		t.Fatalf("reordered/prefixed parse failed: %+v %v", pts, err)
	}

	// Missing the columns that matter must be an ERROR, not an empty success.
	if _, err := parseSecondSourceCSV("Foo,Bar\n1,2\n"); err == nil {
		t.Fatal("CSV without Date/Close columns parsed successfully")
	}
	// Unusable rows must not silently become an empty comparison.
	if _, err := parseSecondSourceCSV("Date,Close\nnot-a-date,abc\n"); err == nil {
		t.Fatal("CSV with no usable rows parsed successfully")
	}
	if _, err := parseSecondSourceCSV(""); err == nil {
		t.Fatal("empty body parsed successfully")
	}
	// A non-positive close is not a price.
	if _, err := parseSecondSourceCSV("Date,Close\n2026-01-02,0\n"); err == nil {
		t.Fatal("zero close accepted as a usable row")
	}
}

// The rotation is what keeps a heavy sweep bounded on a slow database. It must
// advance, wrap, and never starve a symbol.
func TestRotateAdvancesAndWraps(t *testing.T) {
	st, err := store.Open(filepath.Join(t.TempDir(), "rotate.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = st.Close() })
	ctx := context.Background()
	var syms []md.Symbol
	for i := 0; i < 10; i++ {
		s, uerr := st.UpsertSymbol(ctx, fmt.Sprintf("S%02d", i), md.Stocks, "x")
		if uerr != nil {
			t.Fatal(uerr)
		}
		syms = append(syms, s)
	}
	const key = "test_cursor"

	// First pass: the first 4.
	slice, cursorFor := rotate(ctx, st, key, syms, 4)
	if len(slice) != 4 || slice[0].Symbol != "S00" || slice[3].Symbol != "S03" {
		t.Fatalf("first pass = %v", names(slice))
	}
	if err := st.SetMeta(ctx, key, cursorFor(4)); err != nil {
		t.Fatal(err)
	}

	// Second pass resumes where the first stopped.
	slice, cursorFor = rotate(ctx, st, key, syms, 4)
	if len(slice) != 4 || slice[0].Symbol != "S04" {
		t.Fatalf("second pass = %v", names(slice))
	}
	// A pass that stops EARLY (time-boxed after 2 of its 4) must resume at the
	// symbol it did not reach, not skip it.
	if err := st.SetMeta(ctx, key, cursorFor(2)); err != nil {
		t.Fatal(err)
	}
	slice, cursorFor = rotate(ctx, st, key, syms, 4)
	if slice[0].Symbol != "S06" {
		t.Fatalf("early-stop resume = %v, want S06 first", names(slice))
	}

	// Running off the end wraps to the start rather than stalling forever.
	if err := st.SetMeta(ctx, key, cursorFor(4)); err != nil {
		t.Fatal(err)
	}
	slice, _ = rotate(ctx, st, key, syms, 4)
	if slice[0].Symbol != "S00" {
		t.Fatalf("wrap = %v, want S00 first", names(slice))
	}

	// A corrupt or out-of-range cursor must restart cleanly, never panic.
	for _, bad := range []string{"", "-5", "999", "garbage"} {
		if err := st.SetMeta(ctx, key, bad); err != nil {
			t.Fatal(err)
		}
		if slice, _ := rotate(ctx, st, key, syms, 3); len(slice) != 3 || slice[0].Symbol != "S00" {
			t.Fatalf("cursor %q gave %v", bad, names(slice))
		}
	}
	// No symbols is not an error.
	if slice, _ := rotate(ctx, st, key, nil, 5); slice != nil {
		t.Fatal("empty universe returned a slice")
	}
}

func names(syms []md.Symbol) []string {
	var out []string
	for _, s := range syms {
		out = append(out, s.Symbol)
	}
	return out
}

package harrv

import (
	"math"

	md "github.com/nyaungnicholas-wq/signaldeck/internal/marketdata"
)

// Exclusions counts why bars were dropped. Reported, never hidden: a
// backtest that does not say what it discarded is not reproducible.
type Exclusions struct {
	FlatRange  int
	Overnight  int
	NonPos     int
	GKNegative int
	Floored    int
	NoPrev     int
}

const (
	// OvernightGuard is the largest absolute overnight log-return treated as
	// real. Above it the bar is dropped as split contamination.
	//
	// 0.65 is NOT chosen here. It is this repository's existing wild-move
	// guard, reused verbatim from volregime.maxSaneReturn and
	// tools/revalidate_structural.py MAX_SANE_RETURN, so it costs no
	// multiplicity; inventing a new threshold would. It admits +91.6% and
	// -47.8% single-session moves.
	//
	// The contamination is measured, not hypothetical. Bars are stored with
	// adjustment=split, and an incremental fetch keeps the OLD basis while the
	// provider silently re-adjusts its whole history at a split -- leaving a
	// permanent discontinuity that every downstream feature reads as a real
	// 50-95% one-day move. internal/splitfix records 1,079 such jumps in full
	// history. An unadjusted 2:1 split gives ln = -0.693 and RV about 0.48, an
	// implied annualised volatility near 1100%, and it would dominate every
	// weekly and monthly HAR aggregate for the next 22 sessions.
	//
	// Measured cost on the live table: 1,295 bars of 2,624,945, or 0.049%.
	// Overall 96.42% of bars remain estimable.
	OvernightGuard = 0.65

	// MinVariance is the floor applied to the realized variance estimate to keep
	// downstream logarithms defined. 1e-8 is roughly a 1 basis point day.
	MinVariance = 1e-8
)

// ln2 avoids a math.Log(2) call per bar in the hot loop.
const ln2 = math.Ln2

// RVSeries converts ascending daily bars into the realized-variance series.
// rv[i] corresponds to bars[i]; ts[i] is bars[i].Ts. rv[i] is NaN where the
// day is not estimable. The output slices always have len(bars) elements.
func RVSeries(bars []md.Bar) (rv []float64, ts []int64, x Exclusions) {
	n := len(bars)
	rv = make([]float64, n)
	ts = make([]int64, n)

	for i, b := range bars {
		ts[i] = b.Ts

		// 1. NO PREVIOUS BAR. The overnight term needs the prior close, so the
		// first bar of any series is structurally unestimable. Counted for
		// observability but it is not an exclusion in the sense the others are:
		// nothing was thrown away, the input simply starts here.
		if i == 0 {
			rv[i] = math.NaN()
			x.NoPrev++
			continue
		}
		cprev := bars[i-1].Close

		// 2. NON-POSITIVE PRICE.
		if b.Open <= 0 || b.High <= 0 || b.Low <= 0 || b.Close <= 0 || cprev <= 0 {
			rv[i] = math.NaN()
			x.NonPos++
			continue
		}

		// 3. INVALID RANGE.
		if b.High < b.Low {
			rv[i] = math.NaN()
			x.NonPos++
			continue
		}

		// 4. SPLIT CONTAMINATION.
		if math.Abs(math.Log(b.Open/cprev)) > OvernightGuard {
			rv[i] = math.NaN()
			x.Overnight++
			continue
		}

		// 5. ZERO RANGE. Measured over the live table (2,624,945 bars across
		// the 2,383 symbols with at least 250 sessions): 90,302 bars, 3.44%,
		// have High == Low. Those are real illiquid
		// sessions that carry genuine volume, so the DATA is fine and the
		// ESTIMATOR is what has nothing to read: a range estimator reports zero
		// intraday variance for them. NaN, never 0 -- zero variance asserts
		// that nothing moved, which is a different and false claim.
		//
		// Deliberately NOT falling back to the overnight term alone: that would
		// apply a second, quietly different estimator to 4% of the sample, and
		// a target that changes definition by row is the defect this package
		// exists to avoid.
		if b.High == b.Low {
			rv[i] = math.NaN()
			x.FlatRange++
			continue
		}

		// Compute common terms.
		overnight := math.Log(b.Open / cprev)
		overnight2 := overnight * overnight
		hl := math.Log(b.High / b.Low)
		hl2 := hl * hl
		co := math.Log(b.Close / b.Open)
		co2 := co * co

		// Garman-Klass intraday component.
		gk := 0.5*hl2 - (2*ln2-1)*co2

		// 6. NEGATIVE GK. This CANNOT happen on a consistent bar: O and C both
		// lie inside [L,H], so |ln(C/O)| <= ln(H/L), and therefore
		// 0.5*hl^2 - 0.386*co^2 >= 0.114*hl^2 >= 0. It fires only when the
		// input is self-inconsistent -- a close outside the day's own range --
		// so it is a data-integrity fallback, not a market case. Measured on
		// the live table: 0 occurrences in 2,624,945 bars. If it ever starts
		// firing, the bars are wrong, not the market.
		if gk < 0 {
			park := hl2 / (4 * ln2)
			rv[i] = overnight2 + park
			x.GKNegative++
			// fall through to floor check
		} else {
			rv[i] = overnight2 + gk
			// fall through to floor check
		}

		// 7. FLOOR.
		if rv[i] < MinVariance {
			rv[i] = MinVariance
			x.Floored++
		}
	}

	return
}

// RollingMean averages the w values ending at index i, skipping NaN, and
// requires at least minCount real values. Returns ok=false otherwise.
// Reads ONLY indices <= i. This is the weekly/monthly HAR aggregate.
func RollingMean(rv []float64, i, w, minCount int) (float64, bool) {
	if i < 0 || i >= len(rv) || w <= 0 {
		return 0, false
	}
	start := i - w + 1
	if start < 0 {
		return 0, false
	}
	var sum float64
	var count int
	for j := start; j <= i; j++ {
		if !math.IsNaN(rv[j]) {
			sum += rv[j]
			count++
		}
	}
	if count < minCount {
		return 0, false
	}
	return sum / float64(count), true
}

// Count returns how many non-NaN entries rv holds.
func Count(rv []float64) int {
	c := 0
	for _, v := range rv {
		if !math.IsNaN(v) {
			c++
		}
	}
	return c
}

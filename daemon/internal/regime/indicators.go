package regime

import (
	"math"

	"github.com/nyaungnicholas-wq/signaldeck/internal/marketdata"
)

// closesOf extracts the close prices from a bar slice in order.
func closesOf(bars []marketdata.Bar) []float64 {
	cs := make([]float64, len(bars))
	for i := range bars {
		cs[i] = bars[i].Close
	}
	return cs
}

// smaAt returns the simple moving average of the n values of xs ending at index
// end (inclusive), i.e. mean of xs[end-n+1 .. end]. If there are fewer than n
// values available up to end, it averages what is available (never looks past
// end, so no lookahead). Returns 0 for an empty window.
func smaAt(xs []float64, end, n int) float64 {
	if end < 0 || len(xs) == 0 {
		return 0
	}
	start := end - n + 1
	if start < 0 {
		start = 0
	}
	sum := 0.0
	cnt := 0
	for i := start; i <= end && i < len(xs); i++ {
		sum += xs[i]
		cnt++
	}
	if cnt == 0 {
		return 0
	}
	return sum / float64(cnt)
}

// stdevAt returns the population standard deviation of the n values of xs ending
// at index end (inclusive). Same windowing rules as smaAt (no lookahead).
func stdevAt(xs []float64, end, n int) float64 {
	if end < 0 || len(xs) == 0 {
		return 0
	}
	start := end - n + 1
	if start < 0 {
		start = 0
	}
	mean := smaAt(xs, end, n)
	sum := 0.0
	cnt := 0
	for i := start; i <= end && i < len(xs); i++ {
		d := xs[i] - mean
		sum += d * d
		cnt++
	}
	if cnt == 0 {
		return 0
	}
	return math.Sqrt(sum / float64(cnt))
}

// bbWidthAt returns the Bollinger band width at index end: (upper-lower)/mid =
// 2*k*stdev/mid, a scale-free measure of volatility. Uses only xs[..end].
// Returns 0 when the middle band is non-positive (degenerate).
func bbWidthAt(xs []float64, end, length int, k float64) float64 {
	mid := smaAt(xs, end, length)
	if mid <= 0 {
		return 0
	}
	sd := stdevAt(xs, end, length)
	return (2 * k * sd) / mid
}

// bbWidthPercentile computes the current Bollinger band width (at the last
// index) and returns its percentile rank (0..100) within the trailing window of
// band-width samples (up to windowLen of them, ending at the last index). A low
// percentile means the current band width is compressed relative to this
// symbol's own recent volatility — a coil. Uses only data up to the last index
// (no lookahead). With a single sample the percentile is defined as 100 (not a
// squeeze) to avoid a false "coil" read on too little history.
func bbWidthPercentile(xs []float64, length int, k float64, windowLen int) float64 {
	n := len(xs)
	if n == 0 {
		return 100
	}
	last := n - 1
	cur := bbWidthAt(xs, last, length, k)

	// Collect the trailing band-width samples (one per bar we can compute).
	start := last - windowLen + 1
	if start < 0 {
		start = 0
	}
	samples := make([]float64, 0, last-start+1)
	for i := start; i <= last; i++ {
		samples = append(samples, bbWidthAt(xs, i, length, k))
	}
	if len(samples) <= 1 {
		return 100
	}

	// Guard the degenerate case: if the window has essentially no dispersion in
	// band width (a constant/linear series has constant width), it is NOT a
	// coil — nothing has compressed relative to anything. Report 100 so it never
	// reads as a squeeze. We compare the spread to the mean width; a window
	// whose max width barely exceeds its min is uniform, not coiling.
	minW, maxW := samples[0], samples[0]
	sumW := 0.0
	for _, s := range samples {
		if s < minW {
			minW = s
		}
		if s > maxW {
			maxW = s
		}
		sumW += s
	}
	meanW := sumW / float64(len(samples))
	if meanW <= 0 || (maxW-minW) < 0.05*meanW {
		return 100
	}

	// Percentile rank: fraction of samples strictly below cur, as 0..100. With
	// the dispersion guard above ruling out constant-width windows, ties are no
	// longer a concern — a genuine coil is at or near the window minimum and
	// lands in the low single/double digits, which is what we want. (The ADX
	// gate in Classify separately rejects a rising linear trend whose width
	// mechanically shrinks, so we do not need to compensate for that here.)
	below := 0
	for _, s := range samples {
		if s < cur {
			below++
		}
	}
	return float64(below) / float64(len(samples)) * 100
}

// adxLike is an ADX-*like* directional-strength proxy: Wilder-smoothed +DM/-DM
// over true range across an n-bar window, combined into DX and then smoothed
// into an ADX-style value on 0..100. It ranks trendiness the way Wilder's ADX
// does (higher = stronger directional move, regardless of sign) but is a proxy,
// not a tick-for-tick reproduction of a charting package. Uses only the bars
// supplied (the caller passes bars[..i]); no lookahead. Returns 0 when there are
// too few bars to smooth.
func adxLike(bars []marketdata.Bar, n int) float64 {
	m := len(bars)
	if m < n+1 {
		return 0
	}

	// Per-bar directional movement and true range (from index 1 on).
	plusDM := make([]float64, m)
	minusDM := make([]float64, m)
	tr := make([]float64, m)
	for i := 1; i < m; i++ {
		up := bars[i].High - bars[i-1].High
		down := bars[i-1].Low - bars[i].Low
		pdm := 0.0
		mdm := 0.0
		if up > down && up > 0 {
			pdm = up
		}
		if down > up && down > 0 {
			mdm = down
		}
		plusDM[i] = pdm
		minusDM[i] = mdm

		hl := bars[i].High - bars[i].Low
		hc := math.Abs(bars[i].High - bars[i-1].Close)
		lc := math.Abs(bars[i].Low - bars[i-1].Close)
		tr[i] = math.Max(hl, math.Max(hc, lc))
	}

	// Wilder smoothing: seed with the sum of the first n values (indices 1..n),
	// then smooth forward. Produce a DX series, then average DX (Wilder) into ADX.
	// Seed sums.
	var trS, pdmS, mdmS float64
	for i := 1; i <= n; i++ {
		trS += tr[i]
		pdmS += plusDM[i]
		mdmS += minusDM[i]
	}

	dxs := make([]float64, 0, m)
	// DX at the seed point.
	dxs = append(dxs, dxFrom(trS, pdmS, mdmS))
	// Smooth forward from n+1.
	for i := n + 1; i < m; i++ {
		trS = trS - trS/float64(n) + tr[i]
		pdmS = pdmS - pdmS/float64(n) + plusDM[i]
		mdmS = mdmS - mdmS/float64(n) + minusDM[i]
		dxs = append(dxs, dxFrom(trS, pdmS, mdmS))
	}

	// ADX = Wilder average of DX. Seed with the mean of the first n DX values
	// when available, else the mean of what we have; then smooth forward.
	if len(dxs) == 0 {
		return 0
	}
	seed := n
	if seed > len(dxs) {
		seed = len(dxs)
	}
	sum := 0.0
	for i := 0; i < seed; i++ {
		sum += dxs[i]
	}
	adx := sum / float64(seed)
	for i := seed; i < len(dxs); i++ {
		adx = (adx*float64(n-1) + dxs[i]) / float64(n)
	}
	return adx
}

// dxFrom computes Wilder's DX from smoothed TR/+DM/-DM sums. Returns 0 when TR
// is non-positive (flat/degenerate window).
func dxFrom(trS, pdmS, mdmS float64) float64 {
	if trS <= 0 {
		return 0
	}
	plusDI := 100 * pdmS / trS
	minusDI := 100 * mdmS / trS
	den := plusDI + minusDI
	if den <= 0 {
		return 0
	}
	return 100 * math.Abs(plusDI-minusDI) / den
}

// clamp01 constrains x to [0,1].
func clamp01(x float64) float64 {
	if x < 0 {
		return 0
	}
	if x > 1 {
		return 1
	}
	return x
}

// absf is the float absolute value (avoids importing math at call sites in
// regime.go).
func absf(x float64) float64 {
	if x < 0 {
		return -x
	}
	return x
}

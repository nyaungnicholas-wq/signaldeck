// Package breakout detects trend-CREATION events on OHLCV bars: Donchian
// channel breakouts, volume spikes, and Bollinger squeeze releases, plus
// correlation breaks between symbol pairs (see correlation.go).
//
// Every detector is pure and honest about lookahead: a value computed for the
// latest bar uses ONLY bars[..last]. Donchian channels are built from the
// PRIOR N bars (exclusive of the current bar), so a "close above the range"
// compares today's close to a range that was fully known before today. No
// output peeks at a future bar. Detect reports what just happened on the most
// recent bar; grading whether these events actually preceded trends is left to
// the caller's out-of-sample expectancy machinery (each Signal carries the
// numbers needed to reconstruct the trigger).
//
// Insufficient input yields an empty slice rather than a fabricated signal.
package breakout

import (
	"fmt"
	"math"
	"sort"

	"github.com/nyaungnicholas-wq/signaldeck/internal/marketdata"
)

// Detector parameters. Exported so callers can cite the exact regime a Signal
// was produced under and so grading uses the same constants.
const (
	// DonchianN is the Donchian channel lookback in bars, EXCLUSIVE of the
	// current bar: an up-break needs close > max high of the prior 20 bars.
	DonchianN = 20

	// VolumeSMAN is the window for the average volume a spike is measured
	// against.
	VolumeSMAN = 20

	// VolumeSpikeMult is the multiple of SMA20(volume) the latest volume must
	// exceed to count as a spike.
	VolumeSpikeMult = 2.5

	// BollingerN is the Bollinger Band window (SMA + stdev of closes).
	BollingerN = 20

	// BollingerK is the standard-deviation multiplier for the bands, so band
	// width is 2*K*stdev.
	BollingerK = 2.0

	// SqueezeLookback is how many recent width values define "the squeeze
	// distribution" for the release test.
	SqueezeLookback = 90

	// SqueezePct is the percentile threshold: width was "in a squeeze" while it
	// sat in the bottom 20% of the SqueezeLookback distribution.
	SqueezePct = 0.20

	// SqueezeAvgN is the window whose average width the current width must
	// exceed for the squeeze to count as RELEASED (expanding).
	SqueezeAvgN = 20
)

// Signal is one detected trend-creation event on the latest bar.
//
// Kind is one of "donchian_up", "donchian_down", "volume_spike", or
// "squeeze_release". Ts is the latest bar's open time (unix seconds). Detail
// is a human-readable string citing the numbers that fired the trigger.
// Strength is a unitless, monotonically-meaningful magnitude for the event
// (see each detector for its definition); larger means a stronger trigger.
type Signal struct {
	Kind     string
	Ts       int64
	Detail   string
	Strength float64
}

// Detect scans the LATEST bar of an ASCENDING-by-Ts bar slice and returns every
// trend-creation event that fired on it. It never inspects bars after the last
// one (there are none) and every channel/average is built from bars strictly
// before the bar being tested, so the result is free of lookahead.
//
// Detected kinds:
//   - "donchian_up":   close > max high of the prior DonchianN bars (exclusive).
//   - "donchian_down": close < min low  of the prior DonchianN bars (exclusive).
//   - "volume_spike":  volume > VolumeSpikeMult * SMA(volume, VolumeSMAN) over
//     the prior VolumeSMAN bars (exclusive of the latest).
//   - "squeeze_release": Bollinger band width was in the bottom SqueezePct of
//     the last SqueezeLookback widths AND the current width just expanded above
//     the average of the prior SqueezeAvgN widths.
//
// The returned slice is empty (never nil-guarded away — always a valid empty
// slice) when nothing fires or input is too short for a given test.
func Detect(bars []marketdata.Bar) []Signal {
	out := []Signal{}
	if len(bars) == 0 {
		return out
	}

	if s, ok := detectDonchian(bars); ok {
		out = append(out, s)
	}
	if s, ok := detectVolumeSpike(bars); ok {
		out = append(out, s)
	}
	if s, ok := detectSqueezeRelease(bars); ok {
		out = append(out, s)
	}
	return out
}

// detectDonchian tests the latest close against the Donchian channel built from
// the DonchianN bars immediately before it (exclusive). Strength is the
// fractional distance of the close beyond the broken edge, relative to the
// channel height (|close-edge| / channelHeight); 0 when the channel is flat.
func detectDonchian(bars []marketdata.Bar) (Signal, bool) {
	n := len(bars)
	// Need DonchianN prior bars plus the current bar.
	if n < DonchianN+1 {
		return Signal{}, false
	}
	prior := bars[n-1-DonchianN : n-1] // exactly DonchianN bars, exclusive of last
	last := bars[n-1]

	maxHigh := prior[0].High
	minLow := prior[0].Low
	for _, b := range prior {
		if b.High > maxHigh {
			maxHigh = b.High
		}
		if b.Low < minLow {
			minLow = b.Low
		}
	}
	height := maxHigh - minLow

	switch {
	case last.Close > maxHigh:
		strength := 0.0
		if height > 0 {
			strength = (last.Close - maxHigh) / height
		}
		return Signal{
			Kind: "donchian_up",
			Ts:   last.Ts,
			Detail: fmt.Sprintf("close %.4f > %d-bar high %.4f (channel %.4f..%.4f)",
				last.Close, DonchianN, maxHigh, minLow, maxHigh),
			Strength: strength,
		}, true
	case last.Close < minLow:
		strength := 0.0
		if height > 0 {
			strength = (minLow - last.Close) / height
		}
		return Signal{
			Kind: "donchian_down",
			Ts:   last.Ts,
			Detail: fmt.Sprintf("close %.4f < %d-bar low %.4f (channel %.4f..%.4f)",
				last.Close, DonchianN, minLow, minLow, maxHigh),
			Strength: strength,
		}, true
	default:
		return Signal{}, false
	}
}

// detectVolumeSpike compares the latest volume to VolumeSpikeMult times the
// mean volume of the prior VolumeSMAN bars (exclusive of the latest). Strength
// is the ratio latest/mean (so it equals the multiple of average volume).
func detectVolumeSpike(bars []marketdata.Bar) (Signal, bool) {
	n := len(bars)
	if n < VolumeSMAN+1 {
		return Signal{}, false
	}
	prior := bars[n-1-VolumeSMAN : n-1]
	last := bars[n-1]

	sum := 0.0
	for _, b := range prior {
		sum += b.Volume
	}
	mean := sum / float64(VolumeSMAN)
	if mean <= 0 {
		return Signal{}, false
	}
	if last.Volume > VolumeSpikeMult*mean {
		return Signal{
			Kind: "volume_spike",
			Ts:   last.Ts,
			Detail: fmt.Sprintf("volume %.2f > %.1fx SMA%d(vol) %.2f (%.2fx avg)",
				last.Volume, VolumeSpikeMult, VolumeSMAN, mean, last.Volume/mean),
			Strength: last.Volume / mean,
		}, true
	}
	return Signal{}, false
}

// bollingerWidthSeries returns, for each index i where a full BollingerN window
// ending at i exists, the band width 2*K*stdev(closes[i-N+1..i]). The returned
// slice is aligned so element j corresponds to bar index (BollingerN-1+j); it is
// nil when there are fewer than BollingerN bars. Population stdev is used.
func bollingerWidthSeries(bars []marketdata.Bar) []float64 {
	n := len(bars)
	if n < BollingerN {
		return nil
	}
	widths := make([]float64, 0, n-BollingerN+1)
	for end := BollingerN - 1; end < n; end++ {
		start := end - BollingerN + 1
		var sum float64
		for i := start; i <= end; i++ {
			sum += bars[i].Close
		}
		mean := sum / float64(BollingerN)
		var sq float64
		for i := start; i <= end; i++ {
			d := bars[i].Close - mean
			sq += d * d
		}
		stdev := math.Sqrt(sq / float64(BollingerN))
		widths = append(widths, 2*BollingerK*stdev)
	}
	return widths
}

// detectSqueezeRelease fires when the most recent Bollinger width was
// compressed (in the bottom SqueezePct of the last SqueezeLookback widths) and
// has just expanded above the average of the prior SqueezeAvgN widths. The
// squeeze test looks at the widths ENDING with the second-to-last width (i.e.
// the state going into the current bar) so the release is a genuine transition;
// all widths use only closes at or before their own bar. Strength is
// current/avgPrior (the expansion ratio).
func detectSqueezeRelease(bars []marketdata.Bar) (Signal, bool) {
	widths := bollingerWidthSeries(bars)
	// Need the current width, SqueezeAvgN prior widths for the release average,
	// and a SqueezeLookback window (ending before the current width) for the
	// squeeze percentile.
	need := SqueezeLookback + 1
	if SqueezeAvgN+1 > need {
		need = SqueezeAvgN + 1
	}
	if len(widths) < need {
		return Signal{}, false
	}
	m := len(widths)
	cur := widths[m-1]

	// Release: current width above the average of the SqueezeAvgN widths just
	// before it.
	priorAvgWindow := widths[m-1-SqueezeAvgN : m-1]
	var sum float64
	for _, w := range priorAvgWindow {
		sum += w
	}
	avgPrior := sum / float64(SqueezeAvgN)
	if avgPrior <= 0 || cur <= avgPrior {
		return Signal{}, false
	}

	// Squeeze: the width GOING INTO this bar (widths[m-2]) sat in the bottom
	// SqueezePct of the SqueezeLookback widths ending at m-2.
	priorWidth := widths[m-2]
	window := widths[m-1-SqueezeLookback : m-1] // SqueezeLookback widths, ending at m-2
	sorted := make([]float64, len(window))
	copy(sorted, window)
	sort.Float64s(sorted)
	// threshold = value at the SqueezePct percentile (nearest-rank).
	idx := int(math.Ceil(SqueezePct*float64(len(sorted)))) - 1
	if idx < 0 {
		idx = 0
	}
	if idx >= len(sorted) {
		idx = len(sorted) - 1
	}
	threshold := sorted[idx]
	if priorWidth > threshold {
		return Signal{}, false
	}

	last := bars[len(bars)-1]
	return Signal{
		Kind: "squeeze_release",
		Ts:   last.Ts,
		Detail: fmt.Sprintf("BB width %.4f expanded > %d-bar avg %.4f after squeeze (prior width %.4f <= p%.0f %.4f of last %d)",
			cur, SqueezeAvgN, avgPrior, priorWidth, SqueezePct*100, threshold, SqueezeLookback),
		Strength: cur / avgPrior,
	}, true
}

// Feature-drift detection (2026-07-25).
//
// THE HOLE THIS CLOSES
// --------------------
// modelhealth.Inputs has carried a FeatureDriftPct field since it was built,
// and the stability component reads it — but nothing ever computed it, so it
// was always zero and stability always scored a perfect 1.0. One fifth of the
// health score was inert: a model whose inputs had drifted completely away
// from what it learned on would still show full marks on the axis meant to
// catch exactly that.
//
// WHY TWO-SAMPLE KS AND NOT A MEAN COMPARISON
// -------------------------------------------
// Comparing means misses the failure that matters. A feature can hold its mean
// while its variance collapses or its distribution goes bimodal — the model
// then sees inputs that look familiar in aggregate and are unrecognisable in
// detail. The Kolmogorov-Smirnov statistic compares the whole empirical
// distribution, so it catches shape changes a moment-based test sleeps through.
//
// The threshold is a critical value, not a fixed number: what counts as
// "materially different" depends on how many observations back each sample.
// Using a constant would flag noise on small samples and miss real drift on
// large ones.
package modelhealth

import (
	"math"
	"sort"
)

// DriftResult is one feature's verdict.
type DriftResult struct {
	Feature   string  `json:"feature"`
	KS        float64 `json:"ks"`        // 0 = identical distributions, 1 = disjoint
	Critical  float64 `json:"critical"`  // the n-adjusted threshold it was judged against
	Drifted   bool    `json:"drifted"`
	RefN      int     `json:"refN"`
	LiveN     int     `json:"liveN"`
}

// MinDriftSample is the per-sample floor below which no drift verdict is
// claimed. Two handfuls of points can look arbitrarily different by chance.
const MinDriftSample = 50

// ksAlpha is the significance level for the critical value (95%).
const ksAlpha = 1.36 // the standard c(0.05) coefficient

// KS computes the two-sample Kolmogorov-Smirnov statistic: the largest gap
// between two empirical CDFs. Inputs need not be sorted.
func KS(a, b []float64) float64 {
	if len(a) == 0 || len(b) == 0 {
		return 0
	}
	x := append([]float64(nil), a...)
	y := append([]float64(nil), b...)
	sort.Float64s(x)
	sort.Float64s(y)

	i, j := 0, 0
	var maxGap float64
	for i < len(x) && j < len(y) {
		// Step to the next distinct value across BOTH samples and advance each
		// past every occurrence of it before measuring. Advancing only one side
		// through a tie compares the CDFs mid-step and invents a gap that is an
		// artefact of the sweep — identical samples would report drift.
		v := math.Min(x[i], y[j])
		for i < len(x) && x[i] == v {
			i++
		}
		for j < len(y) && y[j] == v {
			j++
		}
		gap := math.Abs(float64(i)/float64(len(x)) - float64(j)/float64(len(y)))
		if gap > maxGap {
			maxGap = gap
		}
	}
	return maxGap
}

// ksCritical is the 95% critical value for a two-sample KS test. Scaling with
// sample size is the point: a fixed cutoff would cry drift on thin samples and
// stay silent on fat ones.
func ksCritical(n1, n2 int) float64 {
	if n1 <= 0 || n2 <= 0 {
		return 1
	}
	return ksAlpha * math.Sqrt(float64(n1+n2)/float64(n1*n2))
}

// DriftFor compares one feature's live distribution against its reference.
func DriftFor(name string, reference, live []float64) DriftResult {
	r := DriftResult{Feature: name, RefN: len(reference), LiveN: len(live)}
	if len(reference) < MinDriftSample || len(live) < MinDriftSample {
		// Not enough evidence to claim drift OR to clear it. Reported as
		// undrifted so a thin sample cannot retire a model by itself — the
		// observation floor in Grade is what guards the other direction.
		r.Critical = 1
		return r
	}
	r.KS = KS(reference, live)
	r.Critical = ksCritical(len(reference), len(live))
	r.Drifted = r.KS > r.Critical
	return r
}

// DriftFraction is the value Inputs.FeatureDriftPct expects: the share of
// features whose distribution has moved materially. Features with too little
// data are excluded from the denominator rather than counted as clean, so a
// mostly-empty feature store cannot dilute a real drift signal into nothing.
func DriftFraction(results []DriftResult) float64 {
	judged, drifted := 0, 0
	for _, r := range results {
		if r.RefN < MinDriftSample || r.LiveN < MinDriftSample {
			continue
		}
		judged++
		if r.Drifted {
			drifted++
		}
	}
	if judged == 0 {
		return 0
	}
	return float64(drifted) / float64(judged)
}

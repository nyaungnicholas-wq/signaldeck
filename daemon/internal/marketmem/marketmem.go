// Package marketmem is a historical-analog engine: given today's market state
// as a feature vector and a history of past days (each past day's feature
// vector plus the forward return that actually followed it), it answers the
// question "which past days looked most like today, and what happened next?".
//
// It is pure — feature vectors in, matched days out — and deterministic: no
// clocks and no randomness, so identical inputs always yield identical analogs.
//
// Honesty is structural. Similarity is only meaningful once the feature
// dimensions share a scale, so each dimension is z-scored across history before
// any distance is taken; a dimension that never varies carries no information
// and is dropped rather than allowed to inject NaNs. When the history is too
// thin to trust (fewer usable days than the caller's floor), when nothing
// matches the query's shape, when every feature is constant, or when every
// candidate falls inside the exclude window, the engine returns Gated with a
// plain-English Note instead of fabricating a confident answer out of noise.
package marketmem

import (
	"math"
	"sort"
	"strconv"
)

// zeroVarEps is the standard-deviation floor below which a feature dimension is
// treated as constant and skipped: z-scoring it would divide by ~0 and poison
// every distance with NaN/Inf.
const zeroVarEps = 1e-12

// Snapshot is one past day: its feature vector and the realized forward return
// that followed (e.g. the market's next-20-trading-day return).
type Snapshot struct {
	Ts        int64     // unix seconds of the day
	Features  []float64 // must be same length/order as the query vector
	FwdReturn float64   // realized forward return in percent
}

// Analog is one matched historical day.
type Analog struct {
	Ts        int64
	Distance  float64 // normalized euclidean distance (smaller = more similar)
	FwdReturn float64
}

// Result is the analog lookup outcome. When Gated is true the statistical
// fields are left zero and Note explains why no analogs were produced.
type Result struct {
	Analogs   []Analog // K nearest, closest first
	MeanFwd   float64  // mean FwdReturn across the analogs
	MedianFwd float64
	HitRate   float64 // share of analogs with FwdReturn > 0
	N         int     // usable (length-matched) history size considered
	Gated     bool
	Note      string
}

// Find returns the k historical snapshots most similar to current.
//
// Each feature dimension is z-score-normalized (subtract the per-dimension mean,
// divide by the per-dimension standard deviation) across the usable history —
// every snapshot whose Features length matches current and whose values are all
// finite — and current is z-scored against those same statistics. Distance is
// the euclidean distance between the z-scored query and each z-scored snapshot;
// a dimension whose standard deviation is ~0 is skipped so it can neither
// dominate the metric nor produce NaNs. Normalization statistics are taken over
// ALL usable snapshots (including any later removed by the exclude window) so
// the feature scale stays stable.
//
// excludeTs and excludeWindowSecs drop any snapshot whose Ts is within
// excludeWindowSecs of excludeTs from the ranking — pass the current day's Ts to
// keep the lookup from trivially matching itself or its immediate neighbours. A
// negative window disables the exclusion.
//
// The result is gated (Gated true, statistical fields zero, Note set) when:
// current is empty or non-finite; k <= 0; fewer than minHistory usable snapshots
// exist (minHistory ~60 is the recommended floor); no snapshot matches the
// query's feature length; every feature dimension is constant; or every usable
// snapshot falls inside the exclude window. Otherwise Gated is false, Analogs
// holds the min(k, candidate) nearest days (closest first, ties broken by
// ascending Ts), and MeanFwd/MedianFwd/HitRate summarize their realized forward
// returns.
func Find(current []float64, history []Snapshot, k int, minHistory int, excludeTs, excludeWindowSecs int64) Result {
	d := len(current)
	switch {
	case d == 0:
		return gated(0, "empty query vector: nothing to compare against")
	case !allFinite(current):
		return gated(0, "query vector contains non-finite values")
	case k <= 0:
		return gated(0, "k must be positive to return analogs")
	}

	// usable = snapshots that share the query's shape and are all finite.
	usable := make([]Snapshot, 0, len(history))
	for _, s := range history {
		if len(s.Features) == d && allFinite(s.Features) {
			usable = append(usable, s)
		}
	}
	n := len(usable)
	if n == 0 {
		return gated(0, "no history snapshot matches the query feature length ("+strconv.Itoa(d)+" dims)")
	}
	if n < minHistory {
		return gated(n, "thin history: "+strconv.Itoa(n)+" usable snapshot(s), need >= "+strconv.Itoa(minHistory))
	}

	mean, std := featureStats(usable, d)
	active := 0
	for j := 0; j < d; j++ {
		if std[j] > zeroVarEps {
			active++
		}
	}
	if active == 0 {
		return gated(n, "every feature dimension is constant across history; cannot rank analogs")
	}

	// Score every usable snapshot except those inside the exclude window.
	candidates := make([]Analog, 0, n)
	for _, s := range usable {
		if withinWindow(s.Ts, excludeTs, excludeWindowSecs) {
			continue
		}
		candidates = append(candidates, Analog{
			Ts:        s.Ts,
			Distance:  distance(current, s.Features, mean, std),
			FwdReturn: s.FwdReturn,
		})
	}
	if len(candidates) == 0 {
		return gated(n, "all "+strconv.Itoa(n)+" usable snapshot(s) fall inside the exclude window")
	}

	// Closest first; ties broken by ascending Ts for a deterministic order.
	sort.SliceStable(candidates, func(a, b int) bool {
		if candidates[a].Distance != candidates[b].Distance {
			return candidates[a].Distance < candidates[b].Distance
		}
		return candidates[a].Ts < candidates[b].Ts
	})

	pool := len(candidates)
	if k > pool {
		k = pool // clamp: cannot return more analogs than candidates exist
	}
	analogs := candidates[:k]
	mFwd, medFwd, hit := aggregate(analogs)

	return Result{
		Analogs:   analogs,
		MeanFwd:   mFwd,
		MedianFwd: medFwd,
		HitRate:   hit,
		N:         n,
		Gated:     false,
		Note: strconv.Itoa(k) + " nearest of " + strconv.Itoa(pool) +
			" candidate day(s); " + strconv.Itoa(active) + " of " +
			strconv.Itoa(d) + " feature dim(s) had variance",
	}
}

// featureStats returns the per-dimension mean and population standard deviation
// of the usable snapshots' feature vectors. Every snapshot is assumed to have
// exactly d finite features (Find guarantees this before calling).
func featureStats(usable []Snapshot, d int) (mean, std []float64) {
	mean = make([]float64, d)
	std = make([]float64, d)
	n := float64(len(usable))
	for _, s := range usable {
		for j := 0; j < d; j++ {
			mean[j] += s.Features[j]
		}
	}
	for j := 0; j < d; j++ {
		mean[j] /= n
	}
	for _, s := range usable {
		for j := 0; j < d; j++ {
			diff := s.Features[j] - mean[j]
			std[j] += diff * diff
		}
	}
	for j := 0; j < d; j++ {
		std[j] = math.Sqrt(std[j] / n)
	}
	return mean, std
}

// distance is the euclidean distance between cur and feat in z-scored space,
// using the supplied per-dimension mean and std. Dimensions whose std is ~0 are
// skipped (the shared mean cancels in the difference, but z-scoring both sides
// keeps the intent explicit).
func distance(cur, feat, mean, std []float64) float64 {
	var sum float64
	for j := range cur {
		if std[j] <= zeroVarEps {
			continue
		}
		zc := (cur[j] - mean[j]) / std[j]
		zf := (feat[j] - mean[j]) / std[j]
		diff := zc - zf
		sum += diff * diff
	}
	return math.Sqrt(sum)
}

// aggregate returns the mean, median and hit rate (fraction strictly > 0) of the
// analogs' forward returns. analogs must be non-empty.
func aggregate(analogs []Analog) (mean, median, hitRate float64) {
	n := len(analogs)
	fwds := make([]float64, n)
	var sum float64
	hits := 0
	for i, a := range analogs {
		fwds[i] = a.FwdReturn
		sum += a.FwdReturn
		if a.FwdReturn > 0 {
			hits++
		}
	}
	mean = sum / float64(n)
	sort.Float64s(fwds)
	mid := n / 2
	if n%2 == 1 {
		median = fwds[mid]
	} else {
		median = (fwds[mid-1] + fwds[mid]) / 2
	}
	hitRate = float64(hits) / float64(n)
	return mean, median, hitRate
}

// withinWindow reports whether ts lies within halfWidth seconds of center. A
// negative halfWidth disables the check (nothing is ever within it).
func withinWindow(ts, center, halfWidth int64) bool {
	if halfWidth < 0 {
		return false
	}
	return absI64(ts-center) <= halfWidth
}

// allFinite reports whether every value in xs is a finite number.
func allFinite(xs []float64) bool {
	for _, x := range xs {
		if math.IsNaN(x) || math.IsInf(x, 0) {
			return false
		}
	}
	return true
}

// absI64 is the absolute value of an int64.
func absI64(x int64) int64 {
	if x < 0 {
		return -x
	}
	return x
}

// gated builds a gated Result carrying the usable history size and a Note.
func gated(n int, note string) Result {
	return Result{N: n, Gated: true, Note: note}
}

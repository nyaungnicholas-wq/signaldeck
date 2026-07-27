package featurehealth

import (
	"math"
	"sort"
)

// ── DERIVING THE SCORECARD FROM LABELED ROWS ────────────────────────────────
//
// Grade takes measurements; this file produces them. It lives in the package
// rather than in the worker for the usual reason: the arithmetic that decides
// whether a feature is decaying is worth unit-testing without a database, and a
// worker is the worst place to hide a correlation.
//
// THE TIME SPLIT IS THE POINT. Decay and stability are both questions about
// WHEN, so every split here is chronological: the recent window is the newest
// rows, and the stability blocks are contiguous spans. A random split would
// compare a feature against itself and report every feature as perfectly stable,
// which is the same mistake internal/pipeline's feature-drift check calls out for
// model distributions.

// Sample is one labeled feature vector with the timestamp it was recorded at.
// Vec is the absent-when-unavailable map exactly as the feature store holds it —
// a missing key means "not computable for this row", never zero.
type Sample struct {
	Ts  int64
	Vec map[string]float64
	Fwd float64
}

// SampleConfig tunes the derivation.
type SampleConfig struct {
	// Allow restricts analysis to these keys. Empty means every key seen, which
	// is almost never what a caller wants: the feature store also holds the
	// blend's own outputs, and clustering a model's prediction as if it were an
	// input is how a system starts grading itself.
	Allow []string
	// RecentFrac is the newest share of rows treated as the recent window.
	RecentFrac float64
	// Blocks is how many contiguous spans the stability check uses.
	Blocks int
	// Redundant marks keys another analysis already clustered with a kept
	// representative (internal/featureredundancy's Redundant set).
	Redundant map[string]bool
	// Drift is measured per-feature distribution movement, when available. A key
	// absent from the map is treated as UNMEASURED, not as zero drift.
	Drift map[string]float64
}

// DefaultSampleConfig returns the standard split.
//
// RecentFrac 0.25 — a quarter of the record is long enough to hold a usable
// sample and short enough that a feature which died recently shows up as dead
// rather than being averaged back to life by three good years.
//
// Blocks 5 — the smallest number of spans where a sign-agreement score can
// distinguish "consistent" from "coin flip" with any resolution. Fewer than 3
// usable blocks and stabilityScore refuses to judge at all.
func DefaultSampleConfig() SampleConfig {
	return SampleConfig{RecentFrac: 0.25, Blocks: 5}
}

// FromSamples derives one Inputs per allowed feature key.
//
// Rows may arrive in any order; they are copied and sorted ascending by ts, so a
// caller passing a newest-first query result (as the store's readers return) gets
// the chronology right rather than an inverted recent window.
func FromSamples(samples []Sample, cfg SampleConfig) []Inputs {
	if cfg.RecentFrac <= 0 || cfg.RecentFrac > 1 {
		cfg.RecentFrac = DefaultSampleConfig().RecentFrac
	}
	if cfg.Blocks < 3 {
		cfg.Blocks = DefaultSampleConfig().Blocks
	}

	ordered := make([]Sample, len(samples))
	copy(ordered, samples)
	sort.SliceStable(ordered, func(i, j int) bool { return ordered[i].Ts < ordered[j].Ts })

	keys := cfg.Allow
	if len(keys) == 0 {
		seen := map[string]struct{}{}
		for _, s := range ordered {
			for k := range s.Vec {
				seen[k] = struct{}{}
			}
		}
		keys = make([]string, 0, len(seen))
		for k := range seen {
			keys = append(keys, k)
		}
		sort.Strings(keys)
	}

	total := len(ordered)
	recentStart := total - int(math.Round(cfg.RecentFrac*float64(total)))
	if recentStart < 0 {
		recentStart = 0
	}

	out := make([]Inputs, 0, len(keys))
	for _, k := range keys {
		in := Inputs{Name: k, Redundant: cfg.Redundant[k]}

		xs, ys := pairs(ordered, k, 0, total)
		in.N = len(xs)
		in.FullIC = pearson(xs, ys)
		if total > 0 {
			in.Coverage = float64(len(xs)) / float64(total)
		}

		if rxs, rys := pairs(ordered, k, recentStart, total); len(rxs) >= 2 {
			in.RecentIC = pearson(rxs, rys)
			in.RecentN = len(rxs)
			in.RecentKnown = true
		}

		in.BlockICs = blockICs(ordered, k, cfg.Blocks)

		if d, ok := cfg.Drift[k]; ok {
			in.DriftPct = d
			in.DriftKnown = true
		}
		out = append(out, in)
	}
	return out
}

// pairs collects the (feature, forward-return) pairs over ordered[lo:hi] for the
// rows where the key is PRESENT and both values are finite. Absent keys are
// skipped, never imputed: a zero would be a fabricated observation and would pull
// every correlation here toward zero.
func pairs(ordered []Sample, key string, lo, hi int) (xs, ys []float64) {
	if lo < 0 {
		lo = 0
	}
	if hi > len(ordered) {
		hi = len(ordered)
	}
	for i := lo; i < hi; i++ {
		v, ok := ordered[i].Vec[key]
		if !ok {
			continue
		}
		f := ordered[i].Fwd
		if math.IsNaN(v) || math.IsInf(v, 0) || math.IsNaN(f) || math.IsInf(f, 0) {
			continue
		}
		xs = append(xs, v)
		ys = append(ys, f)
	}
	return xs, ys
}

// blockICs splits the record into contiguous chronological spans and returns the
// IC of each span that holds enough rows to compute one.
//
// A block with too few present rows is OMITTED rather than reported as an IC of
// zero. A zero would read as "no relationship in that period", which is a
// finding; "we could not look" is not.
func blockICs(ordered []Sample, key string, blocks int) []float64 {
	n := len(ordered)
	if n == 0 || blocks <= 0 {
		return nil
	}
	const minBlockPairs = 20
	size := n / blocks
	if size == 0 {
		return nil
	}
	out := make([]float64, 0, blocks)
	for b := 0; b < blocks; b++ {
		lo := b * size
		hi := lo + size
		if b == blocks-1 {
			hi = n // the last block absorbs the remainder
		}
		xs, ys := pairs(ordered, key, lo, hi)
		if len(xs) < minBlockPairs {
			continue
		}
		ic := pearson(xs, ys)
		if math.IsNaN(ic) {
			continue
		}
		out = append(out, ic)
	}
	return out
}

// pearson is the linear correlation of two equal-length series, 0 when either is
// constant (an undefined correlation, reported as no relationship rather than as
// a division by zero).
func pearson(xs, ys []float64) float64 {
	n := len(xs)
	if n < 2 || n != len(ys) {
		return 0
	}
	var sx, sy float64
	for i := 0; i < n; i++ {
		sx += xs[i]
		sy += ys[i]
	}
	mx, my := sx/float64(n), sy/float64(n)
	var num, dx, dy float64
	for i := 0; i < n; i++ {
		a, b := xs[i]-mx, ys[i]-my
		num += a * b
		dx += a * a
		dy += b * b
	}
	if dx <= 0 || dy <= 0 {
		return 0
	}
	r := num / math.Sqrt(dx*dy)
	if math.IsNaN(r) || math.IsInf(r, 0) {
		return 0
	}
	return math.Max(-1, math.Min(1, r))
}

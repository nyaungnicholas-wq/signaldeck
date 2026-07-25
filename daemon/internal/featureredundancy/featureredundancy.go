// Package featureredundancy answers a question the feature store cannot answer
// about itself: how many INDEPENDENT sources of information are actually in the
// feature vector?
//
// # Why this exists
//
// SignalDeck's vector carries pressure components, indicator signals, trend
// geometry, candlestick bias, ranks and z-scores — and a large share of them are
// transformations of the same daily closes. RSI, MACD, momentum and a
// distance-to-SMA are not four opinions; they are four renderings of one price
// path. That matters concretely here, because the adaptive weighter estimates a
// per-component hit rate and then blends the components as if each were an
// independent vote. Feed it ten copies of one signal and it will confidently
// weight that signal ten times.
//
// This package does not delete anything. It MEASURES the correlation structure,
// clusters features that move together, elects one representative per cluster,
// and reports the honest count of independent inputs alongside the raw field
// count. What the caller does with that — prune, down-weight, or merely display
// it — is the caller's decision.
//
// # Method
//
//   - Pearson correlation on PAIRWISE-COMPLETE observations. The feature vector
//     is absent-when-unavailable by design (a missing source is omitted, never
//     zero-filled), so a pair is correlated over the rows where BOTH are
//     present, and a pair with too few shared rows is treated as UNKNOWN, not
//     as uncorrelated. Assuming independence from thin data is exactly the
//     error this package exists to catch.
//   - SINGLE-LINKAGE clustering at |rho| >= Threshold, via union-find. Single
//     linkage is the conservative choice for this purpose: it merges A and C
//     when both track B, which is the right answer when the shared driver is a
//     common price path.
//   - The representative is the cluster member with the highest |IC| against
//     the realized forward return, ties broken by name so the result is
//     deterministic and reproducible.
//
// # What this is NOT
//
// The representative election reads labels, so it is a SELECTION step performed
// on the same data it describes — it is a redundancy report, not an
// out-of-sample validation. A feature surviving as a representative has NOT
// thereby been shown to predict anything; that judgment belongs to the OOS lift
// gate the legs already pass. Treated as anything more, this would become
// another way to overfit, so Report says so in its own payload.
package featureredundancy

import (
	"math"
	"sort"
)

const (
	// DefaultThreshold is the |rho| at which two features are treated as
	// renderings of the same information. 0.9 is deliberately high: the goal is
	// to catch near-duplicates, not to punish features that are merely related.
	DefaultThreshold = 0.9
	// DefaultMinPairObs is the least shared observations a pair needs before
	// its correlation is trusted. Below this the pair is UNKNOWN and never
	// merged — thin data cannot demonstrate either redundancy or independence.
	DefaultMinPairObs = 100
	// DefaultMinFeatureObs is the least observations a feature needs to be
	// analyzed at all. Rarely-present features are reported separately rather
	// than silently clustered on a handful of rows.
	DefaultMinFeatureObs = 100
)

// Sample is one labeled feature vector: the absent-when-unavailable map exactly
// as the feature store holds it, plus the realized forward return used only to
// elect cluster representatives.
type Sample struct {
	Vec map[string]float64
	Fwd float64
}

// Config tunes the analysis. The zero value is invalid; use Defaults.
type Config struct {
	Threshold     float64
	MinPairObs    int
	MinFeatureObs int
	// Only these feature keys are considered. Empty means "every key seen".
	// The caller passes canonicalFeatureKeys so model outputs (pred_raw,
	// gbm_prob, …) can never be analyzed as if they were inputs.
	Allow []string
}

// Defaults returns the standard configuration.
func Defaults() Config {
	return Config{
		Threshold:     DefaultThreshold,
		MinPairObs:    DefaultMinPairObs,
		MinFeatureObs: DefaultMinFeatureObs,
	}
}

// Feature is one analyzed input's summary.
type Feature struct {
	Name string `json:"name"`
	// N is how many samples carried this feature.
	N int `json:"n"`
	// IC is the Pearson correlation with the realized forward return over the
	// rows where the feature is present. Descriptive only.
	IC float64 `json:"ic"`
}

// Cluster is a set of features that move together at or above the threshold.
type Cluster struct {
	// Representative is the member elected to stand for the cluster.
	Representative string `json:"representative"`
	// Members includes the representative, sorted by name.
	Members []string `json:"members"`
	// Redundant is Members minus the representative, sorted by name — the
	// fields that carry no information the representative does not already.
	Redundant []string `json:"redundant"`
	// MaxAbsRho is the strongest pairwise |rho| observed inside the cluster.
	MaxAbsRho float64 `json:"maxAbsRho"`
}

// Report is the analysis result.
type Report struct {
	// Rows analyzed.
	Samples int `json:"samples"`
	// FieldCount is how many distinct features were analyzed.
	FieldCount int `json:"fieldCount"`
	// EffectiveCount is the number of CLUSTERS — the honest count of
	// independent inputs. The gap between this and FieldCount is the amount of
	// double-counting in the vector.
	EffectiveCount int `json:"effectiveCount"`
	// RedundancyRatio is 1 - EffectiveCount/FieldCount in [0,1]. 0 means every
	// field is independent; 0.6 means 60% of the fields are duplicates.
	RedundancyRatio float64 `json:"redundancyRatio"`
	// Clusters of size >= 2, sorted by size then representative name. Singleton
	// features are omitted here and counted in EffectiveCount.
	Clusters []Cluster `json:"clusters"`
	// Representatives is the deduplicated feature set: one name per cluster,
	// sorted. This is the set a caller would keep.
	Representatives []string `json:"representatives"`
	// Redundant is every non-representative name, sorted.
	Redundant []string `json:"redundant"`
	// Skipped lists features excluded for having fewer than MinFeatureObs
	// observations, with their counts.
	Skipped []Feature `json:"skipped"`
	// Features is the per-feature summary for everything analyzed.
	Features []Feature `json:"features"`
	// Meaningful is false when there was not enough data to conclude anything.
	Meaningful bool `json:"meaningful"`
	// Note states the in-sample-selection caveat in the payload itself.
	Note string `json:"note"`
}

const selectionNote = "redundancy report, not a validation: cluster representatives are elected using the same labeled data they describe, so surviving as a representative is NOT evidence a feature predicts anything — that remains the job of the out-of-sample lift gate."

// Analyze measures the correlation structure of a labeled feature set.
// It never mutates its input and is deterministic: identical samples produce an
// identical report, including ordering.
func Analyze(samples []Sample, cfg Config) Report {
	rep := Report{Samples: len(samples), Note: selectionNote}
	if cfg.Threshold <= 0 {
		cfg.Threshold = DefaultThreshold
	}
	if cfg.MinPairObs <= 0 {
		cfg.MinPairObs = DefaultMinPairObs
	}
	if cfg.MinFeatureObs <= 0 {
		cfg.MinFeatureObs = DefaultMinFeatureObs
	}
	allow := map[string]bool{}
	for _, k := range cfg.Allow {
		allow[k] = true
	}

	// Collect candidate names deterministically.
	seen := map[string]int{}
	for _, s := range samples {
		for k := range s.Vec {
			if len(allow) > 0 && !allow[k] {
				continue
			}
			seen[k]++
		}
	}
	var names []string
	for k, n := range seen {
		if n >= cfg.MinFeatureObs {
			names = append(names, k)
		} else {
			rep.Skipped = append(rep.Skipped, Feature{Name: k, N: n})
		}
	}
	sort.Strings(names)
	sort.Slice(rep.Skipped, func(i, j int) bool { return rep.Skipped[i].Name < rep.Skipped[j].Name })
	rep.FieldCount = len(names)
	if len(names) == 0 {
		return rep
	}

	// Per-feature IC against the realized forward return (present rows only).
	ic := make(map[string]float64, len(names))
	for _, n := range names {
		var xs, ys []float64
		for _, s := range samples {
			if v, ok := s.Vec[n]; ok {
				xs = append(xs, v)
				ys = append(ys, s.Fwd)
			}
		}
		ic[n] = pearson(xs, ys)
		rep.Features = append(rep.Features, Feature{Name: n, N: seen[n], IC: ic[n]})
	}

	// Pairwise-complete correlations + single-linkage union-find.
	uf := newUnionFind(names)
	// Merged pair correlations, keyed by the ORDERED pair. Cluster maxima are
	// derived from this after clustering settles — deriving them during the
	// union pass would key them by a root that later unions change.
	merged := map[[2]string]float64{}
	for i := 0; i < len(names); i++ {
		for j := i + 1; j < len(names); j++ {
			a, b := names[i], names[j]
			var xs, ys []float64
			for _, s := range samples {
				va, oka := s.Vec[a]
				vb, okb := s.Vec[b]
				if oka && okb {
					xs = append(xs, va)
					ys = append(ys, vb)
				}
			}
			if len(xs) < cfg.MinPairObs {
				continue // UNKNOWN: never merge on evidence this thin.
			}
			r := math.Abs(pearson(xs, ys))
			if r >= cfg.Threshold {
				uf.union(a, b)
				merged[[2]string{a, b}] = r
			}
		}
	}

	// Materialize clusters.
	groups := map[string][]string{}
	for _, n := range names {
		root := uf.find(n)
		groups[root] = append(groups[root], n)
	}
	rep.EffectiveCount = len(groups)
	if rep.FieldCount > 0 {
		rep.RedundancyRatio = 1 - float64(rep.EffectiveCount)/float64(rep.FieldCount)
	}
	for _, members := range groups {
		sort.Strings(members)
		best := members[0]
		for _, m := range members[1:] {
			if am, ab := math.Abs(ic[m]), math.Abs(ic[best]); am > ab || (am == ab && m < best) {
				best = m
			}
		}
		rep.Representatives = append(rep.Representatives, best)
		if len(members) < 2 {
			continue
		}
		var redundant []string
		for _, m := range members {
			if m != best {
				redundant = append(redundant, m)
			}
		}
		var mr float64
		for i := 0; i < len(members); i++ {
			for j := i + 1; j < len(members); j++ {
				if r, ok := merged[[2]string{members[i], members[j]}]; ok && r > mr {
					mr = r
				}
			}
		}
		rep.Clusters = append(rep.Clusters, Cluster{
			Representative: best, Members: members, Redundant: redundant, MaxAbsRho: mr,
		})
		rep.Redundant = append(rep.Redundant, redundant...)
	}
	sort.Strings(rep.Representatives)
	sort.Strings(rep.Redundant)
	sort.Slice(rep.Clusters, func(i, j int) bool {
		if len(rep.Clusters[i].Members) != len(rep.Clusters[j].Members) {
			return len(rep.Clusters[i].Members) > len(rep.Clusters[j].Members)
		}
		return rep.Clusters[i].Representative < rep.Clusters[j].Representative
	})
	rep.Meaningful = rep.Samples >= cfg.MinPairObs && rep.FieldCount >= 2
	return rep
}

// pearson returns the correlation of two equal-length series, 0 when undefined
// (fewer than two points, or either series constant — a constant feature
// carries no information, and reporting 0 says exactly that).
func pearson(xs, ys []float64) float64 {
	n := len(xs)
	if n < 2 || n != len(ys) {
		return 0
	}
	var mx, my float64
	for i := range xs {
		mx += xs[i]
		my += ys[i]
	}
	mx /= float64(n)
	my /= float64(n)
	var num, dx, dy float64
	for i := range xs {
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

// unionFind is the standard disjoint-set with path compression, over names.
type unionFind struct{ parent map[string]string }

func newUnionFind(names []string) *unionFind {
	p := make(map[string]string, len(names))
	for _, n := range names {
		p[n] = n
	}
	return &unionFind{parent: p}
}

func (u *unionFind) find(x string) string {
	for u.parent[x] != x {
		u.parent[x] = u.parent[u.parent[x]]
		x = u.parent[x]
	}
	return x
}

func (u *unionFind) union(a, b string) {
	ra, rb := u.find(a), u.find(b)
	if ra == rb {
		return
	}
	// Deterministic merge direction: the lexicographically smaller root wins,
	// so the parent map does not depend on iteration order.
	if rb < ra {
		ra, rb = rb, ra
	}
	u.parent[rb] = ra
}

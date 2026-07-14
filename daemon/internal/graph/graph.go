// Package graph builds a company knowledge graph — a "ripple" engine — out of
// data SignalDeck already has for free: sector/SIC labels, price-return
// correlation, and 13F institutional co-ownership. It turns those three signals
// into nodes (companies) and typed, weighted edges so a single event on one
// company can be traced outward to the companies most likely to move with it.
//
// Every function is pure: values in, nodes and edges out. No I/O, no
// persistence, no clock, no randomness, no network. Ordering is made
// deterministic by explicit sorting rather than by relying on map iteration.
//
// HONESTY NOTES:
//
//   - Edges are only ever drawn from real, supplied evidence. A "peer" edge
//     needs a shared, non-empty sector; a "correlation" edge needs a defined,
//     non-zero Pearson correlation at or above the caller's threshold; a
//     "coowned" edge needs at least one genuinely shared institutional holder.
//     Zero-variance return series and disjoint holder sets produce NO edge
//     rather than a fabricated weight-zero link.
//
//   - A pair of companies can be connected by more than one KIND of evidence.
//     Those are kept as separate typed edges (peer AND correlation AND coowned)
//     rather than being collapsed into one blended score the data cannot justify.
//
//   - Neighborhood carries a Note. When a company has no edges the Note says so
//     plainly — thin data surfaces as an explicit "nothing found", never as an
//     empty result the caller might mistake for "not computed".
package graph

import (
	"math"
	"sort"
)

// Edge kinds. Kind is kept a plain string on Edge for easy serialization; these
// constants are the canonical values it takes.
const (
	// KindPeer links two companies that share a non-empty sector.
	KindPeer = "peer"
	// KindCorrelation links two companies whose return series co-move.
	KindCorrelation = "correlation"
	// KindCoOwned links two companies held by overlapping 13F managers.
	KindCoOwned = "coowned"
)

// Node is one company in the graph.
type Node struct {
	Symbol string
	Name   string
	Sector string // may be empty
}

// Edge is a typed, weighted relationship between two companies.
//
// A and B are symbols in canonical order (A < B) so an unordered pair has a
// single, stable representation. Kind is one of KindPeer, KindCorrelation or
// KindCoOwned. Weight is normalized per kind: peer = 1; correlation = |corr| in
// [0,1]; coowned = Jaccard overlap of holders in [0,1].
type Edge struct {
	A, B   string
	Kind   string
	Weight float64
}

// Graph is the assembled result: the input nodes plus every typed edge.
type Graph struct {
	Nodes []Node
	Edges []Edge
}

// Neighborhood is the "ripple" around one company: the edges incident to a
// center symbol and the distinct symbols on the other end of those edges, both
// ordered strongest-first. Note explains thin data (e.g. a center with no
// edges) so an empty result is never silently ambiguous.
type Neighborhood struct {
	Center    string
	Edges     []Edge
	Neighbors []string
	Note      string
}

// PeerEdges links every pair of nodes that share a non-empty Sector, with
// weight 1. Nodes with an empty Sector, and self-pairs, are skipped. The result
// is sorted canonically and is safe to concatenate with other edge kinds.
func PeerEdges(nodes []Node) []Edge {
	var edges []Edge
	for i := 0; i < len(nodes); i++ {
		si := nodes[i].Sector
		if si == "" {
			continue
		}
		for j := i + 1; j < len(nodes); j++ {
			if nodes[j].Sector != si {
				continue
			}
			if nodes[i].Symbol == nodes[j].Symbol {
				continue
			}
			a, b := canonical(nodes[i].Symbol, nodes[j].Symbol)
			edges = append(edges, Edge{A: a, B: b, Kind: KindPeer, Weight: 1})
		}
	}
	sortEdges(edges)
	return edges
}

// CorrelationEdges links symbols whose return-series Pearson correlation has
// absolute value at least minAbs. returns[i] aligns to symbols[i] and every
// series is expected to be the same length; mismatched or zero-variance series
// yield no edge. The edge weight is the (clamped) absolute correlation in [0,1].
func CorrelationEdges(symbols []string, returns [][]float64, minAbs float64) []Edge {
	n := len(symbols)
	if len(returns) < n {
		n = len(returns)
	}
	var edges []Edge
	for i := 0; i < n; i++ {
		for j := i + 1; j < n; j++ {
			r, ok := pearson(returns[i], returns[j])
			if !ok {
				continue
			}
			w := math.Abs(r)
			if w > 1 {
				w = 1
			}
			// Require a real, positive co-movement: a flat 0 is no evidence.
			if w == 0 || w < minAbs {
				continue
			}
			a, b := canonical(symbols[i], symbols[j])
			edges = append(edges, Edge{A: a, B: b, Kind: KindCorrelation, Weight: w})
		}
	}
	sortEdges(edges)
	return edges
}

// CoOwnershipEdges links symbols held by overlapping institutional managers.
// holders[sym] is the set of manager CIKs holding sym (duplicate CIKs are
// de-duplicated). The weight is the Jaccard overlap of the two holder sets, and
// an edge is added when that overlap is at least minJaccard AND there is at
// least one genuinely shared holder, so disjoint sets never invent a link.
func CoOwnershipEdges(holders map[string][]string, minJaccard float64) []Edge {
	syms := make([]string, 0, len(holders))
	for s := range holders {
		syms = append(syms, s)
	}
	sort.Strings(syms)

	sets := make(map[string]map[string]struct{}, len(syms))
	for _, s := range syms {
		set := make(map[string]struct{}, len(holders[s]))
		for _, cik := range holders[s] {
			set[cik] = struct{}{}
		}
		sets[s] = set
	}

	var edges []Edge
	for i := 0; i < len(syms); i++ {
		for j := i + 1; j < len(syms); j++ {
			w := jaccard(sets[syms[i]], sets[syms[j]])
			if w == 0 || w < minJaccard {
				continue
			}
			a, b := canonical(syms[i], syms[j])
			edges = append(edges, Edge{A: a, B: b, Kind: KindCoOwned, Weight: w})
		}
	}
	sortEdges(edges)
	return edges
}

// NeighborhoodOf returns the edges incident to symbol and the distinct symbols
// directly connected to it — the ripple around one company — with edges sorted
// by weight descending and neighbors ordered by their strongest connecting
// edge. When the center has no incident edges the returned slices are empty and
// Note explains that no relationships were found.
//
// (Go does not allow a type and a function to share a name, so the constructor
// for the Neighborhood type is spelled NeighborhoodOf.)
func NeighborhoodOf(symbol string, edges []Edge) Neighborhood {
	nb := Neighborhood{Center: symbol}

	// strongest[other] = the max weight of any edge joining symbol to other.
	strongest := make(map[string]float64)
	for _, e := range edges {
		var other string
		switch symbol {
		case e.A:
			other = e.B
		case e.B:
			other = e.A
		default:
			continue
		}
		nb.Edges = append(nb.Edges, e)
		if w, seen := strongest[other]; !seen || e.Weight > w {
			strongest[other] = e.Weight
		}
	}

	sort.SliceStable(nb.Edges, func(i, j int) bool {
		return lessByWeightDesc(nb.Edges[i], nb.Edges[j])
	})

	nb.Neighbors = make([]string, 0, len(strongest))
	for s := range strongest {
		nb.Neighbors = append(nb.Neighbors, s)
	}
	sort.SliceStable(nb.Neighbors, func(i, j int) bool {
		wi, wj := strongest[nb.Neighbors[i]], strongest[nb.Neighbors[j]]
		if wi != wj {
			return wi > wj
		}
		return nb.Neighbors[i] < nb.Neighbors[j]
	})

	if len(nb.Edges) == 0 {
		nb.Note = "no relationships found for " + symbol + "; it has no edges in the current graph (thin data)"
	}
	return nb
}

// Build assembles all three edge kinds over the same universe and returns the
// nodes together with the de-duplicated edge set. minCorr and minJaccard are
// the correlation and co-ownership thresholds. A pair connected by several
// kinds keeps one edge per kind; exact (A, B, Kind) duplicates are collapsed.
func Build(nodes []Node, symbols []string, returns [][]float64, holders map[string][]string, minCorr, minJaccard float64) Graph {
	edges := PeerEdges(nodes)
	edges = append(edges, CorrelationEdges(symbols, returns, minCorr)...)
	edges = append(edges, CoOwnershipEdges(holders, minJaccard)...)
	return Graph{Nodes: nodes, Edges: dedupEdges(edges)}
}

// canonical returns the two symbols in ascending order so an unordered pair has
// one stable (A, B) representation.
func canonical(x, y string) (string, string) {
	if x <= y {
		return x, y
	}
	return y, x
}

// pearson computes the Pearson correlation coefficient of two equal-length
// series. The bool is false when the correlation is undefined — unequal or
// empty lengths, or a zero-variance (flat) series — in which case no edge
// should be drawn.
func pearson(x, y []float64) (float64, bool) {
	n := len(x)
	if n == 0 || n != len(y) {
		return 0, false
	}
	var sx, sy float64
	for i := 0; i < n; i++ {
		sx += x[i]
		sy += y[i]
	}
	mx := sx / float64(n)
	my := sy / float64(n)

	var cov, vx, vy float64
	for i := 0; i < n; i++ {
		dx := x[i] - mx
		dy := y[i] - my
		cov += dx * dy
		vx += dx * dx
		vy += dy * dy
	}
	denom := math.Sqrt(vx * vy)
	if denom == 0 {
		return 0, false
	}
	return cov / denom, true
}

// jaccard is the overlap of two sets: |A ∩ B| / |A ∪ B|. Two empty sets, or any
// pair with an empty union, score 0.
func jaccard(a, b map[string]struct{}) float64 {
	if len(a) == 0 || len(b) == 0 {
		return 0
	}
	inter := 0
	for k := range a {
		if _, ok := b[k]; ok {
			inter++
		}
	}
	union := len(a) + len(b) - inter
	if union == 0 {
		return 0
	}
	return float64(inter) / float64(union)
}

// sortEdges orders edges deterministically by A, then B, then Kind.
func sortEdges(edges []Edge) {
	sort.SliceStable(edges, func(i, j int) bool {
		return lessByKey(edges[i], edges[j])
	})
}

// lessByKey is the canonical (A, B, Kind) ordering.
func lessByKey(x, y Edge) bool {
	if x.A != y.A {
		return x.A < y.A
	}
	if x.B != y.B {
		return x.B < y.B
	}
	return x.Kind < y.Kind
}

// lessByWeightDesc orders by weight descending, breaking ties with lessByKey so
// the result is stable.
func lessByWeightDesc(x, y Edge) bool {
	if x.Weight != y.Weight {
		return x.Weight > y.Weight
	}
	return lessByKey(x, y)
}

// dedupEdges sorts edges canonically and removes exact (A, B, Kind) duplicates,
// keeping distinct kinds for the same pair.
func dedupEdges(edges []Edge) []Edge {
	sortEdges(edges)
	var out []Edge
	for _, e := range edges {
		if n := len(out); n > 0 {
			p := out[n-1]
			if p.A == e.A && p.B == e.B && p.Kind == e.Kind {
				continue
			}
		}
		out = append(out, e)
	}
	return out
}

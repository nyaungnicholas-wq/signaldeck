package pipeline

import (
	"testing"

	"github.com/nyaungnicholas-wq/signaldeck/internal/clusterstat"
	md "github.com/nyaungnicholas-wq/signaldeck/internal/marketdata"
)

// The fleet's day-clustered edge is a FALLBACK: it may only speak when the
// per-symbol grade cannot be taken at all. Below RankEdgeMinEval the per-symbol
// bound is arithmetic, not evidence, and that is the one case the fleet edge
// exists for (expectancy: NEval maxes at ~74 against a floor of 80).
func TestRankGateFleetEdgeOnlyBelowPerSymbolFloor(t *testing.T) {
	edges := map[string]float64{"expectancy|1d": 0.031}
	e, ok := rankGate(nil, edges, "expectancy", md.H1d, 0.55, clusterstat.RankEdgeMinEval-1)
	if !ok || e != 0.031 {
		t.Fatalf("below the floor the fleet edge must decide: got (%v,%v)", e, ok)
	}
	// Enough evaluations: the per-symbol Wilson bound decides, whatever the fleet says.
	want, wantOK := clusterstat.RankEdge(0.55, 500)
	e, ok = rankGate(nil, edges, "expectancy", md.H1d, 0.55, 500)
	if ok != wantOK || e != want {
		t.Fatalf("a measured per-symbol bound was overridden: got (%v,%v) want (%v,%v)", e, ok, want, wantOK)
	}
	// No fleet edge and no per-symbol grade: still ungraded, never invented.
	if _, ok := rankGate(nil, nil, "expectancy", md.H1d, 0.55, 10); ok {
		t.Fatal("an ungraded leg was handed an edge from nothing")
	}
}

// A fleet veto outranks both estimators.
func TestRankGateVetoOutranksFleetEdge(t *testing.T) {
	vetoed := map[string]bool{"pressure|1d": true}
	edges := map[string]float64{"pressure|1d": 0.05}
	e, ok := rankGate(vetoed, edges, "pressure", md.H1d, 0.7, 1000)
	if !ok || e != -1 {
		t.Fatalf("vetoed leg admitted: got (%v,%v)", e, ok)
	}
}

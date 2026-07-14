package graph

import (
	"reflect"
	"testing"
)

const tol = 1e-9

func almostEqual(a, b float64) bool {
	d := a - b
	if d < 0 {
		d = -d
	}
	return d <= tol
}

// (1) three nodes, two share a sector -> exactly one peer edge between those two.
func TestPeerEdges(t *testing.T) {
	tests := []struct {
		name  string
		nodes []Node
		want  []Edge
	}{
		{
			name: "two share sector, third alone",
			nodes: []Node{
				{Symbol: "AAPL", Sector: "Technology"},
				{Symbol: "MSFT", Sector: "Technology"},
				{Symbol: "XOM", Sector: "Energy"},
			},
			want: []Edge{{A: "AAPL", B: "MSFT", Kind: KindPeer, Weight: 1}},
		},
		{
			name: "empty sector never links",
			nodes: []Node{
				{Symbol: "AAA", Sector: ""},
				{Symbol: "BBB", Sector: ""},
				{Symbol: "CCC", Sector: "Finance"},
			},
			want: nil,
		},
		{
			name: "canonical A<B ordering on a reversed pair",
			nodes: []Node{
				{Symbol: "ZZZ", Sector: "Health"},
				{Symbol: "AAA", Sector: "Health"},
			},
			want: []Edge{{A: "AAA", B: "ZZZ", Kind: KindPeer, Weight: 1}},
		},
		{
			name: "three in one sector -> all three pairs",
			nodes: []Node{
				{Symbol: "A", Sector: "S"},
				{Symbol: "B", Sector: "S"},
				{Symbol: "C", Sector: "S"},
			},
			want: []Edge{
				{A: "A", B: "B", Kind: KindPeer, Weight: 1},
				{A: "A", B: "C", Kind: KindPeer, Weight: 1},
				{A: "B", B: "C", Kind: KindPeer, Weight: 1},
			},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := PeerEdges(tt.nodes)
			if !reflect.DeepEqual(got, tt.want) {
				t.Fatalf("PeerEdges = %+v, want %+v", got, tt.want)
			}
		})
	}
}

// (2) perfectly correlated -> weight ~1; anti-correlated -> ~1 (abs);
// uncorrelated -> no edge under minAbs.
func TestCorrelationEdges(t *testing.T) {
	tests := []struct {
		name       string
		symbols    []string
		returns    [][]float64
		minAbs     float64
		wantEdges  int
		wantWeight float64 // checked only when wantEdges == 1
	}{
		{
			name:       "perfect positive",
			symbols:    []string{"X", "Y"},
			returns:    [][]float64{{1, 2, 3, 4}, {2, 4, 6, 8}},
			minAbs:     0.5,
			wantEdges:  1,
			wantWeight: 1,
		},
		{
			name:       "perfect negative -> abs weight ~1",
			symbols:    []string{"X", "Y"},
			returns:    [][]float64{{1, 2, 3, 4}, {4, 3, 2, 1}},
			minAbs:     0.5,
			wantEdges:  1,
			wantWeight: 1,
		},
		{
			name:      "uncorrelated -> no edge",
			symbols:   []string{"X", "Y"},
			returns:   [][]float64{{1, 2, 3, 4}, {1, 2, 2, 1}},
			minAbs:    0.5,
			wantEdges: 0,
		},
		{
			name:      "zero-variance (flat) series -> no edge",
			symbols:   []string{"X", "Y"},
			returns:   [][]float64{{5, 5, 5, 5}, {1, 2, 3, 4}},
			minAbs:    0.0,
			wantEdges: 0,
		},
		{
			name:       "canonical ordering with reversed symbol names",
			symbols:    []string{"ZZZ", "AAA"},
			returns:    [][]float64{{1, 2, 3, 4}, {2, 4, 6, 8}},
			minAbs:     0.5,
			wantEdges:  1,
			wantWeight: 1,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := CorrelationEdges(tt.symbols, tt.returns, tt.minAbs)
			if len(got) != tt.wantEdges {
				t.Fatalf("CorrelationEdges returned %d edges (%+v), want %d", len(got), got, tt.wantEdges)
			}
			if tt.wantEdges == 1 {
				e := got[0]
				if !almostEqual(e.Weight, tt.wantWeight) {
					t.Errorf("weight = %v, want ~%v", e.Weight, tt.wantWeight)
				}
				if e.Kind != KindCorrelation {
					t.Errorf("kind = %q, want %q", e.Kind, KindCorrelation)
				}
				if e.A >= e.B {
					t.Errorf("edge not in canonical order: A=%q B=%q", e.A, e.B)
				}
			}
		})
	}
}

// (3) Jaccard: symbols sharing 2 of 3 holders -> weight ~0.5, linked/not per minJaccard.
func TestCoOwnershipEdges(t *testing.T) {
	// AAA and BBB share {m1, m2}; union {m1, m2, m3, m4} -> Jaccard 2/4 = 0.5.
	holders := map[string][]string{
		"AAA": {"m1", "m2", "m3"},
		"BBB": {"m1", "m2", "m4"},
	}
	tests := []struct {
		name       string
		holders    map[string][]string
		minJaccard float64
		wantEdges  int
		wantWeight float64
	}{
		{
			name:       "2 of 3 shared, threshold met",
			holders:    holders,
			minJaccard: 0.5,
			wantEdges:  1,
			wantWeight: 0.5,
		},
		{
			name:       "2 of 3 shared, threshold too high",
			holders:    holders,
			minJaccard: 0.6,
			wantEdges:  0,
		},
		{
			name: "disjoint holders never link",
			holders: map[string][]string{
				"AAA": {"m1", "m2"},
				"BBB": {"m3", "m4"},
			},
			minJaccard: 0.0,
			wantEdges:  0,
		},
		{
			name: "duplicate CIKs are de-duplicated",
			holders: map[string][]string{
				"AAA": {"m1", "m1", "m2"},
				"BBB": {"m1", "m2", "m2"},
			},
			minJaccard: 0.5,
			wantEdges:  1,
			wantWeight: 1.0, // sets {m1,m2} == {m1,m2}
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := CoOwnershipEdges(tt.holders, tt.minJaccard)
			if len(got) != tt.wantEdges {
				t.Fatalf("CoOwnershipEdges returned %d edges (%+v), want %d", len(got), got, tt.wantEdges)
			}
			if tt.wantEdges == 1 {
				e := got[0]
				if !almostEqual(e.Weight, tt.wantWeight) {
					t.Errorf("weight = %v, want ~%v", e.Weight, tt.wantWeight)
				}
				if e.Kind != KindCoOwned {
					t.Errorf("kind = %q, want %q", e.Kind, KindCoOwned)
				}
				if e.A >= e.B {
					t.Errorf("edge not in canonical order: A=%q B=%q", e.A, e.B)
				}
			}
		})
	}
}

// (4) Neighborhood returns only edges touching the center, neighbors sorted by weight.
func TestNeighborhood(t *testing.T) {
	edges := []Edge{
		{A: "X", B: "Y", Kind: KindCorrelation, Weight: 0.9},
		{A: "X", B: "Z", Kind: KindCoOwned, Weight: 0.5},
		{A: "Y", B: "Z", Kind: KindPeer, Weight: 1.0}, // does not touch X
	}

	nb := NeighborhoodOf("X", edges)

	if nb.Center != "X" {
		t.Fatalf("center = %q, want X", nb.Center)
	}
	// Only the two X-incident edges, strongest first.
	if len(nb.Edges) != 2 {
		t.Fatalf("got %d incident edges (%+v), want 2", len(nb.Edges), nb.Edges)
	}
	for _, e := range nb.Edges {
		if e.A != "X" && e.B != "X" {
			t.Errorf("edge %+v does not touch center X", e)
		}
	}
	if nb.Edges[0].Weight < nb.Edges[1].Weight {
		t.Errorf("edges not sorted by weight desc: %+v", nb.Edges)
	}
	// Neighbors sorted by connecting weight: Y (0.9) before Z (0.5).
	if !reflect.DeepEqual(nb.Neighbors, []string{"Y", "Z"}) {
		t.Errorf("neighbors = %v, want [Y Z]", nb.Neighbors)
	}
	if nb.Note != "" {
		t.Errorf("unexpected note for a connected center: %q", nb.Note)
	}

	// Isolated center -> empty result plus an explanatory Note.
	iso := NeighborhoodOf("Q", edges)
	if len(iso.Edges) != 0 || len(iso.Neighbors) != 0 {
		t.Errorf("isolated center should have no edges/neighbors, got %+v / %v", iso.Edges, iso.Neighbors)
	}
	if iso.Note == "" {
		t.Errorf("isolated center should carry an explanatory Note")
	}
}

// (5) canonical A<B ordering holds across every edge kind produced by Build,
// and multiple kinds for one pair are kept as separate typed edges.
func TestBuildCanonicalAndMultiKind(t *testing.T) {
	nodes := []Node{
		{Symbol: "MSFT", Name: "Microsoft", Sector: "Technology"},
		{Symbol: "AAPL", Name: "Apple", Sector: "Technology"},
	}
	symbols := []string{"MSFT", "AAPL"}
	returns := [][]float64{
		{1, 2, 3, 4}, // MSFT
		{2, 4, 6, 8}, // AAPL, perfectly correlated
	}
	holders := map[string][]string{
		"MSFT": {"c1", "c2", "c3"},
		"AAPL": {"c1", "c2", "c4"},
	}

	g := Build(nodes, symbols, returns, holders, 0.5, 0.5)

	if !reflect.DeepEqual(g.Nodes, nodes) {
		t.Errorf("Build did not pass through nodes")
	}

	// The single pair (AAPL, MSFT) should carry all three kinds.
	kinds := map[string]bool{}
	for _, e := range g.Edges {
		if e.A >= e.B {
			t.Errorf("edge not in canonical order: %+v", e)
		}
		if e.A != "AAPL" || e.B != "MSFT" {
			t.Errorf("unexpected pair in edge %+v", e)
		}
		if kinds[e.Kind] {
			t.Errorf("duplicate edge for kind %q", e.Kind)
		}
		kinds[e.Kind] = true
	}
	for _, want := range []string{KindPeer, KindCorrelation, KindCoOwned} {
		if !kinds[want] {
			t.Errorf("missing %q edge; got kinds %v", want, kinds)
		}
	}
	if len(g.Edges) != 3 {
		t.Errorf("want exactly 3 typed edges, got %d (%+v)", len(g.Edges), g.Edges)
	}
}

// Direct check of the Pearson helper, including the zero-variance guard.
func TestPearson(t *testing.T) {
	tests := []struct {
		name   string
		x, y   []float64
		want   float64
		wantOK bool
	}{
		{"perfect positive", []float64{1, 2, 3}, []float64{10, 20, 30}, 1, true},
		{"perfect negative", []float64{1, 2, 3}, []float64{30, 20, 10}, -1, true},
		{"flat x -> undefined", []float64{5, 5, 5}, []float64{1, 2, 3}, 0, false},
		{"length mismatch -> undefined", []float64{1, 2}, []float64{1, 2, 3}, 0, false},
		{"empty -> undefined", []float64{}, []float64{}, 0, false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, ok := pearson(tt.x, tt.y)
			if ok != tt.wantOK {
				t.Fatalf("ok = %v, want %v", ok, tt.wantOK)
			}
			if ok && !almostEqual(got, tt.want) {
				t.Errorf("pearson = %v, want ~%v", got, tt.want)
			}
		})
	}
}

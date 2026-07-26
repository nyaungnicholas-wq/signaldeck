package researchx

import (
	"math"
	"testing"

	rl "github.com/nyaungnicholas-wq/signaldeck/internal/researchledger"
)

func TestBuildGraphLinksAndAggregates(t *testing.T) {
	hyps := []rl.Hypothesis{
		{ID: "H002", Family: "meanrev", Statement: "weekly inverse edge", Posterior: 0.4, Status: rl.StatusUncertain},
		{ID: "H008", Family: "meanrev", Statement: "rsi overextension", Posterior: 0.2, Status: rl.StatusDoubtful},
		{ID: "H020", Family: "", Statement: "no family", Posterior: 0.1},
	}
	ev := []rl.Evidence{
		{HypID: "H002", Kind: rl.KindBacktest, K: 12, N: 20, BF: 2, Note: "era=bear_2022 12/20 winning weeks"},
		{HypID: "H002", Kind: rl.KindBacktest, K: 8, N: 20, BF: 4, Note: "era=calm_2023 8/20 winning weeks"},
		{HypID: "H002", Kind: rl.KindAttack, BF: 0.6, Note: "single-regime: calm only"},
		{HypID: "H008", Kind: rl.KindAttack, BF: 0.4, Note: "time-split: flips"},
		{HypID: "H999", Kind: rl.KindAttack, BF: 0.4, Note: "orphan: unknown hypothesis"},
	}
	g := BuildGraph(hyps, ev)

	byID := map[string]GraphNode{}
	for _, n := range g.Nodes {
		byID[n.ID] = n
	}
	if _, ok := byID["attack:orphan"]; ok {
		t.Error("evidence for an unknown hypothesis created a node — dangling edges")
	}
	for _, e := range g.Edges {
		if e.From == "H999" {
			t.Errorf("dangling edge from an unknown hypothesis: %+v", e)
		}
	}
	// A hypothesis with no family gets no family edge rather than a "" node.
	if _, ok := byID["family:"]; ok {
		t.Error("empty family produced a node")
	}
	if n := byID["H002"]; n.Kind != "hypothesis" || n.Posterior != 0.4 || n.Status != rl.StatusUncertain {
		t.Errorf("hypothesis node = %+v", n)
	}
	if _, ok := byID["era:bear_2022"]; !ok {
		t.Error("era node missing — the era= marker was not parsed")
	}

	find := func(from, to string) (GraphEdge, bool) {
		for _, e := range g.Edges {
			if e.From == from && e.To == to {
				return e, true
			}
		}
		return GraphEdge{}, false
	}
	// Structural family membership is not evidence: AvgBF must be 0 (not
	// applicable), never 1 (which would read as "neutral evidence").
	if e, ok := find("H002", "family:meanrev"); !ok || e.Weight != 1 || e.AvgBF != 0 {
		t.Errorf("family edge = %+v ok=%v, want weight 1 / avgBF 0", e, ok)
	}
	if e, ok := find("H002", "kind:backtest"); !ok || e.Weight != 2 || math.Abs(e.AvgBF-3) > 1e-9 {
		t.Errorf("kind edge = %+v, want weight 2 / avgBF 3", e)
	}
	if e, ok := find("H002", "attack:single-regime"); !ok || math.Abs(e.AvgBF-0.6) > 1e-9 {
		t.Errorf("attack edge = %+v", e)
	}
}

// Ordering must be deterministic — nodes grouped by kind then ID, edges by
// (from, to) — or the payload churns between identical reads and no diff of the
// research graph means anything.
func TestBuildGraphIsDeterministicallyOrdered(t *testing.T) {
	hyps := []rl.Hypothesis{
		{ID: "H020", Family: "zeta"}, {ID: "H002", Family: "alpha"}, {ID: "H008", Family: "alpha"},
	}
	ev := []rl.Evidence{
		{HypID: "H008", Kind: rl.KindAttack, BF: 0.5, Note: "zulu: x"},
		{HypID: "H002", Kind: rl.KindAttack, BF: 0.5, Note: "alpha: y"},
		{HypID: "H020", Kind: rl.KindBacktest, N: 10, BF: 2, Note: "era=zzz"},
		{HypID: "H002", Kind: rl.KindBacktest, N: 10, BF: 2, Note: "era=aaa"},
	}
	first := BuildGraph(hyps, ev)
	for i := 0; i < 20; i++ {
		g := BuildGraph(hyps, ev)
		for j := range g.Nodes {
			if g.Nodes[j] != first.Nodes[j] {
				t.Fatalf("node order unstable at %d: %+v vs %+v", j, g.Nodes[j], first.Nodes[j])
			}
		}
		for j := range g.Edges {
			if g.Edges[j] != first.Edges[j] {
				t.Fatalf("edge order unstable at %d: %+v vs %+v", j, g.Edges[j], first.Edges[j])
			}
		}
	}
	// Hypotheses first, then families, attacks, eras, evidence kinds.
	wantRank := []string{"hypothesis", "hypothesis", "hypothesis", "family", "family",
		"attack", "attack", "era", "era", "evidence_kind", "evidence_kind"}
	if len(first.Nodes) != len(wantRank) {
		t.Fatalf("%d nodes, want %d: %+v", len(first.Nodes), len(wantRank), first.Nodes)
	}
	for i, want := range wantRank {
		if first.Nodes[i].Kind != want {
			t.Errorf("node %d kind = %q, want %q", i, first.Nodes[i].Kind, want)
		}
	}
}

func TestNoteEraParsing(t *testing.T) {
	cases := map[string]string{
		"era=bear_2022: 12/20 winning weeks": "bear_2022",
		"live grade, no era marker":          "",
		"era=":                               "",
		"prefix era=calm2023 suffix":         "calm2023",
		"era=UPPER":                          "", // era tokens are [a-z0-9_]
	}
	for note, want := range cases {
		if got := noteEra(note); got != want {
			t.Errorf("noteEra(%q) = %q, want %q", note, got, want)
		}
	}
}

func TestAttackNoteNameMirrorsTheLedger(t *testing.T) {
	cases := map[string]string{
		"single-regime: calm only": "single-regime",
		"no-colon-here":            "no-colon-here",
		":leading":                 "",
	}
	for note, want := range cases {
		if got := attackNoteName(note); got != want {
			t.Errorf("attackNoteName(%q) = %q, want %q", note, got, want)
		}
	}
}

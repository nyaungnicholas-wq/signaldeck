package lineage

import (
	"context"
	"path/filepath"
	"testing"

	"github.com/nyaungnicholas-wq/signaldeck/internal/store"
)

func openStore(t *testing.T) *store.Store {
	t.Helper()
	st, err := store.Open(filepath.Join(t.TempDir(), "lineage.db"))
	if err != nil {
		t.Fatalf("open store: %v", err)
	}
	t.Cleanup(func() { _ = st.Close() })
	return st
}

func TestLinkValidation(t *testing.T) {
	st := openStore(t)
	ctx := context.Background()
	bad := []Edge{
		{SrcKind: "banana", SrcID: "a", DstKind: KindModel, DstID: "b", EdgeKind: EdgeProduced},
		{SrcKind: KindModel, SrcID: "a", DstKind: "nope", DstID: "b", EdgeKind: EdgeProduced},
		{SrcKind: KindModel, SrcID: "a", DstKind: KindFeature, DstID: "b", EdgeKind: "eats"},
		{SrcKind: KindModel, SrcID: "", DstKind: KindFeature, DstID: "b", EdgeKind: EdgeProduced},
		{SrcKind: KindModel, SrcID: "a", DstKind: KindModel, DstID: "a", EdgeKind: EdgeProduced},
	}
	for i, e := range bad {
		if err := Link(ctx, st, e); err == nil {
			t.Errorf("bad edge %d accepted: %+v", i, e)
		}
	}
	if n, _ := st.CountLineageEdges(ctx); n != 0 {
		t.Fatalf("invalid edges persisted: %d", n)
	}
	good := Edge{SrcKind: KindHypothesis, SrcID: "ledger:H008", DstKind: KindFeature, DstID: "trend21", EdgeKind: EdgeTestedIn}
	if err := Link(ctx, st, good); err != nil {
		t.Fatalf("valid edge rejected: %v", err)
	}
}

func TestLinkIdempotent(t *testing.T) {
	st := openStore(t)
	ctx := context.Background()
	e := Edge{SrcKind: KindPrediction, SrcID: "42", DstKind: KindModel, DstID: "gbm:1w", EdgeKind: EdgeGeneratedBy, MetaJSON: `{"rev":"abc"}`}
	for i := 0; i < 3; i++ {
		if err := Link(ctx, st, e); err != nil {
			t.Fatalf("link %d: %v", i, err)
		}
	}
	n, err := st.CountLineageEdges(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if n != 1 {
		t.Fatalf("want 1 edge after 3 identical links, got %d", n)
	}
	// A repeat with empty meta must not erase the recorded rev.
	e.MetaJSON = ""
	if err := Link(ctx, st, e); err != nil {
		t.Fatal(err)
	}
	edges, err := st.LineageEdgesTouching(ctx, KindPrediction, "42")
	if err != nil {
		t.Fatal(err)
	}
	if len(edges) != 1 || edges[0].MetaJSON != `{"rev":"abc"}` {
		t.Fatalf("meta lost on empty rewrite: %+v", edges)
	}
}

func TestTraceThreeHops(t *testing.T) {
	st := openStore(t)
	ctx := context.Background()
	// feature -> hypothesis -> prediction -> trade, seeded as a chain.
	chain := []Edge{
		{SrcKind: KindHypothesis, SrcID: "ledger:H008", DstKind: KindFeature, DstID: "trend21", EdgeKind: EdgeTestedIn},
		{SrcKind: KindPrediction, SrcID: "7", DstKind: KindHypothesis, DstID: "ledger:H008", EdgeKind: EdgeGeneratedBy},
		{SrcKind: KindTrade, SrcID: "t1", DstKind: KindPrediction, DstID: "7", EdgeKind: EdgeTradedAs},
	}
	for _, e := range chain {
		if err := Link(ctx, st, e); err != nil {
			t.Fatal(err)
		}
	}
	g, err := Trace(ctx, st, KindFeature, "trend21", 3)
	if err != nil {
		t.Fatal(err)
	}
	if len(g.Nodes) != 4 || len(g.Edges) != 3 {
		t.Fatalf("want 4 nodes / 3 edges, got %d / %d: %+v", len(g.Nodes), len(g.Edges), g)
	}
	depths := map[string]int{}
	for _, n := range g.Nodes {
		depths[n.Kind+"|"+n.ID] = n.Depth
	}
	if depths["trade|t1"] != 3 {
		t.Fatalf("trade should be 3 hops out, got %d", depths["trade|t1"])
	}
	// Depth cap: depth 1 must stop at the hypothesis.
	g1, err := Trace(ctx, st, KindFeature, "trend21", 1)
	if err != nil {
		t.Fatal(err)
	}
	if len(g1.Nodes) != 2 {
		t.Fatalf("depth-1 trace should see 2 nodes, got %d", len(g1.Nodes))
	}
}

func TestTraceCycleSafe(t *testing.T) {
	st := openStore(t)
	ctx := context.Background()
	cyc := []Edge{
		{SrcKind: KindHypothesis, SrcID: "loop:a", DstKind: KindExperiment, DstID: "x", EdgeKind: EdgeGradedBy},
		{SrcKind: KindExperiment, SrcID: "x", DstKind: KindModel, DstID: "m", EdgeKind: EdgeProduced},
		{SrcKind: KindModel, SrcID: "m", DstKind: KindHypothesis, DstID: "loop:a", EdgeKind: EdgeProduced},
	}
	for _, e := range cyc {
		if err := Link(ctx, st, e); err != nil {
			t.Fatal(err)
		}
	}
	g, err := Trace(ctx, st, KindHypothesis, "loop:a", MaxTraceDepth)
	if err != nil {
		t.Fatalf("cycle trace: %v", err)
	}
	if len(g.Nodes) != 3 || len(g.Edges) != 3 {
		t.Fatalf("cycle: want 3 nodes / 3 edges, got %d / %d", len(g.Nodes), len(g.Edges))
	}
}

func TestTraceRejectsUnknownKind(t *testing.T) {
	st := openStore(t)
	if _, err := Trace(context.Background(), st, "banana", "x", 3); err == nil {
		t.Fatal("unknown kind accepted")
	}
}

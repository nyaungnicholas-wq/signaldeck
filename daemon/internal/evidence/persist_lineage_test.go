package evidence

import (
	"context"
	"testing"

	"github.com/nyaungnicholas-wq/signaldeck/internal/lineage"
)

// TestPutLinksFeatureKeysIntoLineage verifies that Put correctly creates lineage edges for feature keys.
func TestPutLinksFeatureKeysIntoLineage(t *testing.T) {
	st := newTestStore(t)
	ctx := context.Background()

	c := validClaim()
	c.Lineage = Lineage{FeatureKeys: []string{"trend21", "liquidity21"}, Models: []string{"structural-regime"}}

	if err := Put(ctx, st, c); err != nil {
		t.Fatalf("first Put failed: %v", err)
	}
	if err := Put(ctx, st, c); err != nil {
		t.Fatalf("second Put failed: %v", err)
	}

	edges, err := st.LineageEdgesTouching(ctx, lineage.KindClaim, c.ID)
	if err != nil {
		t.Fatalf("LineageEdgesTouching failed: %v", err)
	}
	if len(edges) != 2 {
		t.Fatalf("expected 2 edges, got %d", len(edges))
	}

	srcIDs := make(map[string]bool)
	for _, e := range edges {
		if e.SrcKind != lineage.KindFeature {
			t.Fatalf("edge SrcKind mismatch: want %q, got %q in %+v", lineage.KindFeature, e.SrcKind, e)
		}
		if e.DstKind != lineage.KindClaim {
			t.Fatalf("edge DstKind mismatch: want %q, got %q in %+v", lineage.KindClaim, e.DstKind, e)
		}
		if e.DstID != c.ID {
			t.Fatalf("edge DstID mismatch: want %q, got %q in %+v", c.ID, e.DstID, e)
		}
		if e.EdgeKind != lineage.EdgeEvidencedBy {
			t.Fatalf("edge EdgeKind mismatch: want %q, got %q in %+v", lineage.EdgeEvidencedBy, e.EdgeKind, e)
		}
		srcIDs[e.SrcID] = true
	}

	expected := map[string]bool{"trend21": true, "liquidity21": true}
	if len(srcIDs) != len(expected) {
		t.Fatalf("expected %d srcIDs, got %d", len(expected), len(srcIDs))
	}
	for id := range srcIDs {
		if !expected[id] {
			t.Fatalf("unexpected srcID: %q", id)
		}
	}
	for id := range expected {
		if !srcIDs[id] {
			t.Fatalf("missing srcID: %q", id)
		}
	}

	n, err := st.CountLineageEdges(ctx)
	if err != nil {
		t.Fatalf("CountLineageEdges failed: %v", err)
	}
	if n != 2 {
		t.Fatalf("expected 2 lineage edges total (model names are not model nodes), got %d", n)
	}
}

// TestPutWithoutFeatureKeysWritesNoLineage verifies that Put writes no lineage edges when FeatureKeys is empty.
func TestPutWithoutFeatureKeysWritesNoLineage(t *testing.T) {
	st := newTestStore(t)
	ctx := context.Background()

	c := validClaim()
	c.Lineage = Lineage{}

	if err := Put(ctx, st, c); err != nil {
		t.Fatalf("Put failed: %v", err)
	}

	n, err := st.CountLineageEdges(ctx)
	if err != nil {
		t.Fatalf("CountLineageEdges failed: %v", err)
	}
	if n != 0 {
		t.Fatalf("expected 0 lineage edges, got %d", n)
	}
}

// TestEnsureSeedsLinksClaimsSeededBeforeLineage: a seed claim already in the
// database (written before Put linked lineage) must still get its feature
// edges. EnsureSeeds skips existing claims and is Put's only caller, so without
// this the live database would never receive a single evidence edge.
func TestEnsureSeedsLinksClaimsSeededBeforeLineage(t *testing.T) {
	st := newTestStore(t)
	ctx := context.Background()
	var seed Claim
	for _, c := range SeedClaims() {
		if len(c.Lineage.FeatureKeys) > 0 {
			seed = c
			break
		}
	}
	if seed.ID == "" {
		t.Fatal("no seed claim carries feature keys, so nothing below would be tested")
	}
	row, err := toRow(seed)
	if err != nil {
		t.Fatal(err)
	}
	// The pre-wiring write: the row exists, its edges do not.
	if err := st.PutEvidenceClaim(ctx, row); err != nil {
		t.Fatal(err)
	}
	if n, _ := st.CountLineageEdges(ctx); n != 0 {
		t.Fatalf("fixture starts with %d edges, want 0", n)
	}
	if _, err := EnsureSeeds(ctx, st); err != nil {
		t.Fatal(err)
	}
	edges, err := st.LineageEdgesTouching(ctx, lineage.KindClaim, seed.ID)
	if err != nil {
		t.Fatal(err)
	}
	if len(edges) != len(seed.Lineage.FeatureKeys) {
		t.Fatalf("existing seed %s got %d edges, want one per feature key %v: %+v",
			seed.ID, len(edges), seed.Lineage.FeatureKeys, edges)
	}
}

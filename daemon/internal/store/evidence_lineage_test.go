// Round-trip tests for the Evidence Engine persistence (claims + items,
// filters, the sweep's state flip) and the lineage_edges spine.
package store

import (
	"context"
	"database/sql"
	"testing"
)

func TestEvidenceClaims_PutReadFilterAndState(t *testing.T) {
	st := openTemp(t)
	ctx := context.Background()

	lo, hi, base := 0.01, 0.05, 0.5
	c := EvidenceClaimRow{
		ID: "clm-momo", Text: "momentum lifts 1w hit rate",
		ScopeJSON: `{"horizon":"1w"}`, Tier: "T2", Status: "active",
		LastValidated: 1000, RevalidateBy: 2000,
		LineageJSON: `{"features":["momo21"]}`, Seeded: true,
		Items: []EvidenceItemRow{
			{Kind: "lift", Value: 0.03, NEffective: 40, Method: "wf-oos",
				Correction: "none", CILow: &lo, CIHigh: &hi, Baseline: &base, SourceRef: "run-1"},
			{Kind: "auc", Value: 0.55, NEffective: 40, Method: "wf-oos", Correction: "none"},
		},
	}
	if err := st.PutEvidenceClaim(ctx, c); err != nil {
		t.Fatalf("put claim: %v", err)
	}
	if err := st.PutEvidenceClaim(ctx, EvidenceClaimRow{
		ID: "clm-vol", Text: "vol regime is sticky", ScopeJSON: `{}`,
		Tier: "T3", Status: "retired", LineageJSON: `{"features":["vol21"]}`,
	}); err != nil {
		t.Fatalf("put claim 2: %v", err)
	}

	ok, err := st.EvidenceClaimExists(ctx, "clm-momo")
	if err != nil || !ok {
		t.Fatalf("EvidenceClaimExists = %v, %v; want true", ok, err)
	}
	if ok, _ := st.EvidenceClaimExists(ctx, "clm-none"); ok {
		t.Fatal("EvidenceClaimExists matched an absent id")
	}

	got, err := st.EvidenceClaim(ctx, "clm-momo")
	if err != nil {
		t.Fatalf("read claim: %v", err)
	}
	if got.Text != c.Text || !got.Seeded || len(got.Items) != 2 {
		t.Fatalf("claim mangled: %+v", got)
	}
	// Items keep insertion order and round-trip nullable CI bounds.
	if got.Items[0].Kind != "lift" || got.Items[0].CILow == nil || *got.Items[0].CILow != lo {
		t.Fatalf("item 0 mangled: %+v", got.Items[0])
	}
	if got.Items[1].CILow != nil {
		t.Fatalf("absent CI should read back nil: %+v", got.Items[1])
	}
	if _, err := st.EvidenceClaim(ctx, "clm-none"); err != sql.ErrNoRows {
		t.Fatalf("absent claim error = %v; want sql.ErrNoRows", err)
	}

	// Re-put replaces the row AND its items (no accumulation).
	c.Items = c.Items[:1]
	c.Status = "active"
	if err := st.PutEvidenceClaim(ctx, c); err != nil {
		t.Fatalf("re-put claim: %v", err)
	}
	if got, _ := st.EvidenceClaim(ctx, "clm-momo"); len(got.Items) != 1 {
		t.Fatalf("re-put accumulated items: %d", len(got.Items))
	}

	// Filters: status, feature, both, none.
	if rows, err := st.EvidenceClaims(ctx, "active", ""); err != nil || len(rows) != 1 || rows[0].ID != "clm-momo" {
		t.Fatalf("status filter = %+v, %v", rows, err)
	}
	if rows, err := st.EvidenceClaims(ctx, "", "vol21"); err != nil || len(rows) != 1 || rows[0].ID != "clm-vol" {
		t.Fatalf("feature filter = %+v, %v", rows, err)
	}
	if rows, err := st.EvidenceClaims(ctx, "retired", "vol21"); err != nil || len(rows) != 1 {
		t.Fatalf("combined filter = %+v, %v", rows, err)
	}
	if rows, err := st.EvidenceClaims(ctx, "", ""); err != nil || len(rows) != 2 {
		t.Fatalf("unfiltered = %d rows, %v; want 2", len(rows), err)
	}

	// The sweep's flip: status+tier change, items untouched.
	if err := st.SetEvidenceClaimState(ctx, "clm-momo", "refuted", "T4"); err != nil {
		t.Fatalf("set state: %v", err)
	}
	got, _ = st.EvidenceClaim(ctx, "clm-momo")
	if got.Status != "refuted" || got.Tier != "T4" || len(got.Items) != 1 {
		t.Fatalf("state flip wrong: %+v", got)
	}
}

func TestLineageEdges_UpsertTraceAndCount(t *testing.T) {
	st := openTemp(t)
	ctx := context.Background()

	e := LineageEdge{SrcKind: "study", SrcID: "S1", DstKind: "claim", DstID: "C1",
		EdgeKind: "supports", CreatedAt: 100, MetaJSON: ""}
	if err := st.UpsertLineageEdge(ctx, e); err != nil {
		t.Fatalf("upsert edge: %v", err)
	}
	// Re-write with meta enriches; created_at keeps the FIRST value.
	e.CreatedAt, e.MetaJSON = 999, `{"rev":"abc"}`
	if err := st.UpsertLineageEdge(ctx, e); err != nil {
		t.Fatalf("enrich edge: %v", err)
	}
	// A later empty-meta write must NOT erase the recorded rev.
	e.MetaJSON = ""
	if err := st.UpsertLineageEdge(ctx, e); err != nil {
		t.Fatalf("re-upsert edge: %v", err)
	}
	if err := st.UpsertLineageEdge(ctx, LineageEdge{
		SrcKind: "claim", SrcID: "C1", DstKind: "surface", DstID: "api",
		EdgeKind: "feeds", CreatedAt: 200,
	}); err != nil {
		t.Fatalf("upsert edge 2: %v", err)
	}

	n, err := st.CountLineageEdges(ctx)
	if err != nil || n != 2 {
		t.Fatalf("CountLineageEdges = %d, %v; want 2", n, err)
	}

	// C1 appears as dst of one edge and src of the other: both must return.
	edges, err := st.LineageEdgesTouching(ctx, "claim", "C1")
	if err != nil || len(edges) != 2 {
		t.Fatalf("edges touching C1 = %d, %v; want 2", len(edges), err)
	}
	if edges[0].CreatedAt != 100 || edges[0].MetaJSON != `{"rev":"abc"}` {
		t.Fatalf("idempotent upsert broke created_at/meta rules: %+v", edges[0])
	}
	if edges, _ := st.LineageEdgesTouching(ctx, "study", "S9"); len(edges) != 0 {
		t.Fatal("unrelated node returned edges")
	}
}

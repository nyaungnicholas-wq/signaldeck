package pipeline

import (
	"context"
	"fmt"
	"go/ast"
	"go/parser"
	"go/token"
	"os"
	"strings"
	"testing"

	"github.com/nyaungnicholas-wq/signaldeck/internal/lineage"
	rl "github.com/nyaungnicholas-wq/signaldeck/internal/researchledger"
	"github.com/nyaungnicholas-wq/signaldeck/internal/store"
)

// TestInsertLedgerEvidenceLinksLineage verifies that insertLedgerEvidence writes the evidence row
// and creates the expected lineage edge from hypothesis to evidence.
func TestInsertLedgerEvidenceLinksLineage(t *testing.T) {
	st := openStore(t)
	ctx := context.Background()
	ts := int64(store.GradingEpochTS)
	// Evidence rows reference their hypothesis (FK), so register it first.
	if err := st.UpsertLedgerHypothesis(ctx, rl.Hypothesis{
		ID: "HLIN", Family: "test", Statement: "lineage fixture", Prior: 0.15, MaxEdge: 0.20,
	}, ts); err != nil {
		t.Fatalf("seed hypothesis: %v", err)
	}
	e := rl.Evidence{
		HypID: "HLIN",
		Ts:    ts,
		Kind:  rl.KindExperiment,
		K:     6,
		N:     10,
		P0:    0.5,
		BF:    1.2,
		Note:  "lineage fixture",
	}
	if err := insertLedgerEvidence(ctx, st, e); err != nil {
		t.Fatalf("insertLedgerEvidence: %v", err)
	}
	rows, err := st.LedgerEvidence(ctx, "HLIN")
	if err != nil {
		t.Fatalf("LedgerEvidence: %v", err)
	}
	if len(rows) != 1 {
		t.Fatalf("expected 1 evidence row, got %d: %v", len(rows), rows)
	}
	edges, err := st.LineageEdgesTouching(ctx, lineage.KindHypothesis, "ledger:HLIN")
	if err != nil {
		t.Fatalf("LineageEdgesTouching: %v", err)
	}
	if len(edges) != 1 {
		t.Fatalf("expected 1 lineage edge, got %d: %v", len(edges), edges)
	}
	edge := edges[0]
	wantDstID := fmt.Sprintf("ledger-evidence:HLIN@%d:%s", ts, rl.KindExperiment)
	if edge.SrcKind != lineage.KindHypothesis {
		t.Fatalf("edge.SrcKind = %q, want %q", edge.SrcKind, lineage.KindHypothesis)
	}
	if edge.SrcID != "ledger:HLIN" {
		t.Fatalf("edge.SrcID = %q, want %q", edge.SrcID, "ledger:HLIN")
	}
	if edge.DstKind != lineage.KindClaim {
		t.Fatalf("edge.DstKind = %q, want %q", edge.DstKind, lineage.KindClaim)
	}
	if edge.DstID != wantDstID {
		t.Fatalf("edge.DstID = %q, want %q", edge.DstID, wantDstID)
	}
	if edge.EdgeKind != lineage.EdgeEvidencedBy {
		t.Fatalf("edge.EdgeKind = %q, want %q", edge.EdgeKind, lineage.EdgeEvidencedBy)
	}
}

// TestLedgerEvidenceWritersLinkLineage ensures all ledger-evidence writers in the package
// route through insertLedgerEvidence so none can bypass the lineage edge.
func TestLedgerEvidenceWritersLinkLineage(t *testing.T) {
	fset := token.NewFileSet()
	var (
		parsedFiles int
		direct      []string // file:function for direct store.InsertLedgerEvidence calls
		routed      int      // count of insertLedgerEvidence calls
	)
	entries, err := os.ReadDir(".")
	if err != nil {
		t.Fatalf("ReadDir .: %v", err)
	}
	for _, entry := range entries {
		if entry.IsDir() {
			continue
		}
		name := entry.Name()
		if !strings.HasSuffix(name, ".go") || strings.HasSuffix(name, "_test.go") {
			continue
		}
		file, err := parser.ParseFile(fset, name, nil, 0)
		if err != nil {
			t.Fatalf("ParseFile %s: %v", name, err)
		}
		parsedFiles++
		// Every top-level declaration, not just function bodies: a closure in a
		// package-level var (ledgerGraders is one) or a method value
		// (f := w.St.InsertLedgerEvidence) must not slip past the scan.
		for _, decl := range file.Decls {
			owner := "<package-level>"
			if fn, ok := decl.(*ast.FuncDecl); ok {
				owner = fn.Name.Name
			}
			ast.Inspect(decl, func(n ast.Node) bool {
				switch x := n.(type) {
				case *ast.SelectorExpr:
					if x.Sel.Name == "InsertLedgerEvidence" {
						direct = append(direct, fmt.Sprintf("%s:%s", name, owner))
					}
				case *ast.CallExpr:
					if id, ok := x.Fun.(*ast.Ident); ok && id.Name == "insertLedgerEvidence" {
						routed++
					}
				}
				return true
			})
		}
	}
	if parsedFiles <= 50 {
		t.Fatalf("parsed file count <= 50: got %d", parsedFiles)
	}
	if len(direct) != 1 || !strings.HasSuffix(direct[0], ":insertLedgerEvidence") {
		t.Fatalf("expected exactly one direct call to store.InsertLedgerEvidence ending with ':insertLedgerEvidence', got %d: %v", len(direct), direct)
	}
	if routed < 8 {
		t.Fatalf("expected at least 8 routed calls to insertLedgerEvidence, got %d", routed)
	}
}

package store

import (
	"os"
	"regexp"
	"strings"
	"testing"
)

// TestResearchLoopContractCoversWriteColumns pins LoopLedgerReady's preflight to
// the write path it is supposed to protect.
//
// The preflight exists so a search whose verdicts cannot be stored costs nothing
// instead of costing a look. That guarantee holds only while the probed object
// set is a SUPERSET of what RecordLoopSearch writes: when the writer named
// fifteen columns and the probe checked two, the loop passed preflight on a
// database missing the rest, incremented its multiplicity counter, ran the grid,
// and then failed the write — the look charged, the judgments gone. A hand-kept
// list drifts silently, so this test re-derives the truth from the source of
// RecordLoopSearch itself and fails when the contract does not cover it.
func TestResearchLoopContractCoversWriteColumns(t *testing.T) {
	src, err := os.ReadFile("researchweeks.go")
	if err != nil {
		t.Fatal(err)
	}
	body := funcBody(t, string(src), "func (s *Store) RecordLoopSearch(")

	// INSERT INTO <table>\n (col, col, ...)
	re := regexp.MustCompile(`(?s)INSERT INTO\s+(\w+)\s*\(([^)]*)\)`)
	found := re.FindAllStringSubmatch(body, -1)
	if len(found) != 4 {
		t.Fatalf("expected 4 INSERT statements in RecordLoopSearch, found %d — "+
			"the extractor must be updated before the contract can be trusted", len(found))
	}

	declared := map[string]bool{}
	for _, o := range SchemaContract["research-loop"] {
		declared[o] = true
	}
	for _, m := range found {
		table := m[1]
		for _, c := range strings.Split(m[2], ",") {
			col := strings.TrimSpace(c)
			if col == "" {
				continue
			}
			obj := table + "." + col
			if !declared[obj] && !declared[table] {
				t.Errorf("RecordLoopSearch writes %s but SchemaContract[\"research-loop\"] "+
					"does not list it: LoopLedgerReady would pass, the search would spend a "+
					"look, and the write would then fail", obj)
			}
		}
	}
}

// funcBody returns the text of the function whose declaration starts with decl,
// up to the closing brace in column 0.
func funcBody(t *testing.T, src, decl string) string {
	t.Helper()
	i := strings.Index(src, decl)
	if i < 0 {
		t.Fatalf("declaration %q not found", decl)
	}
	rest := src[i:]
	if j := strings.Index(rest, "\n}\n"); j >= 0 {
		return rest[:j]
	}
	return rest
}

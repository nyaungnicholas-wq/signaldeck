package store

import (
	"context"
	"path/filepath"
	"strings"
	"testing"
)

// A freshly opened database must satisfy its own declaration. If this ever
// fails, a column was added to schema.sql without a matching migrate() step —
// which is precisely the divergence verifySchema exists to catch.
func TestVerifySchemaCleanOpen(t *testing.T) {
	st := openTestStore(t)
	if err := verifySchema(st.w); err != nil {
		t.Fatalf("fresh database diverges from schema.sql: %v", err)
	}
}

// Dropping a declared column must produce a HARD error naming that column —
// not a warning, not a silent pass.
func TestVerifySchemaDetectsDroppedColumn(t *testing.T) {
	st := openTestStore(t)
	if _, err := st.w.Exec(`ALTER TABLE regime_outcomes DROP COLUMN naive_label`); err != nil {
		t.Fatalf("drop column: %v", err)
	}
	err := verifySchema(st.w)
	if err == nil {
		t.Fatal("verifySchema accepted a database missing a declared column")
	}
	if !strings.Contains(err.Error(), "regime_outcomes.naive_label") {
		t.Fatalf("error does not name the missing column: %v", err)
	}
}

// A declared table that does not exist must also be a hard error.
func TestVerifySchemaDetectsDroppedTable(t *testing.T) {
	st := openTestStore(t)
	if _, err := st.w.Exec(`DROP TABLE research_loop_judgments`); err != nil {
		t.Fatalf("drop table: %v", err)
	}
	err := verifySchema(st.w)
	if err == nil {
		t.Fatal("verifySchema accepted a database missing a declared table")
	}
	if !strings.Contains(err.Error(), "table research_loop_judgments") {
		t.Fatalf("error does not name the missing table: %v", err)
	}
}

// Open itself must refuse: verifySchema is only useful if it gates the Store.
func TestOpenRefusesDivergentDatabase(t *testing.T) {
	path := filepath.Join(t.TempDir(), "sd.db")
	st, err := Open(path)
	if err != nil {
		t.Fatalf("open: %v", err)
	}
	// conviction is declared in schema.sql and NOT managed by migrate() — the
	// exact shape of the bug: only the CREATE statement knows about it, so an
	// existing database can never acquire it.
	if _, err := st.w.Exec(`ALTER TABLE regime_outcomes DROP COLUMN conviction`); err != nil {
		t.Fatalf("drop column: %v", err)
	}
	st.Close() //nolint:errcheck

	if _, err := Open(path); err == nil {
		t.Fatal("Open accepted a database whose declared column is absent")
	} else if !strings.Contains(err.Error(), "regime_outcomes.conviction") {
		t.Fatalf("Open error does not name the missing column: %v", err)
	}
}

// An audit-record worker's contracted table is not a feature that can be
// dropped: without it the worker runs, judges, stores nothing and reports
// status='ok'. Losing it must stop the daemon at boot, naming the object.
func TestOpenRefusesWhenAuditRecordTableMissing(t *testing.T) {
	st := openTestStore(t)
	if err := st.VerifyAuditContract(context.Background()); err != nil {
		t.Fatalf("fresh database fails its own audit contract: %v", err)
	}
	if _, err := st.w.Exec(`DROP TABLE research_loop_judgments`); err != nil {
		t.Fatalf("drop table: %v", err)
	}
	err := st.VerifyAuditContract(context.Background())
	if err == nil {
		t.Fatal("audit contract accepted a database with no place to store judgments")
	}
	if !strings.Contains(err.Error(), "research_loop_judgments") ||
		!strings.Contains(err.Error(), "research-loop") {
		t.Fatalf("error names neither the worker nor the object: %v", err)
	}
}

// Every audit-record worker must actually be under contract; an entry with no
// contracted objects would silently verify nothing.
func TestAuditRecordWorkersAreContracted(t *testing.T) {
	for w := range AuditRecordWorkers {
		if len(SchemaContract[w]) == 0 {
			t.Fatalf("audit-record worker %q has no contracted objects", w)
		}
	}
}

// The contract must refuse the affected worker — and ONLY the affected worker.
func TestContractViolationsRefusesAffectedWorker(t *testing.T) {
	st := openTestStore(t)
	ctx := context.Background()
	names := []string{"research-loop", "regime-outcome-runner", "prediction-runner", "some-other-worker"}

	bad, err := st.ContractViolations(ctx, names)
	if err != nil {
		t.Fatalf("contract check: %v", err)
	}
	if len(bad) != 0 {
		t.Fatalf("fresh database violates the contract: %v", bad)
	}

	if _, err := st.w.Exec(`DROP TABLE research_loop_judgments`); err != nil {
		t.Fatalf("drop table: %v", err)
	}
	bad, err = st.ContractViolations(ctx, names)
	if err != nil {
		t.Fatalf("contract check: %v", err)
	}
	if got := bad["research-loop"]; len(got) != 1 || got[0] != "research_loop_judgments" {
		t.Fatalf("research-loop not refused for the right object: %v", bad)
	}
	if _, ok := bad["regime-outcome-runner"]; ok {
		t.Fatal("an unaffected worker was refused")
	}
}

// A worker not present in the fleet is never reported, so the refusal set
// describes what actually would have run.
func TestContractViolationsIgnoresAbsentWorkers(t *testing.T) {
	st := openTestStore(t)
	if _, err := st.w.Exec(`DROP TABLE research_loop_judgments`); err != nil {
		t.Fatalf("drop table: %v", err)
	}
	bad, err := st.ContractViolations(context.Background(), []string{"prediction-runner"})
	if err != nil {
		t.Fatalf("contract check: %v", err)
	}
	if len(bad) != 0 {
		t.Fatalf("reported a worker that is not in the fleet: %v", bad)
	}
}

func TestParseDeclaredSchemaIgnoresCommentsAndConstraints(t *testing.T) {
	src := `
CREATE TABLE IF NOT EXISTS t (
  id   INTEGER PRIMARY KEY,
  -- rank REAL, this is a comment and is NOT a column
  val  REAL NOT NULL DEFAULT (0),
  ref  INTEGER REFERENCES other(id),
  UNIQUE(id, val),
  FOREIGN KEY (ref) REFERENCES other(id)
);
CREATE INDEX IF NOT EXISTS idx_t ON t (val);
`
	got := parseDeclaredSchema(src)
	if len(got) != 1 || got[0].name != "t" {
		t.Fatalf("tables = %+v", got)
	}
	want := []string{"id", "val", "ref"}
	if len(got[0].columns) != len(want) {
		t.Fatalf("columns = %v, want %v", got[0].columns, want)
	}
	for i, c := range want {
		if got[0].columns[i] != c {
			t.Fatalf("columns = %v, want %v", got[0].columns, want)
		}
	}
}

// openTestStore opens a throwaway store and closes it at test end.
func openTestStore(t *testing.T) *Store {
	t.Helper()
	st, err := Open(filepath.Join(t.TempDir(), "sd.db"))
	if err != nil {
		t.Fatalf("open: %v", err)
	}
	t.Cleanup(func() { st.Close() }) //nolint:errcheck
	return st
}

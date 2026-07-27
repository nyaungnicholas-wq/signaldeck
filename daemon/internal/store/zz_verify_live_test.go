package store

import (
	"context"
	"database/sql"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// Adversarial verification probe (temporary): does Open() still succeed against
// a copy of the real production database after verifySchema was wired in?
func TestZZOpenAgainstLiveCopy(t *testing.T) {
	p := os.Getenv("SD_LIVE_DB")
	if p == "" {
		t.Skip("SD_LIVE_DB unset")
	}
	st, err := Open(p)
	if err != nil {
		t.Fatalf("Open(live copy) FAILED: %v", err)
	}
	defer st.Close() //nolint:errcheck
	bad, err := st.ContractViolations(context.Background(),
		[]string{"research-loop", "regime-outcome-runner", "prediction-runner", "prediction-resolver"})
	if err != nil {
		t.Fatalf("ContractViolations: %v", err)
	}
	t.Logf("Open OK. contract violations after migrate: %v", bad)
}

// Is the parser actually parsing, or silently finding nothing (a no-op gate
// that can never fail)? Measure declared tables/columns vs the live DB shape.
func TestZZParserCoverage(t *testing.T) {
	decl := parseDeclaredSchema(schemaSQL)
	totalCols := 0
	zeroCol := []string{}
	for _, d := range decl {
		totalCols += len(d.columns)
		if len(d.columns) == 0 {
			zeroCol = append(zeroCol, d.name)
		}
	}
	t.Logf("parsed %d CREATE TABLE stmts, %d columns total", len(decl), totalCols)
	if len(zeroCol) > 0 {
		t.Errorf("tables parsed with ZERO columns (parser blind spot): %v", zeroCol)
	}
	if len(decl) < 50 || totalCols < 400 {
		t.Fatalf("parser under-detecting: %d tables / %d cols", len(decl), totalCols)
	}
	// Cross-check against sqlite's own authority on a fresh DB.
	dir := t.TempDir()
	db, err := sql.Open("sqlite", filepath.Join(dir, "x.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close() //nolint:errcheck
	if _, err := db.Exec(schemaSQL); err != nil {
		t.Fatal(err)
	}
	var liveTables int
	if err := db.QueryRow(`SELECT COUNT(*) FROM sqlite_master WHERE type='table' AND name NOT LIKE 'sqlite_%'`).Scan(&liveTables); err != nil {
		t.Fatal(err)
	}
	t.Logf("sqlite created %d tables from the same schema.sql", liveTables)
	if liveTables != len(decl) {
		t.Errorf("parser saw %d tables, sqlite created %d — parser and reality disagree", len(decl), liveTables)
	}
	// And every parsed column must be a real column (no phantom names, which
	// would make verifySchema fail spuriously / permanently).
	phantom := []string{}
	for _, d := range decl {
		cols, exists, err := liveColumns(db, d.name)
		if err != nil || !exists {
			t.Errorf("declared table %s: exists=%v err=%v", d.name, exists, err)
			continue
		}
		for _, c := range d.columns {
			if !cols[c] {
				phantom = append(phantom, d.name+"."+c)
			}
		}
	}
	if len(phantom) > 0 {
		t.Errorf("PHANTOM columns parsed that sqlite did not create (%d): %v", len(phantom), phantom)
	}
}

// Does the gate actually FIRE? Build a DB from schema.sql minus one column and
// confirm Open refuses, naming it.
func TestZZGateActuallyFires(t *testing.T) {
	dir := t.TempDir()
	p := filepath.Join(dir, "drop.db")
	db, err := sql.Open("sqlite", "file:"+p)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := db.Exec(schemaSQL); err != nil {
		t.Fatal(err)
	}
	// prediction_ledger.horizon_days is declared in schema.sql and is NOT one of
	// the columns migrate() re-adds, so dropping it is a genuine divergence.
	var target, table string
	for _, d := range parseDeclaredSchema(schemaSQL) {
		if d.name != "prediction_ledger" {
			continue
		}
		for _, c := range d.columns {
			if c == "id" || c == "seq" || c == "revision" {
				continue
			}
			if _, e := db.Exec(`ALTER TABLE ` + d.name + ` DROP COLUMN ` + c); e == nil {
				target, table = c, d.name
				break
			}
		}
	}
	if target == "" {
		t.Fatal("no droppable column found")
	}
	if _, err := db.Exec(`ALTER TABLE ` + table + ` DROP COLUMN ` + target); err != nil {
		t.Skipf("cannot drop %s.%s: %v", table, target, err)
	}
	db.Close() //nolint:errcheck

	st, err := Open(p)
	if err == nil {
		st.Close() //nolint:errcheck
		t.Fatalf("Open SUCCEEDED after dropping %s.%s — the gate does NOT fire", table, target)
	}
	if !strings.Contains(err.Error(), table+"."+target) {
		t.Fatalf("gate fired but did not name %s.%s: %v", table, target, err)
	}
	t.Logf("gate fired correctly: %v", err)
}

// Does the run.go-level contract gate fire, and does it scope to the affected
// worker only?
func TestZZContractGateFires(t *testing.T) {
	dir := t.TempDir()
	p := filepath.Join(dir, "c.db")
	st, err := Open(p)
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close() //nolint:errcheck
	ctx := context.Background()
	names := []string{"research-loop", "regime-outcome-runner", "prediction-runner", "prediction-resolver", "unrelated-worker"}
	bad, err := st.ContractViolations(ctx, names)
	if err != nil {
		t.Fatal(err)
	}
	if len(bad) != 0 {
		t.Fatalf("fresh DB should satisfy every contract, got %v", bad)
	}
	if _, err := st.w.Exec(`ALTER TABLE research_loop_hypotheses DROP COLUMN null_p0`); err != nil {
		t.Skipf("drop unsupported: %v", err)
	}
	bad, err = st.ContractViolations(ctx, names)
	if err != nil {
		t.Fatal(err)
	}
	if len(bad) != 1 || len(bad["research-loop"]) == 0 {
		t.Fatalf("expected only research-loop to be refused, got %v", bad)
	}
	t.Logf("contract gate scoped correctly: %v", bad)
}

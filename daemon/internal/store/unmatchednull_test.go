package store

import (
	"context"
	"path/filepath"
	"testing"

	md "github.com/nyaungnicholas-wq/signaldeck/internal/marketdata"
)

// UnmatchedNullCount and the write guard disagreed about which rows NEED a
// frozen naive-persistence baseline.
//
// The write guard (AppendRegimeOutcome) refuses a label-less row only when
// structuralNullKinds[kind] is true -- filingsdrift21 is deliberately absent
// from that set, because there is no resolver that can compute a null for it,
// and the registry grades it NO BASELINE instead. The counter, however, counted
// EVERY post-epoch label-less row regardless of kind.
//
// So rows the write path is entitled to write were being counted as proof that
// "the deployed binary differs from source", and regime-outcome-runner refused
// to run because of them. On the live store that was 7 of 15 -- a false alarm
// large enough to block all structural grading indefinitely.
//
// A guard that fires on rows its own writer is allowed to produce is not strict,
// it is broken: it cannot be satisfied by any correct binary.
func TestUnmatchedNullCountIgnoresKindsTheWriteGuardExempts(t *testing.T) {
	st, err := Open(filepath.Join(t.TempDir(), "u.db"))
	if err != nil {
		t.Fatalf("open: %v", err)
	}
	defer st.Close() //nolint:errcheck
	ctx := context.Background()

	post := NullAmendmentEpoch + 3600
	day := post / 86400

	exempt, err := st.UpsertSymbol(ctx, "EXMPT", md.Stocks, "Exempt Kind Co")
	if err != nil {
		t.Fatalf("upsert exempt symbol: %v", err)
	}
	covered, err := st.UpsertSymbol(ctx, "COVRD", md.Stocks, "Covered Kind Co")
	if err != nil {
		t.Fatalf("upsert covered symbol: %v", err)
	}

	// A label-less row of an EXEMPT kind. The write guard permits this, so the
	// counter must not treat it as divergence.
	if _, err := st.w.ExecContext(ctx, `
		INSERT INTO regime_outcomes (symbol_id, kind, ts, day, horizon_days, regime, conviction, historical_accuracy, rank)
		VALUES (?, 'filingsdrift21', ?, ?, 21, 'up', 0.5, 0.5, 1)`, exempt.ID, post, day); err != nil {
		t.Fatalf("insert exempt row: %v", err)
	}
	n, err := st.UnmatchedNullCount(ctx)
	if err != nil {
		t.Fatalf("count: %v", err)
	}
	if n != 0 {
		t.Errorf("UnmatchedNullCount = %d after inserting a label-less row of an EXEMPT kind; "+
			"want 0. The write guard allows this row, so counting it makes the invariant "+
			"unsatisfiable and blocks structural grading forever.", n)
	}

	// A label-less row of a kind the guard DOES cover must still be counted --
	// this is the real divergence signal and must not be weakened.
	if _, err := st.w.ExecContext(ctx, `
		INSERT INTO regime_outcomes (symbol_id, kind, ts, day, horizon_days, regime, conviction, historical_accuracy, rank)
		VALUES (?, 'vol21', ?, ?, 21, 'elevated', 0.5, 0.5, 1)`, covered.ID, post, day); err != nil {
		t.Fatalf("insert structural row: %v", err)
	}
	n, err = st.UnmatchedNullCount(ctx)
	if err != nil {
		t.Fatalf("count: %v", err)
	}
	if n != 1 {
		t.Errorf("UnmatchedNullCount = %d after inserting a label-less row of a COVERED kind; "+
			"want 1. Narrowing to the exempt set must not blind the counter to real "+
			"divergence.", n)
	}
}

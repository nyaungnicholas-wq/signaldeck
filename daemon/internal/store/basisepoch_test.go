package store

import (
	"context"
	"database/sql"
	"testing"

	md "github.com/nyaungnicholas-wq/signaldeck/internal/marketdata"
)

// TestSeededOutcomeCarriesTheBasisEpoch pins the forward half of the basis
// marker: every outcome row this build seeds records WHICH basis produced it.
//
// Without it the column exists and stays NULL forever, which is the state that
// let a repaired model be retired on its predecessor's record — the graded
// population spanned a basis change and nothing in the row said so.
func TestSeededOutcomeCarriesTheBasisEpoch(t *testing.T) {
	st := openTestStore(t)
	ctx := context.Background()

	if err := st.UpsertPrediction(ctx, Prediction{
		SymbolID: 1, Horizon: md.Horizon("1d"), Ts: 1000,
		RawProb: 0.6, CalProb: 0.6, NUsed: 2, Components: "{}",
	}); err != nil {
		t.Fatalf("upsert: %v", err)
	}

	var got sql.NullInt64
	if err := st.db.QueryRowContext(ctx,
		`SELECT basis_epoch FROM prediction_outcomes WHERE symbol_id=1 AND horizon='1d' AND ts=1000`,
	).Scan(&got); err != nil {
		t.Fatalf("select: %v", err)
	}
	if !got.Valid || got.Int64 != BasisEpoch {
		t.Fatalf("basis_epoch = %v (valid=%v), want %d", got.Int64, got.Valid, BasisEpoch)
	}
}

// TestBenchmarkOutcomeCarriesTheBasisEpoch covers the second writer. The
// benchmark is the thing the model is graded AGAINST, so a basis marker on the
// model rows but not the null would leave the comparison unsplittable — the
// half that matters most.
func TestBenchmarkOutcomeCarriesTheBasisEpoch(t *testing.T) {
	st := openTestStore(t)
	ctx := context.Background()

	if err := st.SeedBenchmarkOutcome(ctx, 1, md.Horizon("1d#pm"), 1000, 1); err != nil {
		t.Fatalf("seed: %v", err)
	}

	var got sql.NullInt64
	if err := st.db.QueryRowContext(ctx,
		`SELECT basis_epoch FROM prediction_outcomes WHERE symbol_id=1 AND horizon='1d#pm' AND ts=1000`,
	).Scan(&got); err != nil {
		t.Fatalf("select: %v", err)
	}
	if !got.Valid || got.Int64 != BasisEpoch {
		t.Fatalf("basis_epoch = %v (valid=%v), want %d", got.Int64, got.Valid, BasisEpoch)
	}
}

// TestEvidenceRowSeedsNoOutcomeToStamp guards the interaction between the two
// changes: stamping must not become a reason to start seeding legless rows into
// the graded population. NUsed==0 still writes no outcome row at all.
func TestEvidenceRowSeedsNoOutcomeToStamp(t *testing.T) {
	st := openTestStore(t)
	ctx := context.Background()

	if err := st.UpsertPrediction(ctx, Prediction{
		SymbolID: 2, Horizon: md.Horizon("1d"), Ts: 2000,
		RawProb: 0.5, CalProb: 0.5, NUsed: 0, Components: "{}",
	}); err != nil {
		t.Fatalf("upsert: %v", err)
	}

	var n int
	if err := st.db.QueryRowContext(ctx,
		`SELECT COUNT(*) FROM prediction_outcomes WHERE symbol_id=2`).Scan(&n); err != nil {
		t.Fatalf("count: %v", err)
	}
	if n != 0 {
		t.Fatalf("evidence row seeded %d outcome rows, want 0", n)
	}
}

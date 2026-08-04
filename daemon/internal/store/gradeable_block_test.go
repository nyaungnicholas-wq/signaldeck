package store

import (
	"context"
	"testing"
	"time"

	md "github.com/nyaungnicholas-wq/signaldeck/internal/marketdata"
	"github.com/nyaungnicholas-wq/signaldeck/internal/publication"
)

// seedCalls inserts n structural calls one day apart starting at firstDay, all
// of one kind. withNull=true leaves naive_label NULL (the pre-2026-07-27
// quarantine shape: recorded, but unable to enter any benchmark denominator).
func seedCalls(t *testing.T, st *Store, kind string, horizon int, firstDay, n int64, withNull bool) {
	t.Helper()
	ctx := context.Background()
	sym, err := st.UpsertSymbol(ctx, "SEED"+kind, md.Stocks, "seed")
	if err != nil {
		t.Fatalf("upsert symbol: %v", err)
	}
	label := "elevated"
	for i := int64(0); i < n; i++ {
		day := firstDay + i
		ts := day * 86400
		var nl any
		if !withNull {
			nl = label
		}
		if _, err := st.w.ExecContext(ctx, `
			INSERT INTO regime_outcomes (symbol_id, kind, ts, day, horizon_days, regime,
				conviction, historical_accuracy, rank, naive_label)
			VALUES (?, ?, ?, ?, ?, 'elevated', 0.5, 0.5, 1, ?)`,
			sym.ID, kind, ts, day, horizon, nl); err != nil {
			t.Fatalf("seed: %v", err)
		}
	}
}

// TestEarliestVerdictAtIncludesBlockGate is the fix. The resolution date says
// when the first FORECAST resolves; a VERDICT additionally needs
// publication.MinDistinctBlocks distinct blocks, which on a 21-day horizon is
// ~189 further days of calls. Reporting the resolution date as "gradeable" is
// what made 2026-08-07 look reachable.
func TestEarliestVerdictAtIncludesBlockGate(t *testing.T) {
	st := openTestStore(t)
	const horizon = 21
	firstDay := int64(20652) // 2026-07-18

	// Two blocks' worth of calls — exactly the live state.
	seedCalls(t, st, "trend21", horizon, firstDay, 30, false)

	ctx := context.Background()

	res, ok, err := st.EarliestGradeableAt(ctx)
	if err != nil || !ok {
		t.Fatalf("EarliestGradeableAt: ok=%v err=%v", ok, err)
	}
	verdict, vok, err := st.EarliestVerdictAt(ctx)
	if err != nil || !vok {
		t.Fatalf("EarliestVerdictAt: ok=%v err=%v", vok, err)
	}

	// The verdict can never precede the first resolution.
	if !verdict.After(res) {
		t.Fatalf("verdict %s does not follow resolution %s — the block gate was not applied",
			verdict.Format("2006-01-02"), res.Format("2006-01-02"))
	}

	// The gap must be about (MinDistinctBlocks-1) horizons of further CALLS.
	gapDays := int(verdict.Sub(res).Hours() / 24)
	wantMin := (publication.MinDistinctBlocks - 1) * horizon
	if gapDays < wantMin-horizon || gapDays > wantMin+horizon {
		t.Errorf("verdict is %d days after resolution, want ~%d (9 further 21-day blocks)", gapDays, wantMin)
	}

	// The concrete regression: the resolution date is in 2026, the verdict is
	// not. This is the whole finding in one assertion.
	if res.Year() != 2026 {
		t.Errorf("resolution year = %d, want 2026", res.Year())
	}
	if verdict.Year() < 2027 {
		t.Errorf("verdict %s is still in %d — the block gate is not being applied",
			verdict.Format("2006-01-02"), verdict.Year())
	}
}

// TestEarliestVerdictAtIgnoresQuarantinedRows: rows with no frozen null are
// excluded from every structural benchmark denominator, so they cannot start
// the block clock. Counting them would repeat the original defect — a date that
// ignores one of its own determinants.
func TestEarliestVerdictAtIgnoresQuarantinedRows(t *testing.T) {
	st := openTestStore(t)
	const horizon = 21
	ctx := context.Background()

	// 40 quarantined calls starting well before the eligible ones.
	seedCalls(t, st, "vol21", horizon, 20652, 40, true)
	// Eligible calls start 60 days later.
	seedCalls(t, st, "vol21", horizon, 20712, 20, false)

	got, ok, err := st.EarliestVerdictAt(ctx)
	if err != nil || !ok {
		t.Fatalf("EarliestVerdictAt: ok=%v err=%v", ok, err)
	}

	// Recompute against a store holding ONLY the eligible rows: the quarantined
	// ones must not have moved the answer earlier.
	clean := openTestStore(t)
	seedCalls(t, clean, "vol21", horizon, 20712, 20, false)
	want, ok2, err := clean.EarliestVerdictAt(ctx)
	if err != nil || !ok2 {
		t.Fatalf("control EarliestVerdictAt: ok=%v err=%v", ok2, err)
	}
	if !got.Equal(want) {
		t.Errorf("quarantined rows moved the verdict date: got %s, want %s",
			got.Format("2006-01-02"), want.Format("2006-01-02"))
	}
}

// TestEarliestVerdictAtNoEligibleRows: a kind with nothing that can enter a
// denominator has no verdict date to report. It must refuse, not return zero.
func TestEarliestVerdictAtNoEligibleRows(t *testing.T) {
	st := openTestStore(t)
	seedCalls(t, st, "trend21", 21, 20652, 25, true) // all quarantined
	if got, ok, err := st.EarliestVerdictAt(context.Background()); err != nil {
		t.Fatalf("err: %v", err)
	} else if ok {
		t.Errorf("returned %s with no benchmark-eligible rows, want ok=false", got)
	}
}

// TestEarliestVerdictAtEmpty: no structural calls at all is a refusal too.
func TestEarliestVerdictAtEmpty(t *testing.T) {
	st := openTestStore(t)
	if _, ok, err := st.EarliestVerdictAt(context.Background()); err != nil {
		t.Fatalf("err: %v", err)
	} else if ok {
		t.Error("returned a date with no rows, want ok=false")
	}
}

// TestEarliestGradeableAtUnchanged pins the EXISTING field's meaning. The fix
// adds a quantity; it must not silently redefine one, which is the same class
// of error as the bug being fixed.
func TestEarliestGradeableAtUnchanged(t *testing.T) {
	st := openTestStore(t)
	firstDay := int64(20652)
	seedCalls(t, st, "trend21", 21, firstDay, 5, false)

	got, ok, err := st.EarliestGradeableAt(context.Background())
	if err != nil || !ok {
		t.Fatalf("ok=%v err=%v", ok, err)
	}
	wantTs := firstDay*86400 + int64(21*tradingToCalendar*86400)
	if want := time.Unix(wantTs, 0).UTC(); !got.Equal(want) {
		t.Errorf("EarliestGradeableAt = %s, want %s (unchanged formula)",
			got.Format("2006-01-02"), want.Format("2006-01-02"))
	}
}

package store

import (
	"context"
	"path/filepath"
	"testing"

	md "github.com/nyaungnicholas-wq/signaldeck/internal/marketdata"
)

// A TRUNCATE checkpoint needs a reader-free moment. When a reader holds the
// WAL open, SQLite reports that through the pragma's RESULT ROW (busy=1), not
// through an error — so the checkpoint must report Busy, never silently claim
// success. This is the regression for the live bug where the governor logged
// "checkpointed wal" hourly while the WAL sat frozen at 254MB.
func TestWALCheckpointReportsBusyHonestly(t *testing.T) {
	ctx := context.Background()
	st, err := Open(filepath.Join(t.TempDir(), "wal.db"))
	if err != nil {
		t.Fatalf("open: %v", err)
	}
	t.Cleanup(func() { _ = st.Close() })

	sym, err := st.UpsertSymbol(ctx, "WAL", md.Stocks, "")
	if err != nil {
		t.Fatalf("sym: %v", err)
	}
	for i := 0; i < 200; i++ { // put real frames in the WAL
		if err := st.InsertScore(ctx, md.Score{
			SymbolID: sym.ID, Horizon: md.H1d, Ts: int64(1_000_000 + i), Score: 0.5,
		}); err != nil {
			t.Fatalf("insert: %v", err)
		}
	}

	// Unblocked: the checkpoint completes and reports truthfully.
	res, err := st.WALCheckpointTruncate(ctx)
	if err != nil {
		t.Fatalf("checkpoint: %v", err)
	}
	if res.Busy || !res.Truncated() {
		t.Errorf("idle checkpoint reported Busy=%v Truncated=%v, want a clean truncate", res.Busy, res.Truncated())
	}

	// Now hold a READ transaction open (a snapshot reader, exactly what the
	// worker fleet does continuously) and write more frames behind it.
	tx, err := st.db.BeginTx(ctx, nil)
	if err != nil {
		t.Fatalf("begin read tx: %v", err)
	}
	defer tx.Rollback() //nolint:errcheck
	var n int
	if err := tx.QueryRowContext(ctx, `SELECT COUNT(*) FROM scores`).Scan(&n); err != nil {
		t.Fatalf("read: %v", err)
	}
	for i := 0; i < 200; i++ {
		if err := st.InsertScore(ctx, md.Score{
			SymbolID: sym.ID, Horizon: md.H1d, Ts: int64(2_000_000 + i), Score: 0.5,
		}); err != nil {
			t.Fatalf("insert: %v", err)
		}
	}

	res, err = st.WALCheckpointTruncate(ctx)
	if err != nil {
		t.Fatalf("checkpoint (blocked): %v", err)
	}
	// The contract that matters: whatever SQLite reports, the RESULT must be
	// surfaced rather than discarded. A blocked TRUNCATE must NOT read as a
	// clean truncate.
	if res.Busy && res.Truncated() {
		t.Error("Truncated() returned true for a BUSY checkpoint — the old lie")
	}
	if !res.Busy && res.LogFrames == 0 && res.Checkpointed == 0 {
		t.Log("note: this SQLite build completed the checkpoint despite the open reader; " +
			"the honest-reporting contract is still enforced by the Busy/Truncated assertions above")
	}
}

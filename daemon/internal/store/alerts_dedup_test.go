package store

import (
	"context"
	"path/filepath"
	"testing"
)

// TestInsertAlert_SweepRetryIdempotent: re-inserting a byte-identical alert
// (the partial-failure sweep-retry case) must be a silent no-op, while alerts
// differing in any key field (here: detail) stay distinct.
func TestInsertAlert_SweepRetryIdempotent(t *testing.T) {
	ctx := context.Background()
	st, err := Open(filepath.Join(t.TempDir(), "dedup.db"))
	if err != nil {
		t.Fatalf("open: %v", err)
	}
	defer func() { _ = st.Close() }()
	uid, err := st.CreateUser(ctx, "dedup-user", "h", false)
	if err != nil {
		t.Fatalf("user: %v", err)
	}
	a := Alert{UserID: uid, Kind: "breakout", Detail: "donchian 20d high", Ts: 1751500000}
	for i := 0; i < 3; i++ { // sweep retried twice
		if err := st.InsertAlert(ctx, a); err != nil {
			t.Fatalf("insert %d: %v", i, err)
		}
	}
	b := a
	b.Detail = "volume_spike 4.2x" // same bar, different breakout kind
	if err := st.InsertAlert(ctx, b); err != nil {
		t.Fatalf("insert distinct: %v", err)
	}
	rows, err := st.Alerts(ctx, uid, false, 100)
	if err != nil || len(rows) != 2 {
		t.Fatalf("alerts = %d (%v), want exactly 2 (identical collapsed, distinct kept)", len(rows), err)
	}
}

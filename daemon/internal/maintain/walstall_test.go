package maintain

import (
	"context"
	"fmt"
	"strings"
	"testing"
	"time"
)

// Reproduces 2026-10-01: one read statement (the scores-compactor's strip feed)
// stayed open for hours, the checkpoint stopped at the frame that snapshot
// pinned for three hourly passes while the WAL grew to 14 GB, and
// wal_checkpoint_starved never fired because the stall was keyed on the WAL
// length (which a live writer grows), not on the checkpointed frame. Slow by
// necessity: each blocked pass waits out the busy timeout on RESTART and TRUNCATE.
func TestGovernorNamesAPinnedReaderAndReclaimsTheWALAfterIt(t *testing.T) {
	if testing.Short() {
		t.Skip("each blocked checkpoint waits out its busy timeout")
	}
	ctx := context.Background()
	st := openStore(t)
	t.Setenv("SIGNALDECK_WAL_TRUNCATE_RETRY_SEC", "0")

	for i := 0; i < 5; i++ {
		if err := st.SetMeta(ctx, fmt.Sprintf("walstall_seed_%d", i), "x"); err != nil {
			t.Fatal(err)
		}
	}

	rows, err := st.DB().QueryContext(ctx, `SELECT k, v FROM meta`)
	if err != nil {
		t.Fatal(err)
	}
	if !rows.Next() {
		t.Fatal("no meta rows to pin a snapshot on")
	}
	t.Cleanup(func() { _ = rows.Close() })

	write := func(tag string) {
		for i := 0; i < 200; i++ {
			key := fmt.Sprintf("walstall_%s_%d", tag, i)
			if err := st.SetMeta(ctx, key, strings.Repeat("x", 4000)); err != nil {
				t.Fatal(err)
			}
		}
	}

	write("a")

	saturday := time.Date(2026, 8, 8, 12, 0, 0, 0, time.UTC)
	g := &StorageGovernor{St: st, Now: func() time.Time { return saturday }}

	msg1, err := g.Run(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if !contains(msg1, "WAL NOT truncated") {
		t.Fatalf("pass 1 with a pinned reader truncated anyway; the test pinned nothing: %q", msg1)
	}

	write("b")

	msg2, err := g.Run(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if !contains(msg2, "WAL NOT truncated") {
		t.Fatalf("pass 2 with a pinned reader truncated anyway; the test pinned nothing: %q", msg2)
	}

	var n int
	if err := st.DB().QueryRowContext(ctx, `SELECT count(*) FROM dq_events WHERE kind='wal_checkpoint_starved' AND detail LIKE '%same frame%'`).Scan(&n); err != nil {
		t.Fatal(err)
	}
	if n != 1 {
		t.Fatalf("wal_checkpoint_starved events = %d after two passes stalled behind one pinned reader, want 1\npass1: %s\npass2: %s", n, msg1, msg2)
	}

	if err := rows.Close(); err != nil {
		t.Fatal(err)
	}

	msg3, err := g.Run(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if contains(msg3, "WAL NOT truncated") {
		t.Fatalf("the reader is gone but TRUNCATE still failed: %q", msg3)
	}

	_, wal := st.FileSizes()
	const bound = 64 << 20
	if wal > bound {
		t.Fatalf("WAL is %d bytes after the reader released and TRUNCATE ran, want <= %d", wal, bound)
	}
	if wal != 0 {
		t.Logf("WAL %d bytes after truncate", wal)
	}

	if v, _ := st.GetMeta(ctx, "storage_wal_stall_runs"); v != "0" {
		t.Errorf("stall cursor = %q after a successful truncate, want 0", v)
	}

	t.Logf("pass1: %s\npass2: %s\npass3: %s", msg1, msg2, msg3)
}

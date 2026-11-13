package store

import (
	"bytes"
	"context"
	"log/slog"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// The read pool must name a snapshot held past longReadHold: the starver the
// WAL governor could only see as a frozen frame number.
func TestReadPoolNamesALongReadHold(t *testing.T) {
	var buf bytes.Buffer
	prev := slog.Default()
	slog.SetDefault(slog.New(slog.NewTextHandler(&buf, nil)))
	t.Cleanup(func() { slog.SetDefault(prev) })
	old := longReadHold
	t.Cleanup(func() { longReadHold = old })

	st, err := Open(filepath.Join(t.TempDir(), "rh.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = st.Close() })
	ctx := context.Background()

	longReadHold = time.Hour
	rows, err := st.DB().QueryContext(ctx, "SELECT 1")
	if err != nil {
		t.Fatal(err)
	}
	for rows.Next() {
	}
	_ = rows.Close()
	if strings.Contains(buf.String(), "long read hold") {
		t.Fatalf("a fast read was logged: %s", buf.String())
	}

	// Negative, not 0: a fast read on Windows measures exactly 0 (coarse clock).
	longReadHold = -time.Nanosecond
	rows, err = st.DB().QueryContext(ctx, "SELECT 1")
	if err != nil {
		t.Fatal(err)
	}
	for rows.Next() {
	}
	_ = rows.Close()
	out := buf.String()
	if !strings.Contains(out, "long read hold") || !strings.Contains(out, "kind=query") ||
		!strings.Contains(out, "TestReadPoolNamesALongReadHold") {
		t.Fatalf("the read pool did not name a held read and its caller: %q", out)
	}

	if _, err := st.DB().ExecContext(ctx, "SELECT 1"); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(buf.String(), "kind=statement") {
		t.Fatalf("a statement on the read pool (VACUUM INTO runs there) was not timed: %q", buf.String())
	}

	tx, err := st.DB().BeginTx(ctx, nil)
	if err != nil {
		t.Fatal(err)
	}
	_ = tx.Rollback()
	if !strings.Contains(buf.String(), "kind=transaction") {
		t.Fatalf("a read transaction was not timed: %q", buf.String())
	}
}

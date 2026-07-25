package store

import (
	"context"
	"path/filepath"
	"testing"

	md "github.com/nyaungnicholas-wq/signaldeck/internal/marketdata"
)

// The API's private read pool: a clone reads the same data, SHARES the single
// write connection (so the no-SQLITE_BUSY discipline is untouched), and closing
// it must not close the parent's writer.
func TestReaderCloneIsolatesReadsAndSharesWriter(t *testing.T) {
	ctx := context.Background()
	st, err := Open(filepath.Join(t.TempDir(), "clone.db"))
	if err != nil {
		t.Fatalf("open: %v", err)
	}
	t.Cleanup(func() { _ = st.Close() })

	sym, err := st.UpsertSymbol(ctx, "CLONE", md.Stocks, "")
	if err != nil {
		t.Fatalf("sym: %v", err)
	}

	clone, err := st.ReaderClone(4)
	if err != nil {
		t.Fatalf("clone: %v", err)
	}

	// Separate read pool...
	if clone.db == st.db {
		t.Error("clone shares the parent's read pool — no isolation")
	}
	// ...but the SAME writer (a second write conn would reintroduce SQLITE_BUSY).
	if clone.w != st.w {
		t.Error("clone opened its own write connection — the single-writer discipline is broken")
	}

	// The clone sees the parent's committed writes.
	got, err := clone.GetSymbolByID(ctx, sym.ID)
	if err != nil || got.Symbol != "CLONE" {
		t.Fatalf("clone read = (%+v, %v), want the parent's symbol", got, err)
	}

	// A write THROUGH the clone lands on the shared writer and is visible to
	// the parent (proves it is not a divergent connection).
	if _, err := clone.UpsertSymbol(ctx, "VIACLONE", md.Stocks, ""); err != nil {
		t.Fatalf("write via clone: %v", err)
	}
	if _, err := st.GetSymbol(ctx, "VIACLONE", md.Stocks); err != nil {
		t.Fatalf("parent cannot see the clone's write: %v", err)
	}

	// Closing the clone must NOT close the shared writer.
	if err := clone.Close(); err != nil {
		t.Fatalf("clone close: %v", err)
	}
	if _, err := st.UpsertSymbol(ctx, "AFTERCLOSE", md.Stocks, ""); err != nil {
		t.Fatalf("parent writes broken after clone.Close() — the clone closed the shared writer: %v", err)
	}
	if _, err := st.GetSymbolByID(ctx, sym.ID); err != nil {
		t.Fatalf("parent reads broken after clone.Close(): %v", err)
	}
}

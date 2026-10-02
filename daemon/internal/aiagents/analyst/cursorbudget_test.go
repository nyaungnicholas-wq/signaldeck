package analyst

import (
	"context"
	"database/sql"
	"testing"
	"time"

	"github.com/nyaungnicholas-wq/signaldeck/internal/store"
)

// holdWriteLock returns a function that releases a held write lock on the store's DB.
// It opens a separate connection, begins an immediate transaction, and after `d`
// rolls back and closes everything. The returned func waits for the goroutine to finish.
func holdWriteLock(t *testing.T, st *store.Store, d time.Duration) func() {
	db, err := sql.Open("sqlite", "file:"+st.Path()+"?_pragma=busy_timeout(5000)")
	if err != nil {
		t.Fatalf("sql.Open: %v", err)
	}
	ctx := context.Background()
	conn, err := db.Conn(ctx)
	if err != nil {
		t.Fatalf("db.Conn: %v", err)
	}
	if _, err = conn.ExecContext(ctx, "BEGIN IMMEDIATE"); err != nil {
		t.Fatalf("BEGIN IMMEDIATE: %v", err)
	}
	done := make(chan struct{})
	go func() {
		time.Sleep(d)
		if _, err := conn.ExecContext(context.Background(), "ROLLBACK"); err != nil {
			t.Errorf("ROLLBACK: %v", err)
		}
		if err := conn.Close(); err != nil {
			t.Errorf("conn.Close: %v", err)
		}
		if err := db.Close(); err != nil {
			t.Errorf("db.Close: %v", err)
		}
		close(done)
	}()
	return func() { <-done }
}

// The on-demand GET keeps its short cursor budget; the hourly run waits out a
// busy writer (it lost the cursor on 9 runs in 24h at 3 s, after boots and
// checkpoint passes).
func TestCursorBudgetsByPath(t *testing.T) {
	// 1. Production sanity checks
	if cursorWriteBudget > 10*time.Second {
		t.Fatalf("on-demand cursorWriteBudget = %v; the GET must not wait on a held writer", cursorWriteBudget)
	}
	if scheduledCursorWriteBudget <= 5*time.Second {
		t.Fatalf("scheduledCursorWriteBudget = %v; it must outlast the main writer's 5s busy wait, or the hourly run keeps losing the cursor to a busy writer", scheduledCursorWriteBudget)
	}

	// 2. Override for test and restore
	prevOn, prevSched := cursorWriteBudget, scheduledCursorWriteBudget
	cursorWriteBudget, scheduledCursorWriteBudget = 300*time.Millisecond, 4*time.Second
	t.Cleanup(func() {
		cursorWriteBudget, scheduledCursorWriteBudget = prevOn, prevSched
	})

	// 3. on-demand gives up
	t.Run("on-demand gives up", func(t *testing.T) {
		st := openStore(t)
		_ = seedSymbolWithScore(t, st)
		wait := holdWriteLock(t, st, 2*time.Second)
		t0 := time.Now()
		b, err := Run(context.Background(), &fakeClient{enabled: true, model: "m", reply: "MARKET: flat."}, st)
		el := time.Since(t0)
		wait()
		if err != nil {
			t.Fatalf("Run: %v", err)
		}
		if b.Market == "" {
			t.Fatal("the brief was lost with the cursor write")
		}
		if v, _ := st.GetMeta(context.Background(), cursorKey); v != "" {
			t.Fatalf("on-demand Run waited out a 2s held writer to save the cursor (%q after %v); its budget is %v", v, el, cursorWriteBudget)
		}
		t.Logf("on-demand Run returned after %v", el)
	})

	// 4. scheduled waits it out
	t.Run("scheduled waits it out", func(t *testing.T) {
		st := openStore(t)
		_ = seedSymbolWithScore(t, st)
		wait := holdWriteLock(t, st, 1*time.Second)
		t0 := time.Now()
		_, err := RunScheduled(context.Background(), &fakeClient{enabled: true, model: "m", reply: "MARKET: flat."}, st)
		el := time.Since(t0)
		wait()
		if err != nil {
			t.Fatalf("RunScheduled: %v", err)
		}
		if v, _ := st.GetMeta(context.Background(), cursorKey); v == "" {
			t.Fatalf("scheduled run lost the cursor to a writer held for 1s (returned after %v); its budget is %v", el, scheduledCursorWriteBudget)
		}
		t.Logf("scheduled run saved the cursor after %v", el)
	})
}

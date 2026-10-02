package store

import (
	"context"
	"database/sql"
	"testing"
	"time"
)

// SQLite in WAL mode. With a DEFERRED BEGIN, a transaction that reads first
// takes its snapshot without the write lock; if another connection commits
// before it writes, its first write cannot upgrade and fails at once with
// SQLITE_BUSY (BUSY_SNAPSHOT), which busy_timeout does not retry.
// Main-writer transactions such as AppendLedger read the chain head and then
// insert, and they share the file with the account writer. BEGIN IMMEDIATE
// takes the write lock at BEGIN, so the other writer waits on its busy_timeout
// instead. Reads use the read pool, and a ReadOnly transaction stays DEFERRED.
func TestMainWriterReadThenWriteSurvivesAnAccountCommit(t *testing.T) {
	st := openTemp(t)
	ctx := context.Background()

	if _, err := st.w.ExecContext(ctx, "CREATE TABLE txl_probe (v INTEGER)"); err != nil {
		t.Fatalf("create table: %v", err)
	}
	if _, err := st.w.ExecContext(ctx, "INSERT INTO txl_probe (v) VALUES (0)"); err != nil {
		t.Fatalf("insert seed: %v", err)
	}

	tx, err := st.w.BeginTx(ctx, nil)
	if err != nil {
		t.Fatalf("begin tx: %v", err)
	}
	defer func() { _ = tx.Rollback() }()

	var n int
	if err := tx.QueryRowContext(ctx, "SELECT count(*) FROM txl_probe").Scan(&n); err != nil {
		t.Fatalf("query count: %v", err)
	}

	done := make(chan error, 1)
	go func() {
		release := st.Priority()
		_, err := st.authW().ExecContext(ctx, "INSERT INTO txl_probe (v) VALUES (2)")
		release()
		done <- err
	}()

	var early error
	finishedEarly := false
	select {
	case early = <-done:
		finishedEarly = true
	case <-time.After(500 * time.Millisecond):
	}

	if finishedEarly && early != nil {
		t.Fatalf("account write failed: %v", early)
	}

	t0 := time.Now()
	var m int
	if err := st.db.QueryRowContext(ctx, "SELECT count(*) FROM txl_probe").Scan(&m); err != nil {
		t.Fatalf("read pool blocked by a main-writer transaction: %v", err)
	}
	if el := time.Since(t0); el > time.Second {
		t.Fatalf("read pool waited %v behind a main-writer transaction", el)
	}

	if _, err := tx.ExecContext(ctx, "INSERT INTO txl_probe (v) VALUES (1)"); err != nil {
		t.Fatalf("main writer's read-then-write transaction failed after an account write committed under it (account write finished first: %v): %v", finishedEarly, err)
	}
	if err := tx.Commit(); err != nil {
		t.Fatal(err)
	}

	if !finishedEarly {
		select {
		case err := <-done:
			if err != nil {
				t.Fatalf("account write after the transaction: %v", err)
			}
		case <-time.After(15 * time.Second):
			t.Fatal("account write never finished")
		}
	}

	var total int
	if err := st.db.QueryRowContext(ctx, "SELECT count(*) FROM txl_probe").Scan(&total); err != nil {
		t.Fatalf("final count: %v", err)
	}
	if total != 3 {
		t.Fatalf("%d rows, want 3 (seed, main writer, account writer)", total)
	}

	t.Logf("account write finished before the transaction wrote: %v", finishedEarly)
}

// A ReadOnly transaction on the main writer stays DEFERRED: it must not take
// the write lock and hold an account write behind it.
func TestReadOnlyMainWriterTxDoesNotTakeTheWriteLock(t *testing.T) {
	st := openTemp(t)
	ctx := context.Background()

	if _, err := st.w.ExecContext(ctx, "CREATE TABLE ro_probe (v INTEGER)"); err != nil {
		t.Fatalf("create table: %v", err)
	}
	if _, err := st.w.ExecContext(ctx, "INSERT INTO ro_probe (v) VALUES (0)"); err != nil {
		t.Fatalf("insert seed: %v", err)
	}

	rtx, err := st.w.BeginTx(ctx, &sql.TxOptions{ReadOnly: true})
	if err != nil {
		t.Fatalf("begin readonly tx: %v", err)
	}
	defer func() { _ = rtx.Rollback() }()

	var n int
	if err := rtx.QueryRowContext(ctx, "SELECT count(*) FROM ro_probe").Scan(&n); err != nil {
		t.Fatalf("query count: %v", err)
	}

	t0 := time.Now()
	release := st.Priority()
	_, err = st.authW().ExecContext(ctx, "INSERT INTO ro_probe (v) VALUES (2)")
	release()
	if err != nil {
		t.Fatalf("account write behind a read-only main-writer transaction: %v", err)
	}
	if el := time.Since(t0); el > 2*time.Second {
		t.Fatalf("account write waited %v behind a READ-ONLY transaction: it took the write lock", el)
	}

	if err := rtx.Rollback(); err != nil {
		t.Fatal(err)
	}
}

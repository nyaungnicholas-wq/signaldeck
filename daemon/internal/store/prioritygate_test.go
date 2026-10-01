package store

import (
	"bytes"
	"context"
	"log/slog"
	"strings"
	"path/filepath"
	"sync/atomic"
	"testing"
	"time"
)

// A worker that writes back-to-back on the main writer must not starve an
// account write that asked for priority. Reproduces the 2026-09-30 live shape:
// each worker transaction holds the SQLite lock for 40ms and the next begins
// immediately, so without the gate the account connection's busy handler keeps
// losing the race.
func TestPriorityWriteBeatsASaturatedWriter(t *testing.T) {
	st, err := Open(filepath.Join(t.TempDir(), "gate.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = st.Close() }()
	ctx := context.Background()
	if _, err := st.w.ExecContext(ctx, `CREATE TABLE gate_probe (v INTEGER)`); err != nil {
		t.Fatal(err)
	}

	stop := make(chan struct{})
	var commits atomic.Int64
	done := make(chan struct{})
	go func() {
		defer close(done)
		for {
			select {
			case <-stop:
				return
			default:
			}
			tx, err := st.w.BeginTx(ctx, nil)
			if err != nil {
				continue
			}
			_, _ = tx.ExecContext(ctx, `INSERT INTO gate_probe (v) VALUES (1)`)
			time.Sleep(40 * time.Millisecond) // hold the write lock
			if tx.Commit() == nil {
				commits.Add(1)
			}
		}
	}()
	time.Sleep(150 * time.Millisecond) // let the saturation start

	for i := 0; i < 5; i++ {
		release := st.Priority()
		t0 := time.Now()
		_, err := st.authW().ExecContext(ctx, `INSERT INTO gate_probe (v) VALUES (2)`)
		release()
		if err != nil {
			t.Fatalf("priority write %d failed: %v", i, err)
		}
		if el := time.Since(t0); el > time.Second {
			t.Fatalf("priority write %d took %v against a saturated writer; want well under 1s", i, el)
		}
	}
	close(stop)
	<-done
	if commits.Load() < 3 {
		t.Fatalf("the worker loop barely ran (%d commits); the test did not saturate anything", commits.Load())
	}
}

// A TRUNCATE checkpoint that is waiting for a reader holds the WRITE lock the
// whole time it waits. With the store's 5s busy_timeout, the governor's
// once-a-second retries locked account writes out for minutes (2026-09-30).
// An account write behind a reader-blocked checkpoint must still land fast.
func TestCheckpointWaitingOnAReaderDoesNotLockOutAccountWrites(t *testing.T) {
	// The bound under test is "one checkpoint wait, then the account write",
	// so shrink the wait to keep the test fast; the production value is pinned
	// against the account budget in TestCheckpointBusyFitsTheAccountBudget.
	prev := checkpointBusy
	checkpointBusy = 300 * time.Millisecond
	defer func() { checkpointBusy = prev }()
	st, err := Open(filepath.Join(t.TempDir(), "ckpt.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = st.Close() }()
	ctx := context.Background()
	if _, err := st.w.ExecContext(ctx, `CREATE TABLE ckpt_probe (v INTEGER)`); err != nil {
		t.Fatal(err)
	}
	// A reader pinned to an old snapshot, then newer frames it has not seen:
	// TRUNCATE cannot finish until that reader goes away.
	rtx, err := st.db.BeginTx(ctx, nil)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = rtx.Rollback() }()
	var n int
	if err := rtx.QueryRowContext(ctx, `SELECT count(*) FROM ckpt_probe`).Scan(&n); err != nil {
		t.Fatal(err)
	}
	for i := 0; i < 20; i++ {
		if _, err := st.w.ExecContext(ctx, `INSERT INTO ckpt_probe (v) VALUES (1)`); err != nil {
			t.Fatal(err)
		}
	}
	done := make(chan WALCheckpointResult, 1)
	go func() {
		r, _ := st.WALCheckpointTruncate(ctx)
		done <- r
	}()
	time.Sleep(50 * time.Millisecond) // let the checkpoint take the write lock
	t0 := time.Now()
	release := st.Priority()
	_, err = st.authW().ExecContext(ctx, `INSERT INTO ckpt_probe (v) VALUES (2)`)
	release()
	if err != nil {
		t.Fatalf("account write behind a checkpoint failed: %v", err)
	}
	if el := time.Since(t0); el > 1500*time.Millisecond {
		t.Fatalf("account write took %v behind a reader-blocked checkpoint; want well under 1.5s", el)
	}
	if r := <-done; !r.Busy {
		t.Fatalf("the checkpoint was not blocked by the reader (%+v); the test exercised nothing", r)
	}
	// The connection must be back on the store's normal busy_timeout afterwards.
	var bt int
	if err := st.w.QueryRowContext(ctx, `PRAGMA busy_timeout`).Scan(&bt); err != nil || bt != 5000 {
		t.Fatalf("main writer busy_timeout = %d (%v), want 5000 restored", bt, err)
	}
}

// A write transaction held past longHold is logged with the code that opened
// it, so the job locking people out of sign-in can be named from the log.
func TestLongWriteHoldIsLoggedWithItsCaller(t *testing.T) {
	st, err := Open(filepath.Join(t.TempDir(), "hold.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = st.Close() }()
	var buf bytes.Buffer
	prev, prevHold := slog.Default(), longHold
	slog.SetDefault(slog.New(slog.NewTextHandler(&buf, nil)))
	longHold = 20 * time.Millisecond
	defer func() { slog.SetDefault(prev); longHold = prevHold }()

	tx, err := st.w.BeginTx(context.Background(), nil)
	if err != nil {
		t.Fatal(err)
	}
	time.Sleep(40 * time.Millisecond)
	if err := tx.Commit(); err != nil {
		t.Fatal(err)
	}
	out := buf.String()
	if !strings.Contains(out, "long write-lock hold") || !strings.Contains(out, "TestLongWriteHoldIsLoggedWithItsCaller") {
		t.Fatalf("no named long-hold line; log was: %q", out)
	}
	t.Log(strings.TrimSpace(out))
}

// A TRUNCATE checkpoint must be able to outlast a reader that finishes within
// a second or two; otherwise it never meets its reader-free instant and the WAL
// grows without bound. 200ms (d63f47b) failed exactly this on the live box:
// 18 governor runs, zero truncations, WAL at 1.28 GB.
func TestTruncateOutlastsAShortReader(t *testing.T) {
	st, err := Open(filepath.Join(t.TempDir(), "trunc.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = st.Close() }()
	ctx := context.Background()
	if _, err := st.w.ExecContext(ctx, `CREATE TABLE trunc_probe (v INTEGER)`); err != nil {
		t.Fatal(err)
	}
	rtx, err := st.db.BeginTx(ctx, nil)
	if err != nil {
		t.Fatal(err)
	}
	var n int
	if err := rtx.QueryRowContext(ctx, `SELECT count(*) FROM trunc_probe`).Scan(&n); err != nil {
		t.Fatal(err)
	}
	for i := 0; i < 50; i++ {
		if _, err := st.w.ExecContext(ctx, `INSERT INTO trunc_probe (v) VALUES (1)`); err != nil {
			t.Fatal(err)
		}
	}
	// The reader finishes 700ms into the checkpoint's wait.
	go func() {
		time.Sleep(700 * time.Millisecond)
		_ = rtx.Rollback()
	}()
	res, err := st.WALCheckpointTruncate(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if res.Busy {
		t.Fatalf("TRUNCATE gave up on a reader that finished after 700ms (%+v): checkpointBusy=%v is too short to ever truncate a busy WAL", res, checkpointBusy)
	}
}

// The production wait must leave the account writer (busy_timeout 12s) room to
// get in after one full checkpoint wait.
func TestCheckpointBusyFitsTheAccountBudget(t *testing.T) {
	if checkpointBusy < time.Second || checkpointBusy > 6*time.Second {
		t.Fatalf("checkpointBusy = %v; want 1-6s: long enough to drain readers, short enough for a 12s sign-in budget", checkpointBusy)
	}
}

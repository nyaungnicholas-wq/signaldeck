package store

import (
	"context"
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

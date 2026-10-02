package maintain

import (
	"context"
	"fmt"
	"strings"
	"testing"
	"time"
)

// The governor's TRUNCATE retries ran back to back, each holding the single
// main-writer connection and the write lock for its 5 s reader wait, for up to
// the 300 s budget; worker writes and sign-in queued behind them (2026-10-01
// 00:58-01:07: sign-in 14-40 s). Both must get through within 2 s while the
// retries run, and the pass must still truncate once the reader goes.
func TestWritesGetThroughDuringTheGovernorsTruncateRetries(t *testing.T) {
	if testing.Short() {
		t.Skip("waits out two 5s checkpoint rungs before the retries start")
	}
	ctx := context.Background()
	st := openStore(t)
	t.Setenv("SIGNALDECK_WAL_TRUNCATE_RETRY_SEC", "30")
	for i := 0; i < 5; i++ {
		if err := st.SetMeta(ctx, fmt.Sprintf("retry_seed_%d", i), "x"); err != nil {
			t.Fatalf("SetMeta seed %d: %v", i, err)
		}
	}
	rows, err := st.DB().QueryContext(ctx, "SELECT k, v FROM meta")
	if err != nil {
		t.Fatalf("DB query: %v", err)
	}
	if !rows.Next() {
		t.Fatal("no meta rows to pin a snapshot on")
	}
	t.Cleanup(func() { _ = rows.Close() })
	for i := 0; i < 200; i++ {
		if err := st.SetMeta(ctx, fmt.Sprintf("retry_past_%d", i), strings.Repeat("x", 4000)); err != nil {
			t.Fatalf("SetMeta past %d: %v", i, err)
		}
	}
	saturday := time.Date(2026, 8, 8, 12, 0, 0, 0, time.UTC)
	g := &StorageGovernor{St: st, Now: func() time.Time { return saturday }}
	done := make(chan string, 1)
	go func() {
		msg, err := g.Run(ctx)
		if err != nil {
			msg = "error: " + err.Error()
		}
		done <- msg
	}()
	time.Sleep(12 * time.Second) // RESTART and first TRUNCATE each wait ~5s for pinned reader; retries now running
	var worstAcct, worstMain time.Duration
	for i := 0; i < 8; i++ {
		select {
		case msg := <-done:
			t.Fatalf("the governor pass ended before round %d (%q); writes so far waited up to %v (sign-in) and %v (worker)", i, msg, worstAcct, worstMain)
		default:
		}
		t0 := time.Now()
		release := st.Priority()
		_, err := st.IncrAskCount(ctx, int64(i+1), "2026-10-02")
		release()
		if err != nil {
			t.Fatalf("account write %d: %v", i, err)
		}
		if el := time.Since(t0); el > worstAcct {
			worstAcct = el
		}
		t1 := time.Now()
		if err := st.SetMeta(ctx, fmt.Sprintf("retry_live_%d", i), "x"); err != nil {
			t.Fatalf("worker write %d: %v", i, err)
		}
		if el := time.Since(t1); el > worstMain {
			worstMain = el
		}
		time.Sleep(700 * time.Millisecond)
	}
	if worstAcct > 2*time.Second {
		t.Errorf("a sign-in write waited %v behind the governor's TRUNCATE retries, want < 2s", worstAcct)
	}
	if worstMain > 2*time.Second {
		t.Errorf("a worker write waited %v behind the governor's TRUNCATE retries, want < 2s", worstMain)
	}
	if err := rows.Close(); err != nil {
		t.Fatal(err)
	}
	var msg string
	select {
	case msg = <-done:
	case <-time.After(60 * time.Second):
		t.Fatal("the governor pass did not finish after the reader released")
	}
	if !contains(msg, "attempts") {
		t.Fatalf("the pass never reached its TRUNCATE retries, so the writes above were not measured against them: %q", msg)
	}
	if contains(msg, "WAL NOT truncated") {
		t.Fatalf("the reader is gone but the retries never truncated: %q", msg)
	}
	t.Logf("worst sign-in write %v, worst worker write %v; pass: %s", worstAcct, worstMain, msg)
}

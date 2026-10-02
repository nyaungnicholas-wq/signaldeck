package maintain

import (
	"context"
	"database/sql"
	"fmt"
	"math/rand"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/nyaungnicholas-wq/signaldeck/internal/store"
)

// pinMetaReader opens a read on an old snapshot and writes past it, so every
// checkpoint rung loses until the returned rows close.
func pinMetaReader(t *testing.T, st *store.Store) *sql.Rows {
	ctx := context.Background()
	for i := 0; i < 5; i++ {
		if err := st.SetMeta(ctx, fmt.Sprintf("gov_seed_%d", i), "x"); err != nil {
			t.Fatalf("SetMeta seed %d: %v", i, err)
		}
	}
	rows, err := st.DB().QueryContext(ctx, "SELECT k, v FROM meta")
	if err != nil {
		t.Fatalf("DB.QueryContext: %v", err)
	}
	if !rows.Next() {
		t.Fatalf("nothing to pin")
	}
	t.Cleanup(func() { _ = rows.Close() })
	for i := 0; i < 200; i++ {
		if err := st.SetMeta(ctx, fmt.Sprintf("gov_past_%d", i), strings.Repeat("x", 4000)); err != nil {
			t.Fatalf("SetMeta past %d: %v", i, err)
		}
	}
	return rows
}

// runGovernor runs one governor pass (market closed) in the background.
func runGovernor(st *store.Store) <-chan string {
	saturday := time.Date(2026, 8, 8, 12, 0, 0, 0, time.UTC)
	g := &StorageGovernor{St: st, Now: func() time.Time { return saturday }}
	done := make(chan string, 1)
	go func() {
		msg, err := g.Run(context.Background())
		if err != nil {
			msg = "error: " + err.Error()
		}
		done <- msg
	}()
	return done
}

// A retry has to outwait the readers already open: while TRUNCATE holds the
// write lock, readers that start meanwhile read the database file. Measured:
// against overlapping 200-800ms readers TRUNCATE won 0/12 attempts with a 100ms
// wait and 11/12 with 2s; against 0.5-2.5s readers, 0/12 at 2s and 9/12 at 5s.
// The 100ms retries (2810f53) lost 261 in a row live on 2026-10-02 03:31.
func TestTruncateRetriesWinAgainstOverlappingReaders(t *testing.T) {
	if testing.Short() {
		t.Skip("waits out the checkpoint rungs")
	}
	ctx := context.Background()
	st := openStore(t)
	t.Setenv("SIGNALDECK_WAL_TRUNCATE_RETRY_SEC", "60")
	rows := pinMetaReader(t, st)

	stop := make(chan struct{})
	var wg sync.WaitGroup
	for i := 0; i < 10; i++ {
		wg.Add(1)
		go func(id int) {
			defer wg.Done()
			r := rand.New(rand.NewSource(int64(id)))
			for {
				select {
				case <-stop:
					return
				default:
				}
				rr, err := st.DB().QueryContext(ctx, "SELECT k, v FROM meta")
				if err != nil {
					continue
				}
				rr.Next() // hold the snapshot while "processing"
				time.Sleep(time.Duration(200+r.Intn(600)) * time.Millisecond)
				_ = rr.Close()
			}
		}(i)
	}
	wg.Add(1)
	go func() {
		defer wg.Done()
		var i int
		for {
			select {
			case <-stop:
				return
			default:
			}
			_ = st.SetMeta(ctx, fmt.Sprintf("gov_load_%d", i%500), "y")
			i++
			time.Sleep(50 * time.Millisecond)
		}
	}()

	var stopOnce sync.Once
	t.Cleanup(func() {
		stopOnce.Do(func() {
			close(stop)
			wg.Wait()
		})
	})

	done := runGovernor(st)
	time.Sleep(12 * time.Second) // RESTART and the first TRUNCATE lose to the pin
	_ = rows.Close()
	released := time.Now()

	var msg string
	select {
	case msg = <-done:
	case <-time.After(90 * time.Second):
		t.Fatalf("governor pass did not finish")
	}
	took := time.Since(released)
	if !contains(msg, "attempts") {
		t.Fatalf("the pass never retried, so this measured nothing: %q", msg)
	}
	if contains(msg, "WAL NOT truncated") {
		t.Fatalf("no TRUNCATE retry won against overlapping short readers once the pin was gone: %q", msg)
	}
	// The next retry after the release must win: one gap plus one wait. Count
	// attempts, not wall time: under -race on a loaded host a correct pass took
	// 24s (3 attempts) while the 100ms-retry code it guards against needed 29.
	m := regexp.MustCompile(`\((\d+) attempts\)`).FindStringSubmatch(msg)
	if m == nil {
		t.Fatalf("no attempt count in the pass detail: %q", msg)
	}
	if n, _ := strconv.Atoi(m[1]); n > 10 {
		t.Fatalf("the pass needed %d TRUNCATE attempts (%v after the pin went); the retries did not outwait the overlapping readers: %q", n, took, msg)
	}
	t.Logf("truncated %v after the pin went; pass: %s", took, msg)
}

// The retries used to run back to back, each holding the main-writer connection
// and the write lock for its 5s reader wait, for up to the 300s budget; sign-in
// queued behind them (2026-10-01 00:58-01:07: sign-in 14-40s). Spaced 10s apart,
// most writes must land at once and none may wait longer than one attempt.
func TestWritesGetThroughDuringTheGovernorsTruncateRetries(t *testing.T) {
	if testing.Short() {
		t.Skip("waits out the checkpoint rungs")
	}
	ctx := context.Background()
	st := openStore(t)
	t.Setenv("SIGNALDECK_WAL_TRUNCATE_RETRY_SEC", "60")
	rows := pinMetaReader(t, st)
	done := runGovernor(st)
	time.Sleep(12 * time.Second)

	var acct, work []time.Duration
	deadline := time.Now().Add(30 * time.Second)
	for i := 0; time.Now().Before(deadline); i++ {
		select {
		case msg := <-done:
			t.Fatalf("the governor pass ended after %d writes (%q), before the measurement finished", i, msg)
		default:
		}
		t0 := time.Now()
		release := st.Priority()
		_, err := st.IncrAskCount(ctx, int64(i+1), "2026-10-02")
		release()
		if err != nil {
			t.Fatalf("account write %d: %v", i, err)
		}
		acct = append(acct, time.Since(t0))

		t1 := time.Now()
		if err := st.SetMeta(ctx, fmt.Sprintf("gov_live_%d", i), "x"); err != nil {
			t.Fatalf("worker write %d: %v", i, err)
		}
		work = append(work, time.Since(t1))

		time.Sleep(500 * time.Millisecond)
	}

	check := func(name string, ds []time.Duration) {
		if len(ds) == 0 {
			return
		}
		sorted := make([]time.Duration, len(ds))
		copy(sorted, ds)
		sort.Slice(sorted, func(i, j int) bool { return sorted[i] < sorted[j] })
		slow := 0
		for _, d := range sorted {
			if d > time.Second {
				slow++
			}
		}
		median := sorted[len(sorted)/2]
		worst := sorted[len(sorted)-1]
		t.Logf("%s writes: n=%d median=%v max=%v slow(>1s)=%d", name, len(ds), median, worst, slow)
		if slow*2 > len(ds) {
			t.Errorf("%s: %d of %d writes waited over 1s; the retries are holding the writer most of the time", name, slow, len(ds))
		}
		if worst > 7*time.Second {
			t.Errorf("%s: a write waited %v, longer than one 5s retry", name, worst)
		}
	}
	check("sign-in", acct)
	check("worker", work)

	if err := rows.Close(); err != nil {
		t.Fatal(err)
	}
	var msg string
	select {
	case msg = <-done:
	case <-time.After(60 * time.Second):
		t.Fatalf("governor pass did not finish after the reader released")
	}
	if !contains(msg, "attempts") {
		t.Fatalf("the pass never reached its retries: %q", msg)
	}
	if contains(msg, "WAL NOT truncated") {
		t.Fatalf("the reader is gone but the retries never truncated: %q", msg)
	}
	t.Logf("pass: %s", msg)
}

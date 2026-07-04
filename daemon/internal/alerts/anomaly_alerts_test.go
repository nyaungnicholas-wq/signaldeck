// Signal8 wave Stage 3: fan-out tests for the three anomaly alert kinds —
// watchlist scoping, cursor advancement, and sweep idempotency. Separate
// file from alerts_test.go so parallel edits never collide.
package alerts

import (
	"context"
	"strings"
	"testing"
	"time"

	"github.com/nyaungnicholas-wq/signaldeck/internal/store"
)

func seedAnomaly(t *testing.T, st *store.Store, symbolID, ts int64, kind string, z float64, detail string) {
	t.Helper()
	fresh, err := st.InsertAnomaly(context.Background(), store.AnomalyRow{
		SymbolID: symbolID, Ts: ts, Kind: kind, Z: z, Detail: detail,
	})
	if err != nil || !fresh {
		t.Fatalf("seed anomaly %s: fresh=%v err=%v", kind, fresh, err)
	}
}

func TestRunnerAnomalyFanOut(t *testing.T) {
	ctx := context.Background()
	st := newTestStore(t)
	uid, sym := seedUserWithSymbol(t, st, "alice", "NVDA")
	// Bob does NOT watch NVDA — he must get nothing.
	uid2, _ := seedUserWithSymbol(t, st, "bob", "AAPL")

	now := time.Now()
	seedAnomaly(t, st, sym.ID, now.Unix()-300, KindAnomalyImbalance, 3.1,
		"unusual buy-side volume: up/down-volume share +0.72 over last 30×1m bars (z=+3.1 vs trailing baseline of 9 windows) — volume-side proxy (no order-book on free stock data); descriptive, not a prediction")
	seedAnomaly(t, st, sym.ID, now.Unix()-240, KindAnomalyVol, 2.8, "unusual volatility: …")
	seedAnomaly(t, st, sym.ID, now.Unix()-180, KindAnomalyVolume, 3.4, "unusual volume: …")

	r := &Runner{St: st, Notify: func(string) error { return nil }}
	if _, err := r.Run(ctx); err != nil {
		t.Fatalf("run: %v", err)
	}

	got, err := st.Alerts(ctx, uid, false, 100)
	if err != nil {
		t.Fatalf("alerts: %v", err)
	}
	kinds := map[string]int{}
	for _, a := range got {
		kinds[a.Kind]++
		if a.Symbol != "NVDA" {
			t.Errorf("alert for wrong symbol: %+v", a)
		}
	}
	if kinds[KindAnomalyImbalance] != 1 || kinds[KindAnomalyVol] != 1 || kinds[KindAnomalyVolume] != 1 {
		t.Fatalf("kinds = %v (%d alerts)", kinds, len(got))
	}
	// The fanned-out detail keeps the detector's honest wording (z, baseline,
	// proxy label) and prefixes the symbol — the "unusual buy pressure on X
	// (z=…)" ping shape.
	for _, a := range got {
		if a.Kind != KindAnomalyImbalance {
			continue
		}
		if !strings.HasPrefix(a.Detail, "NVDA: ") ||
			!strings.Contains(a.Detail, "z=+3.1") ||
			!strings.Contains(a.Detail, "volume-side proxy (no order-book on free stock data)") {
			t.Errorf("imbalance alert detail = %q", a.Detail)
		}
	}

	// Watchlist scoping: the non-watcher got nothing.
	if other, _ := st.Alerts(ctx, uid2, false, 100); len(other) != 0 {
		t.Errorf("bob (not watching NVDA) got %d alerts", len(other))
	}

	// Idempotency: a second sweep advances nothing and duplicates nothing.
	if _, err := r.Run(ctx); err != nil {
		t.Fatalf("run 2: %v", err)
	}
	if got2, _ := st.Alerts(ctx, uid, false, 100); len(got2) != len(got) {
		t.Fatalf("second sweep duplicated: %d → %d", len(got), len(got2))
	}

	// Cursor pickup: a NEW anomaly (different kind-hour) alerts on the next
	// sweep without re-delivering the old ones.
	seedAnomaly(t, st, sym.ID, now.Unix()+3600, KindAnomalyImbalance, -2.9,
		"unusual sell pressure: … — descriptive, not a prediction")
	if _, err := r.Run(ctx); err != nil {
		t.Fatalf("run 3: %v", err)
	}
	got3, _ := st.Alerts(ctx, uid, false, 100)
	if len(got3) != len(got)+1 {
		t.Fatalf("new anomaly not alerted: %d → %d", len(got), len(got3))
	}
}

func TestRunnerAnomalyFirstSweepSkipsAncient(t *testing.T) {
	ctx := context.Background()
	st := newTestStore(t)
	uid, sym := seedUserWithSymbol(t, st, "alice", "TSLA")

	// An anomaly older than 24h must NOT fire on a first (cold-cursor) sweep…
	old := time.Now().Add(-48 * time.Hour).Unix()
	seedAnomaly(t, st, sym.ID, old, KindAnomalyVolume, 5.0, "ancient volume anomaly")

	r := &Runner{St: st, Notify: func(string) error { return nil }}
	if _, err := r.Run(ctx); err != nil {
		t.Fatalf("run: %v", err)
	}
	if got, _ := st.Alerts(ctx, uid, false, 100); len(got) != 0 {
		t.Fatalf("ancient anomaly alerted on first sweep: %+v", got)
	}

	// …and the cursor started at the table max, so it can never fire later.
	if _, err := r.Run(ctx); err != nil {
		t.Fatalf("run 2: %v", err)
	}
	if got, _ := st.Alerts(ctx, uid, false, 100); len(got) != 0 {
		t.Fatalf("ancient anomaly leaked on second sweep: %+v", got)
	}
}

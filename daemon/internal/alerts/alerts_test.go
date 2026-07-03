package alerts

import (
	"context"
	"fmt"
	"path/filepath"
	"testing"
	"time"

	md "github.com/nyaungnicholas-wq/signaldeck/internal/marketdata"
	"github.com/nyaungnicholas-wq/signaldeck/internal/store"
)

// ── pure rule tests ─────────────────────────────────────────────────────

func TestThresholds(t *testing.T) {
	cases := []struct {
		hi, lo         string
		wantHi, wantLo float64
	}{
		{"", "", DefaultHi, DefaultLo},       // unset → defaults
		{"0.7", "0.3", 0.7, 0.3},             // valid override
		{"0.7", "", 0.7, DefaultLo},          // partial override
		{"nonsense", "0.3", DefaultHi, 0.3},  // bad hi ignored
		{"0.3", "0.7", DefaultHi, DefaultLo}, // inverted → defaults
		{"1.5", "0.2", DefaultHi, DefaultLo}, // hi out of range → defaults
		{"0.6", "0", DefaultHi, DefaultLo},   // lo must be > 0
		{"0.5", "0.5", DefaultHi, DefaultLo}, // lo must be < hi
	}
	for _, c := range cases {
		hi, lo := Thresholds(c.hi, c.lo)
		if hi != c.wantHi || lo != c.wantLo {
			t.Errorf("Thresholds(%q,%q) = %v,%v want %v,%v", c.hi, c.lo, hi, lo, c.wantHi, c.wantLo)
		}
	}
}

func TestPredictionKind(t *testing.T) {
	cases := []struct {
		prob     float64
		wantKind string
		wantOK   bool
	}{
		{0.65, KindPredictionHigh, true}, // inclusive at hi
		{0.9, KindPredictionHigh, true},
		{0.35, KindPredictionLow, true}, // inclusive at lo
		{0.1, KindPredictionLow, true},
		{0.5, "", false},
		{0.649, "", false},
		{0.351, "", false},
	}
	for _, c := range cases {
		kind, ok := PredictionKind(c.prob, DefaultHi, DefaultLo)
		if kind != c.wantKind || ok != c.wantOK {
			t.Errorf("PredictionKind(%v) = %q,%v want %q,%v", c.prob, kind, ok, c.wantKind, c.wantOK)
		}
	}
	// Custom band.
	if k, ok := PredictionKind(0.66, 0.8, 0.2); ok {
		t.Errorf("0.66 inside 0.2-0.8 band fired %q", k)
	}
	if k, ok := PredictionKind(0.85, 0.8, 0.2); !ok || k != KindPredictionHigh {
		t.Errorf("0.85 vs hi=0.8: got %q,%v", k, ok)
	}
}

// ── runner integration on a temp store ──────────────────────────────────

func newTestStore(t *testing.T) *store.Store {
	t.Helper()
	st, err := store.Open(filepath.Join(t.TempDir(), "alerts.db"))
	if err != nil {
		t.Fatalf("open store: %v", err)
	}
	t.Cleanup(func() { _ = st.Close() })
	return st
}

// seedUserWithSymbol creates a user watching one stock symbol.
func seedUserWithSymbol(t *testing.T, st *store.Store, username, symbol string) (int64, md.Symbol) {
	t.Helper()
	ctx := context.Background()
	uid, err := st.CreateUser(ctx, username, "x", false)
	if err != nil {
		t.Fatalf("create user: %v", err)
	}
	sym, err := st.UpsertSymbol(ctx, symbol, md.Stocks, "")
	if err != nil {
		t.Fatalf("upsert symbol: %v", err)
	}
	if err := st.AddUserSymbol(ctx, uid, sym.ID); err != nil {
		t.Fatalf("watch: %v", err)
	}
	return uid, sym
}

func TestRunnerSweepAndDedup(t *testing.T) {
	ctx := context.Background()
	st := newTestStore(t)
	uid, sym := seedUserWithSymbol(t, st, "alice", "NVDA")
	// A second user who does NOT watch NVDA must get nothing.
	uid2, _ := seedUserWithSymbol(t, st, "bob", "AAPL")

	now := time.Now()
	// Breakout + regime change + extreme predictions for NVDA.
	sid := sym.ID
	if err := st.InsertBreakout(ctx, &sid, now.Unix()-60, "high_break", "20d high broken", 1.2); err != nil {
		t.Fatalf("seed breakout: %v", err)
	}
	if err := st.UpsertRegime(ctx, sid, now.Unix()-3600, "downtrend", 0.5, ""); err != nil {
		t.Fatalf("seed regime: %v", err)
	}
	if err := st.UpsertRegime(ctx, sid, now.Unix()-120, "uptrend", 0.7, ""); err != nil {
		t.Fatalf("seed regime change: %v", err)
	}
	if err := st.UpsertPrediction(ctx, store.Prediction{
		SymbolID: sid, Horizon: md.H1d, Ts: now.Unix(), RawProb: 0.8, CalProb: 0.8, NUsed: 3,
	}); err != nil {
		t.Fatalf("seed prediction hi: %v", err)
	}
	if err := st.UpsertPrediction(ctx, store.Prediction{
		SymbolID: sid, Horizon: md.H1w, Ts: now.Unix(), RawProb: 0.2, CalProb: 0.2, NUsed: 2,
	}); err != nil {
		t.Fatalf("seed prediction lo: %v", err)
	}

	notified := 0
	r := &Runner{St: st, Notify: func(string) error { notified++; return nil }}
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
	if kinds[KindBreakout] != 1 || kinds[KindRegimeChange] != 1 ||
		kinds[KindPredictionHigh] != 1 || kinds[KindPredictionLow] != 1 {
		t.Fatalf("expected one of each kind, got %v (%d alerts)", kinds, len(got))
	}
	if notified != 1 {
		t.Errorf("notify calls = %d want 1 (batched)", notified)
	}

	// Non-watcher gets nothing.
	if other, _ := st.Alerts(ctx, uid2, false, 100); len(other) != 0 {
		t.Errorf("bob (not watching NVDA) got %d alerts", len(other))
	}

	// Second sweep: cursors + 24h prediction dedup → zero new alerts,
	// and the 30-min notify cooldown suppresses a second notification.
	if _, err := r.Run(ctx); err != nil {
		t.Fatalf("run 2: %v", err)
	}
	got2, _ := st.Alerts(ctx, uid, false, 100)
	if len(got2) != len(got) {
		t.Fatalf("second sweep duplicated alerts: %d → %d", len(got), len(got2))
	}
	if notified != 1 {
		t.Errorf("notify fired again inside cooldown: %d", notified)
	}

	// A NEW breakout is picked up by the cursor on the next sweep.
	if err := st.InsertBreakout(ctx, &sid, now.Unix(), "squeeze", "vol compression", 0.9); err != nil {
		t.Fatalf("seed breakout 2: %v", err)
	}
	if _, err := r.Run(ctx); err != nil {
		t.Fatalf("run 3: %v", err)
	}
	got3, _ := st.Alerts(ctx, uid, false, 100)
	if len(got3) != len(got)+1 {
		t.Fatalf("new breakout not alerted: %d → %d", len(got), len(got3))
	}
}

func TestRunnerPredictionDedupWindowAndThresholdOverride(t *testing.T) {
	ctx := context.Background()
	st := newTestStore(t)
	uid, sym := seedUserWithSymbol(t, st, "alice", "TSLA")
	now := time.Now()

	// CalProb 0.7 is an alert at default hi=0.65 but NOT at override hi=0.75.
	if err := st.UpsertPrediction(ctx, store.Prediction{
		SymbolID: sym.ID, Horizon: md.H1d, Ts: now.Unix(), RawProb: 0.7, CalProb: 0.7, NUsed: 3,
	}); err != nil {
		t.Fatalf("seed prediction: %v", err)
	}
	strict := &Runner{St: st, Hi: 0.75, Lo: 0.25, Notify: func(string) error { return nil }}
	if _, err := strict.Run(ctx); err != nil {
		t.Fatalf("strict run: %v", err)
	}
	if got, _ := st.Alerts(ctx, uid, false, 100); len(got) != 0 {
		t.Fatalf("0.7 fired at hi=0.75: %+v", got)
	}

	loose := &Runner{St: st, Hi: 0.65, Lo: 0.35, Notify: func(string) error { return nil }}
	if _, err := loose.Run(ctx); err != nil {
		t.Fatalf("loose run: %v", err)
	}
	got, _ := st.Alerts(ctx, uid, false, 100)
	if len(got) != 1 || got[0].Kind != KindPredictionHigh || got[0].Horizon != "1d" {
		t.Fatalf("expected one 1d prediction_high, got %+v", got)
	}

	// Same side within 24h: dedup even across separate runner instances.
	if _, err := loose.Run(ctx); err != nil {
		t.Fatalf("loose run 2: %v", err)
	}
	if got2, _ := st.Alerts(ctx, uid, false, 100); len(got2) != 1 {
		t.Fatalf("prediction alert duplicated within 24h: %d", len(got2))
	}

	// Flipping to the OTHER side is a new alert (dedup is per side).
	if err := st.UpsertPrediction(ctx, store.Prediction{
		SymbolID: sym.ID, Horizon: md.H1d, Ts: now.Unix() + 60, RawProb: 0.2, CalProb: 0.2, NUsed: 3,
	}); err != nil {
		t.Fatalf("seed flip: %v", err)
	}
	if _, err := loose.Run(ctx); err != nil {
		t.Fatalf("loose run 3: %v", err)
	}
	got3, _ := st.Alerts(ctx, uid, false, 100)
	if len(got3) != 2 {
		t.Fatalf("side flip not alerted: %d alerts", len(got3))
	}
}

func TestMarkAlertsSeenFlow(t *testing.T) {
	ctx := context.Background()
	st := newTestStore(t)
	uid, sym := seedUserWithSymbol(t, st, "alice", "SPY")
	sid := sym.ID
	for i := 0; i < 3; i++ {
		// Distinct details: byte-identical rows are deliberately collapsed by
		// idx_alerts_dedup (sweep-retry idempotency).
		if err := st.InsertAlert(ctx, store.Alert{
			UserID: uid, SymbolID: &sid, Kind: KindBreakout,
			Detail: fmt.Sprintf("x%d", i), Ts: time.Now().Unix(),
		}); err != nil {
			t.Fatalf("insert alert: %v", err)
		}
	}
	unseen, err := st.Alerts(ctx, uid, true, 100)
	if err != nil || len(unseen) != 3 {
		t.Fatalf("unseen = %d (%v) want 3", len(unseen), err)
	}
	n, err := st.MarkAlertsSeen(ctx, uid)
	if err != nil || n != 3 {
		t.Fatalf("marked = %d (%v) want 3", n, err)
	}
	unseen, _ = st.Alerts(ctx, uid, true, 100)
	if len(unseen) != 0 {
		t.Fatalf("unseen after mark = %d want 0", len(unseen))
	}
	all, _ := st.Alerts(ctx, uid, false, 100)
	if len(all) != 3 || !all[0].Seen {
		t.Fatalf("all alerts should remain, seen=true: %+v", all)
	}
	// Idempotent.
	if n, _ := st.MarkAlertsSeen(ctx, uid); n != 0 {
		t.Fatalf("second mark changed %d rows", n)
	}
}

func TestRunnerFirstSweepIgnoresAncientEvents(t *testing.T) {
	ctx := context.Background()
	st := newTestStore(t)
	uid, sym := seedUserWithSymbol(t, st, "alice", "QQQ")
	sid := sym.ID
	// A breakout 3 days old must NOT flood the first sweep (24h lookback).
	old := time.Now().Add(-72 * time.Hour).Unix()
	if err := st.InsertBreakout(ctx, &sid, old, "high_break", "ancient", 1); err != nil {
		t.Fatalf("seed old breakout: %v", err)
	}
	r := &Runner{St: st, Notify: func(string) error { return nil }}
	if _, err := r.Run(ctx); err != nil {
		t.Fatalf("run: %v", err)
	}
	if got, _ := st.Alerts(ctx, uid, false, 100); len(got) != 0 {
		t.Fatalf("ancient event alerted on first sweep: %+v", got)
	}
}

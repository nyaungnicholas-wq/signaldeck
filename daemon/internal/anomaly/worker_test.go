package anomaly

import (
	"context"
	"math"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/nyaungnicholas-wq/signaldeck/internal/marketcal"
	md "github.com/nyaungnicholas-wq/signaldeck/internal/marketdata"
	"github.com/nyaungnicholas-wq/signaldeck/internal/store"
)

func newTestStore(t *testing.T) *store.Store {
	t.Helper()
	st, err := store.Open(filepath.Join(t.TempDir(), "anomaly.db"))
	if err != nil {
		t.Fatalf("open store: %v", err)
	}
	t.Cleanup(func() { _ = st.Close() })
	return st
}

// marketOpenWednesday is 2026-07-08 13:00 ET — a regular NYSE session.
var marketOpenWednesday = time.Date(2026, 7, 8, 13, 0, 0, 0, marketcal.Loc())

// closedSaturday is 2026-07-04 12:00 ET — a weekend (and July 4th).
var closedSaturday = time.Date(2026, 7, 4, 12, 0, 0, 0, marketcal.Loc())

func seedImbalanceSnaps(t *testing.T, st *store.Store, symbolID int64, now time.Time) {
	t.Helper()
	ctx := context.Background()
	// 3600s flat-noise baseline then 300s of +0.8 imbalance, ending at `now`.
	start := now.Unix() - 3899
	for i := 0; i < 3900; i++ {
		imb := 0.1 * math.Sin(float64(i))
		if i >= 3600 {
			imb = 0.8
		}
		if err := st.InsertSnap1s(ctx, md.Snap1s{
			SymbolID: symbolID, Ts: start + int64(i), ImbSigned: imb,
		}); err != nil {
			t.Fatalf("seed snap: %v", err)
		}
	}
}

// seedBuyHeavyMinuteBars writes 10 balanced 30-bar windows then a recent
// all-buy window — fires the volume-side imbalance proxy and nothing else.
func seedBuyHeavyMinuteBars(t *testing.T, st *store.Store, symbolID int64) {
	t.Helper()
	var bars []md.Bar
	ts := int64(1700000000)
	push := func(o, c, v float64) {
		bars = append(bars, md.Bar{SymbolID: symbolID, TF: md.TF1m, Ts: ts,
			Open: o, High: math.Max(o, c) + 1, Low: math.Min(o, c) - 1, Close: c, Volume: v})
		ts += 60
	}
	for i := 0; i < 300; i++ {
		v := 100.0 + float64(i%7)
		if i%2 == 0 {
			push(10, 11, v)
		} else {
			push(11, 10, v)
		}
	}
	for i := 0; i < 30; i++ {
		push(10, 11, 100)
	}
	if err := st.UpsertBars(context.Background(), bars); err != nil {
		t.Fatalf("seed 1m bars: %v", err)
	}
}

func TestScanner_CryptoImbalance_DetectAndHourDedup(t *testing.T) {
	ctx := context.Background()
	st := newTestStore(t)
	btc, err := st.UpsertSymbol(ctx, "BTC/USD", md.Crypto, "")
	if err != nil {
		t.Fatalf("upsert: %v", err)
	}
	now := marketOpenWednesday
	seedImbalanceSnaps(t, st, btc.ID, now)

	w := &Scanner{St: st, Threshold: 2.5, Now: func() time.Time { return now }}
	detail, err := w.Run(ctx)
	if err != nil {
		t.Fatalf("run: %v", err)
	}
	rows, err := st.Anomalies(ctx, 0, "", 100)
	if err != nil {
		t.Fatalf("anomalies: %v", err)
	}
	if len(rows) != 1 || rows[0].Kind != KindImbalance || rows[0].Symbol != "BTC/USD" {
		t.Fatalf("rows = %+v (detail %q)", rows, detail)
	}
	if rows[0].Z < 2.5 || !strings.Contains(rows[0].Detail, "buy") {
		t.Errorf("row = %+v", rows[0])
	}

	// Same condition scanned again 5 minutes later, SAME hour bucket → the
	// dedup index absorbs it (one open anomaly per symbol+kind per hour).
	later := now.Add(5 * time.Minute)
	// Keep the snap shape identical relative to the new "now".
	seedImbalanceSnaps(t, st, btc.ID, later)
	w.Now = func() time.Time { return later }
	if _, err := w.Run(ctx); err != nil {
		t.Fatalf("run 2: %v", err)
	}
	rows2, _ := st.Anomalies(ctx, 0, "", 100)
	if len(rows2) != 1 {
		t.Fatalf("hour dedup failed: %d rows", len(rows2))
	}

	// Next hour bucket → a fresh row records the still-open condition.
	nextHour := now.Add(61 * time.Minute)
	seedImbalanceSnaps(t, st, btc.ID, nextHour)
	w.Now = func() time.Time { return nextHour }
	if _, err := w.Run(ctx); err != nil {
		t.Fatalf("run 3: %v", err)
	}
	rows3, _ := st.Anomalies(ctx, 0, "", 100)
	if len(rows3) != 2 {
		t.Fatalf("next-hour re-detection missing: %d rows", len(rows3))
	}
}

func TestScanner_StockProxy_MarketcalGated(t *testing.T) {
	ctx := context.Background()
	st := newTestStore(t)
	nvda, err := st.UpsertSymbol(ctx, "NVDA", md.Stocks, "")
	if err != nil {
		t.Fatalf("upsert: %v", err)
	}
	if err := st.SetSymbolStream(ctx, nvda.ID, true); err != nil {
		t.Fatalf("stream: %v", err)
	}
	seedBuyHeavyMinuteBars(t, st, nvda.ID)

	// CLOSED (Saturday): the stock 1m scan must be skipped entirely.
	w := &Scanner{St: st, Threshold: 2.5, Now: func() time.Time { return closedSaturday }}
	detail, err := w.Run(ctx)
	if err != nil {
		t.Fatalf("closed run: %v", err)
	}
	if !strings.Contains(detail, "market closed") {
		t.Errorf("closed-run detail = %q", detail)
	}
	if rows, _ := st.Anomalies(ctx, 0, "", 100); len(rows) != 0 {
		t.Fatalf("closed-market scan produced anomalies: %+v", rows)
	}

	// OPEN (Wednesday session): the volume-side proxy fires, labeled.
	w.Now = func() time.Time { return marketOpenWednesday }
	if _, err := w.Run(ctx); err != nil {
		t.Fatalf("open run: %v", err)
	}
	rows, _ := st.Anomalies(ctx, nvda.ID, KindImbalance, 100)
	if len(rows) != 1 {
		t.Fatalf("expected 1 proxy imbalance, got %+v", rows)
	}
	if !strings.Contains(rows[0].Detail, "volume-side proxy (no order-book on free stock data)") {
		t.Errorf("proxy label missing: %q", rows[0].Detail)
	}
}

func TestScanner_DailySweep_OncePerTradingDay(t *testing.T) {
	ctx := context.Background()
	st := newTestStore(t)
	// Daily-only universe symbol (active, stream=0) with a volume-spike day.
	xyz, err := st.UpsertSymbol(ctx, "XYZ", md.Stocks, "")
	if err != nil {
		t.Fatalf("upsert: %v", err)
	}
	var bars []md.Bar
	ts := int64(1600000000)
	for i := 0; i < 60; i++ {
		bars = append(bars, md.Bar{SymbolID: xyz.ID, TF: md.TF1d, Ts: ts,
			Open: 10, High: 11, Low: 9, Close: 10.5, Volume: 1000 + float64(i%50)})
		ts += 86400
	}
	bars = append(bars, md.Bar{SymbolID: xyz.ID, TF: md.TF1d, Ts: ts,
		Open: 10, High: 11, Low: 9, Close: 10.5, Volume: 10000})
	if err := st.UpsertBars(ctx, bars); err != nil {
		t.Fatalf("seed 1d bars: %v", err)
	}

	// Weekend run: daily sweep must NOT run (and must not burn the cursor).
	w := &Scanner{St: st, Threshold: 2.5, Now: func() time.Time { return closedSaturday }}
	if _, err := w.Run(ctx); err != nil {
		t.Fatalf("weekend run: %v", err)
	}
	if rows, _ := st.Anomalies(ctx, 0, "", 100); len(rows) != 0 {
		t.Fatalf("weekend daily sweep ran: %+v", rows)
	}
	if v, _ := st.GetMeta(ctx, metaDailySweepKey); v != "" {
		t.Fatalf("weekend run set the daily cursor: %q", v)
	}

	// Trading-day run: sweep fires once…
	w.Now = func() time.Time { return marketOpenWednesday }
	detail, err := w.Run(ctx)
	if err != nil {
		t.Fatalf("run: %v", err)
	}
	if !strings.Contains(detail, "daily sweep") {
		t.Errorf("detail = %q", detail)
	}
	rows, _ := st.Anomalies(ctx, xyz.ID, KindVolume, 100)
	if len(rows) != 1 || rows[0].Z < 2.5 {
		t.Fatalf("daily volume anomaly = %+v", rows)
	}

	// …and the SAME day never re-sweeps (date cursor), even next tick.
	w.Now = func() time.Time { return marketOpenWednesday.Add(5 * time.Minute) }
	detail2, err := w.Run(ctx)
	if err != nil {
		t.Fatalf("run 2: %v", err)
	}
	if strings.Contains(detail2, "daily sweep") {
		t.Errorf("daily sweep re-ran same day: %q", detail2)
	}

	// The NEXT trading day (with a NEW spike bar — same data would dedup on
	// the unchanged bar ts, correctly reporting nothing new) sweeps again.
	ts += 86400
	if err := st.UpsertBars(ctx, []md.Bar{{SymbolID: xyz.ID, TF: md.TF1d, Ts: ts,
		Open: 10, High: 11, Low: 9, Close: 10.5, Volume: 12000}}); err != nil {
		t.Fatalf("seed next-day bar: %v", err)
	}
	w.Now = func() time.Time { return marketOpenWednesday.AddDate(0, 0, 1) }
	if _, err := w.Run(ctx); err != nil {
		t.Fatalf("run 3: %v", err)
	}
	rows3, _ := st.Anomalies(ctx, xyz.ID, KindVolume, 100)
	if len(rows3) != 2 {
		t.Fatalf("next-day sweep missing: %d rows", len(rows3))
	}
}

func TestScanner_InsufficientData_NoSignalNoError(t *testing.T) {
	ctx := context.Background()
	st := newTestStore(t)
	if _, err := st.UpsertSymbol(ctx, "BTC/USD", md.Crypto, ""); err != nil {
		t.Fatalf("upsert: %v", err)
	}
	if _, err := st.UpsertSymbol(ctx, "EMPT", md.Stocks, ""); err != nil {
		t.Fatalf("upsert: %v", err)
	}
	w := &Scanner{St: st, Threshold: 2.5, Now: func() time.Time { return marketOpenWednesday }}
	if _, err := w.Run(ctx); err != nil {
		t.Fatalf("run: %v", err)
	}
	if rows, _ := st.Anomalies(ctx, 0, "", 100); len(rows) != 0 {
		t.Fatalf("no-data symbols produced anomalies: %+v", rows)
	}
}

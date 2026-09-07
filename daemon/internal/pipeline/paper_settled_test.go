package pipeline

import (
	"context"
	"testing"
	"time"

	md "github.com/nyaungnicholas-wq/signaldeck/internal/marketdata"
)

// TestPaperTrader_ClockIgnoresFormingDailyBar: the as-of clock must be the
// newest SETTLED daily bar, not the newest bar in the store. Ingestion writes
// the still-forming daily bar during the session, and the old clock advanced
// onto it — so a fill could land on a bar whose close was the live intraday
// price. The fix: a bar stamped ts is settled when time.Now().Unix() >=
// ts + 22*3600; an unsettled newest bar falls back to the previous bar.
//
// Fixture: 30 prior daily bars (so ADV/volatility from completed prior bars
// exist), then b1 (close 100), b2 (close 110), b3 (close 200). b3 is the
// forming bar — its ts is today, so it is never settled at test time. The
// prediction is stamped at b1+60, so the next-bar fill would land on b2's
// open (110) if the clock is correct, or on b3's open (200) if the bug is
// back. The cursor must stop at b2 and no trade may carry px==200.
func TestPaperTrader_ClockIgnoresFormingDailyBar(t *testing.T) {
	st := openStore(t)
	ctx := context.Background()
	sym, _ := st.UpsertSymbol(ctx, "AAA", md.Stocks, "")

	// todayStamp is the 04:00 UTC stamp of "today" (the forming bar's ts).
	// If today is already past 22h-settlement, push it to tomorrow so b3 is
	// never settled at test time.
	todayStamp := time.Now().UTC().Truncate(24*time.Hour).Unix() + 4*3600
	if time.Now().Unix() >= todayStamp+22*3600 {
		todayStamp += 86400
	}
	b1 := todayStamp - 2*86400
	b2 := todayStamp - 86400
	b3 := todayStamp

	// 30 prior daily bars so ADV/volatility from completed prior bars exist.
	prior := make([]md.Bar, 0, 33)
	for k := 30; k >= 1; k-- {
		ts := b1 - int64(k)*86400
		prior = append(prior, md.Bar{
			SymbolID: sym.ID, TF: md.TF1d, Ts: ts,
			Open: 100, High: 101, Low: 99, Close: 100, Volume: 1_000_000,
		})
	}
	// b1, b2, b3.
	prior = append(prior,
		md.Bar{SymbolID: sym.ID, TF: md.TF1d, Ts: b1, Open: 100, High: 101, Low: 99, Close: 100, Volume: 1_000_000},
		md.Bar{SymbolID: sym.ID, TF: md.TF1d, Ts: b2, Open: 110, High: 111, Low: 109, Close: 110, Volume: 1_000_000},
		md.Bar{SymbolID: sym.ID, TF: md.TF1d, Ts: b3, Open: 200, High: 201, Low: 199, Close: 200, Volume: 1_000_000},
	)
	if err := st.UpsertBars(ctx, prior); err != nil {
		t.Fatalf("seed bars: %v", err)
	}

	// Calibrated prediction stamped at b1+60 — next-bar fill would land on b2.
	seedPrediction(t, st, sym.ID, md.H1d, b1+60, 0.95)

	w := &PaperTrader{St: st}
	if _, err := w.Run(ctx); err != nil {
		t.Fatalf("run: %v", err)
	}

	// (a) The flagship-1d cursor's last_bar_ts must be b2 — the forming bar
	// (b3) must not advance the clock.
	cur, _, err := st.PaperCursor(ctx, "flagship-1d")
	if err != nil {
		t.Fatalf("cursor: %v", err)
	}
	if cur.LastBarTs != b2 {
		t.Fatalf("forming bar advanced the clock: cursor.LastBarTs=%d want %d (b2); "+
			"the still-forming daily bar (b3, ts=%d) must not be used as the as-of clock",
			cur.LastBarTs, b2, b3)
	}

	// (b) No recorded trade may carry ts > b2, and none may carry px == 200
	// (the forming bar's open).
	trades, _ := st.PaperTrades(ctx, "flagship-1d", 50)
	for _, tr := range trades {
		if tr.Ts > b2 {
			t.Fatalf("trade ts=%d is past b2=%d: the forming bar must not advance the clock", tr.Ts, b2)
		}
		if tr.Px == 200 {
			t.Fatalf("trade px=%v == 200 (forming bar's open): the forming bar must not advance the clock", tr.Px)
		}
	}
}

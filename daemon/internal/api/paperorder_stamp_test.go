package api

import (
	"context"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	md "github.com/nyaungnicholas-wq/signaldeck/internal/marketdata"
)

// A manual fill has to be reconcilable against the bar it filled against.
// checkPaperFills matches on bar.Ts == trade.Ts EXACTLY, and CheckFillFidelity
// reports the whole book unverified if even one trade finds no bar. The handler
// was stamping trades with the paper CURSOR's value instead -- and the cursor is
// bumped to LastBarTs+1 whenever a second order lands inside a bar the book has
// already consumed. Live, that put three of book manual:u5's nine fills at ts%60
// of 1, 2 and 3, matching no stored 1m bar and flagging the book false.
//
// Two orders against ONE bar is the exact shape that triggered it, so that is
// what this drives.
func TestPaperOrderStampsTheBarItFilledAgainst(t *testing.T) {
	d, _ := newExportDeps(t)
	st := d.St
	ctx := context.Background()
	now := time.Now().Unix()

	sym, err := st.UpsertSymbol(ctx, "BTC/USD", md.Crypto, "Bitcoin")
	if err != nil {
		t.Fatalf("upsert symbol: %v", err)
	}
	for k := 25; k >= 1; k-- {
		ts := (now/86400)*86400 - int64(k)*86400
		if err := st.UpsertBars(ctx, []md.Bar{{
			SymbolID: sym.ID, TF: md.TF1d, Ts: ts,
			Open: 100, High: 102, Low: 98, Close: 100, Volume: 1_000_000,
		}}); err != nil {
			t.Fatalf("upsert daily bar: %v", err)
		}
	}
	// One 1m bar, on an exact minute boundary as stored bars always are.
	ts1 := ((now - 60) / 60) * 60
	if err := st.UpsertBars(ctx, []md.Bar{{
		SymbolID: sym.ID, TF: md.TF1m, Ts: ts1,
		Open: 100, High: 100.5, Low: 99.5, Close: 100.2, Volume: 50,
	}}); err != nil {
		t.Fatalf("upsert 1m bar: %v", err)
	}

	post := func(body string, uid int64) *httptest.ResponseRecorder {
		r := httptest.NewRequest("POST", "/api/paper/order", strings.NewReader(body))
		r.Header.Set("Content-Type", "application/json")
		r = withUser(r, uid)
		rec := httptest.NewRecorder()
		d.paperOrder(rec, r)
		return rec
	}

	const uid = int64(11)
	for i, body := range []string{
		`{"symbol":"BTC/USD","market":"crypto","side":"buy","qty":1}`,
		`{"symbol":"BTC/USD","market":"crypto","side":"buy","qty":1}`,
	} {
		if rec := post(body, uid); rec.Code != 200 {
			t.Fatalf("order %d: expected 200, got %d: %s", i+1, rec.Code, rec.Body.String())
		}
	}

	strategy := manualPaperStrategy(uid)
	trades, err := st.AllPaperTradesAsc(ctx, strategy)
	if err != nil {
		t.Fatalf("read trades: %v", err)
	}
	if len(trades) != 2 {
		t.Fatalf("expected 2 trades, got %d", len(trades))
	}
	for i, tr := range trades {
		if tr.Ts != ts1 {
			t.Fatalf("trade %d stamped ts=%d, want the 1m bar's %d (off by %d). "+
				"A stamp that matches no stored bar is counted NoBar and reports the whole book unverified",
				i+1, tr.Ts, ts1, tr.Ts-ts1)
		}
		if tr.Ts%60 != 0 {
			t.Fatalf("trade %d stamped at ts%%60=%d: a 1m bar timestamp is always on a minute boundary, "+
				"so this is a synthetic cursor value, not a real bar", i+1, tr.Ts%60)
		}
	}

	// The cursor must still have advanced past the bar, or the second order
	// could never have been applied at all.
	cur, ok, err := st.PaperCursor(ctx, strategy)
	if err != nil || !ok {
		t.Fatalf("read cursor: ok=%v err=%v", ok, err)
	}
	if cur.LastBarTs <= ts1 {
		t.Fatalf("cursor did not advance past the bar: LastBarTs=%d, bar=%d. "+
			"Decoupling the trade stamp from the cursor must not stop the cursor stepping",
			cur.LastBarTs, ts1)
	}
}

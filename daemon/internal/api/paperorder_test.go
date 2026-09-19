package api

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	md "github.com/nyaungnicholas-wq/signaldeck/internal/marketdata"
)

func TestPaperOrderManualBookRoundTrip(t *testing.T) {
	d, _ := newExportDeps(t)
	st := d.St
	ctx := context.Background()
	now := time.Now().Unix()

	sym, err := st.UpsertSymbol(ctx, "BTC/USD", md.Crypto, "Bitcoin")
	if err != nil {
		t.Fatalf("upsert symbol: %v", err)
	}

	// Seed 25 daily bars
	for k := 25; k >= 1; k-- {
		ts := (now/86400)*86400 - int64(k)*86400
		bar := md.Bar{
			SymbolID: sym.ID,
			TF:       md.TF1d,
			Ts:       ts,
			Open:     100,
			High:     102,
			Low:      98,
			Close:    100,
			Volume:   1_000_000,
		}
		if err := st.UpsertBars(ctx, []md.Bar{bar}); err != nil {
			t.Fatalf("upsert daily bar: %v", err)
		}
	}

	// Seed one 1m bar at now - 60
	ts1 := now - 60
	bar1m := md.Bar{
		SymbolID: sym.ID,
		TF:       md.TF1m,
		Ts:       ts1,
		Open:     100,
		High:     100.5,
		Low:      99.5,
		Close:    100.2,
		Volume:   50,
	}
	if err := st.UpsertBars(ctx, []md.Bar{bar1m}); err != nil {
		t.Fatalf("upsert 1m bar: %v", err)
	}

	post := func(body string, uid int64) *httptest.ResponseRecorder {
		r := httptest.NewRequest("POST", "/api/paper/order", strings.NewReader(body))
		r.Header.Set("Content-Type", "application/json")
		if uid > 0 {
			r = withUser(r, uid)
		}
		rec := httptest.NewRecorder()
		d.paperOrder(rec, r)
		return rec
	}

	// Anonymous
	rec := post(`{"symbol":"BTC/USD","market":"crypto","side":"buy","qty":1}`, 0)
	if rec.Code != 401 {
		t.Fatalf("anonymous: expected 401, got %d", rec.Code)
	}

	// Unknown symbol
	rec = post(`{"symbol":"NOPE","market":"stocks","side":"buy","qty":1}`, 7)
	if rec.Code != 404 {
		t.Fatalf("unknown symbol: expected 404, got %d", rec.Code)
	}

	// Buy
	rec = post(`{"symbol":"BTC/USD","market":"crypto","side":"buy","qty":2}`, 7)
	if rec.Code != 200 {
		t.Fatalf("buy: expected 200, got %d", rec.Code)
	}
	var resp map[string]any
	if err := json.NewDecoder(rec.Body).Decode(&resp); err != nil {
		t.Fatalf("decode buy response: %v", err)
	}
	if ok := resp["ok"]; ok != true {
		t.Fatalf("buy: expected ok=true, got %v", ok)
	}

	// Check position
	positions, err := st.PaperPositions(ctx, manualPaperStrategy(7))
	if err != nil {
		t.Fatalf("paper positions: %v", err)
	}
	if len(positions) != 1 {
		t.Fatalf("expected 1 position, got %d", len(positions))
	}
	pos := positions[0]
	if pos.Qty <= 0 {
		t.Fatalf("position qty <= 0: %f", pos.Qty)
	}
	if pos.Qty > 2+1e-9 {
		t.Fatalf("position qty > 2: %f", pos.Qty)
	}

	// Check cursor
	cur, ok, err := st.PaperCursor(ctx, manualPaperStrategy(7))
	if err != nil {
		t.Fatalf("paper cursor: %v", err)
	}
	if !ok {
		t.Fatalf("cursor not found")
	}
	if cur.Cash >= 100000 {
		t.Fatalf("cursor cash not decreased: %f", cur.Cash)
	}

	// Check trades
	trades, err := st.AllPaperTradesAsc(ctx, manualPaperStrategy(7))
	if err != nil {
		t.Fatalf("all paper trades: %v", err)
	}
	if len(trades) != 1 {
		t.Fatalf("expected 1 trade, got %d", len(trades))
	}
	trade := trades[0]
	if trade.Side != "buy" {
		t.Fatalf("expected buy trade, got %s", trade.Side)
	}
	if trade.Px != 100 {
		t.Fatalf("expected price 100, got %f", trade.Px)
	}
	if trade.Cost <= 0 {
		t.Fatalf("expected positive cost, got %f", trade.Cost)
	}
	expectedCash := 100000 - pos.Qty*100 - trade.Cost
	if cur.Cash < expectedCash-1e-6 || cur.Cash > expectedCash+1e-6 {
		t.Fatalf("cursor cash mismatch: got %f, want %f ± 1e-6", cur.Cash, expectedCash)
	}

	// Oversell
	rec = post(`{"symbol":"BTC/USD","market":"crypto","side":"sell","qty":5}`, 7)
	if rec.Code != 400 {
		t.Fatalf("oversell: expected 400, got %d", rec.Code)
	}

	// Sell all
	sellQty := pos.Qty
	rec = post(fmt.Sprintf(`{"symbol":"BTC/USD","market":"crypto","side":"sell","qty":%.8f}`, sellQty), 7)
	if rec.Code != 200 {
		t.Fatalf("sell all: expected 200, got %d", rec.Code)
	}
	if err := json.NewDecoder(rec.Body).Decode(&resp); err != nil {
		t.Fatalf("decode sell response: %v", err)
	}
	if ok := resp["ok"]; ok != true {
		t.Fatalf("sell all: expected ok=true, got %v", ok)
	}

	// Check position empty
	positions, err = st.PaperPositions(ctx, manualPaperStrategy(7))
	if err != nil {
		t.Fatalf("paper positions after sell: %v", err)
	}
	if len(positions) != 0 {
		t.Fatalf("expected empty positions, got %d", len(positions))
	}

	// Check cursor after sell
	cur, ok, err = st.PaperCursor(ctx, manualPaperStrategy(7))
	if err != nil {
		t.Fatalf("paper cursor after sell: %v", err)
	}
	if !ok {
		t.Fatalf("cursor not found after sell")
	}
	if cur.Cash >= 100000 {
		t.Fatalf("cursor cash not decreased after round trip: %f", cur.Cash)
	}
	if cur.Cash <= 99000 {
		t.Fatalf("cursor cash too low after round trip: %f", cur.Cash)
	}

	// Check trades length 2
	trades, err = st.AllPaperTradesAsc(ctx, manualPaperStrategy(7))
	if err != nil {
		t.Fatalf("all paper trades after sell: %v", err)
	}
	if len(trades) != 2 {
		t.Fatalf("expected 2 trades, got %d", len(trades))
	}
	if trades[1].Side != "sell" {
		t.Fatalf("expected second trade sell, got %s", trades[1].Side)
	}

	// Isolation: strategy for uid 8 must be empty
	positions8, err := st.PaperPositions(ctx, manualPaperStrategy(8))
	if err != nil {
		t.Fatalf("paper positions for uid 8: %v", err)
	}
	if len(positions8) != 0 {
		t.Fatalf("expected empty positions for uid 8, got %d", len(positions8))
	}
}

func TestPaperOrderRefusesStaleQuote(t *testing.T) {
	d, _ := newExportDeps(t)
	st := d.St
	ctx := context.Background()
	now := time.Now().Unix()

	sym, err := st.UpsertSymbol(ctx, "BTC/USD", md.Crypto, "Bitcoin")
	if err != nil {
		t.Fatalf("upsert symbol: %v", err)
	}

	// Seed 25 daily bars (same as before)
	for k := 25; k >= 1; k-- {
		ts := (now/86400)*86400 - int64(k)*86400
		bar := md.Bar{
			SymbolID: sym.ID,
			TF:       md.TF1d,
			Ts:       ts,
			Open:     100,
			High:     102,
			Low:      98,
			Close:    100,
			Volume:   1_000_000,
		}
		if err := st.UpsertBars(ctx, []md.Bar{bar}); err != nil {
			t.Fatalf("upsert daily bar: %v", err)
		}
	}

	// Seed one 1m bar at now - 3600 (stale)
	ts1 := now - 3600
	bar1m := md.Bar{
		SymbolID: sym.ID,
		TF:       md.TF1m,
		Ts:       ts1,
		Open:     100,
		High:     100.5,
		Low:      99.5,
		Close:    100.2,
		Volume:   50,
	}
	if err := st.UpsertBars(ctx, []md.Bar{bar1m}); err != nil {
		t.Fatalf("upsert stale 1m bar: %v", err)
	}

	post := func(body string, uid int64) *httptest.ResponseRecorder {
		r := httptest.NewRequest("POST", "/api/paper/order", strings.NewReader(body))
		r.Header.Set("Content-Type", "application/json")
		if uid > 0 {
			r = withUser(r, uid)
		}
		rec := httptest.NewRecorder()
		d.paperOrder(rec, r)
		return rec
	}

	// Buy with stale quote -> 409
	rec := post(`{"symbol":"BTC/USD","market":"crypto","side":"buy","qty":1}`, 7)
	if rec.Code != 409 {
		t.Fatalf("stale quote: expected 409, got %d", rec.Code)
	}
	if !strings.Contains(rec.Body.String(), "no fresh quote") {
		t.Fatalf("stale quote: expected 'no fresh quote' in body, got %q", rec.Body.String())
	}
}

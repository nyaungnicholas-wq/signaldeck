package api

import (
	"encoding/json"
	"fmt"
	"math"
	"net/http"
	"strings"
	"time"

	md "github.com/nyaungnicholas-wq/signaldeck/internal/marketdata"
	"github.com/nyaungnicholas-wq/signaldeck/internal/papertrade"
	"github.com/nyaungnicholas-wq/signaldeck/internal/store"
)

// paperOrder is POST /api/paper/order: a MANUAL simulated market order in the
// caller's own paper book (manualPaperStrategy). Same execution-cost model,
// same transactional store step and same no-lookahead liquidity inputs as the
// flagship books; the only difference is that a person, not a model, decides.
//
//	{ "symbol": "SPY", "market": "stocks", "side": "buy", "qty": 3 }
//
// Fills quote the newest 1m bar and are refused when that bar is stale
// (manualQuoteMaxAgeSecs), so a closed market cannot be traded against a
// pretend price. Long-only: a sell may not exceed the held quantity. NEVER
// contacts a broker; every number here is arithmetic over stored bars.
func (d Deps) paperOrder(w http.ResponseWriter, r *http.Request) {
	uid := userID(r)
	if uid == 0 {
		httpErr(w, 401, "sign in to trade the manual paper book")
		return
	}
	var body struct {
		Symbol string    `json:"symbol"`
		Market md.Market `json:"market"`
		Side   string    `json:"side"`
		Qty    float64   `json:"qty"`
	}
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		httpErr(w, 400, "bad json: "+err.Error())
		return
	}
	side := strings.ToLower(strings.TrimSpace(body.Side))
	if side != "buy" && side != "sell" {
		httpErr(w, 400, `side must be "buy" or "sell"`)
		return
	}
	if !(body.Qty > 0) || math.IsInf(body.Qty, 0) || math.IsNaN(body.Qty) {
		httpErr(w, 400, "qty must be a positive finite number")
		return
	}
	if body.Market != md.Stocks && body.Market != md.Crypto {
		httpErr(w, 400, "market must be stocks or crypto")
		return
	}
	ctx := r.Context()
	sym, err := d.St.GetSymbol(ctx, strings.ToUpper(strings.TrimSpace(body.Symbol)), body.Market)
	if err != nil {
		httpErr(w, 404, "unknown symbol")
		return
	}
	now := time.Now().Unix()
	in, refusal, err := manualQuote(ctx, d.St, sym, now)
	if err != nil {
		httpInternal(w, err)
		return
	}
	if refusal != "" {
		httpErr(w, 409, refusal)
		return
	}

	strategy := manualPaperStrategy(uid)
	if _, err := d.St.InitPaperBook(ctx, strategy, papertrade.StartingCash(), now); err != nil {
		httpInternal(w, err)
		return
	}
	cur, ok, err := d.St.PaperCursor(ctx, strategy)
	if err != nil {
		httpInternal(w, err)
		return
	}
	if !ok {
		httpInternal(w, fmt.Errorf("paper book %s has no cursor after init", strategy))
		return
	}
	existing, has, err := d.St.PaperPosition(ctx, strategy, sym.ID)
	if err != nil {
		httpInternal(w, err)
		return
	}

	var (
		fill      papertrade.Fill
		opens     []store.PaperPosition
		closeIDs  []int64
		remaining float64
	)
	switch side {
	case "buy":
		budget := body.Qty * in.Bar.Open
		if budget > cur.Cash {
			httpErr(w, 400, fmt.Sprintf("insufficient cash: need %.2f, have %.2f", budget, cur.Cash))
			return
		}
		var gotQty float64
		var okFill bool
		fill, gotQty, _, okFill = papertrade.EnterLong(budget, in)
		if !okFill {
			httpErr(w, 409, "order refused: "+fill.Reason)
			return
		}
		pos := store.PaperPosition{Strategy: strategy, SymbolID: sym.ID, Qty: gotQty, AvgPx: fill.Px, OpenedTs: in.Bar.Ts}
		if has {
			pos.Qty = existing.Qty + gotQty
			pos.AvgPx = (existing.Qty*existing.AvgPx + gotQty*fill.Px) / pos.Qty
			pos.OpenedTs = existing.OpenedTs
		}
		opens = []store.PaperPosition{pos}
		remaining = pos.Qty
	case "sell":
		held := 0.0
		if has {
			held = existing.Qty
		}
		if body.Qty > held*(1+1e-6)+1e-9 {
			httpErr(w, 400, fmt.Sprintf("cannot sell %.4f: position is %.4f (no shorting in the manual book)", body.Qty, held))
			return
		}
		if body.Qty >= held*(1-1e-6) {
			body.Qty = held // "sell all" typed to a few decimals: close exactly, leave no dust lot
		}
		var okFill bool
		fill, okFill = papertrade.ExitLong(body.Qty, in)
		if !okFill {
			httpErr(w, 409, "order refused: "+fill.Reason)
			return
		}
		remaining = held - body.Qty
		if remaining <= 1e-9 {
			remaining = 0
			closeIDs = []int64{sym.ID}
		} else {
			opens = []store.PaperPosition{{Strategy: strategy, SymbolID: sym.ID, Qty: remaining, AvgPx: existing.AvgPx, OpenedTs: existing.OpenedTs}}
		}
	}

	newCash := cur.Cash + fill.CashDelta
	others, err := manualPositionsValue(ctx, d.St, strategy, sym.ID, now)
	if err != nil {
		httpInternal(w, err)
		return
	}
	positionsValue := others + remaining*in.Bar.Open
	equity := newCash + positionsValue

	// The cursor must strictly advance: one step per second per book.
	barTs := in.Bar.Ts
	if barTs <= cur.LastBarTs {
		barTs = cur.LastBarTs + 1
	}
	trade := store.PaperTrade{
		Strategy: strategy, SymbolID: sym.ID, Side: fill.Side, Qty: fill.Qty, Px: fill.Px, Cost: fill.Cost, Ts: barTs,
		Reason: fmt.Sprintf("manual %s order (quote 1m bar %d)", side, in.Bar.Ts),
	}
	applied, err := d.St.ApplyPaperStep(ctx, store.PaperApply{
		Strategy: strategy, BarTs: barTs, NewCash: newCash,
		Opens: opens, CloseSymbolIDs: closeIDs, Trades: []store.PaperTrade{trade},
		EquityTs: barTs, EquityCash: newCash, EquityPositionsValue: positionsValue, EquityValue: equity,
	})
	if err != nil {
		httpInternal(w, err)
		return
	}
	if !applied {
		httpErr(w, 409, "book advanced concurrently; retry")
		return
	}
	writeJSON(w, map[string]any{
		"ok": true, "strategy": strategy, "symbol": sym.Symbol, "market": sym.Market,
		"fill": fill, "quoteBarTs": in.Bar.Ts, "quoteAgeS": now - in.Bar.Ts,
		"cash": newCash, "equity": equity,
		"label": "simulated manual paper book - not live money, not advice",
	})
}

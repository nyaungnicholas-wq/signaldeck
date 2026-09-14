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
//
// REPLAY POSTURE, stated because the absence of an idempotency key is a decision
// and not an oversight. There is no Idempotency-Key header, no client key and no
// request hash: two identical POSTs are two orders, deliberately, because three
// equal-size buys a second apart are a legitimate thing for a person to do and
// nothing in the request distinguishes that from a retry.
//
// What actually defends against an accidental double-submit is the client:
// components/paper/OrderForm.tsx disables its button on `busy` for the duration
// of the request. What does NOT defend against it is the paper cursor -- the
// 409 "book advanced concurrently; retry" only fires when the cursor moved
// underneath the request, and this handler steps the cursor past its own bar
// (LastBarTs+1) specifically so a second order inside one bar SUCCEEDS.
//
// That is acceptable here and would not be anywhere near a broker. The book is
// per-user (strategy manual:u<uid>), simulated, 401s without a session, and is
// absent from security.go's publicRoutes so it is closed entirely on a published
// deployment. The worst outcome is a user holding play-money units they did not
// mean to buy, which they can sell. If this handler ever reaches real money, an
// idempotency key stops being optional.
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

	// Two different clocks, and stamping the trade with the wrong one made the book
	// unverifiable. The CURSOR is a per-book monotonic step counter: it must strictly
	// advance, so a second order inside the same 1m bar pushes it to LastBarTs+1. The
	// TRADE records the bar it actually filled against. Stamping the cursor's
	// synthetic value on the trade produced timestamps at ts%60 of 1, 2 and 3 --
	// matching no stored 1m bar, so checkPaperFills (which requires bar.Ts == trade.Ts
	// exactly) counted them NoBar and CheckFillFidelity reported the WHOLE book
	// unverified. Two fills in one bar legitimately share a timestamp; store/paper.go
	// orders trade history by id, not ts, so equal stamps disturb nothing.
	barTs := in.Bar.Ts
	if barTs <= cur.LastBarTs {
		barTs = cur.LastBarTs + 1
	}
	trade := store.PaperTrade{
		Strategy: strategy, SymbolID: sym.ID, Side: fill.Side, Qty: fill.Qty, Px: fill.Px, Cost: fill.Cost, Ts: in.Bar.Ts,
		Reason: fill.WithReason(fmt.Sprintf("manual %s order (quote 1m bar %d)", side, in.Bar.Ts)),
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
	// requestedQty is echoed because a buy of N units does NOT fill N units and
	// the response had no way to show it. EnterLong is BUDGET-denominated: this
	// handler passes `body.Qty * bar.Open` as a budget, and EnterLong solves
	// notional = budget / (1 + spread + impact), so execution cost comes OUT of
	// the requested size rather than being added on top. fill.Qty was always the
	// truthful filled amount, but nothing beside it said what was asked for, so
	// the shortfall was invisible unless the caller remembered their own input.
	//
	// fill.UnfilledNotional does not cover this: it is populated ONLY when the
	// ADV participation cap binds, not for the cost-driven shrinkage that
	// happens on every order.
	//
	// Reporting it rather than changing it, deliberately. EnterLong's
	// budget-denominated contract is shared with the automated books; making
	// the manual path fill exactly N would give this system two different
	// execution models, and the quieter of the two bugs is not the one that
	// tells you less.
	resp := map[string]any{
		"ok": true, "strategy": strategy, "symbol": sym.Symbol, "market": sym.Market,
		"fill": fill, "quoteBarTs": in.Bar.Ts, "quoteAgeS": now - in.Bar.Ts,
		"cash": newCash, "equity": equity,
		"requestedQty": body.Qty,
		"label":        "simulated manual paper book - not live money, not advice",
	}
	if d := body.Qty - fill.Qty; d > 1e-9 {
		resp["qtyShortfall"] = d
		resp["qtyNote"] = "filled less than requested: execution cost is taken out of the order's notional, not added to it"
	}
	writeJSON(w, resp)
}

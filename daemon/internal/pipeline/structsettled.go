package pipeline

import (
	md "github.com/nyaungnicholas-wq/signaldeck/internal/marketdata"
	"github.com/nyaungnicholas-wq/signaldeck/internal/store"
)

// structSettledSinceTs is the cut-over for the structural family's settled-bar
// rule. Calls made at or after it are built from, frozen against and resolved
// on SETTLED daily bars only. Calls before it keep the rule they were frozen
// under, so no stored row changes meaning.
//
// WHY. The structural runner loaded bars without the settled-bar trim every
// other runner applies, and it runs every six hours, so the first call of a
// trading day was usually made mid-session from a bar still forming. The
// naive-persistence baseline was then frozen at a later tick from whatever
// that bar had become, and the resolver only checked that enough bars
// existed, not that the last one had closed. Three places read the same
// unfinished bar three different ways.
//
// THE CUT-OVER IS store.StructSettledEpochTS (start of Monday 16 Nov 2026, New
// York), the owner's decision of 13 Nov 2026, registered on the chain as
// "structural-settled-bar" before any call it governs existed. Before that
// instant every function below returns exactly what the old code computed. It
// is a variable only so tests can move it.
var structSettledSinceTs = store.StructSettledEpochTS

// structSettledBars drops a still-forming newest bar from a predictor's input
// once the cut-over has passed.
func structSettledBars(market md.Market, bars []md.Bar, now int64) []md.Bar {
	if now < structSettledSinceTs {
		return bars
	}
	out, _ := trimFormingDaily(market, bars, now)
	return out
}

// structCallIndex is the base bar of a structural call made at callTs: the last
// bar at or before it, and from the cut-over on, the last bar that had SETTLED
// by then. That is the bar the predictor used, so the baseline and the forward
// window are measured from the same place the call was.
func structCallIndex(market md.Market, ts []int64, callTs int64) int {
	t := barIndexAt(ts, callTs)
	if callTs >= structSettledSinceTs && t >= 0 && !md.DailyBarSettled(market, ts[t], callTs) {
		t--
	}
	return t
}

// structUsableLen is how many bars of a series may be read when resolving a
// call made at callTs: all of them before the cut-over, and after it all but a
// newest bar that has not settled as of now.
func structUsableLen(market md.Market, ts []int64, callTs, now int64) int {
	n := len(ts)
	if callTs >= structSettledSinceTs && n > 0 && !md.DailyBarSettled(market, ts[n-1], now) {
		n--
	}
	return n
}

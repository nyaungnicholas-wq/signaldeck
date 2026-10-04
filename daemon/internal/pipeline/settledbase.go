package pipeline

import (
	"context"
	"time"

	"github.com/nyaungnicholas-wq/signaldeck/internal/marketcal"
	md "github.com/nyaungnicholas-wq/signaldeck/internal/marketdata"
	"github.com/nyaungnicholas-wq/signaldeck/internal/store"
)

// settledBaseSinceTs is the cutoff timestamp (2026-09-07T18:20:00Z) after which
// predictions were built from settled daily bars only and are graded from the
// settled base; earlier rows keep the prior rule (base = bar at/before ts,
// even if forming).
const settledBaseSinceTs = int64(1788807600)

// labelBaseSinceTs (2026-10-04T00:00:00Z) starts the SD-30 label window
// (owner's call 2026-10-02/03; PREREGISTRATION.md §15, chain kind
// label-window-reregistration). From here the base is the ISSUE DAY'S OWN daily
// bar, the bar settle_ts already names and the grader groups by, so the label
// runs from that bar's close to the next close and never starts before issue
// for a stock row issued in or before its session or any crypto row. The
// settled-only step back below made the base the PREVIOUS session for every
// row issued between the close and settlement (the 00:00-02:00Z pass the
// grader keeps): 93.8% of kept stock 1d outcomes matched the move already
// visible at issue (crypto 78.6%). 1w keeps its calendar-week target from the
// new base. Rows before this cutoff keep the rule they were frozen under.
const labelBaseSinceTs = int64(1791072000)

// trimFormingDaily removes the newest daily bar if it is still forming.
// daily must be sorted ascending by Ts; only the last bar can be forming.
// Returns the possibly trimmed slice and a bool indicating whether a bar was dropped.
func trimFormingDaily(market md.Market, daily []md.Bar, now int64) ([]md.Bar, bool) {
	if len(daily) == 0 {
		return daily, false
	}
	last := daily[len(daily)-1]
	if !md.DailyBarSettled(market, last.Ts, now) {
		return daily[:len(daily)-1], true
	}
	return daily, false
}

func symbolMarkets(ctx context.Context, st *store.Store) (map[int64]md.Market, error) { // inactive included: their rows still resolve
	syms, err := st.ListSymbols(ctx, false)
	if err != nil {
		return nil, err
	}
	m := make(map[int64]md.Market, len(syms))
	for _, s := range syms {
		m[s.ID] = s.Market
	}
	return m, nil
}

// settledBase returns the settled daily bar to use as the base for a prediction
// made at timestamp ts. For ts < settledBaseSinceTs the base is simply the bar
// at-or-before ts (even if that bar is still forming). For ts >= settledBaseSinceTs
// the base must be a settled bar; if the at-or-before bar is still forming we
// step back one second and look again, guaranteeing we never regrade history
// under a rule it was not frozen under.
func settledBase(ctx context.Context, st *store.Store, market md.Market, symbolID int64, ts int64) (md.Bar, bool, error) {
	base, ok, err := st.BarAtOrBefore(ctx, symbolID, md.TF1d, ts)
	if err != nil || !ok {
		return base, ok, err
	}
	if ts >= labelBaseSinceTs {
		// The issue day's own bar or nothing: when that bar is missing, the
		// bar at-or-before is an earlier session whose move was visible at
		// issue, and grading from it would put the leak back. Leave the row
		// pending until the backfill supplies the bar.
		if labelBaseMissing(market, base.Ts, ts) {
			return md.Bar{}, false, nil
		}
		return base, true, nil
	}
	if ts >= settledBaseSinceTs && !md.DailyBarSettled(market, base.Ts, ts) {
		return st.BarAtOrBefore(ctx, symbolID, md.TF1d, base.Ts-1)
	}
	return base, true, nil
}

// labelBaseMissing reports whether baseTs, the daily bar at-or-before an issue
// at ts, is OLDER than the issue day's bar: for stocks a whole session closed
// after the bar's session and before issue; for crypto (one bar per UTC day)
// the bar is a day or more before issue.
func labelBaseMissing(market md.Market, baseTs, ts int64) bool {
	if market == md.Crypto {
		return ts-baseTs >= 86400
	}
	return marketcal.SessionsClosedSince(baseTs, time.Unix(ts, 0)) > 0
}
